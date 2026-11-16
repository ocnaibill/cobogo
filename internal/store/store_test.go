package store_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

var ctx = context.Background()

func newProject(t *testing.T, st *store.Store) domain.Project {
	t.Helper()
	p, err := st.CreateProject(ctx, "app-"+uuid.NewString()[:8], "https://example.com/r.git", "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

const (
	testLease    = time.Minute
	testAttempts = 3
)

// claim reivindica o próximo job, como faz o worker, e devolve a posse.
func claim(t *testing.T, st *store.Store) (*store.Owned, *domain.Job, *domain.Deployment) {
	t.Helper()
	job, dep, err := st.ClaimNextJob(ctx, testLease, testAttempts)
	if err != nil || job == nil {
		t.Fatalf("claim: job=%v err=%v", job, err)
	}
	return st.Owned(store.LeaseOf(job)), job, dep
}

// awaitingInput leva um deploy ao ponto em que o worker o deixa quando a
// análise pede informação: SHA fixado, NEEDS_INPUT e job concluído. Reivindica
// o próximo job pendente, então deve ser chamado antes de criar outros.
func awaitingInput(t *testing.T, st *store.Store, overrides map[string]string) domain.Deployment {
	t.Helper()
	p := newProject(t, st)
	dep, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k-"+uuid.NewString(), overrides)
	if err != nil {
		t.Fatal(err)
	}
	o, _, _ := claim(t, st)
	for _, step := range []domain.DeploymentState{domain.DeploymentFetching, domain.DeploymentAnalyzing} {
		if err := o.Transition(ctx, dep.ID, step, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.SetCommitSHA(ctx, dep.ID, "abc123"); err != nil {
		t.Fatal(err)
	}
	if err := o.Transition(ctx, dep.ID, domain.DeploymentNeedsInput, ""); err != nil {
		t.Fatal(err)
	}
	if err := o.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	return dep
}

func TestDeploymentOverridesRoundTrip(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)

	none, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "sem-overrides", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.GetDeployment(ctx, none.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Overrides == nil || len(got.Overrides) != 0 {
		t.Errorf("a deployment without overrides must expose an empty map, got %#v", got.Overrides)
	}

	with, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "com-overrides", map[string]string{"port": "3000"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = st.GetDeployment(ctx, with.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Overrides["port"] != "3000" {
		t.Errorf("overrides = %#v", got.Overrides)
	}
}

func TestAnswerDeploymentRequeuesOnTheSameSHA(t *testing.T) {
	st := storetest.New(t)
	dep := awaitingInput(t, st, map[string]string{"port": "3000"})

	if job, _, err := st.ClaimNextJob(ctx, testLease, testAttempts); err != nil || job != nil {
		t.Fatalf("the finished job must not be claimable before the answer: job=%v err=%v", job, err)
	}

	answered, err := st.AnswerDeployment(ctx, dep.ID, map[string]string{"start_command": "node server.js", "port": "4000"})
	if err != nil {
		t.Fatal(err)
	}
	if answered.State != domain.DeploymentQueued {
		t.Errorf("state = %s, want QUEUED", answered.State)
	}
	if answered.CommitSHA != "abc123" {
		t.Errorf("the pinned SHA must survive the answer, got %q", answered.CommitSHA)
	}
	want := map[string]string{"port": "4000", "start_command": "node server.js"}
	if len(answered.Overrides) != len(want) || answered.Overrides["port"] != "4000" || answered.Overrides["start_command"] != "node server.js" {
		t.Errorf("overrides = %#v, want the merge %#v", answered.Overrides, want)
	}

	job, claimed, err := st.ClaimNextJob(ctx, testLease, testAttempts)
	if err != nil || job == nil {
		t.Fatalf("the answer must make the job claimable again: job=%v err=%v", job, err)
	}
	if job.AttemptCount != 1 {
		t.Errorf("an answer starts a fresh run, attempt_count = %d, want 1", job.AttemptCount)
	}
	if claimed.ID != dep.ID || claimed.CommitSHA != "abc123" {
		t.Errorf("claimed %s at %q, want the same deployment at the same SHA", claimed.ID, claimed.CommitSHA)
	}
}

func TestAnswerEventRecordsFieldNamesNotValues(t *testing.T) {
	st := storetest.New(t)
	dep := awaitingInput(t, st, nil)
	const canary = "TOKEN=canary-1234 node server.js"

	if _, err := st.AnswerDeployment(ctx, dep.ID, map[string]string{"start_command": canary}); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListEvents(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != "answered" {
		t.Fatalf("events = %+v", events)
	}
	fields, _ := events[0].Payload["fields"].([]any)
	if len(fields) != 1 || fields[0] != "start_command" {
		t.Errorf("payload = %#v, want only the field name", events[0].Payload)
	}
	for _, v := range events[0].Payload {
		if s, ok := v.(string); ok && s == canary {
			t.Errorf("the answered value leaked into the event")
		}
	}
}

func TestAnswerRejectsWrongState(t *testing.T) {
	st := storetest.New(t)
	awaiting := awaitingInput(t, st, nil)
	p := newProject(t, st)
	queued, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k1", nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.AnswerDeployment(ctx, queued.ID, map[string]string{"port": "3000"}); !errors.Is(err, store.ErrNotAwaitingInput) {
		t.Errorf("answering a QUEUED deployment: err = %v, want ErrNotAwaitingInput", err)
	}
	if _, err := st.AnswerDeployment(ctx, uuid.New(), map[string]string{"port": "3000"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("answering an unknown deployment: err = %v, want ErrNotFound", err)
	}

	if _, err := st.AnswerDeployment(ctx, awaiting.ID, map[string]string{"port": "3000"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AnswerDeployment(ctx, awaiting.ID, map[string]string{"port": "3001"}); !errors.Is(err, store.ErrNotAwaitingInput) {
		t.Errorf("answering twice: err = %v, want ErrNotAwaitingInput", err)
	}
}

func TestConcurrentAnswersApplyOnce(t *testing.T) {
	st := storetest.New(t)
	dep := awaitingInput(t, st, nil)

	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := st.AnswerDeployment(ctx, dep.ID, map[string]string{"port": "3000"}); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	if ok.Load() != 1 {
		t.Errorf("%d answers succeeded, want exactly 1", ok.Load())
	}
	events, err := st.ListEvents(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Errorf("%d answer events, want 1", len(events))
	}
}

// A migration 0002 troca port_override por overrides sem perder o valor já
// gravado.
func TestMigrationKeepsLegacyPortOverride(t *testing.T) {
	pool := storetest.Pool(t)

	initial, err := os.ReadFile("migrations/0001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(initial)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ('0001_init.sql')`); err != nil {
		t.Fatal(err)
	}

	projectID, withPort, withoutPort := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, slug, repository_url) VALUES ($1, 'legado', 'https://example.com/r.git')`, projectID); err != nil {
		t.Fatal(err)
	}
	for _, d := range []struct {
		id   uuid.UUID
		key  string
		port *int
	}{{withPort, "a", ptr(8080)}, {withoutPort, "b", nil}} {
		if _, err := pool.Exec(ctx, `INSERT INTO deployments (id, project_id, state, idempotency_key, port_override) VALUES ($1, $2, 'QUEUED', $3, $4)`, d.id, projectID, d.key, d.port); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}

	st := store.New(pool)
	got, err := st.GetDeployment(ctx, withPort)
	if err != nil {
		t.Fatal(err)
	}
	if got.Overrides["port"] != "8080" {
		t.Errorf("legacy port override lost: %#v", got.Overrides)
	}
	got, err = st.GetDeployment(ctx, withoutPort)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Overrides) != 0 {
		t.Errorf("a deployment without override gained %#v", got.Overrides)
	}
}

func ptr[T any](v T) *T { return &v }

// API e worker sobem ao mesmo tempo e migram o mesmo banco.
func TestMigrateIsSafeUnderConcurrency(t *testing.T) {
	pool := storetest.Pool(t)

	const n = 8
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() { errs <- store.Migrate(ctx, pool) }()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Migrate: %v", err)
		}
	}
	var applied int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil || applied < 3 {
		t.Errorf("applied migrations = %d (%v)", applied, err)
	}
}
