package store_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

func newQueued(t *testing.T, st *store.Store) domain.Deployment {
	t.Helper()
	p := newProject(t, st)
	dep, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k-"+uuid.NewString(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return dep
}

func expireLease(t *testing.T, pool *pgxpool.Pool, jobID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE jobs SET lease_until = now() - interval '1 second' WHERE id = $1`, jobID); err != nil {
		t.Fatal(err)
	}
}

func jobRow(t *testing.T, pool *pgxpool.Pool, jobID uuid.UUID) (state string, generation, attempts int) {
	t.Helper()
	if err := pool.QueryRow(ctx, `SELECT state, ownership_generation, attempt_count FROM jobs WHERE id = $1`, jobID).Scan(&state, &generation, &attempts); err != nil {
		t.Fatal(err)
	}
	return
}

func TestExpiredLeaseIsReclaimed(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	newQueued(t, st)

	_, first, _ := claim(t, st)
	if first.OwnershipGeneration != 1 || first.AttemptCount != 1 {
		t.Fatalf("first claim = gen %d attempt %d", first.OwnershipGeneration, first.AttemptCount)
	}
	if job, _, err := st.ClaimNextJob(ctx, testLease, testAttempts); err != nil || job != nil {
		t.Fatalf("a job with a valid lease must not be claimed: job=%v err=%v", job, err)
	}

	expireLease(t, pool, first.ID)
	_, second, _ := claim(t, st)
	if second.ID != first.ID || second.OwnershipGeneration != 2 || second.AttemptCount != 2 {
		t.Errorf("reclaim = job %s gen %d attempt %d, want the same job at gen 2 attempt 2", second.ID, second.OwnershipGeneration, second.AttemptCount)
	}
}

// Um worker que perdeu a posse não grava nada, em nenhuma das escritas.
func TestStaleOwnerCannotWriteAnything(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	dep := newQueued(t, st)
	stale, job, _ := claim(t, st)
	if err := stale.Transition(ctx, dep.ID, domain.DeploymentFetching, ""); err != nil {
		t.Fatal(err)
	}

	expireLease(t, pool, job.ID)
	current, _, _ := claim(t, st)

	writes := map[string]func() error{
		"transition":   func() error { return stale.Transition(ctx, dep.ID, domain.DeploymentFailed, "tarde demais") },
		"set sha":      func() error { return stale.SetCommitSHA(ctx, dep.ID, "deadbeef") },
		"append event": func() error { return stale.AppendEvent(ctx, dep.ID, "fetching", "late", nil) },
		"save analysis": func() error {
			_, err := stale.SaveAnalysis(ctx, dep.ID, dep.ProjectID, "sha", domain.AnalysisResult{})
			return err
		},
		"create release": func() error {
			_, err := stale.CreateRelease(ctx, dep.ID, "img", "ctr", "active", "healthy", 1)
			return err
		},
		"renew":   func() error { return stale.Renew(ctx, time.Hour) },
		"check":   func() error { return stale.Check(ctx) },
		"finish":  func() error { return stale.Finish(ctx) },
		"release": func() error { return stale.Release(ctx) },
	}
	for name, write := range writes {
		if err := write(); !errors.Is(err, store.ErrLeaseLost) {
			t.Errorf("%s by the previous owner: %v, want ErrLeaseLost", name, err)
		}
	}

	got, err := st.GetDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.DeploymentFetching || got.CommitSHA != "" || got.FailureReason != nil || got.AnalysisID != nil {
		t.Errorf("the deployment changed under a stale owner: %+v", got)
	}
	if events, _ := st.ListEvents(ctx, dep.ID); len(events) != 0 {
		t.Errorf("a stale owner wrote events: %+v", events)
	}
	if state, gen, _ := jobRow(t, pool, job.ID); state != "LEASED" || gen != 2 {
		t.Errorf("the job is %s at generation %d, want LEASED at 2 (the stale owner's finish/release must not touch it)", state, gen)
	}

	// O dono atual continua escrevendo normalmente.
	if err := current.Transition(ctx, dep.ID, domain.DeploymentFetching, ""); err != nil {
		t.Errorf("the current owner must resume from FETCHING: %v", err)
	}
}

func TestRenewExtendsTheLease(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	newQueued(t, st)
	o, job, _ := claim(t, st)

	if _, err := pool.Exec(ctx, `UPDATE jobs SET lease_until = now() + interval '2 seconds' WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}
	if err := o.Renew(ctx, time.Minute); err != nil {
		t.Fatal(err)
	}
	var remaining float64
	if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM lease_until - now()) FROM jobs WHERE id = $1`, job.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining < 50 {
		t.Errorf("lease has %.0fs left after renewing for 60s", remaining)
	}
}

func TestJobIsRetiredAfterTheLastAttempt(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	stuck := newQueued(t, st)
	next := newQueued(t, st)

	var jobID uuid.UUID
	for attempt := 1; attempt <= testAttempts; attempt++ {
		_, job, _ := claim(t, st)
		jobID = job.ID
		if job.DeploymentID != stuck.ID || job.AttemptCount != attempt {
			t.Fatalf("attempt %d claimed deployment %s attempt %d", attempt, job.DeploymentID, job.AttemptCount)
		}
		expireLease(t, pool, job.ID) // o worker "caiu"
	}

	// A quarta reivindicação não executa o job esgotado: encerra-o, falha o
	// deploy e segue para o próximo da fila.
	_, job, _ := claim(t, st)
	if job.DeploymentID != next.ID {
		t.Errorf("after exhausting %s the next claim was for %s, want %s", stuck.ID, job.DeploymentID, next.ID)
	}
	got, err := st.GetDeployment(ctx, stuck.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.DeploymentFailed || got.FailureReason == nil || !strings.Contains(*got.FailureReason, "tentativas esgotadas") {
		t.Errorf("stuck deployment = %+v", got)
	}
	if state, _, _ := jobRow(t, pool, jobID); state != "DONE" {
		t.Errorf("exhausted job is %s, want DONE", state)
	}
	events, _ := st.ListEvents(ctx, stuck.ID)
	if len(events) != 1 || events[0].EventType != "failed" {
		t.Errorf("events = %+v", events)
	}
}

// Um deploy que já terminou ou espera resposta não é falhado por ter um job
// esgotado: o que importa é o estado dele.
func TestExhaustedJobDoesNotOverwriteAFinishedDeployment(t *testing.T) {
	for _, final := range []domain.DeploymentState{domain.DeploymentSucceeded, domain.DeploymentNeedsInput, domain.DeploymentUnsupported} {
		t.Run(string(final), func(t *testing.T) {
			st, pool := storetest.NewWithPool(t)
			dep := newQueued(t, st)
			_, job, _ := claim(t, st)
			if _, err := pool.Exec(ctx, `UPDATE deployments SET state = $1 WHERE id = $2`, final, dep.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `UPDATE jobs SET attempt_count = $1, lease_until = now() - interval '1 second' WHERE id = $2`, testAttempts, job.ID); err != nil {
				t.Fatal(err)
			}
			if got, _, err := st.ClaimNextJob(ctx, testLease, testAttempts); err != nil || got != nil {
				t.Fatalf("claim = %v, %v; want nothing", got, err)
			}
			if d, _ := st.GetDeployment(ctx, dep.ID); d.State != final {
				t.Errorf("state = %s, want %s", d.State, final)
			}
			if state, _, _ := jobRow(t, pool, job.ID); state != "DONE" {
				t.Errorf("job = %s, want DONE", state)
			}
		})
	}
}

func TestReleaseDoesNotCountTheAttempt(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	newQueued(t, st)
	o, job, _ := claim(t, st)

	if err := o.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if state, _, attempts := jobRow(t, pool, job.ID); state != "PENDING" || attempts != 0 {
		t.Errorf("released job = %s with %d attempts, want PENDING with 0", state, attempts)
	}
	if err := o.Finish(ctx); !errors.Is(err, store.ErrLeaseLost) {
		t.Errorf("the previous owner finishing a released job: %v, want ErrLeaseLost", err)
	}

	_, again, _ := claim(t, st)
	if again.AttemptCount != 1 || again.OwnershipGeneration != 2 {
		t.Errorf("after release the claim has attempt %d gen %d, want attempt 1 gen 2", again.AttemptCount, again.OwnershipGeneration)
	}
}

func TestOnlyOneWorkerClaimsAJob(t *testing.T) {
	st := storetest.New(t)
	newQueued(t, st)

	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if job, _, err := st.ClaimNextJob(ctx, testLease, testAttempts); err == nil && job != nil {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Errorf("%d workers claimed the same job, want 1", winners.Load())
	}
}

func TestTransitionsFollowTheStateMachine(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	dep := newQueued(t, st)
	o, _, _ := claim(t, st)

	state := func() domain.DeploymentState {
		d, err := st.GetDeployment(ctx, dep.ID)
		if err != nil {
			t.Fatal(err)
		}
		return d.State
	}
	refuse := func(to domain.DeploymentState) {
		t.Helper()
		before := state()
		if err := o.Transition(ctx, dep.ID, to, ""); !errors.Is(err, store.ErrInvalidTransition) {
			t.Errorf("%s -> %s: %v, want ErrInvalidTransition", before, to, err)
		}
		if state() != before {
			t.Errorf("a refused transition changed the state to %s", state())
		}
	}

	refuse(domain.DeploymentBuilding) // não se pula etapa
	for _, to := range []domain.DeploymentState{domain.DeploymentFetching, domain.DeploymentAnalyzing, domain.DeploymentGenerating, domain.DeploymentBuilding} {
		if err := o.Transition(ctx, dep.ID, to, ""); err != nil {
			t.Fatalf("-> %s: %v", to, err)
		}
	}
	// Retomada: de uma etapa ativa volta ao início.
	if err := o.Transition(ctx, dep.ID, domain.DeploymentFetching, ""); err != nil {
		t.Errorf("BUILDING -> FETCHING (resume): %v", err)
	}
	refuse(domain.DeploymentSucceeded)

	if err := o.Transition(ctx, dep.ID, domain.DeploymentFailed, "falhou aqui"); err != nil {
		t.Fatal(err)
	}
	if d, _ := st.GetDeployment(ctx, dep.ID); d.FailureReason == nil || *d.FailureReason != "falhou aqui" {
		t.Errorf("failure reason = %v", d.FailureReason)
	}
	// Estados terminais não mudam, nem para sucesso nem para retomada.
	refuse(domain.DeploymentSucceeded)
	refuse(domain.DeploymentFetching)

	// NEEDS_INPUT só sai por uma resposta.
	if _, err := pool.Exec(ctx, `UPDATE deployments SET state = 'NEEDS_INPUT', failure_reason = NULL WHERE id = $1`, dep.ID); err != nil {
		t.Fatal(err)
	}
	refuse(domain.DeploymentFetching)
}

func TestCommitSHAIsPinnedOnce(t *testing.T) {
	st := storetest.New(t)
	dep := newQueued(t, st)
	o, _, _ := claim(t, st)

	if err := o.SetCommitSHA(ctx, dep.ID, "aaa111"); err != nil {
		t.Fatal(err)
	}
	if err := o.SetCommitSHA(ctx, dep.ID, "aaa111"); err != nil {
		t.Errorf("a resumed attempt reaffirms the same SHA: %v", err)
	}
	if err := o.SetCommitSHA(ctx, dep.ID, "bbb222"); err == nil {
		t.Error("changing the pinned SHA must be refused (RN-04)")
	}
	if d, _ := st.GetDeployment(ctx, dep.ID); d.CommitSHA != "aaa111" {
		t.Errorf("SHA = %q", d.CommitSHA)
	}
}
