package dockerrt

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/errdefs"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Volumes de dados (DEC-41). Todo volume leva os labels do Cobogó e do projeto;
// o worker só cria, mede, monta e remove o que tem esses labels (RF-18).

// LabelVolumePath guarda o caminho do contêiner a que o volume serve.
const LabelVolumePath = "cobogo.volume.path"

// VolumeName é o nome do volume de um caminho de um projeto: derivado do
// identificador do projeto e do caminho, nunca de texto do repositório.
func VolumeName(projectID, path string) string { return domain.VolumeName(projectID, path) }

// WithVolumes monta volumes nomeados nos caminhos do contêiner (caminho -> nome
// do volume). O repositório nunca decide isto: vem do projeto (DEC-41 item 1).
func WithVolumes(volumes map[string]string) RunOption {
	return func(s *runSpec) { s.volumes = volumes }
}

func volumeMounts(volumes map[string]string) []mount.Mount {
	var out []mount.Mount
	for path, name := range volumes {
		out = append(out, mount.Mount{Type: mount.TypeVolume, Source: name, Target: path})
	}
	return out
}

// EnsureVolume cria o volume se ele ainda não existe. Devolve se acabou de
// criá-lo. Um volume que existe sem os labels do Cobogó é recusado: o nome é
// derivado, mas quem o criou não foi o Cobogó.
func (r *Runtime) EnsureVolume(ctx context.Context, name, projectID, path string) (created bool, err error) {
	existing, err := r.cli.VolumeInspect(ctx, name)
	switch {
	case err == nil:
		if existing.Labels[LabelManaged] != "true" || existing.Labels[LabelProject] != projectID {
			return false, fmt.Errorf("dockerrt: o volume %s existe mas não é deste projeto no Cobogó", name)
		}
		return false, nil
	case !errdefs.IsNotFound(err):
		return false, fmt.Errorf("dockerrt: inspecionar volume: %w", err)
	}
	_, err = r.cli.VolumeCreate(ctx, volume.CreateOptions{Name: name, Labels: map[string]string{
		LabelManaged: "true", LabelProject: projectID, LabelVolumePath: path,
	}})
	if err != nil {
		return false, fmt.Errorf("dockerrt: criar volume: %w", err)
	}
	return true, nil
}

// VolumeExists diz se o volume existe (sem criar nada).
func (r *Runtime) VolumeExists(ctx context.Context, name string) (bool, error) {
	_, err := r.cli.VolumeInspect(ctx, name)
	if errdefs.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("dockerrt: inspecionar volume: %w", err)
	}
	return true, nil
}

// RemoveVolume remove um volume do Cobogó. O de outro, ou sem os labels, é
// recusado; um volume que já não existe não é erro; um em uso é recusado pelo
// daemon.
func (r *Runtime) RemoveVolume(ctx context.Context, name string) error {
	v, err := r.cli.VolumeInspect(ctx, name)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("dockerrt: inspecionar volume: %w", err)
	}
	if v.Labels[LabelManaged] != "true" {
		return fmt.Errorf("dockerrt: o volume %s não é gerenciado pelo Cobogó", name)
	}
	if err := r.cli.VolumeRemove(ctx, name, false); err != nil && !errdefs.IsNotFound(err) {
		if errdefs.IsConflict(err) {
			return fmt.Errorf("dockerrt: o volume está em uso por um contêiner (a release no ar ainda o monta); faça um deploy sem esse caminho e apague depois: %w", err)
		}
		return fmt.Errorf("dockerrt: remover volume: %w", err)
	}
	return nil
}

// VolumeSizes mede, em bytes, os volumes do Cobogó (só os com os labels).
func (r *Runtime) VolumeSizes(ctx context.Context) (map[string]int64, error) {
	du, err := r.cli.DiskUsage(ctx, types.DiskUsageOptions{Types: []types.DiskUsageObject{types.VolumeObject}})
	if err != nil {
		return nil, fmt.Errorf("dockerrt: medir volumes: %w", err)
	}
	out := map[string]int64{}
	for _, v := range du.Volumes {
		if v == nil || v.Labels[LabelManaged] != "true" || v.UsageData == nil || v.UsageData.Size < 0 {
			continue
		}
		out[v.Name] = v.UsageData.Size
	}
	return out, nil
}

// ImageUser devolve o usuário com que a imagem roda ("" ou "root" é root).
func (r *Runtime) ImageUser(ctx context.Context, imageTag string) (string, error) {
	img, _, err := r.cli.ImageInspectWithRaw(ctx, imageTag)
	if err != nil {
		return "", fmt.Errorf("dockerrt: inspecionar imagem: %w", err)
	}
	if img.Config == nil {
		return "", nil
	}
	return img.Config.User, nil
}

// chownScript entrega ao usuário da aplicação tudo o que há nos caminhos. O
// grupo é o principal do usuário, se ele existe na imagem.
const chownScript = `u="$1"; shift; g=$(id -g "$u" 2>/dev/null) || g="$u"; chown -R "$u:$g" "$@"`

// ChownVolumes dá ao usuário da aplicação a posse dos volumes recém-criados.
// Um caminho que a imagem não tinha vira um ponto de montagem do root, onde a
// aplicação, que não é root, não consegue escrever (DEC-41). Roda um contêiner
// descartável, com um comando fixo da plataforma, sem rede e só com as
// capacidades de posse, da própria imagem do projeto. Falhar não derruba o
// deploy: quem chama registra o aviso.
func (r *Runtime) ChownVolumes(ctx context.Context, imageTag, user, projectID string, volumes map[string]string) error {
	if user == "" || user == "root" || user == "0" || strings.HasPrefix(user, "0:") || len(volumes) == 0 {
		return nil
	}
	args := []string{"_", user}
	for path := range volumes {
		args = append(args, path)
	}
	pids := int64(64)
	created, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image: imageTag, User: "0", Entrypoint: []string{"/bin/sh", "-c", chownScript}, Cmd: args,
			Labels: map[string]string{LabelManaged: "true", LabelProject: projectID, "cobogo.role": "volume-init"},
		},
		&container.HostConfig{
			Mounts: volumeMounts(volumes), NetworkMode: "none",
			CapDrop: []string{"ALL"}, CapAdd: []string{"CHOWN", "DAC_OVERRIDE", "FOWNER"},
			SecurityOpt: []string{"no-new-privileges"},
			Resources:   container.Resources{Memory: 128 * mib, MemorySwap: 128 * mib, PidsLimit: &pids},
		}, nil, nil, "")
	if err != nil {
		return fmt.Errorf("dockerrt: preparar a posse dos volumes: %w", err)
	}
	defer func() {
		_ = r.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true})
	}()
	if err := r.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return fmt.Errorf("dockerrt: iniciar a posse dos volumes: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	statusCh, errCh := r.cli.ContainerWait(waitCtx, created.ID, container.WaitConditionNotRunning)
	select {
	case st := <-statusCh:
		if st.StatusCode != 0 {
			return fmt.Errorf("dockerrt: a posse dos volumes terminou com código %d", st.StatusCode)
		}
	case err := <-errCh:
		return fmt.Errorf("dockerrt: esperar a posse dos volumes: %w", err)
	}
	return nil
}
