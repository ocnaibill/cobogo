package dockerrt

import (
	"context"
	"strings"
	"testing"
	"time"
)

func waitCommand(t *testing.T, rt *Runtime, id string, within time.Duration) (int, bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		running, code, oom, err := rt.CommandState(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if !running {
			return code, oom
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("the command did not finish in time")
	return 0, false
}

// Com Docker de verdade (DEC-45): o comando roda na jaula da aplicação, devolve
// a saída e o código de saída, a saída é limitada, e só se age em contêiner de comando.
func TestCommandRunsInTheSandboxAndReportsOutputAndExitCode(t *testing.T) {
	rt := dockerForTest(t)
	tag := idleImage(t, rt)
	ctx := context.Background()
	project := "projeto-comando-" + strings.ToLower(t.Name())
	net := ProjectNetwork(project)
	if err := rt.EnsureNetwork(ctx, net, map[string]string{LabelProject: project}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.RemoveProjectNetwork(context.Background(), project) })

	start := func(id, command string, extra func(*CommandSpec)) string {
		spec := CommandSpec{ProjectID: project, CommandID: id, Image: tag, Command: command, Network: net, MemoryMB: 128, CPUs: 0.5, Pids: 64,
			Env: map[string]string{"SEGREDO": "valor-do-canario"}}
		if extra != nil {
			extra(&spec)
		}
		cid, err := rt.StartCommand(ctx, spec)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rt.RemoveCommand(context.Background(), cid) })
		return cid
	}

	// Saída, ambiente, arquivo da plataforma e código de saída.
	id := start("11111111-aaaa", `echo "oi $SEGREDO"; echo erro >&2; cat /etc/cobogo/x; id -u; exit 7`, func(s *CommandSpec) {
		s.Files = map[string][]byte{"/etc/cobogo/x": []byte("arquivo\n")}
	})
	code, oom := waitCommand(t, rt, id, 30*time.Second)
	out, cut, err := rt.CommandOutput(ctx, id, 4096)
	if err != nil || cut || code != 7 || oom {
		t.Fatalf("exit=%d oom=%v cut=%v err=%v out=%q", code, oom, cut, err, out)
	}
	for _, want := range []string{"oi valor-do-canario", "erro", "arquivo"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q misses %q", out, want)
		}
	}

	// A jaula: sem capacidades, sem porta publicada, só a rede do projeto, labels do Cobogó.
	inspect, err := rt.cli.ContainerInspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	hc := inspect.HostConfig
	if len(hc.CapDrop) != 1 || hc.CapDrop[0] != "ALL" || len(hc.CapAdd) != 0 || hc.Privileged || len(hc.SecurityOpt) != 1 || hc.SecurityOpt[0] != "no-new-privileges" {
		t.Errorf("hardening: drop=%v add=%v privileged=%v opt=%v", hc.CapDrop, hc.CapAdd, hc.Privileged, hc.SecurityOpt)
	}
	if hc.Memory != 128*mib || hc.NanoCPUs != 5e8 || hc.PidsLimit == nil || *hc.PidsLimit != 64 || hc.Binds != nil || len(hc.PortBindings) != 0 {
		t.Errorf("limits/mounts: mem=%d cpu=%d pids=%v binds=%v ports=%v", hc.Memory, hc.NanoCPUs, hc.PidsLimit, hc.Binds, hc.PortBindings)
	}
	if len(inspect.NetworkSettings.Networks) != 1 || !connectedTo(inspect, net) {
		t.Errorf("networks = %v", inspect.NetworkSettings.Networks)
	}
	if inspect.Config.Labels[LabelManaged] != "true" || inspect.Config.Labels[LabelRole] != RoleCommand || inspect.Config.Labels[LabelProject] != project {
		t.Errorf("labels = %v", inspect.Config.Labels)
	}

	// A saída enorme é cortada: cabeça e cauda, com o aviso.
	big := start("22222222-bbbb", `i=0; while [ $i -lt 4000 ]; do echo "linha-$i-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"; i=$((i+1)); done`, nil)
	waitCommand(t, rt, big, 60*time.Second)
	text, cut, err := rt.CommandOutput(ctx, big, 2048)
	if err != nil || !cut || len(text) > 2048+80 || !strings.Contains(text, "linha-0-") || !strings.Contains(text, "linha-3999-") || !strings.Contains(text, "omitidos") {
		t.Errorf("cut=%v len=%d err=%v", cut, len(text), err)
	}

	// Parar mata um comando que não termina.
	slow := start("33333333-cccc", `sleep 300`, nil)
	began := time.Now()
	if err := rt.StopCommand(ctx, slow, 2); err != nil {
		t.Fatal(err)
	}
	if running, _, _, _ := rt.CommandState(ctx, slow); running || time.Since(began) > 20*time.Second {
		t.Errorf("the command must be stopped quickly (%s)", time.Since(began))
	}

	// Só se age em contêiner de comando: a aplicação não é tocada.
	app := create(t, rt, "cobogo-test-not-a-command", map[string]string{LabelManaged: "true", LabelProject: project})
	if err := rt.StopCommand(ctx, app, 1); err == nil {
		t.Error("stopping a container that is not a command must be refused")
	}
	if err := rt.RemoveCommand(ctx, app); err == nil || !exists(t, rt, app) {
		t.Error("removing a container that is not a command must be refused")
	}
	// Os órfãos saem, e só eles.
	orphan := start("44444444-dddd", `sleep 300`, nil)
	if n, err := rt.RemoveCommandContainers(ctx, project); err != nil || n < 1 || exists(t, rt, orphan) || !exists(t, rt, app) {
		t.Errorf("orphans removed = %d, %v (orphan exists=%v, app exists=%v)", n, err, exists(t, rt, orphan), exists(t, rt, app))
	}
}
