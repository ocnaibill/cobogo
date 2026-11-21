package dockerrt

import (
	"context"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func TestCPUCoresIsTheShareOfSystemTimeTimesTheCores(t *testing.T) {
	stats := func(total, preTotal, sys, preSys uint64, online uint32) container.StatsResponse {
		var s container.StatsResponse
		s.CPUStats.CPUUsage.TotalUsage, s.PreCPUStats.CPUUsage.TotalUsage = total, preTotal
		s.CPUStats.SystemUsage, s.PreCPUStats.SystemUsage = sys, preSys
		s.CPUStats.OnlineCPUs = online
		return s
	}
	// Meio núcleo de CPU em 4 núcleos: o contêiner usou 1/8 do tempo do sistema.
	if got, ok := cpuCores(stats(2_000_000, 1_000_000, 18_000_000, 10_000_000, 4)); !ok || got != 0.5 {
		t.Errorf("cpuCores = %v, %v; want 0.5, true", got, ok)
	}
	// Sem leitura anterior ou sem variação do sistema, não há valor (nunca zero).
	for name, s := range map[string]container.StatsResponse{
		"no previous reading": stats(5, 0, 100, 0, 2),
		"no system delta":     stats(5, 1, 100, 100, 2),
		"counter went back":   stats(1, 5, 200, 100, 2),
	} {
		if got, ok := cpuCores(s); ok {
			t.Errorf("%s: cpuCores = %v, true; want no value", name, got)
		}
	}
}

// Amostra de verdade: só contêineres do Cobogó, com limite, memória e CPU; o
// parado vem sem leitura, o alheio não entra (COBOGO_TEST_DOCKER=1).
func TestSampleManagedReadsOnlyManagedContainers(t *testing.T) {
	rt := dockerForTest(t)
	ctx := context.Background()
	labels := map[string]string{LabelManaged: "true", LabelProject: "11111111-2222-3333-4444-555555555555", LabelDeployment: "66666666-7777-8888-9999-000000000000"}
	spawn := func(name string, lab map[string]string, cmd []string, host *container.HostConfig) string {
		c, err := rt.cli.ContainerCreate(ctx, &container.Config{Image: "node:26-slim", Cmd: cmd, Labels: lab}, host, nil, nil, name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rt.cli.ContainerRemove(context.Background(), c.ID, container.RemoveOptions{Force: true}) })
		return c.ID
	}
	limits := &container.HostConfig{Resources: container.Resources{Memory: 256 << 20, MemorySwap: 256 << 20, NanoCPUs: 500_000_000}}
	busy := spawn("cobogo-test-sample-busy", labels, []string{"sh", "-c", "while :; do :; done"}, limits)
	stopped := spawn("cobogo-test-sample-stopped", labels, []string{"true"}, nil)
	foreign := spawn("cobogo-test-sample-foreign", nil, []string{"sleep", "60"}, nil)
	for _, id := range []string{busy, stopped, foreign} {
		if err := rt.cli.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(3 * time.Second) // o contêiner parado termina; o ocupado ganha CPU para medir

	samples, errs := rt.SampleManaged(ctx, 4)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	by := map[string]Sample{}
	for _, s := range samples {
		by[s.ContainerID] = s
	}
	if _, ok := by[foreign]; ok {
		t.Error("a container without the Cobogó label must never be sampled")
	}
	b, ok := by[busy]
	if !ok || b.State != "running" || b.Role != "app" || b.ProjectID != labels[LabelProject] || b.DeploymentID != labels[LabelDeployment] {
		t.Fatalf("busy = %+v", b)
	}
	if b.CPUCores == nil || *b.CPUCores <= 0.05 || *b.CPUCores > 0.6 {
		t.Errorf("a busy loop limited to 0.5 CPU must read between 0.05 and 0.6 cores, got %v", b.CPUCores)
	}
	if b.CPULimitCores == nil || *b.CPULimitCores != 0.5 || b.MemLimitBytes == nil || *b.MemLimitBytes != 256<<20 {
		t.Errorf("limits = %v / %v", b.CPULimitCores, b.MemLimitBytes)
	}
	if b.MemBytes == nil || *b.MemBytes <= 0 || *b.MemBytes > 256<<20 {
		t.Errorf("memory = %v", b.MemBytes)
	}
	s, ok := by[stopped]
	if !ok || s.State == "running" || s.CPUCores != nil || s.MemBytes != nil {
		t.Errorf("a stopped container has a state and no reading, never zeros: %+v", s)
	}
}
