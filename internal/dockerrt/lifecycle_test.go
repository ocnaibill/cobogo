package dockerrt

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func TestSelectPrunableKeepsTheNewestAndTheProtected(t *testing.T) {
	now := time.Now()
	imgs := []ProjectImage{
		{ID: "old", Created: now.Add(-4 * time.Hour)},
		{ID: "newest", Created: now},
		{ID: "active-but-old", Created: now.Add(-3 * time.Hour)},
		{ID: "previous", Created: now.Add(-time.Hour)},
		{ID: "older", Created: now.Add(-2 * time.Hour)},
	}
	got := SelectPrunable(imgs, 2, map[string]bool{"active-but-old": true})
	var ids []string
	for _, i := range got {
		ids = append(ids, i.ID)
	}
	// Ficam 2 no total: a protegida e a mais nova; o resto sai, da mais antiga.
	if want := "old,older,previous"; strings.Join(ids, ",") != want {
		t.Errorf("prunable = %v, want %s", ids, want)
	}
	if got := SelectPrunable(imgs, 10, nil); len(got) != 0 {
		t.Errorf("keeping more than exist prunes nothing, got %v", got)
	}
	if got := SelectPrunable(nil, 2, nil); len(got) != 0 {
		t.Errorf("no images, nothing to prune, got %v", got)
	}
}

func TestMemoryInUseDiscountsTheFileCache(t *testing.T) {
	for name, c := range map[string]struct {
		m    container.MemoryStats
		want int64
	}{
		"cgroup v2":                         {container.MemoryStats{Usage: 300, Stats: map[string]uint64{"inactive_file": 100}}, 200},
		"cgroup v1":                         {container.MemoryStats{Usage: 300, Stats: map[string]uint64{"total_inactive_file": 50}}, 250},
		"old cache":                         {container.MemoryStats{Usage: 300, Stats: map[string]uint64{"cache": 10}}, 290},
		"no stats":                          {container.MemoryStats{Usage: 300}, 300},
		"cache > usage never goes negative": {container.MemoryStats{Usage: 10, Stats: map[string]uint64{"inactive_file": 99}}, 10},
	} {
		if got := memoryInUse(c.m); got != c.want {
			t.Errorf("%s: memoryInUse = %d, want %d", name, got, c.want)
		}
	}
}

func TestBelowUsageErrorSaysWhatHappened(t *testing.T) {
	msg := (&ErrBelowUsage{UsageMB: 300, RequestedMB: 128}).Error()
	for _, want := range []string{"300 MiB", "128 MiB", "nada foi alterado"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
}

// Com Docker real (COBOGO_TEST_DOCKER=1).

func TestListManagedAndRetireTouchOnlyManagedContainers(t *testing.T) {
	rt := dockerForTest(t)
	ctx := context.Background()
	mine := create(t, rt, "cobogo-test-life-mine", map[string]string{LabelManaged: "true", LabelDeployment: "dep-life-1", LabelProject: "proj-life-test"})
	other := create(t, rt, "cobogo-test-life-other", map[string]string{LabelManaged: "true", LabelDeployment: "dep-life-2", LabelProject: "proj-life-other"})
	foreign := create(t, rt, "cobogo-test-life-foreign", map[string]string{LabelProject: "proj-life-test"}) // sem o label do Cobogó

	list, err := rt.ListManaged(ctx, "proj-life-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != mine || list[0].DeploymentID != "dep-life-1" || !list[0].Running || list[0].ProjectID != "proj-life-test" {
		t.Errorf("list for the project = %+v, want only the managed one", list)
	}
	all, _ := rt.ListManaged(ctx, "")
	seen := map[string]bool{}
	for _, c := range all {
		seen[c.ID] = true
	}
	if !seen[mine] || !seen[other] || seen[foreign] {
		t.Errorf("ListManaged(all) must include managed containers and exclude foreign ones: %v", seen)
	}

	if err := rt.RetireContainer(ctx, foreign); err == nil {
		t.Error("retiring a container the Cobogó did not create must be refused (RF-18)")
	}
	if !exists(t, rt, foreign) {
		t.Error("the foreign container must still exist")
	}
	if err := rt.RetireContainer(ctx, mine); err != nil {
		t.Fatal(err)
	}
	if exists(t, rt, mine) {
		t.Error("the managed container must be gone")
	}
	if !exists(t, rt, other) {
		t.Error("another project's container was touched")
	}
	if err := rt.RetireContainer(ctx, mine); err != nil {
		t.Errorf("retiring what is already gone is a no-op: %v", err)
	}
}

func TestRetireTakesTheContainerOutOfTheIngress(t *testing.T) {
	rt := dockerForTest(t)
	ctx := context.Background()
	if err := rt.EnsureNetwork(ctx, "cobogo-test-ingress", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.cli.NetworkRemove(ctx, "cobogo-test-ingress") })
	id := create(t, rt, "cobogo-test-life-ingress", map[string]string{LabelManaged: "true", LabelDeployment: "dep-ing", LabelProject: "proj-ing"})
	if err := rt.ConnectContainer(ctx, id, "cobogo-test-ingress"); err != nil {
		t.Fatal(err)
	}
	if err := rt.RetireContainer(ctx, id); err != nil {
		t.Fatal(err)
	}
	if exists(t, rt, id) {
		t.Error("the container must be gone")
	}
}

func TestRemoveProjectNetworkOnlyRemovesTheProjectsOwn(t *testing.T) {
	rt := dockerForTest(t)
	ctx := context.Background()
	pid := "proj-net-test"
	if err := rt.EnsureNetwork(ctx, ProjectNetwork(pid), map[string]string{LabelProject: pid}); err != nil {
		t.Fatal(err)
	}
	if err := rt.RemoveProjectNetwork(ctx, pid); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.cli.NetworkInspect(ctx, ProjectNetwork(pid), network.InspectOptions{}); err == nil {
		t.Error("the project network must be gone")
	}
	if err := rt.RemoveProjectNetwork(ctx, pid); err != nil {
		t.Errorf("removing a missing network is a no-op: %v", err)
	}

	// Uma rede com o nome do projeto, mas sem os labels do Cobogó, não é tocada.
	alien := "proj-net-alien"
	if _, err := rt.cli.NetworkCreate(ctx, ProjectNetwork(alien), network.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.cli.NetworkRemove(ctx, ProjectNetwork(alien)) })
	if err := rt.RemoveProjectNetwork(ctx, alien); err == nil {
		t.Error("a network without the Cobogó labels must be refused")
	}
}

func TestUpdateLimitsOnALiveContainerAndRefusesToKillIt(t *testing.T) {
	rt := dockerForTest(t)
	ctx := context.Background()
	buildTestImage(t, rt, "cobogo-test/limits:1", `FROM node:26-slim
CMD ["node", "-e", "const b = Buffer.alloc(80*1024*1024, 1); setInterval(()=>{}, 1000)"]
`)
	res, err := rt.Run(ctx, "cobogo-test/limits:1", "cobogo-test-limits", 3000, nil, "dep-limits-test",
		WithLabels(map[string]string{LabelProject: "proj-limits"}))
	t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second) // deixa a aplicação ocupar a memória

	before, after, err := rt.UpdateLimits(ctx, res.ContainerID, domain.Limits{AppMemoryMB: 768, AppCPUs: 0.5, AppPids: 100})
	if err != nil {
		t.Fatal(err)
	}
	if before.MemoryMB != 1024 || after.MemoryMB != 768 || after.CPUs != 0.5 || after.Pids != 100 {
		t.Errorf("before = %+v, after = %+v; want 1024 -> 768 MiB, 0.5 CPUs, 100 pids", before, after)
	}
	if running, _, _ := rt.State(ctx, res.ContainerID); !running {
		t.Error("updating limits must not restart or stop the application")
	}
	// O swap acompanha a memória: sem swap extra (DEC-29).
	inspect, _ := rt.cli.ContainerInspect(ctx, res.ContainerID)
	if inspect.HostConfig.MemorySwap != inspect.HostConfig.Memory {
		t.Errorf("swap = %d, memory = %d; swap must equal memory", inspect.HostConfig.MemorySwap, inspect.HostConfig.Memory)
	}

	// Abaixo do uso atual: recusa e não muda nada.
	_, still, err := rt.UpdateLimits(ctx, res.ContainerID, domain.Limits{AppMemoryMB: 16, AppCPUs: 0.5, AppPids: 100})
	var below *ErrBelowUsage
	if !errors.As(err, &below) {
		t.Fatalf("shrinking below the usage = %v, want ErrBelowUsage", err)
	}
	if still.MemoryMB != 768 {
		t.Errorf("a refused update must leave the limit alone, got %d MiB", still.MemoryMB)
	}
	if running, _, _ := rt.State(ctx, res.ContainerID); !running {
		t.Error("a refused update must leave the application running")
	}

	// Um contêiner que não é do Cobogó nunca é tocado.
	foreign := create(t, rt, "cobogo-test-limits-foreign", nil)
	if _, _, err := rt.UpdateLimits(ctx, foreign, domain.DefaultLimits()); err == nil {
		t.Error("updating a container the Cobogó did not create must be refused")
	}
}

func TestProjectImagesAreFoundByLabelAndPrunedSafely(t *testing.T) {
	rt := dockerForTest(t)
	ctx := context.Background()
	labels := map[string]string{LabelManaged: "true", LabelProject: "proj-img-test"}
	build := func(tag, marker string, lbl map[string]string) {
		t.Helper()
		dockerfile := "FROM node:26-slim\nRUN echo " + marker + " > /marker\nCMD [\"sleep\", \"300\"]\n"
		bctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		if _, err := rt.BuildWithArgs(bctx, t.TempDir(), dockerfile, tag, "node:26-slim", nil, lbl, nil); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rt.RemoveImage(context.Background(), ProjectImage{ID: tag, Tags: []string{tag}}) })
	}
	build("cobogo-test/img:a", "a", labels)
	time.Sleep(1100 * time.Millisecond)
	build("cobogo-test/img:b", "b", labels)
	build("cobogo-test/img-foreign:1", "f", map[string]string{LabelProject: "proj-img-test"}) // sem o label gerenciado

	imgs, err := rt.ListProjectImages(ctx, "proj-img-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(imgs) != 2 {
		t.Fatalf("project images = %+v, want exactly the 2 managed ones", imgs)
	}
	prunable := SelectPrunable(imgs, 1, nil)
	if len(prunable) != 1 || prunable[0].Tags[0] != "cobogo-test/img:a" {
		t.Fatalf("prunable = %+v, want the older image a", prunable)
	}
	if err := rt.RemoveImage(ctx, prunable[0]); err != nil {
		t.Fatal(err)
	}
	left, _ := rt.ListProjectImages(ctx, "proj-img-test")
	if len(left) != 1 || left[0].Tags[0] != "cobogo-test/img:b" {
		t.Errorf("after pruning = %+v, want only b", left)
	}

	// Uma imagem em uso não sai.
	run, err := rt.Run(ctx, "cobogo-test/img:b", "cobogo-test-img-use", 3000, nil, "dep-img-use")
	t.Cleanup(func() { _ = rt.Remove(context.Background(), run.ContainerID) })
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.RemoveImage(ctx, left[0]); !errors.Is(err, ErrImageInUse) {
		t.Errorf("removing an image in use = %v, want ErrImageInUse", err)
	}
}
