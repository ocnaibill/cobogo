package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// verifying leva um novo deploy do projeto até VERIFYING, como o worker faz, e
// devolve a posse. Reivindica o próximo job pendente.
func verifying(t *testing.T, st *store.Store, p domain.Project) (*store.Owned, domain.Deployment) {
	t.Helper()
	dep, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k-"+uuid.NewString(), nil)
	if err != nil {
		t.Fatal(err)
	}
	o, _, _ := claim(t, st)
	for _, to := range []domain.DeploymentState{
		domain.DeploymentFetching, domain.DeploymentAnalyzing, domain.DeploymentGenerating, domain.DeploymentBuilding,
		domain.DeploymentProvisioning, domain.DeploymentStarting, domain.DeploymentVerifying,
	} {
		if err := o.Transition(ctx, dep.ID, to, ""); err != nil {
			t.Fatal(err)
		}
	}
	return o, dep
}

func routed(container string, port int) store.ReleaseSpec {
	return store.ReleaseSpec{
		ImageID: "img-" + container, ContainerID: container, Health: "healthy", HostPort: 40000 + port,
		URL: "https://app.cobogo.test/", RouteHost: "app.cobogo.test", RouteTarget: "http://" + container + ":3000",
	}
}

func TestPromoteReleaseConcludesTheDeploymentAndTheReleaseTogether(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	o, dep := verifying(t, st, p)

	rel, err := o.PromoteRelease(ctx, dep.ID, routed("ctr-1", 1))
	if err != nil {
		t.Fatal(err)
	}
	if rel.OperationalState != "active" || rel.URL != "https://app.cobogo.test/" {
		t.Errorf("release = %+v", rel)
	}
	if got, _ := st.GetDeployment(ctx, dep.ID); got.State != domain.DeploymentSucceeded {
		t.Errorf("state = %s, want SUCCEEDED", got.State)
	}
	stored, err := st.GetReleaseByDeployment(ctx, dep.ID)
	if err != nil || stored.ID != rel.ID || stored.OperationalState != "active" || stored.URL != rel.URL {
		t.Errorf("stored = %+v, %v", stored, err)
	}

	routes, err := st.ListRoutes(ctx)
	if err != nil || len(routes) != 1 || routes[0].Slug != p.Slug || routes[0].Host != "app.cobogo.test" || routes[0].Target != "http://ctr-1:3000" {
		t.Errorf("routes = %+v, %v", routes, err)
	}
}

// A release nova substitui só as do mesmo projeto, e a tabela de rotas passa a
// apontar para ela (DEC-31 itens 9 e 10).
func TestPromoteReleaseSupersedesOnlyTheSameProject(t *testing.T) {
	st := storetest.New(t)
	a, b := newProject(t, st), newProject(t, st)

	oa1, depA1 := verifying(t, st, a)
	if _, err := oa1.PromoteRelease(ctx, depA1.ID, routed("a-1", 1)); err != nil {
		t.Fatal(err)
	}
	ob, depB := verifying(t, st, b)
	specB := routed("b-1", 2)
	specB.RouteHost = "outro.cobogo.test"
	if _, err := ob.PromoteRelease(ctx, depB.ID, specB); err != nil {
		t.Fatal(err)
	}
	oa2, depA2 := verifying(t, st, a)
	if _, err := oa2.PromoteRelease(ctx, depA2.ID, routed("a-2", 3)); err != nil {
		t.Fatal(err)
	}

	first, _ := st.GetReleaseByDeployment(ctx, depA1.ID)
	other, _ := st.GetReleaseByDeployment(ctx, depB.ID)
	second, _ := st.GetReleaseByDeployment(ctx, depA2.ID)
	if first.OperationalState != "superseded" || second.OperationalState != "active" || other.OperationalState != "active" {
		t.Errorf("states = %s / %s / %s, want superseded / active / active", first.OperationalState, second.OperationalState, other.OperationalState)
	}

	byHost := map[string]string{}
	routes, err := st.ListRoutes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range routes {
		byHost[r.Host] = r.Target
	}
	if len(routes) != 2 || byHost["app.cobogo.test"] != "http://a-2:3000" || byHost["outro.cobogo.test"] != "http://b-1:3000" {
		t.Errorf("routes = %+v", routes)
	}
}

// Sem tudo-ou-nada, um deploy ficaria SUCCEEDED sem release, ou o contrário.
func TestPromoteReleaseIsAllOrNothing(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	o1, dep1 := verifying(t, st, p)
	if _, err := o1.PromoteRelease(ctx, dep1.ID, routed("ctr-1", 1)); err != nil {
		t.Fatal(err)
	}

	// Um deploy que ainda não chegou a VERIFYING não pode ser promovido.
	dep2, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k-segundo", nil)
	if err != nil {
		t.Fatal(err)
	}
	o2, _, _ := claim(t, st)
	for _, to := range []domain.DeploymentState{domain.DeploymentFetching, domain.DeploymentAnalyzing, domain.DeploymentGenerating} {
		if err := o2.Transition(ctx, dep2.ID, to, ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := o2.PromoteRelease(ctx, dep2.ID, routed("ctr-2", 2)); !errors.Is(err, store.ErrInvalidTransition) {
		t.Fatalf("promoting from GENERATING: %v, want ErrInvalidTransition", err)
	}
	if got, _ := st.GetDeployment(ctx, dep2.ID); got.State != domain.DeploymentGenerating {
		t.Errorf("a refused promotion must not change the state, got %s", got.State)
	}
	if _, err := st.GetReleaseByDeployment(ctx, dep2.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused promotion must not create a release: %v", err)
	}
	if prev, _ := st.GetReleaseByDeployment(ctx, dep1.ID); prev.OperationalState != "active" {
		t.Errorf("the previous release must stay active, got %s", prev.OperationalState)
	}
	if routes, _ := st.ListRoutes(ctx); len(routes) != 1 || routes[0].Target != "http://ctr-1:3000" {
		t.Errorf("routes = %+v", routes)
	}
}

func TestRoutesIgnoreReleasesWithoutARoute(t *testing.T) {
	st := storetest.New(t)
	if routes, err := st.ListRoutes(ctx); err != nil || len(routes) != 0 {
		t.Fatalf("no releases = %+v, %v", routes, err)
	}
	p := newProject(t, st)
	o, dep := verifying(t, st, p)
	if _, err := o.PromoteRelease(ctx, dep.ID, store.ReleaseSpec{ImageID: "i", ContainerID: "c", Health: "healthy", HostPort: 1}); err != nil {
		t.Fatal(err)
	}
	if routes, _ := st.ListRoutes(ctx); len(routes) != 0 {
		t.Errorf("a release without a route must not be listed: %+v", routes)
	}

	// Voltar a rotear: promover com rota inclui o projeto; promover de novo sem
	// rota (domínio desligado) o tira, porque a nova release é a ativa.
	o2, dep2 := verifying(t, st, p)
	if _, err := o2.PromoteRelease(ctx, dep2.ID, routed("ctr-2", 2)); err != nil {
		t.Fatal(err)
	}
	if routes, _ := st.ListRoutes(ctx); len(routes) != 1 {
		t.Fatalf("routes = %+v", routes)
	}
	o3, dep3 := verifying(t, st, p)
	if _, err := o3.PromoteRelease(ctx, dep3.ID, store.ReleaseSpec{ImageID: "i", ContainerID: "c3", Health: "healthy", HostPort: 3}); err != nil {
		t.Fatal(err)
	}
	if routes, _ := st.ListRoutes(ctx); len(routes) != 0 {
		t.Errorf("the newest release has no route, so the project is no longer routed: %+v", routes)
	}
}
