package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// Ciclo de vida (DEC-38).

func TestDeleteProjectKeepsHistoryAndRemovesTheRest(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	o, dep := verifying(t, st, p)
	if _, err := o.PromoteRelease(ctx, dep.ID, routed("ctr-del", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PutEnvVariable(ctx, p.ID, "TOKEN", "segredo", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	if routes, _ := st.ListRoutes(ctx); len(routes) != 1 {
		t.Fatalf("routes before = %v", routes)
	}

	op, err := st.DeleteProject(ctx, p.ID, own.ID, p.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if op.Kind != domain.OpDeleteProject || op.State != domain.OpPending || op.ProjectID == nil || *op.ProjectID != p.ID {
		t.Errorf("operation = %+v", op)
	}

	// Some da API, da lista e das rotas.
	if _, err := st.GetProjectFor(ctx, p.ID, own.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetProjectFor after delete = %v, want ErrNotFound", err)
	}
	if _, err := st.GetDeploymentFor(ctx, dep.ID, own.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetDeploymentFor after delete = %v, want ErrNotFound", err)
	}
	items, _, err := st.ListProjectsFor(ctx, own.ID, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.ID == p.ID {
			t.Error("a deleted project must not be listed")
		}
	}
	if routes, _ := st.ListRoutes(ctx); len(routes) != 0 {
		t.Errorf("routes after delete = %v, want none", routes)
	}

	// As variáveis cifradas somem; o histórico fica.
	if vars, _ := st.ListEnvVariables(ctx, p.ID); len(vars) != 0 {
		t.Errorf("env variables after delete = %v, want none", vars)
	}
	if got, err := st.GetDeployment(ctx, dep.ID); err != nil || got.State != domain.DeploymentSucceeded {
		t.Errorf("the deployment history must stay: %+v, %v", got, err)
	}
	if rel, err := st.GetReleaseByDeployment(ctx, dep.ID); err != nil || rel.ContainerID != "ctr-del" {
		t.Errorf("the release history must stay: %+v, %v", rel, err)
	}
	// O worker ainda lê o projeto excluído, para limpar o que sobrou.
	if got, err := st.GetProject(ctx, p.ID); err != nil || got.DeletedAt == nil {
		t.Errorf("worker read = %+v, %v; want the project with deleted_at", got, err)
	}
	// Excluir de novo é como excluir o que não existe.
	if _, err := st.DeleteProject(ctx, p.ID, own.ID, p.Slug); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("second delete = %v, want ErrNotFound", err)
	}
}

func TestDeletedSlugCanBeReused(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p, err := st.CreateProject(ctx, own.ID, "reuso-"+uuid.NewString()[:6], "https://github.com/a/b", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProject(ctx, own.ID, p.Slug, "https://github.com/a/c", ""); err == nil {
		t.Fatal("a live slug must stay unique")
	}
	if _, err := st.DeleteProject(ctx, p.ID, own.ID, p.Slug); err != nil {
		t.Fatal(err)
	}
	again, err := st.CreateProject(ctx, own.ID, p.Slug, "https://github.com/a/c", "")
	if err != nil {
		t.Fatalf("a deleted slug must be free: %v", err)
	}
	if again.ID == p.ID {
		t.Error("the new project is another project")
	}
}

func TestDeleteProjectNeedsTheRightNameAndTheRightOwner(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	if _, err := st.DeleteProject(ctx, p.ID, own.ID, "outro-nome"); !errors.Is(err, store.ErrConfirmationMismatch) {
		t.Errorf("wrong name = %v, want ErrConfirmationMismatch", err)
	}
	if _, err := st.DeleteProject(ctx, p.ID, uuid.New(), p.Slug); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner = %v, want ErrNotFound", err)
	}
	if _, err := st.GetProjectFor(ctx, p.ID, own.ID); err != nil {
		t.Errorf("a refused delete must leave the project alone: %v", err)
	}
}

func TestDeleteProjectRefusesARunningDeploymentAndCancelsWaitingOnes(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)

	// Em execução: recusa e não muda nada.
	running := newProject(t, st)
	_, _ = verifying(t, st, running)
	if _, err := st.DeleteProject(ctx, running.ID, own.ID, running.Slug); !errors.Is(err, store.ErrDeploymentInProgress) {
		t.Fatalf("running deploy = %v, want ErrDeploymentInProgress", err)
	}
	if _, err := st.GetProjectFor(ctx, running.ID, own.ID); err != nil {
		t.Errorf("a refused delete must keep the project: %v", err)
	}

	// Na fila e aguardando resposta: cancela e exclui.
	waiting := newProject(t, st)
	queued, _, err := st.CreateDeploymentWithJob(ctx, waiting.ID, "k-"+uuid.NewString(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DeleteProject(ctx, waiting.ID, own.ID, waiting.Slug); err != nil {
		t.Fatalf("a queued deploy must be cancelled, not block: %v", err)
	}
	got, _ := st.GetDeployment(ctx, queued.ID)
	if got.State != domain.DeploymentCancelled {
		t.Errorf("queued deploy state = %s, want CANCELLED", got.State)
	}
	events, _ := st.ListEvents(ctx, queued.ID)
	var reason any
	for _, e := range events {
		if e.EventType == "cancelled" {
			reason = e.Payload["reason"]
		}
	}
	if reason != "projeto excluído" {
		t.Errorf("cancel event reason = %v", reason)
	}
}

func TestOperationsAreClaimedWhenDueAndRetriedWithALimit(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	now := time.Now().UTC()

	future, err := st.EnqueueOperation(ctx, &p.ID, domain.OpRetireReleases, nil, now.Add(time.Hour), nil)
	if err != nil {
		t.Fatal(err)
	}
	if op, _ := st.ClaimNextOperation(ctx, time.Minute, 3); op != nil {
		t.Fatalf("an operation scheduled for later must not be claimed yet: %+v", op)
	}
	// Um segundo pedido igual de aposentadoria reaproveita o pendente.
	again, err := st.EnqueueOperation(ctx, &p.ID, domain.OpRetireReleases, nil, now.Add(-time.Second), nil)
	if err != nil || again.ID != future.ID {
		t.Fatalf("retire requests must coalesce: %+v, %v", again, err)
	}
	op, err := st.ClaimNextOperation(ctx, time.Minute, 3)
	if err != nil || op == nil || op.ID != future.ID || op.State != domain.OpRunning || op.Attempts != 1 {
		t.Fatalf("claim = %+v, %v", op, err)
	}
	if again, _ := st.ClaimNextOperation(ctx, time.Minute, 3); again != nil {
		t.Errorf("a running operation with a live lease must not be claimed twice: %+v", again)
	}

	// Falha transitória: volta a PENDING com backoff e conta a tentativa.
	if err := st.FailOperation(ctx, op.ID, "docker fora do ar", true, 3, 0, nil); err != nil {
		t.Fatal(err)
	}
	retry, _ := st.ClaimNextOperation(ctx, time.Minute, 3)
	if retry == nil || retry.Attempts != 2 || retry.Error != "docker fora do ar" {
		t.Fatalf("retry = %+v", retry)
	}
	// Esgotadas as tentativas, fica FAILED e visível.
	_ = st.FailOperation(ctx, retry.ID, "de novo", true, 2, 0, nil)
	got, err := st.GetOperationFor(ctx, retry.ID, own.ID)
	if err != nil || got.State != domain.OpFailed || got.Error != "de novo" {
		t.Errorf("exhausted = %+v, %v", got, err)
	}

	// Concluir grava o resultado.
	done, _ := st.EnqueueOperation(ctx, &p.ID, domain.OpApplyLimits, nil, now.Add(-time.Second), &own.ID)
	claimed, _ := st.ClaimNextOperation(ctx, time.Minute, 3)
	if claimed == nil || claimed.ID != done.ID {
		t.Fatalf("claimed = %+v", claimed)
	}
	if err := st.FinishOperation(ctx, done.ID, map[string]any{"memory_mb": 512}); err != nil {
		t.Fatal(err)
	}
	finished, _ := st.GetOperationFor(ctx, done.ID, own.ID)
	if finished.State != domain.OpDone || finished.Result["memory_mb"] != float64(512) {
		t.Errorf("finished = %+v", finished)
	}
}

func TestAnOperationWhoseWorkerDiedIsTakenOver(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	if _, err := st.EnqueueOperation(ctx, &p.ID, domain.OpSweep, nil, time.Now().UTC().Add(-time.Second), nil); err != nil {
		t.Fatal(err)
	}
	// Posse de 1 ms: vence logo e outro worker assume.
	first, _ := st.ClaimNextOperation(ctx, time.Millisecond, 3)
	time.Sleep(20 * time.Millisecond)
	second, err := st.ClaimNextOperation(ctx, time.Minute, 3)
	if err != nil || first == nil || second == nil || second.ID != first.ID || second.Attempts != 2 {
		t.Fatalf("takeover = %+v (first %+v), %v", second, first, err)
	}
}

func TestOperationsOfOtherOwnersAndSweepsStayHidden(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	op, _ := st.EnqueueOperation(ctx, &p.ID, domain.OpApplyLimits, nil, time.Now().UTC(), &own.ID)
	if _, err := st.GetOperationFor(ctx, op.ID, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner = %v, want ErrNotFound", err)
	}
	if err := st.RecordOperation(ctx, nil, domain.OpSweep, map[string]any{"removed": []string{"a"}}); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListOperationsFor(ctx, p.ID, own.ID, 10)
	if err != nil || len(list) != 1 || list[0].ID != op.ID {
		t.Errorf("list = %+v, %v; want only the project's own operation", list, err)
	}
}

func TestRequestApplyLimitsNeedsAnActiveRelease(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	if _, err := st.RequestApplyLimits(ctx, p.ID, own.ID); !errors.Is(err, store.ErrNoActiveRelease) {
		t.Fatalf("without a release = %v, want ErrNoActiveRelease", err)
	}
	o, dep := verifying(t, st, p)
	if _, err := o.PromoteRelease(ctx, dep.ID, routed("ctr-lim", 2)); err != nil {
		t.Fatal(err)
	}
	op, err := st.RequestApplyLimits(ctx, p.ID, own.ID)
	if err != nil || op.Kind != domain.OpApplyLimits {
		t.Fatalf("with a release = %+v, %v", op, err)
	}
	if _, err := st.DeleteProject(ctx, p.ID, own.ID, p.Slug); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RequestApplyLimits(ctx, p.ID, own.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a deleted project = %v, want ErrNotFound", err)
	}
}

func TestContainerFactsAndReleaseMarks(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	o1, d1 := verifying(t, st, p)
	if _, err := o1.PromoteRelease(ctx, d1.ID, routed("ctr-a", 3)); err != nil {
		t.Fatal(err)
	}
	_ = o1.Finish(ctx)
	o2, d2 := verifying(t, st, p)
	if _, err := o2.PromoteRelease(ctx, d2.ID, routed("ctr-b", 4)); err != nil {
		t.Fatal(err)
	}

	facts, err := st.ContainerFacts(ctx, []uuid.UUID{d1.ID, d2.ID, uuid.New()})
	if err != nil || len(facts) != 2 {
		t.Fatalf("facts = %+v, %v", facts, err)
	}
	for _, id := range []uuid.UUID{d1.ID, d2.ID} {
		f := facts[id]
		if f.ActiveContainer != "ctr-b" || f.DeploymentState != domain.DeploymentSucceeded || f.ProjectID != p.ID || f.ProjectDeleted {
			t.Errorf("fact %s = %+v, want the active container ctr-b", id, f)
		}
	}

	if err := st.MarkReleasesRetired(ctx, []string{"ctr-a"}); err != nil {
		t.Fatal(err)
	}
	a, _ := st.GetReleaseByDeployment(ctx, d1.ID)
	b, _ := st.GetReleaseByDeployment(ctx, d2.ID)
	if a.OperationalState != "retired" || b.OperationalState != "active" {
		t.Errorf("states = %s / %s, want retired / active", a.OperationalState, b.OperationalState)
	}
	// A release ativa nunca é aposentada, nem com o contêiner dela na lista.
	_ = st.MarkReleasesRetired(ctx, []string{"ctr-b"})
	if r, _ := st.GetReleaseByDeployment(ctx, d2.ID); r.OperationalState != "active" {
		t.Errorf("the active release was retired: %s", r.OperationalState)
	}
	// Aposentar nunca desfaz uma release removida.
	_ = st.MarkProjectReleasesRemoved(ctx, p.ID)
	_ = st.MarkReleasesRetired(ctx, []string{"ctr-a", "ctr-b"})
	if r, _ := st.GetReleaseByDeployment(ctx, d2.ID); r.OperationalState != "removed" {
		t.Errorf("removed release came back as %s", r.OperationalState)
	}
}
