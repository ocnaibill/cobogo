package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// Parar, iniciar e reiniciar (DEC-40): o estado vem do banco, a rota o segue e
// um deploy novo traz o projeto de volta ao ar.
func TestStoppedAppKeepsItsReleaseActiveAndRoutesToTheInactivePage(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)

	// Sem release: nada a parar, e o estado é "none".
	if _, err := st.RequestAppOperation(ctx, p.ID, own.ID, domain.OpStopApp); !errors.Is(err, store.ErrNoActiveRelease) {
		t.Fatalf("stop without a release = %v, want ErrNoActiveRelease", err)
	}
	if got, _ := st.GetProject(ctx, p.ID); got.AppState != domain.AppNone {
		t.Errorf("state without a release = %q, want none", got.AppState)
	}

	o, dep := verifying(t, st, p)
	rel, err := o.PromoteRelease(ctx, dep.ID, routed("ctr-stop", 1))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetProject(ctx, p.ID); got.AppState != domain.AppRunning {
		t.Errorf("state with a release = %q, want running", got.AppState)
	}
	// Iniciar o que está no ar não muda nada; parar, sim.
	if _, err := st.RequestAppOperation(ctx, p.ID, own.ID, domain.OpStartApp); !errors.Is(err, store.ErrAppAlreadyRunning) {
		t.Errorf("start while running = %v, want ErrAppAlreadyRunning", err)
	}
	op, err := st.RequestAppOperation(ctx, p.ID, own.ID, domain.OpStopApp)
	if err != nil || op.Kind != domain.OpStopApp || op.State != domain.OpPending {
		t.Fatalf("stop = %+v, %v", op, err)
	}

	if err := st.SetAppStopped(ctx, rel.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetProject(ctx, p.ID)
	if got.AppState != domain.AppStopped {
		t.Errorf("state after stop = %q, want stopped", got.AppState)
	}
	// A release continua a ativa: a faxina e a aposentadoria a protegem como antes.
	active, err := st.ActiveRelease(ctx, p.ID)
	if err != nil || active.ID != rel.ID || active.StoppedAt == nil {
		t.Fatalf("active release = %+v, %v", active, err)
	}
	facts, err := st.ContainerFacts(ctx, []uuid.UUID{dep.ID})
	if err != nil || facts[dep.ID].ActiveContainer != "ctr-stop" {
		t.Errorf("a stopped release must still be the active container: %+v, %v", facts, err)
	}

	routes, err := st.ListRoutes(ctx)
	if err != nil || len(routes) != 1 || !routes[0].Stopped {
		t.Fatalf("routes = %+v, %v; want one stopped route", routes, err)
	}
	if stopped, _ := st.AppStoppedBySlug(ctx, p.Slug); !stopped {
		t.Error("AppStoppedBySlug = false for a stopped app")
	}
	if _, err := st.RequestAppOperation(ctx, p.ID, own.ID, domain.OpStopApp); !errors.Is(err, store.ErrAppAlreadyStopped) {
		t.Errorf("stop while stopped = %v, want ErrAppAlreadyStopped", err)
	}
	for _, kind := range []domain.OperationKind{domain.OpStartApp, domain.OpRestartApp} {
		if _, err := st.RequestAppOperation(ctx, p.ID, own.ID, kind); err != nil {
			t.Errorf("%s while stopped = %v", kind, err)
		}
	}

	// A lista de projetos também diz que está parado.
	items, _, _ := st.ListProjectsFor(ctx, own.ID, 10, nil)
	if len(items) != 1 || items[0].AppState != domain.AppStopped {
		t.Errorf("listing = %+v", items)
	}

	// Iniciar tira a marca: a rota volta ao contêiner.
	if err := st.SetAppStopped(ctx, rel.ID, false); err != nil {
		t.Fatal(err)
	}
	if routes, _ := st.ListRoutes(ctx); len(routes) != 1 || routes[0].Stopped {
		t.Errorf("routes after start = %+v", routes)
	}
	if stopped, _ := st.AppStoppedBySlug(ctx, p.Slug); stopped {
		t.Error("AppStoppedBySlug = true after start")
	}
}

// Quem pede um deploy quer o projeto no ar: promover a release nova tira o
// projeto do estado parado, e a antiga fica substituída.
func TestANewDeployBringsAStoppedProjectBack(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	o, dep := verifying(t, st, p)
	rel, _ := o.PromoteRelease(ctx, dep.ID, routed("ctr-old", 1))
	if err := st.SetAppStopped(ctx, rel.ID, true); err != nil {
		t.Fatal(err)
	}
	o.Finish(ctx)

	o2, dep2 := verifying(t, st, p)
	if _, err := o2.PromoteRelease(ctx, dep2.ID, routed("ctr-new", 2)); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.GetProject(ctx, p.ID); got.AppState != domain.AppRunning {
		t.Errorf("state after a new deploy = %q, want running", got.AppState)
	}
	// Marcar a release que já não é a ativa não tem efeito.
	if err := st.SetAppStopped(ctx, rel.ID, true); !errors.Is(err, store.ErrNoActiveRelease) {
		t.Errorf("stopping a superseded release = %v, want ErrNoActiveRelease", err)
	}
}

func TestUpdateReleaseHostPort(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	o, dep := verifying(t, st, p)
	rel, _ := o.PromoteRelease(ctx, dep.ID, routed("ctr-port", 3))
	if err := st.UpdateReleaseHostPort(ctx, rel.ID, 45678); err != nil {
		t.Fatal(err)
	}
	if a, _ := st.ActiveRelease(ctx, p.ID); a.HostPort != 45678 {
		t.Errorf("host port = %d, want 45678", a.HostPort)
	}
}
