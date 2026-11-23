package dockerrt

import (
	"context"
	"fmt"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
	"github.com/docker/docker/pkg/stdcopy"
)

// Comandos pontuais (DEC-45). Um comando é um contêiner **novo e descartável**, da
// imagem da release ativa, na jaula da aplicação (sem capacidades, sem ganho de
// privilégio, limites, só a rede do projeto, sem porta publicada e sem bind
// mount). Nunca é um `exec` no contêiner da aplicação: o proxy do Docker segue
// com EXEC=0.

// RoleCommand é o valor de LabelRole dos contêineres de comando, e RoleRelease o da
// fase de release de um deploy (DEC-52 item 4), que usa o mesmo caminho.
const (
	RoleCommand = "command"
	RoleRelease = "release"
)

// LabelCommand guarda o identificador do comando no contêiner.
const LabelCommand = "cobogo.command"

// CommandSpec descreve um comando pontual.
type CommandSpec struct {
	ProjectID string
	CommandID string
	// Name é o nome do contêiner; vazio usa cobogo-cmd-<id>.
	Name string
	// Role é o papel no rótulo; vazio é um comando pontual.
	Role string
	// Image é a imagem da release ativa.
	Image string
	// Command é o texto, executado por `sh -c`: dentro da jaula o shell não dá
	// poder que o código do repositório já não tenha.
	Command string
	Env     map[string]string
	Network string
	// Volumes: caminho no contêiner -> nome do volume (os mesmos da aplicação).
	Volumes map[string]string
	// Files: arquivos da plataforma, como a CA do banco.
	Files map[string][]byte

	MemoryMB int
	CPUs     float64
	Pids     int
}

func commandContainerName(commandID string) string {
	if len(commandID) > 8 {
		commandID = commandID[:8]
	}
	return "cobogo-cmd-" + commandID
}

// StartCommand cria e inicia o contêiner do comando. Devolve o ID dele. Se algo
// falhar depois de criado, o contêiner é removido.
func (r *Runtime) StartCommand(ctx context.Context, spec CommandSpec) (string, error) {
	envList := make([]string, 0, len(spec.Env))
	for k, v := range spec.Env {
		envList = append(envList, k+"="+v)
	}
	memory := int64(spec.MemoryMB) * mib
	pids := int64(spec.Pids)
	useInit := true
	hostCfg := &container.HostConfig{
		Init:        &useInit,
		NetworkMode: container.NetworkMode(spec.Network),
		Mounts:      volumeMounts(spec.Volumes),
		AutoRemove:  false,
		CapDrop:     []string{"ALL"},
		SecurityOpt: []string{"no-new-privileges"},
		LogConfig:   container.LogConfig{Type: "json-file", Config: map[string]string{"max-size": "10m", "max-file": "2"}},
		Resources: container.Resources{
			Memory: memory, MemorySwap: memory, NanoCPUs: int64(spec.CPUs * 1e9), PidsLimit: &pids,
		},
	}
	name, role := spec.Name, spec.Role
	if name == "" {
		name = commandContainerName(spec.CommandID)
	}
	if role == "" {
		role = RoleCommand
	}
	created, err := r.cli.ContainerCreate(ctx,
		&container.Config{
			Image:      spec.Image,
			Entrypoint: []string{"/bin/sh", "-c"},
			Cmd:        []string{spec.Command},
			Env:        envList,
			Labels: map[string]string{
				LabelManaged: "true", LabelProject: spec.ProjectID, LabelRole: role, LabelCommand: spec.CommandID,
			},
		},
		hostCfg,
		&network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{spec.Network: {}}},
		nil, name)
	if err != nil {
		return "", fmt.Errorf("dockerrt: criar o contêiner do comando: %w", err)
	}
	cleanup := func() {
		_ = r.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, container.RemoveOptions{Force: true})
	}
	if len(spec.Files) > 0 {
		if err := r.putFiles(ctx, created.ID, spec.Files); err != nil {
			cleanup()
			return "", err
		}
	}
	if err := r.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		cleanup()
		return "", fmt.Errorf("dockerrt: iniciar o comando: %w", err)
	}
	return created.ID, nil
}

// commandInspect confere que o ID é de um contêiner de comando do Cobogó: só se
// age nesses.
func (r *Runtime) commandInspect(ctx context.Context, id string) error {
	inspect, err := r.cli.ContainerInspect(ctx, id)
	if err != nil {
		return err
	}
	if inspect.Config == nil || inspect.Config.Labels[LabelManaged] != "true" || (inspect.Config.Labels[LabelRole] != RoleCommand && inspect.Config.Labels[LabelRole] != RoleRelease) {
		return fmt.Errorf("dockerrt: o contêiner %s não é um comando do Cobogó", shortID(id))
	}
	return nil
}

// CommandState diz se o comando ainda roda, com que código saiu e se o kernel o
// matou por falta de memória.
func (r *Runtime) CommandState(ctx context.Context, id string) (running bool, exitCode int, oom bool, err error) {
	if err := r.commandInspect(ctx, id); err != nil {
		return false, 0, false, err
	}
	inspect, err := r.cli.ContainerInspect(ctx, id)
	if err != nil {
		return false, 0, false, err
	}
	if inspect.State == nil {
		return false, 0, false, fmt.Errorf("dockerrt: sem estado para o comando")
	}
	return inspect.State.Running, inspect.State.ExitCode, inspect.State.OOMKilled, nil
}

// StopCommand pede o término (SIGTERM) e, passado o prazo, mata (SIGKILL).
func (r *Runtime) StopCommand(ctx context.Context, id string, graceSeconds int) error {
	if err := r.commandInspect(ctx, id); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := r.cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &graceSeconds}); err != nil && !errdefs.IsNotModified(err) && !errdefs.IsNotFound(err) {
		return fmt.Errorf("dockerrt: parar o comando: %w", err)
	}
	return nil
}

// RemoveCommand remove o contêiner de um comando.
func (r *Runtime) RemoveCommand(ctx context.Context, id string) error {
	if err := r.commandInspect(ctx, id); err != nil {
		if errdefs.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := r.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("dockerrt: remover o comando: %w", err)
	}
	return nil
}

// RemoveCommandContainers remove os contêineres de comando que sobraram (o worker caiu
// no meio). projectID vazio remove os de todos os projetos (é o que o worker faz ao
// subir); com um projeto, só os dele (os testes, que rodam ao lado de outros, usam isto).
// Devolve quantos removeu.
func (r *Runtime) RemoveCommandContainers(ctx context.Context, projectID string) (int, error) {
	n := 0
	for _, role := range []string{RoleCommand, RoleRelease} {
		args := filters.NewArgs(filters.Arg("label", LabelManaged+"=true"), filters.Arg("label", LabelRole+"="+role))
		if projectID != "" {
			args.Add("label", LabelProject+"="+projectID)
		}
		list, err := r.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
		if err != nil {
			return n, fmt.Errorf("dockerrt: listar comandos: %w", err)
		}
		for _, c := range list {
			if err := r.cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err == nil {
				n++
			}
		}
	}
	return n, nil
}

// headTail guarda os primeiros head bytes e os últimos tail bytes do que passa,
// e quanto foi descartado no meio.
type headTail struct {
	head, tail int
	first      []byte
	last       []byte
	dropped    int64
}

func (h *headTail) Write(p []byte) (int, error) {
	n := len(p)
	if room := h.head - len(h.first); room > 0 {
		take := min(room, len(p))
		h.first = append(h.first, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		h.last = append(h.last, p...)
		if over := len(h.last) - h.tail; over > 0 {
			h.dropped += int64(over)
			h.last = append(h.last[:0], h.last[over:]...)
		}
	}
	return n, nil
}

func (h *headTail) String() (string, bool) {
	if h.dropped == 0 {
		return string(h.first) + string(h.last), false
	}
	return string(h.first) + fmt.Sprintf("\n[... %d bytes omitidos ...]\n", h.dropped) + string(h.last), true
}

// CommandOutput lê a saída (stdout e stderr) do comando, no máximo maxBytes: a
// cabeça e a cauda quando passa. Devolve se houve corte.
func (r *Runtime) CommandOutput(ctx context.Context, id string, maxBytes int) (string, bool, error) {
	if err := r.commandInspect(ctx, id); err != nil {
		return "", false, err
	}
	body, err := r.cli.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return "", false, fmt.Errorf("dockerrt: ler a saída do comando: %w", err)
	}
	defer body.Close()
	ht := &headTail{head: maxBytes / 2, tail: maxBytes / 2}
	var sink io.Writer = ht
	if _, err := stdcopy.StdCopy(sink, sink, body); err != nil && err != io.EOF {
		return "", false, fmt.Errorf("dockerrt: decodificar a saída do comando: %w", err)
	}
	text, cut := ht.String()
	return text, cut, nil
}
