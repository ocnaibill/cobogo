package dockerrt

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Um processo além do web (DEC-51) com Docker de verdade: sem porta publicada, só na rede indicada,
// com o label de processo, listado pelo deploy, e a janela de estabilidade distingue o que fica de pé
// do que sai.
func TestRunProcessAndWaitStable(t *testing.T) {
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes com Docker")
	}
	rt, err := New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := rt.EnsureImage(ctx, UtilityImage); err != nil {
		t.Fatal(err)
	}
	const dep = "dep-process-teste"
	netName := "cobogo-test-process-net"
	if err := rt.EnsureNetwork(ctx, netName, map[string]string{LabelProject: "proc-teste"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = rt.RemoveByDeployment(context.Background(), dep)
		_ = exec.Command("docker", "network", "rm", netName).Run()
	})
	opts := []RunOption{WithNetwork(netName), WithLabels(map[string]string{LabelProject: "proc-teste"})}

	// Um worker que fica de pé.
	id, err := rt.RunProcess(ctx, UtilityImage, "cobogo-test-process-worker", "exec sleep 600", map[string]string{"X": "1"}, dep, "worker", opts...)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.WaitStable(ctx, id, 2*time.Second); err != nil {
		t.Errorf("um processo de pé não passou na janela: %v", err)
	}
	// Um que sai logo falha, com o código de saída.
	bad, err := rt.RunProcess(ctx, UtilityImage, "cobogo-test-process-bad", "echo falhou >&2; exit 3", nil, dep, "scheduler", opts...)
	if err != nil {
		t.Fatal(err)
	}
	var exited *ExitedError
	if err := rt.WaitStable(ctx, bad, 5*time.Second); !errors.As(err, &exited) || exited.ExitCode != 3 {
		t.Errorf("um processo que sai deveria falhar com o código 3: %v", err)
	}

	list, err := rt.ListProcesses(ctx, dep)
	if err != nil || len(list) != 2 {
		t.Fatalf("ListProcesses = %+v %v", list, err)
	}
	names := map[string]bool{}
	for _, c := range list {
		names[c.Process] = true
	}
	if !names["worker"] || !names["scheduler"] {
		t.Errorf("processos = %v", names)
	}
	// Sem porta publicada nem ingress, e com o label do deploy (a aposentadoria os acha por ele).
	if out, _ := exec.Command("docker", "port", id).CombinedOutput(); strings.TrimSpace(string(out)) != "" {
		t.Errorf("o processo publicou uma porta: %s", out)
	}
	for _, c := range list {
		if c.InIngress || c.DeploymentID != dep {
			t.Errorf("processo %+v", c)
		}
	}
	// Parar e iniciar: o contêiner é mantido.
	if err := rt.StopContainer(ctx, id); err != nil {
		t.Fatal(err)
	}
	if running, _, _ := rt.State(ctx, id); running {
		t.Error("o processo parado segue rodando")
	}
	if err := rt.StartProcess(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := rt.WaitStable(ctx, id, time.Second); err != nil {
		t.Errorf("o processo iniciado de novo: %v", err)
	}
}
