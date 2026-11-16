package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/diagnosis"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestKeepLeaseRenewsPeriodically(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stop := keepLease(ctx, cancel, 5*time.Millisecond, func(context.Context) error { calls.Add(1); return nil }, quiet)
	waitFor(t, "three renewals", func() bool { return calls.Load() >= 3 })
	stop()
	if ctx.Err() != nil {
		t.Errorf("a healthy heartbeat must not cancel the pipeline: %v", context.Cause(ctx))
	}
}

// Perder a posse cancela o pipeline com essa causa, para ele parar de gravar.
func TestKeepLeaseCancelsThePipelineWhenTheLeaseIsLost(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stop := keepLease(ctx, cancel, 5*time.Millisecond, func(context.Context) error {
		if calls.Add(1) >= 2 {
			return store.ErrLeaseLost
		}
		return nil
	}, quiet)
	defer stop()

	waitFor(t, "the pipeline to be canceled", func() bool { return ctx.Err() != nil })
	if !errors.Is(context.Cause(ctx), store.ErrLeaseLost) {
		t.Errorf("cause = %v, want ErrLeaseLost", context.Cause(ctx))
	}
	settled := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != settled {
		t.Error("the heartbeat must stop renewing after the lease is lost")
	}
}

// Uma falha de rede passageira não pode derrubar um deploy saudável.
func TestKeepLeaseSurvivesTransientErrors(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stop := keepLease(ctx, cancel, 5*time.Millisecond, func(context.Context) error {
		if calls.Add(1) <= 2 {
			return errors.New("conexão com o banco falhou")
		}
		return nil
	}, quiet)
	waitFor(t, "renewals after the errors", func() bool { return calls.Load() >= 5 })
	stop()
	if ctx.Err() != nil {
		t.Errorf("transient errors canceled the pipeline: %v", context.Cause(ctx))
	}
}

func TestKeepLeaseStopWaitsAndStopsRenewing(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stop := keepLease(ctx, cancel, 5*time.Millisecond, func(context.Context) error { calls.Add(1); return nil }, quiet)
	waitFor(t, "a renewal", func() bool { return calls.Load() >= 1 })
	stop()
	after := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != after {
		t.Error("renewals continued after stop")
	}
}

func TestKeepLeaseStopsWhenThePipelineEnds(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancelCause(context.Background())
	stop := keepLease(ctx, cancel, 5*time.Millisecond, func(context.Context) error { calls.Add(1); return nil }, quiet)
	cancel(errors.New("fim"))
	stop() // não pode travar
	after := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != after {
		t.Error("renewals continued after the pipeline ended")
	}
}

func TestContainerNamesNeverCollideBetweenDeploys(t *testing.T) {
	a := containerNameFor("app", uuid.MustParse("11111111-aaaa-bbbb-cccc-000000000000"))
	b := containerNameFor("app", uuid.MustParse("22222222-aaaa-bbbb-cccc-000000000000"))
	if a == b {
		t.Errorf("two deploys of the same project got the same name %q", a)
	}
	if a != "cobogo-app-11111111" {
		t.Errorf("name = %q", a)
	}
	// O mesmo deploy, em qualquer tentativa, tem sempre o mesmo nome.
	if again := containerNameFor("app", uuid.MustParse("11111111-aaaa-bbbb-cccc-000000000000")); again != a {
		t.Errorf("a resumed attempt must reuse the name: %q vs %q", again, a)
	}
}

func TestStageErrors(t *testing.T) {
	if at("BUILDING", nil) != nil {
		t.Error("at(stage, nil) must stay nil: a successful step is not a failure")
	}
	inner := errors.New("falhou")
	err := at("BUILDING", inner)
	var se *stageError
	if !errors.As(err, &se) || se.stage != "BUILDING" || !errors.Is(err, inner) || err.Error() != "falhou" {
		t.Errorf("stage error = %#v", err)
	}
	if !errors.Is(at("STARTING", store.ErrLeaseLost), store.ErrLeaseLost) {
		t.Error("a lost lease wrapped by a stage must still be recognizable")
	}
}

// Um contêiner real que cai no boot: o log guardado não contém os valores das
// variáveis do projeto e o diagnóstico nomeia a variável que falta (DEC-26).
// Precisa de Docker e da imagem node:26-slim; só roda com COBOGO_TEST_DOCKER=1.
func TestInvestigateRedactsSecretsAndNamesTheMissingVariable(t *testing.T) {
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes com Docker")
	}
	rt, err := dockerrt.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	const secret = "valor-secreto-do-projeto-4321"
	const tag = "cobogo-test/investigate:1"
	dockerfile := `FROM node:26-slim
CMD ["node", "-e", "console.error('conectando com ' + process.env.DB_PASSWORD + ' falhou'); console.error('Error: DATABASE_URL is not defined'); process.exit(1)"]
`
	if _, err := rt.Build(ctx, t.TempDir(), dockerfile, tag, "node:26-slim", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", tag).Run() })

	res, runErr := rt.Run(ctx, tag, "cobogo-test-investigate", 3000, map[string]string{"DB_PASSWORD": secret}, "deploy-investigate-test")
	t.Cleanup(func() { _ = rt.Remove(context.Background(), res.ContainerID) })
	if res.ContainerID == "" {
		t.Fatalf("run: %v", runErr)
	}
	if runErr == nil {
		_, runErr = dockerrt.ProbeReady(ctx, res.HostPort, "/", 20*time.Second, func(c context.Context) (bool, int, error) {
			return rt.State(c, res.ContainerID)
		})
	}

	w := &Worker{runtime: rt, log: quiet}
	inventory := []domain.ConfigVariable{
		{Name: "DATABASE_URL", Class: domain.ClassUser},
		{Name: "DB_PASSWORD", Class: domain.ClassUser},
		{Name: "PORT", Class: domain.ClassPlatform},
	}
	detail := w.investigate(ctx, res.ContainerID, runErr, []string{secret}, inventory, map[string]string{"DB_PASSWORD": ""})

	if strings.Contains(detail.LogTail, secret) {
		t.Errorf("the project's secret reached the stored log: %q", detail.LogTail)
	}
	if !strings.Contains(detail.LogTail, "conectando com [REDIGIDO] falhou") {
		t.Errorf("the log must keep its shape with the value redacted: %q", detail.LogTail)
	}
	if detail.ExitCode == nil || *detail.ExitCode != 1 {
		t.Errorf("exit code = %v", detail.ExitCode)
	}
	if detail.Diagnosis.Confidence != diagnosis.Confirmed || len(detail.Diagnosis.Variables) != 1 || detail.Diagnosis.Variables[0] != "DATABASE_URL" {
		t.Errorf("diagnosis = %+v", detail.Diagnosis)
	}
}

// O motivo e o evento de falha carregam log, código de saída e diagnóstico.
func TestFailureDetailTravelsWithTheError(t *testing.T) {
	code := 7
	cause := &detailedError{
		err: errors.New("o contêiner terminou"),
		detail: failureDetail{
			LogTail: "log", ExitCode: &code,
			Diagnosis: diagnosis.Diagnosis{Confidence: diagnosis.Confirmed, Variables: []string{"X"}, Message: "Provável causa: `X`"},
		},
	}
	wrapped := at("STARTING", cause)
	var de *detailedError
	if !errors.As(wrapped, &de) || de.detail.LogTail != "log" || *de.detail.ExitCode != 7 {
		t.Errorf("detail lost in the chain: %#v", wrapped)
	}
	var se *stageError
	if !errors.As(wrapped, &se) || se.stage != "STARTING" {
		t.Errorf("stage lost in the chain: %#v", wrapped)
	}
}
