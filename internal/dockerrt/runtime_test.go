package dockerrt

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
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
