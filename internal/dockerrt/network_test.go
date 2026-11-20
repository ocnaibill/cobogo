package dockerrt

import (
	"context"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/network"
)

func TestProjectNetworkName(t *testing.T) {
	if got := ProjectNetwork("abc"); got != "cobogo-proj-abc" {
		t.Errorf("ProjectNetwork = %q", got)
	}
}

func TestWithLabelsCannotReplaceTheCobogoOnes(t *testing.T) {
	var spec runSpec
	WithLabels(map[string]string{"a": "1"})(&spec)
	WithLabels(map[string]string{"b": "2", LabelManaged: "false", LabelDeployment: "forjado"})(&spec)
	if spec.labels["a"] != "1" || spec.labels["b"] != "2" {
		t.Errorf("labels = %v", spec.labels)
	}
	// A proteção está no Run, que grava os labels do Cobogó por último; o teste
	// com Docker confere o resultado.
}

func netExists(t *testing.T, rt *Runtime, name string) bool {
	t.Helper()
	_, err := rt.cli.NetworkInspect(context.Background(), name, network.InspectOptions{})
	return err == nil
}

func TestEnsureNetworkIsIdempotentAndLabelled(t *testing.T) {
	rt := dockerForTest(t)
	name := "cobogo-test-net-" + t.Name()
	t.Cleanup(func() { _ = rt.cli.NetworkRemove(context.Background(), name) })

	for i := 0; i < 3; i++ {
		if err := rt.EnsureNetwork(context.Background(), name, map[string]string{LabelProject: "proj-x"}); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	got, err := rt.cli.NetworkInspect(context.Background(), name, network.InspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Labels[LabelManaged] != "true" || got.Labels[LabelProject] != "proj-x" || got.Driver != "bridge" {
		t.Errorf("network = driver %q labels %v", got.Driver, got.Labels)
	}
}

func idleImage(t *testing.T, rt *Runtime) string {
	t.Helper()
	const tag = "cobogo-test/idle:1"
	buildTestImage(t, rt, tag, "FROM node:26-slim\nCMD [\"node\", \"-e\", \"require('http').createServer((q,s)=>s.end('ok')).listen(3000)\"]\n")
	return tag
}

// Com Docker de verdade: o contêiner roda na rede do projeto, fora do ingress;
// promover o conecta, e rebaixar tira os anteriores sem parar nada.
func TestPromoteAndDemoteMoveTrafficBetweenReleases(t *testing.T) {
	rt := dockerForTest(t)
	tag := idleImage(t, rt)
	ctx := context.Background()
	project := "projeto-teste-" + strings.ToLower(t.Name())
	projectNet, ingress := ProjectNetwork(project), "cobogo-test-ingress-"+strings.ToLower(t.Name())
	for _, n := range []string{projectNet, ingress} {
		if err := rt.EnsureNetwork(ctx, n, nil); err != nil {
			t.Fatal(err)
		}
		n := n
		t.Cleanup(func() { _ = rt.cli.NetworkRemove(context.Background(), n) })
	}

	run := func(name, deploy string) string {
		res, err := rt.Run(ctx, tag, name, 3000, nil, deploy,
			WithNetwork(projectNet),
			WithLabels(map[string]string{LabelProject: project, LabelManaged: "false", LabelDeployment: "forjado"}))
		t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
		if err != nil {
			t.Fatal(err)
		}
		return res.ContainerID
	}
	old := run("cobogo-test-old", "deploy-old")
	candidate := run("cobogo-test-new", "deploy-new")

	// Labels do Cobogó não são substituíveis; o do projeto entra.
	inspect, err := rt.cli.ContainerInspect(ctx, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if inspect.Config.Labels[LabelManaged] != "true" || inspect.Config.Labels[LabelDeployment] != "deploy-new" || inspect.Config.Labels[LabelProject] != project {
		t.Errorf("labels = %v", inspect.Config.Labels)
	}
	// Roda na rede do projeto e em nenhuma outra: nem na bridge padrão.
	if len(inspect.NetworkSettings.Networks) != 1 || !connectedTo(inspect, projectNet) {
		t.Errorf("networks = %v, want only %s", inspect.NetworkSettings.Networks, projectNet)
	}

	on := func(id, net string) bool {
		i, err := rt.cli.ContainerInspect(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return connectedTo(i, net)
	}
	if on(old, ingress) || on(candidate, ingress) {
		t.Fatal("nothing is routed before promotion")
	}

	// Promover é idempotente.
	for i := 0; i < 2; i++ {
		if err := rt.Promote(ctx, old, ingress); err != nil {
			t.Fatal(err)
		}
	}
	if err := rt.Promote(ctx, candidate, ingress); err != nil {
		t.Fatal(err)
	}
	if !on(old, ingress) || !on(candidate, ingress) {
		t.Fatal("both must be on the ingress right after promoting the candidate")
	}

	// Um contêiner de outro projeto no ingress não pode ser tocado.
	foreignRes, err := rt.Run(ctx, tag, "cobogo-test-foreign", 3000, nil, "deploy-foreign",
		WithNetwork(ProjectNetwork(project)), WithLabels(map[string]string{LabelProject: "outro-projeto"}))
	t.Cleanup(func() { _ = rt.Remove(context.Background(), foreignRes.ContainerID) })
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.Promote(ctx, foreignRes.ContainerID, ingress); err != nil {
		t.Fatal(err)
	}

	demoted, err := rt.Demote(ctx, project, candidate, ingress)
	if err != nil {
		t.Fatal(err)
	}
	if len(demoted) != 1 || demoted[0] != old {
		t.Errorf("demoted = %v, want only the old release", demoted)
	}
	if on(old, ingress) || !on(candidate, ingress) || !on(foreignRes.ContainerID, ingress) {
		t.Error("only the previous release of the same project may leave the ingress")
	}
	if running, _, _ := rt.State(ctx, old); !running {
		t.Error("demoting must not stop the previous release (RF-15 is 5c)")
	}

	// Rebaixar de novo não tem o que fazer.
	if again, err := rt.Demote(ctx, project, candidate, ingress); err != nil || len(again) != 0 {
		t.Errorf("second demote = %v, %v", again, err)
	}
}

// Conectar e desconectar o worker a uma rede é idempotente (DEC-32 item 5).
func TestConnectAndDisconnectContainerAreIdempotent(t *testing.T) {
	rt := dockerForTest(t)
	tag := idleImage(t, rt)
	ctx := context.Background()
	name := "cobogo-test-self-net"
	if err := rt.EnsureNetwork(ctx, name, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.cli.NetworkRemove(context.Background(), name) })
	res, err := rt.Run(ctx, tag, "cobogo-test-self", 3000, nil, "deploy-self")
	t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
	if err != nil {
		t.Fatal(err)
	}
	on := func() bool {
		i, err := rt.cli.ContainerInspect(ctx, res.ContainerID)
		if err != nil {
			t.Fatal(err)
		}
		return connectedTo(i, name)
	}
	for i := 0; i < 2; i++ {
		if err := rt.ConnectContainer(ctx, res.ContainerID, name); err != nil {
			t.Fatal(err)
		}
	}
	if !on() {
		t.Fatal("not connected")
	}
	for i := 0; i < 2; i++ {
		if err := rt.DisconnectContainer(ctx, res.ContainerID, name); err != nil {
			t.Fatal(err)
		}
	}
	if on() {
		t.Error("still connected")
	}
	if id, err := SelfID(); err != nil || id == "" {
		t.Errorf("SelfID = %q, %v", id, err)
	}
}
