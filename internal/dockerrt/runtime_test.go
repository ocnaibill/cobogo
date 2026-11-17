package dockerrt

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
)

// Precisam de Docker e da imagem node:26-slim; só rodam com COBOGO_TEST_DOCKER=1.
func dockerForTest(t *testing.T) *Runtime {
	t.Helper()
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes com Docker")
	}
	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := rt.EnsureImage(ctx, "node:26-slim"); err != nil {
		t.Fatal(err)
	}
	return rt
}

func create(t *testing.T, rt *Runtime, name string, labels map[string]string) string {
	t.Helper()
	ctx := context.Background()
	created, err := rt.cli.ContainerCreate(ctx, &container.Config{Image: "node:26-slim", Cmd: []string{"sleep", "300"}, Labels: labels}, nil, nil, nil, name)
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

func exists(t *testing.T, rt *Runtime, id string) bool {
	t.Helper()
	list, err := rt.cli.ContainerList(context.Background(), container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("id", id))})
	if err != nil {
		t.Fatal(err)
	}
	return len(list) > 0
}

// A retomada remove só o que o Cobogó criou para aquele deploy.
func TestRemoveByDeploymentTouchesOnlyManagedContainersOfThatDeployment(t *testing.T) {
	rt := dockerForTest(t)
	mine := map[string]string{LabelManaged: "true", LabelDeployment: "deploy-a-test"}

	running := create(t, rt, "cobogo-test-a-running", mine)
	stopped := create(t, rt, "cobogo-test-a-stopped", mine)
	_ = rt.cli.ContainerStop(context.Background(), stopped, container.StopOptions{})
	other := create(t, rt, "cobogo-test-b", map[string]string{LabelManaged: "true", LabelDeployment: "deploy-b-test"})
	foreign := create(t, rt, "cobogo-test-foreign", map[string]string{LabelDeployment: "deploy-a-test"}) // sem o label do Cobogó

	if err := rt.RemoveByDeployment(context.Background(), "deploy-a-test"); err != nil {
		t.Fatal(err)
	}
	if exists(t, rt, running) || exists(t, rt, stopped) {
		t.Error("the deployment's containers (running or stopped) must be removed")
	}
	if !exists(t, rt, other) {
		t.Error("another deployment's container was removed")
	}
	if !exists(t, rt, foreign) {
		t.Error("a container the Cobogó did not create was removed (RF-18)")
	}
	if err := rt.RemoveByDeployment(context.Background(), "deploy-a-test"); err != nil {
		t.Errorf("removing again must be a no-op: %v", err)
	}
}

func TestEnsureImage(t *testing.T) {
	rt := dockerForTest(t)
	start := time.Now()
	if err := rt.EnsureImage(context.Background(), "node:26-slim"); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("an image already in the cache must not be pulled again")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := rt.EnsureImage(ctx, "cobogo-test/imagem-que-nao-existe:0"); err == nil {
		t.Error("a missing image must be reported")
	}
}

// buildTestImage constrói uma imagem a partir de um Dockerfile mínimo.
func buildTestImage(t *testing.T, rt *Runtime, tag, dockerfile string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := rt.Build(ctx, t.TempDir(), dockerfile, tag, "node:26-slim", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = rt.cli.ImageRemove(context.Background(), tag, image.RemoveOptions{Force: true}) })
}

// Uma aplicação que cai no boot deixa o log e o código de saída à disposição.
func TestRunReturnsTheContainerWhenTheAppCrashesAtBoot(t *testing.T) {
	rt := dockerForTest(t)
	buildTestImage(t, rt, "cobogo-test/crash:1", `FROM node:26-slim
CMD ["node", "-e", "console.error('Error: DATABASE_URL is not defined'); process.exit(3)"]
`)
	res, err := rt.Run(context.Background(), "cobogo-test/crash:1", "cobogo-test-crash", 3000, nil, "deploy-crash-test")
	t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
	if res.ContainerID == "" {
		t.Fatalf("the container ID must come back even when the start fails: %v", err)
	}

	// Pode ser o daemon já ter publicado a porta antes de o processo cair; nesse
	// caso a sonda é quem acusa a saída. Os dois caminhos devem dar o mesmo.
	if err == nil {
		_, err = ProbeReady(context.Background(), res.HostPort, "/", 20*time.Second, func(c context.Context) (bool, int, error) {
			return rt.State(c, res.ContainerID)
		})
	}
	var exited *ExitedError
	if !errors.As(err, &exited) || exited.ExitCode != 3 {
		t.Fatalf("err = %v, want ExitedError with code 3", err)
	}

	logs, lerr := rt.LogTail(context.Background(), res.ContainerID, 50)
	if lerr != nil || !strings.Contains(logs, "DATABASE_URL is not defined") {
		t.Errorf("log = %q, err %v", logs, lerr)
	}
	if running, code, serr := rt.State(context.Background(), res.ContainerID); serr != nil || running || code != 3 {
		t.Errorf("state = running %v code %d err %v", running, code, serr)
	}
	if oom, err := rt.OOMKilled(context.Background(), res.ContainerID); err != nil || oom {
		t.Errorf("a plain exit(3) is not a memory kill: OOMKilled = %v, %v", oom, err)
	}
}

func TestLogTailKeepsOnlyTheLastLines(t *testing.T) {
	rt := dockerForTest(t)
	buildTestImage(t, rt, "cobogo-test/lines:1", `FROM node:26-slim
CMD ["node", "-e", "for (let i = 1; i <= 200; i++) console.log('linha ' + i)"]
`)
	res, _ := rt.Run(context.Background(), "cobogo-test/lines:1", "cobogo-test-lines", 3000, nil, "deploy-lines-test")
	t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
	time.Sleep(2 * time.Second)

	logs, err := rt.LogTail(context.Background(), res.ContainerID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs, "linha 200") || strings.Contains(logs, "linha 100\n") || len(strings.Split(strings.TrimSpace(logs), "\n")) != 50 {
		t.Errorf("tail has %d lines, first/last: %.40q", len(strings.Split(strings.TrimSpace(logs), "\n")), logs)
	}
}
