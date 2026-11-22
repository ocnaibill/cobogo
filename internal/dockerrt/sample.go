package dockerrt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/errdefs"
)

// Leitura de amostras para o coletor de métricas (DEC-43). Só chamadas de
// leitura: lista, inspeciona e pede estatísticas, o que o proxy só de leitura
// deixa passar.

// Sample é o que se sabe de um contêiner do Cobogó em um instante. CPUCores e
// MemBytes são nulos quando não há leitura (contêiner parado): ausência de dado
// não vira zero.
type Sample struct {
	ContainerID  string
	Name         string
	ProjectID    string
	DeploymentID string
	// Role é "app" (aplicação de um deploy) ou "service" (banco do projeto).
	Role     string
	Service  string
	State    string
	Health   string
	Restarts int
	OOM      bool
	Started  *time.Time

	CPUCores      *float64
	CPULimitCores *float64
	MemBytes      *int64
	MemLimitBytes *int64
}

// SampleManaged lê todos os contêineres do Cobogó e devolve uma amostra de cada
// um. Os rodando são medidos em paralelo (no máximo `parallel`), porque cada
// leitura de estatísticas leva cerca de um segundo. Um contêiner que falha ao ser
// lido volta sem medida (e entra em `errs`); nada derruba a rodada.
func (r *Runtime) SampleManaged(ctx context.Context, parallel int) (samples []Sample, errs []error) {
	list, err := r.ListManaged(ctx, "")
	if err != nil {
		return nil, []error{err}
	}
	if parallel < 1 {
		parallel = 1
	}
	type result struct {
		s   Sample
		err error
	}
	// Os passageiros (comando, posse de volume) não são aplicações: ficam fora da medição.
	kept := list[:0]
	for _, c := range list {
		if c.Role == "" {
			kept = append(kept, c)
		}
	}
	list = kept
	out := make([]result, len(list))
	sem := make(chan struct{}, parallel)
	done := make(chan struct{}, len(list))
	for i, c := range list {
		go func(i int, id string) {
			sem <- struct{}{}
			defer func() { <-sem; done <- struct{}{} }()
			s, err := r.sampleOne(ctx, id)
			out[i] = result{s, err}
		}(i, c.ID)
	}
	for range list {
		<-done
	}
	for _, res := range out {
		if res.err != nil {
			if !errdefs.IsNotFound(res.err) { // sumiu no meio da rodada: não é erro
				errs = append(errs, res.err)
			}
			continue
		}
		samples = append(samples, res.s)
	}
	return samples, errs
}

func (r *Runtime) sampleOne(ctx context.Context, id string) (Sample, error) {
	inspect, err := r.cli.ContainerInspect(ctx, id)
	if err != nil {
		return Sample{}, err
	}
	s := Sample{ContainerID: inspect.ID, Name: strings.TrimPrefix(inspect.Name, "/")}
	if inspect.Config != nil {
		s.ProjectID = inspect.Config.Labels[LabelProject]
		s.DeploymentID = inspect.Config.Labels[LabelDeployment]
		s.Service = inspect.Config.Labels[LabelService]
	}
	s.Role = "app"
	if s.Service != "" {
		s.Role = "service"
	}
	if st := inspect.State; st != nil {
		s.State = st.Status
		s.Restarts = inspect.RestartCount
		s.OOM = st.OOMKilled
		if st.Health != nil {
			s.Health = st.Health.Status
		}
		if t, err := time.Parse(time.RFC3339Nano, st.StartedAt); err == nil && !t.IsZero() && t.Year() > 1 {
			s.Started = &t
		}
	}
	if hc := inspect.HostConfig; hc != nil {
		if hc.NanoCPUs > 0 {
			v := float64(hc.NanoCPUs) / 1e9
			s.CPULimitCores = &v
		}
		if hc.Memory > 0 {
			v := hc.Memory
			s.MemLimitBytes = &v
		}
	}
	if s.State != "running" {
		return s, nil
	}

	// stream=false devolve o instante e o anterior, então o CPU dá para calcular
	// sem guardar estado.
	resp, err := r.cli.ContainerStats(ctx, id, false)
	if err != nil {
		return s, err
	}
	defer resp.Body.Close()
	var stats container.StatsResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&stats); err != nil {
		return s, fmt.Errorf("dockerrt: ler estatísticas de %s: %w", shortID(id), err)
	}
	if cores, ok := cpuCores(stats); ok {
		s.CPUCores = &cores
	}
	mem := memoryInUse(stats.MemoryStats)
	s.MemBytes = &mem
	if s.MemLimitBytes == nil && stats.MemoryStats.Limit > 0 && stats.MemoryStats.Limit < 1<<50 {
		v := int64(stats.MemoryStats.Limit)
		s.MemLimitBytes = &v
	}
	return s, nil
}

// cpuCores é o CPU em núcleos usados entre as duas leituras do daemon: a
// variação do tempo de CPU do contêiner sobre a do sistema, vezes os núcleos
// disponíveis (a conta do docker stats). Sem leitura anterior, não há valor.
func cpuCores(s container.StatsResponse) (float64, bool) {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if s.PreCPUStats.CPUUsage.TotalUsage == 0 || sysDelta <= 0 || cpuDelta < 0 {
		return 0, false
	}
	online := float64(s.CPUStats.OnlineCPUs)
	if online == 0 {
		online = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if online == 0 {
		online = 1
	}
	return cpuDelta / sysDelta * online, true
}

// CPUCoresOf expõe a conta aos testes de outro pacote.
func CPUCoresOf(s container.StatsResponse) (float64, bool) { return cpuCores(s) }
