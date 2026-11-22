package dockerrt

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// PostgreSQL auxiliar por projeto (DEC-42). O contêiner fica só na rede do
// projeto, sem porta publicada, com os labels do Cobogó e do projeto, e os dados
// num volume nomeado (DEC-41).

// PostgresImage é a imagem do PostgreSQL gerenciado, fixada por digest (a mesma
// do banco interno do Cobogó).
const PostgresImage = "postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea"

// LabelService marca o contêiner de um serviço auxiliar (valor: a engine).
const LabelService = "cobogo.service"

// Limites fixos do banco gerenciado (DEC-42 item 3).
const (
	postgresMemoryMB = 256
	postgresCPUs     = 0.5
	postgresPids     = 128
)

// PostgresSpec descreve o PostgreSQL de um projeto.
type PostgresSpec struct {
	ProjectID string
	Container string
	Network   string
	Volume    string
	Password  string
	// TLS: o banco aceita TLS, com certificado próprio (DEC-44).
	TLS bool
}

// EnsurePostgres garante o contêiner do banco: cria o que falta (volume e
// contêiner), inicia o que está parado e **não** cria um segundo. Devolve se
// criou o contêiner agora. Um contêiner com o nome, mas sem os labels do Cobogó
// e do projeto, é recusado.
func (r *Runtime) EnsurePostgres(ctx context.Context, spec PostgresSpec) (created bool, err error) {
	inspect, err := r.cli.ContainerInspect(ctx, spec.Container)
	switch {
	case err == nil:
		if inspect.Config == nil || inspect.Config.Labels[LabelManaged] != "true" ||
			inspect.Config.Labels[LabelProject] != spec.ProjectID || inspect.Config.Labels[LabelService] != domain.ServicePostgres {
			return false, fmt.Errorf("dockerrt: o contêiner %s existe mas não é o banco deste projeto no Cobogó", spec.Container)
		}
		if inspect.State != nil && !inspect.State.Running {
			if err := r.cli.ContainerStart(ctx, inspect.ID, container.StartOptions{}); err != nil {
				return false, fmt.Errorf("dockerrt: iniciar o banco: %w", err)
			}
		}
		return false, nil
	case !errdefs.IsNotFound(err):
		return false, fmt.Errorf("dockerrt: inspecionar o banco: %w", err)
	}

	if err := r.EnsureImage(ctx, PostgresImage); err != nil {
		return false, err
	}
	if _, err := r.EnsureVolume(ctx, spec.Volume, spec.ProjectID, domain.PostgresDataPath); err != nil {
		return false, err
	}
	env := []string{"POSTGRES_USER=app", "POSTGRES_DB=app", "POSTGRES_PASSWORD=" + spec.Password}
	var args []string
	if spec.TLS {
		if err := r.putPostgresTLS(ctx, spec); err != nil {
			return false, err
		}
		env = append(env, "PGDATA="+PostgresPGData)
		args = []string{"-c", "ssl=on", "-c", "ssl_cert_file=" + PostgresTLSDir + "/" + serverCertName, "-c", "ssl_key_file=" + PostgresTLSDir + "/" + serverKeyName}
	}
	memory := int64(postgresMemoryMB) * mib
	pids := int64(postgresPids)
	useInit := true
	created2, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image: PostgresImage,
			Env:   env,
			Cmd:   args,
			Labels: map[string]string{
				LabelManaged: "true", LabelProject: spec.ProjectID, LabelService: domain.ServicePostgres,
			},
			Healthcheck: &container.HealthConfig{
				// O próprio daemon roda a verificação; o worker não usa exec (DEC-32).
				Test:        []string{"CMD-SHELL", "pg_isready -U app -d app -h 127.0.0.1"},
				Interval:    2 * time.Second,
				Timeout:     3 * time.Second,
				Retries:     60,
				StartPeriod: 3 * time.Second,
			},
		},
		&container.HostConfig{
			NetworkMode: container.NetworkMode(spec.Network),
			Mounts:      volumeMounts(map[string]string{domain.PostgresDataPath: spec.Volume}),
			Init:        &useInit,
			// O banco precisa voltar sozinho depois de um reinício da máquina; as
			// aplicações, de propósito, não (DEC-29).
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			// As capacidades que a imagem usa para ajustar a posse do diretório de
			// dados e largar o root; nada além.
			CapDrop:     []string{"ALL"},
			CapAdd:      []string{"CHOWN", "DAC_OVERRIDE", "FOWNER", "SETGID", "SETUID"},
			SecurityOpt: []string{"no-new-privileges"},
			LogConfig:   container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
			Resources: container.Resources{
				Memory: memory, MemorySwap: memory, NanoCPUs: int64(postgresCPUs * 1e9), PidsLimit: &pids,
			},
		},
		&network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{spec.Network: {}}},
		nil, spec.Container)
	if err != nil {
		return false, fmt.Errorf("dockerrt: criar o banco: %w", err)
	}
	if err := r.cli.ContainerStart(ctx, created2.ID, container.StartOptions{}); err != nil {
		_ = r.cli.ContainerRemove(context.WithoutCancel(ctx), created2.ID, container.RemoveOptions{Force: true})
		return false, fmt.Errorf("dockerrt: iniciar o banco: %w", err)
	}
	return true, nil
}

// WaitHealthy espera a verificação de saúde do contêiner passar. Falha cedo se
// ele parar.
func (r *Runtime) WaitHealthy(ctx context.Context, name string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		inspect, err := r.cli.ContainerInspect(ctx, name)
		if err != nil {
			return fmt.Errorf("dockerrt: inspecionar o banco: %w", err)
		}
		if inspect.State == nil || !inspect.State.Running {
			code := -1
			if inspect.State != nil {
				code = inspect.State.ExitCode
			}
			return fmt.Errorf("dockerrt: o banco parou antes de ficar pronto (código de saída %d)", code)
		}
		if inspect.State.Health != nil && inspect.State.Health.Status == "healthy" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("dockerrt: o banco não ficou pronto em %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// RemovePostgres remove o contêiner do banco, se for do Cobogó e do projeto, e o
// volume dos dados. Irreversível.
func (r *Runtime) RemovePostgres(ctx context.Context, projectID, containerName, volumeName string) error {
	inspect, err := r.cli.ContainerInspect(ctx, containerName)
	switch {
	case err == nil:
		if inspect.Config == nil || inspect.Config.Labels[LabelManaged] != "true" || inspect.Config.Labels[LabelProject] != projectID {
			return fmt.Errorf("dockerrt: o contêiner %s não é do Cobogó neste projeto", containerName)
		}
		if err := r.Remove(ctx, inspect.ID); err != nil {
			return err
		}
	case !errdefs.IsNotFound(err):
		return fmt.Errorf("dockerrt: inspecionar o banco: %w", err)
	}
	return r.RemoveVolume(ctx, volumeName)
}
