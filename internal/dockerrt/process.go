package dockerrt

import (
	"context"
	"fmt"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"
)

// Processos de um projeto além do web (DEC-51): um contêiner da mesma imagem da release, sem
// porta nem rota, com as mesmas variáveis, volumes e limites. O label `cobogo.process` os distingue
// (o web não o tem); o label de deploy os liga à release.

// LabelProcess é o label de um processo além do web (valor: o nome do processo).
const LabelProcess = "cobogo.process"

// RunProcess cria e inicia o contêiner de um processo da release. Só a rede do projeto (o worker não
// entra no ingress), nenhuma porta publicada, o comando por `sh -c` (com `exec`, para o processo receber
// o SIGTERM). Depois do create, toda falha devolve o ID, para quem chama ler o log e limpar.
func (r *Runtime) RunProcess(ctx context.Context, imageTag, containerName, command string, env map[string]string, deploymentID, process string, opts ...RunOption) (string, error) {
	var spec runSpec
	for _, o := range opts {
		o(&spec)
	}
	envList := make([]string, 0, len(env))
	for k, v := range env {
		envList = append(envList, k+"="+v)
	}
	cfg := &container.Config{Image: imageTag, Env: envList, Cmd: []string{"sh", "-c", command}, Labels: map[string]string{}}
	for k, v := range spec.labels {
		cfg.Labels[k] = v
	}
	cfg.Labels[LabelManaged] = "true"
	cfg.Labels[LabelDeployment] = deploymentID
	cfg.Labels[LabelProcess] = process
	hostCfg := appHostConfig(nat.Port("0/tcp"), r.limits)
	hostCfg.PortBindings = nil
	if spec.network != "" {
		hostCfg.NetworkMode = container.NetworkMode(spec.network)
	}
	hostCfg.Mounts = volumeMounts(spec.volumes)
	created, err := r.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("dockerrt: criar o processo %s: %w", process, err)
	}
	if len(spec.files) > 0 {
		if err := r.putFiles(ctx, created.ID, spec.files); err != nil {
			return created.ID, err
		}
	}
	if err := r.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		return created.ID, fmt.Errorf("dockerrt: iniciar o processo %s: %w", process, err)
	}
	return created.ID, nil
}

// ListProcesses lista os processos além do web de um deploy.
func (r *Runtime) ListProcesses(ctx context.Context, deploymentID string) ([]ManagedContainer, error) {
	list, err := r.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(
		filters.Arg("label", LabelManaged+"=true"), filters.Arg("label", LabelDeployment+"="+deploymentID), filters.Arg("label", LabelProcess),
	)})
	if err != nil {
		return nil, fmt.Errorf("dockerrt: listar os processos do deploy: %w", err)
	}
	out := make([]ManagedContainer, 0, len(list))
	for _, c := range list {
		out = append(out, toManaged(c))
	}
	return out, nil
}

// StartProcess inicia um processo parado. Iniciar o que já está no ar não é erro.
func (r *Runtime) StartProcess(ctx context.Context, containerID string) error {
	if _, err := r.managedInspect(ctx, containerID); err != nil {
		return err
	}
	if err := r.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil && !errdefs.IsNotModified(err) {
		return fmt.Errorf("dockerrt: iniciar o processo: %w", err)
	}
	return nil
}

// WaitStable espera um processo sem porta: ele passa se continuar rodando por toda a janela, e falha na hora,
// com *ExitedError, se sair antes (um worker não tem HTTP para sondar; ficar de pé é o sinal).
func (r *Runtime) WaitStable(ctx context.Context, containerID string, window time.Duration) error {
	deadline := time.Now().Add(window)
	for {
		inspect, err := r.cli.ContainerInspect(ctx, containerID)
		if err != nil {
			return fmt.Errorf("dockerrt: inspecionar o processo: %w", err)
		}
		if inspect.State == nil || !inspect.State.Running {
			code := -1
			if inspect.State != nil {
				code = inspect.State.ExitCode
			}
			return &ExitedError{ExitCode: code}
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
