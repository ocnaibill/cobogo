package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// PostgreSQL auxiliar (DEC-42): a escolha, o vínculo da variável e a exclusão.
func TestChooseDatabaseLinksTheVariableAndNeverOverwritesTheUsers(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	const url = "postgres://app:senha-do-canario@host:5432/app"

	if _, err := st.GetService(ctx, p.ID, domain.ServicePostgres); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a new project has no choice: %v", err)
	}
	sv, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, "DATABASE_URL", url)
	if err != nil {
		t.Fatal(err)
	}
	if sv.Mode != domain.ServiceManaged || sv.Variable != "DATABASE_URL" || sv.Status != domain.ServicePending ||
		sv.ContainerName != domain.ServiceContainerName(p.ID.String()) || sv.VolumeName != domain.ServiceVolumeName(p.ID.String()) {
		t.Errorf("service = %+v", sv)
	}
	v, err := st.GetEnvVariable(ctx, p.ID, "DATABASE_URL")
	if err != nil || v.Source != domain.EnvSourceLinked {
		t.Fatalf("the connection must be stored as a linked variable: %+v, %v", v, err)
	}

	// Escolher o gerenciado de novo não muda nada, e a senha não é trocada.
	again, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, "DATABASE_URL", "postgres://app:OUTRA@host:5432/app")
	if err != nil || again.ID != sv.ID {
		t.Fatalf("choosing managed twice must be a no-op: %+v, %v", again, err)
	}
	if vals, _, _ := st.RevealEnvValues(ctx, p.ID); vals["DATABASE_URL"] != url {
		t.Error("the password must not change when the choice is repeated")
	}
	// Com o gerenciado, outra escolha deixaria banco e dados órfãos.
	for _, mode := range []string{domain.ServiceExternal, domain.ServiceNone} {
		if _, err := st.ChooseDatabase(ctx, p.ID, own.ID, mode, "DATABASE_URL", ""); !errors.Is(err, store.ErrManagedExists) {
			t.Errorf("%s over a managed database = %v, want ErrManagedExists", mode, err)
		}
	}

	// Um valor digitado pela pessoa nunca é sobrescrito.
	q := newProject(t, st)
	if _, err := st.PutEnvVariable(ctx, q.ID, "DATABASE_URL", "postgres://meu/banco", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ChooseDatabase(ctx, q.ID, own.ID, domain.ServiceManaged, "DATABASE_URL", url); !errors.Is(err, store.ErrVariableDefined) {
		t.Errorf("managed over a typed value = %v, want ErrVariableDefined", err)
	}
	if vals, _, _ := st.RevealEnvValues(ctx, q.ID); vals["DATABASE_URL"] != "postgres://meu/banco" {
		t.Error("the user's value was overwritten")
	}
	if _, err := st.GetService(ctx, q.ID, domain.ServicePostgres); !errors.Is(err, store.ErrNotFound) {
		t.Error("a refused choice must not register an intent")
	}
	// Mas external e none valem, e dá para trocar de um para o outro.
	if sv, err := st.ChooseDatabase(ctx, q.ID, own.ID, domain.ServiceExternal, "DATABASE_URL", ""); err != nil || sv.Mode != domain.ServiceExternal {
		t.Errorf("external = %+v, %v", sv, err)
	}
	if sv, err := st.ChooseDatabase(ctx, q.ID, own.ID, domain.ServiceNone, "DATABASE_URL", ""); err != nil || sv.Mode != domain.ServiceNone {
		t.Errorf("none after external = %+v, %v", sv, err)
	}
	// Só o dono escolhe.
	if _, err := st.ChooseDatabase(ctx, p.ID, uuid.New(), domain.ServiceNone, "X", ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner = %v, want ErrNotFound", err)
	}
}

func TestDeleteServiceRemovesTheLinkedVariableAndTurnsTheVolumeIntoAnOrphan(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	if _, err := st.RequestDeleteService(ctx, p.ID, own.ID, p.Slug); !errors.Is(err, store.ErrNoManagedService) {
		t.Fatalf("nothing to delete = %v", err)
	}
	if _, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, "DATABASE_URL", "postgres://app:x@h:5432/app"); err != nil {
		t.Fatal(err)
	}
	vol := domain.ServiceVolumeName(p.ID.String())
	if err := st.UpsertServiceVolume(ctx, vol, p.ID, domain.PostgresDataPath, domain.ServicePostgres); err != nil {
		t.Fatal(err)
	}
	// O volume de um serviço vivo não é "sem projeto", mesmo fora dos caminhos persistentes.
	if orphans, _ := st.ListOrphanVolumesFor(ctx, own.ID); len(orphans) != 0 {
		t.Fatalf("a live service's volume is not an orphan: %+v", orphans)
	}
	if _, err := st.RequestDeleteService(ctx, p.ID, own.ID, "outro-nome"); !errors.Is(err, store.ErrConfirmationMismatch) {
		t.Errorf("wrong confirmation = %v", err)
	}
	op, err := st.RequestDeleteService(ctx, p.ID, own.ID, p.Slug)
	if err != nil || op.Kind != domain.OpDeleteService {
		t.Fatalf("delete = %+v, %v", op, err)
	}
	if _, err := st.GetEnvVariable(ctx, p.ID, "DATABASE_URL"); !errors.Is(err, store.ErrNotFound) {
		t.Error("the linked variable must go with the database")
	}
	sv, _ := st.GetService(ctx, p.ID, domain.ServicePostgres)
	if sv.Status != domain.ServiceDeleting {
		t.Errorf("status = %q, want deleting", sv.Status)
	}
	if orphans, _ := st.ListOrphanVolumesFor(ctx, own.ID); len(orphans) != 1 || orphans[0].Name != vol {
		t.Errorf("the volume of a service being deleted must be listed as an orphan: %+v", orphans)
	}
	if err := st.MarkServiceDeleted(ctx, sv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetService(ctx, p.ID, domain.ServicePostgres); !errors.Is(err, store.ErrNotFound) {
		t.Error("a deleted service is no longer current")
	}
	// Escolher de novo depois de apagar cria outro vínculo, com outra senha.
	if sv2, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, "DATABASE_URL", "postgres://app:nova@h:5432/app"); err != nil || sv2.ID == sv.ID {
		t.Errorf("choose after delete = %+v, %v", sv2, err)
	}
}

// Os deploys parados na pergunta do banco voltam à fila com a escolha.
func TestWaitingForDatabaseFindsOnlyDeploysAskingAboutTheDatabase(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	ask := func(field string) {
		dep, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k-"+field, nil)
		if err != nil {
			t.Fatal(err)
		}
		o, _, _ := claim(t, st)
		for _, to := range []domain.DeploymentState{domain.DeploymentFetching, domain.DeploymentAnalyzing} {
			if err := o.Transition(ctx, dep.ID, to, ""); err != nil {
				t.Fatal(err)
			}
		}
		res := domain.AnalysisResult{Outcome: domain.AnalysisNeedsInput, Pending: []domain.Finding{{Field: field, Message: "?"}}}
		if _, err := o.SaveAnalysis(ctx, dep.ID, p.ID, "sha", res); err != nil {
			t.Fatal(err)
		}
		if err := o.Transition(ctx, dep.ID, domain.DeploymentNeedsInput, ""); err != nil {
			t.Fatal(err)
		}
		o.Finish(ctx)
	}
	ask(domain.FieldPort)
	ask(domain.FieldDatabase)
	ids, err := st.WaitingForDatabase(ctx, p.ID)
	if err != nil || len(ids) != 1 {
		t.Fatalf("waiting = %v, %v; want only the one asking about the database", ids, err)
	}
	if _, err := st.AnswerDeployment(ctx, ids[0], map[string]string{}); err != nil {
		t.Errorf("re-queuing with no overrides: %v", err)
	}
	if ids, _ := st.WaitingForDatabase(ctx, p.ID); len(ids) != 0 {
		t.Errorf("after re-queuing: %v", ids)
	}
}
