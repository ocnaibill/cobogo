package worker

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// Comandos pontuais de verdade (DEC-45): o desfecho de cada caso, a redação dos
// valores secretos e que cancelar e o prazo encerram o contêiner. Precisa de
// Docker (COBOGO_TEST_DOCKER=1), PostgreSQL e da imagem node:26-slim.
func TestRunCommandsEndToEndOnTheWorker(t *testing.T) {
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes com Docker")
	}
	rt, err := dockerrt.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := rt.EnsureImage(ctx, "node:26-slim"); err != nil {
		t.Fatal(err)
	}
	st := storetest.New(t)
	admin, err := st.CreateAdmin(ctx, "dono-cmd", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, admin.ID, "comandos", "https://github.com/x/comandos", "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.RemoveProjectNetwork(context.Background(), p.ID.String()) })
	const secret = "valor-secreto-do-canario"
	if _, err := st.PutEnvVariable(ctx, p.ID, "SEGREDO", secret, domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}

	// Uma release ativa, com uma análise pronta, cuja imagem é a do teste.
	dep, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k-"+uuid.NewString(), nil)
	if err != nil {
		t.Fatal(err)
	}
	job, _, err := st.ClaimNextJob(ctx, time.Minute, 3)
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	o := st.Owned(store.LeaseOf(job))
	ready := domain.AnalysisResult{Outcome: domain.AnalysisReady, Fields: map[string]domain.Field{
		domain.FieldProfile:      {Value: domain.ProfileNode, Origin: domain.OriginInference},
		domain.FieldStartCommand: {Value: "node server.js", Origin: domain.OriginDeclaration},
		domain.FieldPort:         {Value: "3000", Origin: domain.OriginDefault},
	}}
	if _, err := o.SaveAnalysis(ctx, dep.ID, p.ID, "abc123", ready); err != nil {
		t.Fatal(err)
	}
	for _, to := range []domain.DeploymentState{domain.DeploymentFetching, domain.DeploymentAnalyzing, domain.DeploymentGenerating, domain.DeploymentBuilding,
		domain.DeploymentProvisioning, domain.DeploymentStarting, domain.DeploymentVerifying} {
		if err := o.Transition(ctx, dep.ID, to, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := o.PromoteRelease(ctx, dep.ID, store.ReleaseSpec{ImageID: "node:26-slim", ContainerID: "ctr-nao-existe", Health: "healthy", HostPort: 40111}); err != nil {
		t.Fatal(err)
	}

	w := &Worker{store: st, runtime: rt, log: quiet, settings: settings.NewResolver(st, settings.Env{})}
	run := func(command string, pause bool, timeout int, during func(id uuid.UUID)) domain.Command {
		t.Helper()
		c, err := st.CreateCommand(ctx, p.ID, admin.ID, command, domain.CommandCustom, pause, timeout)
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := st.ClaimNextCommand(ctx)
		if err != nil || claimed == nil {
			t.Fatalf("claim: %v %v", claimed, err)
		}
		if during != nil {
			go during(c.ID)
		}
		w.executeCommand(ctx, claimed)
		got, err := st.GetCommandFor(ctx, p.ID, c.ID, admin.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	// Falha com código de saída; o valor da variável sai redigido da saída e do texto.
	got := run("echo \"valor: $SEGREDO\"; echo literal-"+secret+"; echo \"porta: $PORT\"; exit 3", false, 60, nil)
	if got.State != domain.CommandFailed || got.ExitCode == nil || *got.ExitCode != 3 {
		t.Fatalf("failed run = %+v", got)
	}
	if strings.Contains(got.Output, secret) || strings.Contains(got.Command, secret) || !strings.Contains(got.Output, "valor: [REDIGIDO]") || !strings.Contains(got.Output, "porta: 3000") {
		t.Errorf("redaction: command=%q output=%q", got.Command, got.Output)
	}

	// Sucesso.
	if got := run("true", false, 60, nil); got.State != domain.CommandSucceeded || got.ExitCode == nil || *got.ExitCode != 0 || got.FinishedAt == nil {
		t.Errorf("success run = %+v", got)
	}

	// Prazo: encerra o comando que não termina.
	began := time.Now()
	got = run("echo antes; sleep 120", false, domain.CommandMinTimeout, nil)
	if got.State != domain.CommandTimedOut || !strings.Contains(got.Output, "antes") || time.Since(began) > time.Minute {
		t.Errorf("timeout run = %+v (%s)", got, time.Since(began))
	}

	// Cancelar: o dono pede e o contêiner morre.
	began = time.Now()
	got = run("sleep 120", false, 300, func(id uuid.UUID) {
		time.Sleep(3 * time.Second)
		_, _ = st.RequestCancelCommand(ctx, p.ID, id, admin.ID)
	})
	if got.State != domain.CommandCanceled || time.Since(began) > time.Minute {
		t.Errorf("cancel run = %+v (%s)", got, time.Since(began))
	}

	// Pausar a aplicação que não dá para parar: o comando não roda.
	got = run("echo nao-deve-rodar", true, 60, nil)
	if got.State != domain.CommandFailed || strings.Contains(got.Output, "nao-deve-rodar") || !strings.Contains(got.Error, "pausar") {
		t.Errorf("pause failure run = %+v", got)
	}

	// Nenhum contêiner de comando sobra.
	left, err := rt.ListManaged(ctx, p.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range left {
		if c.Role == dockerrt.RoleCommand {
			t.Errorf("a command container was left behind: %s", c.Name)
		}
	}
}
