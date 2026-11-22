package dockerrt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
)

// Backup dos dados de um projeto (DEC-47, parte B). A cópia é feita por contêineres
// descartáveis, nunca copiando arquivo de um volume em uso: um `tar` lê o volume
// **somente leitura** e um `pg_dump` fala com o banco pela rede do projeto. O
// destino é uma subpasta do volume de backups, e só ela: o contêiner não enxerga
// o resto.

// RoleBackupCopy é o valor de LabelRole dos contêineres de cópia.
const RoleBackupCopy = "backup-copy"

// CopySpec descreve uma cópia.
type CopySpec struct {
	ProjectID string
	BackupID  string
	// Image e Command: o que roda (o comando é fixo da plataforma, nunca do repositório).
	Image   string
	Command []string
	Env     []string
	// User roda o comando como este usuário ("" é o da imagem).
	User string
	// SourceVolume é o volume de dados do projeto, montado somente leitura em /src; vazio no banco.
	SourceVolume string
	// BackupVolume e Subpath são o destino: a subpasta, montada em /out.
	BackupVolume string
	Subpath      string
	// Network é a rede do projeto (para falar com o banco); vazio é sem rede.
	Network string
	// ReadAnyFile acrescenta CAP_DAC_READ_SEARCH (ler arquivos de qualquer dono, só leitura).
	ReadAnyFile bool
	MemoryMB    int
}

// RunCopy roda a cópia até o fim, com prazo, e a remove. Devolve o final da saída
// (stdout e stderr) quando falha, para o motivo ir ao painel.
func (r *Runtime) RunCopy(ctx context.Context, spec CopySpec, timeout time.Duration) error {
	// O Docker cria sozinho um volume que não existe, e vazio: uma cópia de um volume
	// "criado agora" daria um backup vazio e pareceria boa. Os dois têm de existir, e
	// o de dados tem de ser deste projeto, com os labels do Cobogó.
	if spec.SourceVolume != "" {
		v, err := r.cli.VolumeInspect(ctx, spec.SourceVolume)
		if err != nil {
			return fmt.Errorf("dockerrt: o volume de dados %s não existe: %w", spec.SourceVolume, err)
		}
		if v.Labels[LabelManaged] != "true" || v.Labels[LabelProject] != spec.ProjectID {
			return fmt.Errorf("dockerrt: o volume %s não é deste projeto no Cobogó", spec.SourceVolume)
		}
	}
	if _, err := r.cli.VolumeInspect(ctx, spec.BackupVolume); err != nil {
		return fmt.Errorf("dockerrt: o volume de backups %s não existe: %w", spec.BackupVolume, err)
	}
	if err := r.EnsureImage(ctx, spec.Image); err != nil {
		return err
	}
	mounts := []mount.Mount{{
		Type: mount.TypeVolume, Source: spec.BackupVolume, Target: "/out",
		VolumeOptions: &mount.VolumeOptions{Subpath: spec.Subpath},
	}}
	if spec.SourceVolume != "" {
		mounts = append(mounts, mount.Mount{Type: mount.TypeVolume, Source: spec.SourceVolume, Target: "/src", ReadOnly: true})
	}
	netMode := container.NetworkMode("none")
	var netCfg *network.NetworkingConfig
	if spec.Network != "" {
		netMode = container.NetworkMode(spec.Network)
		netCfg = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{spec.Network: {}}}
	}
	capAdd := []string{}
	if spec.ReadAnyFile {
		capAdd = append(capAdd, "DAC_READ_SEARCH")
	}
	memory := int64(spec.MemoryMB) * mib
	pids := int64(64)
	created, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image: spec.Image, Entrypoint: []string{"/bin/sh", "-c"}, Cmd: []string{strings.Join(spec.Command, " ")},
			Env: spec.Env, User: spec.User,
			Labels: map[string]string{LabelManaged: "true", LabelProject: spec.ProjectID, LabelRole: RoleBackupCopy, "cobogo.backup": spec.BackupID},
		},
		&container.HostConfig{
			Mounts: mounts, NetworkMode: netMode,
			CapDrop: []string{"ALL"}, CapAdd: capAdd, SecurityOpt: []string{"no-new-privileges"},
			LogConfig: container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "1m", "max-file": "1"}},
			Resources: container.Resources{Memory: memory, MemorySwap: memory, PidsLimit: &pids},
		}, netCfg, nil, "")
	if err != nil {
		return fmt.Errorf("dockerrt: criar o contêiner da cópia: %w", err)
	}
	defer func() {
		_ = r.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true})
	}()
	if err := r.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("dockerrt: iniciar a cópia: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	statusCh, errCh := r.cli.ContainerWait(waitCtx, created.ID, container.WaitConditionNotRunning)
	select {
	case st := <-statusCh:
		if st.StatusCode != 0 {
			tail, _ := r.LogTail(context.WithoutCancel(ctx), created.ID, 20)
			return fmt.Errorf("dockerrt: a cópia terminou com código %d: %s", st.StatusCode, strings.TrimSpace(tail))
		}
		return nil
	case err := <-errCh:
		if waitCtx.Err() != nil && ctx.Err() == nil {
			_ = r.cli.ContainerKill(context.WithoutCancel(ctx), created.ID, "KILL")
			return fmt.Errorf("dockerrt: a cópia passou do prazo de %s", timeout)
		}
		if errdefs.IsNotFound(err) {
			return fmt.Errorf("dockerrt: a cópia sumiu antes de terminar")
		}
		return fmt.Errorf("dockerrt: esperar a cópia: %w", err)
	}
}

// TarVolume grava um volume de dados num .tar.gz em /out/<file>, lendo-o somente leitura.
func (r *Runtime) TarVolume(ctx context.Context, projectID, backupID, sourceVolume, backupVolume, subpath, file string, timeout time.Duration) error {
	return r.RunCopy(ctx, CopySpec{
		ProjectID: projectID, BackupID: backupID, Image: PostgresImage,
		Command:      []string{"tar", "czf", "/out/" + file, "-C", "/src", "."},
		User:         "0",
		SourceVolume: sourceVolume, BackupVolume: backupVolume, Subpath: subpath,
		ReadAnyFile: true, MemoryMB: 256,
	}, timeout)
}

// DumpPostgres grava o dump (formato custom) do banco gerenciado em /out/<file>. A
// senha vai por ambiente do contêiner descartável, nunca na linha de comando.
func (r *Runtime) DumpPostgres(ctx context.Context, projectID, backupID, host, password, projectNetwork, backupVolume, subpath, file string, timeout time.Duration) error {
	return r.RunCopy(ctx, CopySpec{
		ProjectID: projectID, BackupID: backupID, Image: PostgresImage,
		Command:      []string{"pg_dump", "--format=custom", "--no-owner", "--no-acl", "-h", host, "-U", "app", "-d", "app", "-f", "/out/" + file},
		Env:          []string{"PGPASSWORD=" + password},
		User:         "70",
		BackupVolume: backupVolume, Subpath: subpath, Network: projectNetwork, MemoryMB: 256,
	}, timeout)
}

// RemoveBackupCopyContainers remove os contêineres de cópia que sobraram (o worker
// caiu no meio). Devolve quantos removeu.
func (r *Runtime) RemoveBackupCopyContainers(ctx context.Context) (int, error) {
	list, err := r.ListManaged(ctx, "")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, c := range list {
		if c.Role != RoleBackupCopy {
			continue
		}
		if err := r.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err == nil {
			n++
		}
	}
	return n, nil
}
