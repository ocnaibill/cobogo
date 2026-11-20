package dockerrt

import (
	"context"
	"errors"
	"fmt"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/errdefs"
)

// Nomes e labels da topologia de rede (DEC-19, DEC-31).
const (
	// LabelProject identifica o projeto dono de um contêiner ou de uma rede.
	LabelProject = "cobogo.project"
	// IngressNetwork é a rede compartilhada só de roteamento: o Traefik e as
	// releases que recebem tráfego.
	IngressNetwork = "cobogo-ingress"
)

// ProjectNetwork é o nome da rede de um projeto.
func ProjectNetwork(projectID string) string { return "cobogo-proj-" + projectID }

// RunOption ajusta a criação de um contêiner de aplicação.
type RunOption func(*runSpec)

type runSpec struct {
	network string
	labels  map[string]string
}

// WithNetwork roda o contêiner na rede indicada, em vez da bridge padrão.
func WithNetwork(name string) RunOption { return func(s *runSpec) { s.network = name } }

// WithLabels acrescenta labels. Os do Cobogó (gerenciado e deploy) não podem
// ser substituídos.
func WithLabels(labels map[string]string) RunOption {
	return func(s *runSpec) {
		if s.labels == nil {
			s.labels = map[string]string{}
		}
		for k, v := range labels {
			s.labels[k] = v
		}
	}
}

// EnsureNetwork cria a rede se ela ainda não existe (bridge comum). Uma rede
// que já existe, criada por quem for, serve como está.
func (r *Runtime) EnsureNetwork(ctx context.Context, name string, labels map[string]string) error {
	if _, err := r.cli.NetworkInspect(ctx, name, network.InspectOptions{}); err == nil {
		return nil
	} else if !errdefs.IsNotFound(err) {
		return fmt.Errorf("dockerrt: inspecionar rede %s: %w", name, err)
	}
	all := map[string]string{LabelManaged: "true"}
	for k, v := range labels {
		all[k] = v
	}
	if _, err := r.cli.NetworkCreate(ctx, name, network.CreateOptions{Driver: "bridge", Labels: all}); err != nil {
		// Uma criação concorrente (ou do compose) pode ter vencido.
		if _, ierr := r.cli.NetworkInspect(ctx, name, network.InspectOptions{}); ierr == nil {
			return nil
		}
		return fmt.Errorf("dockerrt: criar rede %s: %w", name, err)
	}
	return nil
}

func connectedTo(inspect types.ContainerJSON, networkName string) bool {
	if inspect.NetworkSettings == nil {
		return false
	}
	_, ok := inspect.NetworkSettings.Networks[networkName]
	return ok
}

// Promote conecta o contêiner à rede de ingress: a partir daí o Traefik o
// alcança. É idempotente.
func (r *Runtime) Promote(ctx context.Context, containerID, networkName string) error {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("dockerrt: inspecionar contêiner: %w", err)
	}
	if connectedTo(inspect, networkName) {
		return nil
	}
	if err := r.cli.NetworkConnect(ctx, networkName, containerID, &network.EndpointSettings{}); err != nil {
		return fmt.Errorf("dockerrt: conectar à rede %s: %w", networkName, err)
	}
	return nil
}

// Demote desconecta da rede de ingress os contêineres gerenciados do projeto,
// menos o indicado: o tráfego fica só com a release promovida. Não os para nem
// os remove (isso é a atualização com release preservada, RF-15). Devolve os IDs
// que desconectou.
func (r *Runtime) Demote(ctx context.Context, projectID, exceptContainerID, networkName string) ([]string, error) {
	list, err := r.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", LabelManaged+"=true"),
			filters.Arg("label", LabelProject+"="+projectID),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("dockerrt: listar contêineres do projeto: %w", err)
	}
	var demoted []string
	var firstErr error
	for _, c := range list {
		if c.ID == exceptContainerID {
			continue
		}
		if _, on := c.NetworkSettings.Networks[networkName]; !on {
			continue
		}
		if err := r.cli.NetworkDisconnect(ctx, networkName, c.ID, false); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("dockerrt: desconectar %s da rede %s: %w", c.ID[:12], networkName, err)
			}
			continue
		}
		demoted = append(demoted, c.ID)
	}
	return demoted, errors.Join(firstErr)
}
