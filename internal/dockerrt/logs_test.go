package dockerrt

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

// Com Docker de verdade (DEC-46): segue stdout e stderr com a hora do Docker,
// retoma sem repetir, lê as últimas linhas e recusa o que não é de um projeto.
func TestFollowLogsReadsBothStreamsResumesAndRefusesOthers(t *testing.T) {
	rt := dockerForTest(t)
	ctx := context.Background()
	mk := func(name string, labels map[string]string, script string) string {
		created, err := rt.cli.ContainerCreate(ctx, &container.Config{Image: "node:26-slim", Cmd: []string{"sh", "-c", script}, Labels: labels}, nil, nil, nil, name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = rt.cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
		})
		if err := rt.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
			t.Fatal(err)
		}
		return created.ID
	}
	labels := map[string]string{LabelManaged: "true", LabelProject: "projeto-logs"}

	id := mk("cobogo-test-logs", labels, `echo um; echo dois >&2; sleep 2; echo tres; echo "quatro com  espaços"`)
	var mu sync.Mutex
	var got []LogEntry
	collect := func(e LogEntry) { mu.Lock(); got = append(got, e); mu.Unlock() }
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := rt.FollowLogs(cctx, id, time.Time{}, 100, true, collect); err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[0].Line != "um" || got[0].Stream != "stdout" || got[1].Line != "dois" || got[1].Stream != "stderr" || got[3].Line != "quatro com  espaços" {
		t.Fatalf("followed = %+v", got)
	}
	if got[2].At.Sub(got[0].At) < time.Second || got[0].At.IsZero() || got[0].At.Location() != time.UTC {
		t.Errorf("the Docker timestamps must be kept (UTC, 2 s apart): %v %v", got[0].At, got[2].At)
	}

	// Retomar a partir da hora da segunda linha traz só as seguintes.
	var resumed []LogEntry
	if err := rt.FollowLogs(ctx, id, got[1].At, 0, false, func(e LogEntry) { resumed = append(resumed, e) }); err != nil {
		t.Fatal(err)
	}
	if len(resumed) != 2 || resumed[0].Line != "tres" {
		t.Errorf("resumed = %+v", resumed)
	}
	// As últimas linhas, sem seguir.
	var last []LogEntry
	if err := rt.FollowLogs(ctx, id, time.Time{}, 1, false, func(e LogEntry) { last = append(last, e) }); err != nil {
		t.Fatal(err)
	}
	if len(last) != 1 || last[0].Line != "quatro com  espaços" {
		t.Errorf("tail = %+v", last)
	}

	// Sem label de projeto, ou sem o do Cobogó: recusado.
	other := mk("cobogo-test-logs-foreign", map[string]string{"x": "y"}, `echo segredo`)
	if err := rt.FollowLogs(ctx, other, time.Time{}, 10, false, collect); err == nil {
		t.Error("a container that is not a project's must be refused")
	}
	noProject := mk("cobogo-test-logs-noproject", map[string]string{LabelManaged: "true"}, `echo x`)
	if err := rt.FollowLogs(ctx, noProject, time.Time{}, 10, false, collect); err == nil {
		t.Error("a managed container without a project must be refused")
	}
}
