package dockerrt

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
)

// Serviço de dados auxiliar por projeto (DEC-42, DEC-50). O contêiner fica só na rede
// do projeto, sem porta publicada, com os labels do Cobogó e do projeto, e os dados num
// volume nomeado (DEC-41). O que muda de uma engine para outra está em Engine.

// PostgresImage é a imagem do PostgreSQL gerenciado, fixada por digest (a mesma
// do banco interno do Cobogó).
const PostgresImage = "postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea"

// LabelService marca o contêiner de um serviço auxiliar (valor: a engine).
const LabelService = "cobogo.service"

// ServiceSpec descreve o serviço de dados gerenciado de um projeto.
type ServiceSpec struct {
	// Engine é uma das engines providas (domain.Service*).
	Engine    string
	ProjectID string
	Container string
	Network   string
	Volume    string
	Password  string
	// TLS: o serviço aceita TLS, com certificado próprio (DEC-44); só para engines que o suportam.
	TLS bool
}

// EnsureService garante o contêiner do serviço: cria o que falta (volume e
// contêiner), inicia o que está parado e **não** cria um segundo. Devolve se
// criou o contêiner agora. Um contêiner com o nome, mas sem os labels do Cobogó,
// do projeto e da engine, é recusado.
func (r *Runtime) EnsureService(ctx context.Context, spec ServiceSpec) (created bool, err error) {
	eng, err := EngineFor(spec.Engine)
	if err != nil {
		return false, err
	}
	inspect, err := r.cli.ContainerInspect(ctx, spec.Container)
	switch {
	case err == nil:
		if inspect.Config == nil || inspect.Config.Labels[LabelManaged] != "true" ||
			inspect.Config.Labels[LabelProject] != spec.ProjectID || inspect.Config.Labels[LabelService] != eng.Name {
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

	if err := r.EnsureImage(ctx, eng.Image); err != nil {
		return false, err
	}
	if _, err := r.EnsureVolume(ctx, spec.Volume, spec.ProjectID, eng.DataPath); err != nil {
		return false, err
	}
	env := eng.InitEnv(spec.Password)
	args := append([]string(nil), eng.Args...)
	if spec.TLS {
		if eng.TLS == nil {
			return false, fmt.Errorf("dockerrt: %s ainda não suporta TLS gerenciado", eng.Label)
		}
		tlsEnv, tlsArgs, err := eng.TLS(ctx, r, spec)
		if err != nil {
			return false, err
		}
		env, args = append(env, tlsEnv...), append(args, tlsArgs...)
	}
	memory := int64(eng.MemoryMB) * mib
	pids := eng.Pids
	useInit := true
	created2, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image: eng.Image,
			Env:   env,
			Cmd:   args,
			Labels: map[string]string{
				LabelManaged: "true", LabelProject: spec.ProjectID, LabelService: eng.Name,
			},
			Healthcheck: &container.HealthConfig{
				// O próprio daemon roda a verificação; o worker não usa exec (DEC-32).
				Test:        eng.Health,
				Interval:    2 * time.Second,
				Timeout:     3 * time.Second,
				Retries:     60,
				StartPeriod: 3 * time.Second,
			},
		},
		&container.HostConfig{
			NetworkMode: container.NetworkMode(spec.Network),
			Mounts:      volumeMounts(map[string]string{eng.DataPath: spec.Volume}),
			Init:        &useInit,
			// O banco precisa voltar sozinho depois de um reinício da máquina; as
			// aplicações, de propósito, não (DEC-29).
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			// As capacidades que a imagem usa para ajustar a posse do diretório de
			// dados e largar o root; nada além.
			CapDrop:     []string{"ALL"},
			CapAdd:      eng.CapAdd,
			SecurityOpt: []string{"no-new-privileges"},
			LogConfig:   container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "3"}},
			Resources: container.Resources{
				Memory: memory, MemorySwap: memory, NanoCPUs: int64(eng.CPUs * 1e9), PidsLimit: &pids,
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

// RemoveService remove o contêiner do serviço, se for do Cobogó e do projeto, e o
// volume dos dados. Irreversível.
func (r *Runtime) RemoveService(ctx context.Context, projectID, containerName, volumeName string) error {
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
