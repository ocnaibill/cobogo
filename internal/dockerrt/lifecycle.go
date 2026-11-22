package dockerrt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/errdefs"
	"github.com/docker/go-connections/nat"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Ciclo de vida de contêineres, redes e imagens (DEC-38). Tudo aqui só toca o
// que tem os labels do Cobogó (RF-18).

// ManagedContainer é um contêiner criado pelo Cobogó.
type ManagedContainer struct {
	ID           string
	Name         string
	DeploymentID string
	ProjectID    string
	// Role é o papel de um contêiner auxiliar e passageiro (comando pontual, posse de
	// volume, certificado); vazio nas aplicações e nos bancos.
	Role      string
	Created   time.Time
	Running   bool
	InIngress bool
}

func toManaged(c types.Container) ManagedContainer {
	name := ""
	if len(c.Names) > 0 {
		name = c.Names[0]
		if len(name) > 0 && name[0] == '/' {
			name = name[1:]
		}
	}
	_, inIngress := c.NetworkSettings.Networks[IngressNetwork]
	return ManagedContainer{
		ID: c.ID, Name: name, DeploymentID: c.Labels[LabelDeployment], ProjectID: c.Labels[LabelProject], Role: c.Labels[LabelRole],
		Created: time.Unix(c.Created, 0).UTC(), Running: c.State == "running", InIngress: inIngress,
	}
}

// ListManaged lista os contêineres do Cobogó, de todos os projetos, com ou sem
// o filtro de projeto (projectID vazio lista todos).
func (r *Runtime) ListManaged(ctx context.Context, projectID string) ([]ManagedContainer, error) {
	args := filters.NewArgs(filters.Arg("label", LabelManaged+"=true"))
	if projectID != "" {
		args.Add("label", LabelProject+"="+projectID)
	}
	list, err := r.cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, fmt.Errorf("dockerrt: listar contêineres gerenciados: %w", err)
	}
	out := make([]ManagedContainer, 0, len(list))
	for _, c := range list {
		out = append(out, toManaged(c))
	}
	return out, nil
}

// RetireContainer tira o contêiner do ingress, o para e o remove. Só age em
// contêiner com o label do Cobogó: o ID de qualquer outro é recusado.
func (r *Runtime) RetireContainer(ctx context.Context, containerID string) error {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("dockerrt: inspecionar contêiner: %w", err)
	}
	if inspect.Config == nil || inspect.Config.Labels[LabelManaged] != "true" {
		return fmt.Errorf("dockerrt: o contêiner %s não é gerenciado pelo Cobogó", shortID(containerID))
	}
	// O ingress primeiro: sem ele o Traefik já não alcança o contêiner.
	if err := r.DisconnectContainer(ctx, containerID, IngressNetwork); err != nil && !errdefs.IsNotFound(err) {
		return err
	}
	return r.Remove(ctx, containerID)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// RemoveProjectNetwork remove a rede de um projeto, se existir e se for dele.
func (r *Runtime) RemoveProjectNetwork(ctx context.Context, projectID string) error {
	name := ProjectNetwork(projectID)
	inspect, err := r.cli.NetworkInspect(ctx, name, networkInspectOptions)
	if errdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("dockerrt: inspecionar rede %s: %w", name, err)
	}
	if inspect.Labels[LabelManaged] != "true" || inspect.Labels[LabelProject] != projectID {
		return fmt.Errorf("dockerrt: a rede %s não é deste projeto no Cobogó", name)
	}
	if err := r.cli.NetworkRemove(ctx, inspect.ID); err != nil && !errdefs.IsNotFound(err) {
		return fmt.Errorf("dockerrt: remover rede %s: %w", name, err)
	}
	return nil
}

// ProjectImage é uma imagem que o Cobogó construiu para um projeto.
type ProjectImage struct {
	ID      string
	Tags    []string
	Created time.Time
}

// ListProjectImages lista as imagens rotuladas com o projeto (DEC-38 item 3).
func (r *Runtime) ListProjectImages(ctx context.Context, projectID string) ([]ProjectImage, error) {
	list, err := r.cli.ImageList(ctx, image.ListOptions{Filters: filters.NewArgs(
		filters.Arg("label", LabelManaged+"=true"),
		filters.Arg("label", LabelProject+"="+projectID),
	)})
	if err != nil {
		return nil, fmt.Errorf("dockerrt: listar imagens do projeto: %w", err)
	}
	out := make([]ProjectImage, 0, len(list))
	for _, img := range list {
		out = append(out, ProjectImage{ID: img.ID, Tags: img.RepoTags, Created: time.Unix(img.Created, 0).UTC()})
	}
	return out, nil
}

// ErrImageInUse indica uma imagem que um contêiner ainda usa.
var ErrImageInUse = errors.New("dockerrt: a imagem está em uso")

// RemoveImage remove uma imagem do projeto: primeiro cada tag, depois a imagem.
// Uma imagem em uso devolve ErrImageInUse e fica.
func (r *Runtime) RemoveImage(ctx context.Context, img ProjectImage) error {
	for _, ref := range append(slices.Clone(img.Tags), img.ID) {
		_, err := r.cli.ImageRemove(ctx, ref, image.RemoveOptions{PruneChildren: true})
		switch {
		case err == nil, errdefs.IsNotFound(err):
		case errdefs.IsConflict(err):
			return ErrImageInUse
		default:
			return fmt.Errorf("dockerrt: remover imagem %s: %w", ref, err)
		}
	}
	return nil
}

// SelectPrunable escolhe quais imagens remover. Ficam, no total, keep imagens:
// as protegidas (a da release ativa) sempre ficam e contam primeiro, e o resto
// da cota vai para as mais recentes. Devolve o que sobra, da mais antiga para a
// mais nova (DEC-38 item 3).
func SelectPrunable(images []ProjectImage, keep int, protected map[string]bool) []ProjectImage {
	sorted := slices.Clone(images)
	slices.SortFunc(sorted, func(a, b ProjectImage) int { return b.Created.Compare(a.Created) })
	kept := 0
	for _, img := range sorted {
		if protected[img.ID] {
			kept++
		}
	}
	var out []ProjectImage
	for _, img := range sorted {
		switch {
		case protected[img.ID]:
		case kept < keep:
			kept++
		default:
			out = append(out, img)
		}
	}
	slices.Reverse(out)
	return out
}

// Resources são os limites de um contêiner de aplicação como o daemon os tem.
type Resources struct {
	MemoryMB int     `json:"memory_mb"`
	CPUs     float64 `json:"cpus"`
	Pids     int     `json:"pids"`
}

// CurrentResources lê os limites de um contêiner gerenciado.
func (r *Runtime) CurrentResources(ctx context.Context, containerID string) (Resources, error) {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return Resources{}, fmt.Errorf("dockerrt: inspecionar contêiner: %w", err)
	}
	if inspect.Config == nil || inspect.Config.Labels[LabelManaged] != "true" {
		return Resources{}, fmt.Errorf("dockerrt: o contêiner %s não é gerenciado pelo Cobogó", shortID(containerID))
	}
	res := Resources{MemoryMB: int(inspect.HostConfig.Memory / mib), CPUs: float64(inspect.HostConfig.NanoCPUs) / 1e9}
	if inspect.HostConfig.PidsLimit != nil {
		res.Pids = int(*inspect.HostConfig.PidsLimit)
	}
	return res, nil
}

// MemoryUsage é a memória em uso do contêiner, em bytes, sem o cache de
// arquivos que o kernel devolve sob pressão (a mesma conta do docker stats).
func (r *Runtime) MemoryUsage(ctx context.Context, containerID string) (int64, error) {
	resp, err := r.cli.ContainerStatsOneShot(ctx, containerID)
	if err != nil {
		return 0, fmt.Errorf("dockerrt: ler uso de memória: %w", err)
	}
	defer resp.Body.Close()
	var stats container.StatsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&stats); err != nil {
		return 0, fmt.Errorf("dockerrt: decodificar estatísticas: %w", err)
	}
	return memoryInUse(stats.MemoryStats), nil
}

// memoryInUse desconta o cache: inactive_file no cgroup v2, total_inactive_file
// ou cache no v1.
func memoryInUse(m container.MemoryStats) int64 {
	cache := uint64(0)
	for _, key := range []string{"inactive_file", "total_inactive_file", "cache"} {
		if v, ok := m.Stats[key]; ok {
			cache = v
			break
		}
	}
	if cache > m.Usage {
		return int64(m.Usage)
	}
	return int64(m.Usage - cache)
}

// ErrBelowUsage recusa reduzir a memória abaixo do que o contêiner usa agora.
type ErrBelowUsage struct{ UsageMB, RequestedMB int }

func (e *ErrBelowUsage) Error() string {
	return fmt.Sprintf("a aplicação usa %d MiB agora e o novo limite seria %d MiB; reduzir mataria a aplicação, então nada foi alterado", e.UsageMB, e.RequestedMB)
}

// UpdateLimits aplica os limites a um contêiner vivo, sem reiniciá-lo
// (DEC-38 item 6): memória e swap juntos (sem swap, DEC-29), CPUs e processos.
// Recusa reduzir a memória abaixo do uso atual. Devolve os limites de antes e
// os de depois.
func (r *Runtime) UpdateLimits(ctx context.Context, containerID string, l domain.Limits) (before, after Resources, err error) {
	before, err = r.CurrentResources(ctx, containerID)
	if err != nil {
		return Resources{}, Resources{}, err
	}
	if l.AppMemoryMB > 0 && (before.MemoryMB == 0 || l.AppMemoryMB < before.MemoryMB) {
		usage, uerr := r.MemoryUsage(ctx, containerID)
		if uerr != nil {
			return before, before, uerr
		}
		if usageMB := int((usage + mib - 1) / mib); l.AppMemoryMB > 0 && l.AppMemoryMB < usageMB {
			return before, before, &ErrBelowUsage{UsageMB: usageMB, RequestedMB: l.AppMemoryMB}
		}
	}
	memory := int64(l.AppMemoryMB) * mib
	pids := int64(l.AppPids)
	update := container.UpdateConfig{Resources: container.Resources{
		Memory: memory, MemorySwap: memory, NanoCPUs: int64(l.AppCPUs * 1e9), PidsLimit: &pids,
	}}
	// Quando o limite de memória sobe, o swap tem de subir antes ou junto; o
	// daemon aceita os dois no mesmo pedido. Ao descer, também.
	if _, err := r.cli.ContainerUpdate(ctx, containerID, update); err != nil {
		return before, before, fmt.Errorf("dockerrt: atualizar limites: %w", err)
	}
	after, err = r.CurrentResources(ctx, containerID)
	if err != nil {
		return before, Resources{}, err
	}
	return before, after, nil
}

// managedInspect inspeciona um contêiner e recusa o que não é gerenciado pelo
// Cobogó: o ID de qualquer outro contêiner nunca chega a uma ação (RF-18).
func (r *Runtime) managedInspect(ctx context.Context, containerID string) (types.ContainerJSON, error) {
	inspect, err := r.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return types.ContainerJSON{}, fmt.Errorf("dockerrt: inspecionar contêiner: %w", err)
	}
	if inspect.Config == nil || inspect.Config.Labels[LabelManaged] != "true" {
		return types.ContainerJSON{}, fmt.Errorf("dockerrt: o contêiner %s não é gerenciado pelo Cobogó", shortID(containerID))
	}
	return inspect, nil
}

// StopContainer para o contêiner e o **mantém** (DEC-40): imagem, configuração e
// redes continuam, então iniciar de novo é rápido. Parar o que já está parado
// não é erro.
func (r *Runtime) StopContainer(ctx context.Context, containerID string) error {
	if _, err := r.managedInspect(ctx, containerID); err != nil {
		return err
	}
	timeout := 10
	if err := r.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil && !errdefs.IsNotModified(err) {
		return fmt.Errorf("dockerrt: parar contêiner: %w", err)
	}
	return nil
}

// Started é o que se sabe de um contêiner recém-iniciado.
type Started struct {
	// Name é o nome do contêiner, o que a sonda por rede usa.
	Name string
	// HostPort é a porta de host em que o daemon o publicou agora: a porta
	// efêmera pode mudar quando um contêiner é iniciado de novo.
	HostPort int
}

// StartContainer inicia um contêiner gerenciado que estava parado. Iniciar o
// que já está no ar não é erro.
func (r *Runtime) StartContainer(ctx context.Context, containerID string, containerPort int) (Started, error) {
	if _, err := r.managedInspect(ctx, containerID); err != nil {
		return Started{}, err
	}
	if err := r.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil && !errdefs.IsNotModified(err) {
		return Started{}, fmt.Errorf("dockerrt: iniciar contêiner: %w", err)
	}
	inspect, err := r.managedInspect(ctx, containerID)
	if err != nil {
		return Started{}, err
	}
	portSpec := nat.Port(strconv.Itoa(containerPort) + "/tcp")
	bindings := inspect.NetworkSettings.Ports[portSpec]
	if len(bindings) == 0 {
		if inspect.State != nil && !inspect.State.Running {
			return Started{}, &ExitedError{ExitCode: inspect.State.ExitCode}
		}
		return Started{}, fmt.Errorf("dockerrt: porta %s não foi publicada pelo daemon", portSpec)
	}
	hostPort, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return Started{}, fmt.Errorf("dockerrt: porta de host inválida: %w", err)
	}
	return Started{Name: strings.TrimPrefix(inspect.Name, "/"), HostPort: hostPort}, nil
}
