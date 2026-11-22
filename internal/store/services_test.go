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
	sv, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": url})
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
	again, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": "postgres://app:OUTRA@host:5432/app"})
	if err != nil || again.ID != sv.ID {
		t.Fatalf("choosing managed twice must be a no-op: %+v, %v", again, err)
	}
	if vals, _, _ := st.RevealEnvValues(ctx, p.ID); vals["DATABASE_URL"] != url {
		t.Error("the password must not change when the choice is repeated")
	}
	// Com o gerenciado, outra escolha deixaria banco e dados órfãos.
	for _, mode := range []string{domain.ServiceExternal, domain.ServiceNone} {
		if _, err := st.ChooseDatabase(ctx, p.ID, own.ID, mode, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": ""}); !errors.Is(err, store.ErrManagedExists) {
			t.Errorf("%s over a managed database = %v, want ErrManagedExists", mode, err)
		}
	}

	// Um valor digitado pela pessoa nunca é sobrescrito.
	q := newProject(t, st)
	if _, err := st.PutEnvVariable(ctx, q.ID, "DATABASE_URL", "postgres://meu/banco", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ChooseDatabase(ctx, q.ID, own.ID, domain.ServiceManaged, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": url}); !errors.Is(err, store.ErrVariableDefined) {
		t.Errorf("managed over a typed value = %v, want ErrVariableDefined", err)
	}
	if vals, _, _ := st.RevealEnvValues(ctx, q.ID); vals["DATABASE_URL"] != "postgres://meu/banco" {
		t.Error("the user's value was overwritten")
	}
	if _, err := st.GetService(ctx, q.ID, domain.ServicePostgres); !errors.Is(err, store.ErrNotFound) {
		t.Error("a refused choice must not register an intent")
	}
	// Mas external e none valem, e dá para trocar de um para o outro.
	if sv, err := st.ChooseDatabase(ctx, q.ID, own.ID, domain.ServiceExternal, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": ""}); err != nil || sv.Mode != domain.ServiceExternal {
		t.Errorf("external = %+v, %v", sv, err)
	}
	if sv, err := st.ChooseDatabase(ctx, q.ID, own.ID, domain.ServiceNone, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": ""}); err != nil || sv.Mode != domain.ServiceNone {
		t.Errorf("none after external = %+v, %v", sv, err)
	}
	// Só o dono escolhe.
	if _, err := st.ChooseDatabase(ctx, p.ID, uuid.New(), domain.ServiceNone, map[string]string{"url": "X"}, map[string]string{"url": ""}); !errors.Is(err, store.ErrNotFound) {
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
	if _, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": "postgres://app:x@h:5432/app"}); err != nil {
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
	if sv2, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": "postgres://app:nova@h:5432/app"}); err != nil || sv2.ID == sv.ID {
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

// Conexão em partes (DEC-44): cada papel vira uma variável vinculada, nenhuma
// colisão com valor do usuário grava algo, e apagar o serviço leva todas.
func TestChooseDatabaseInPartsLinksEveryRole(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	names := map[string]string{"host": "POSTGRES_HOST", "port": "POSTGRES_PORT", "user": "POSTGRES_USER", "password": "POSTGRES_PASSWORD", "database": "POSTGRES_DB", "ca": "POSTGRES_CA"}
	values := map[string]string{"host": "cobogo-pg-x", "port": "5432", "user": "app", "password": "senha-do-canario", "database": "app"}

	// Uma variável do usuário na família: recusa e nada é gravado.
	if _, err := st.PutEnvVariable(ctx, p.ID, "POSTGRES_PORT", "6543", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	_, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, names, values)
	var def *store.DefinedVariableError
	if !errors.Is(err, store.ErrVariableDefined) || !errors.As(err, &def) || def.Name != "POSTGRES_PORT" {
		t.Fatalf("want a defined-variable error naming POSTGRES_PORT, got %v", err)
	}
	if _, err := st.GetEnvVariable(ctx, p.ID, "POSTGRES_HOST"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a refused choice must not write anything: %v", err)
	}
	if err := st.DeleteEnvVariable(ctx, p.ID, "POSTGRES_PORT"); err != nil {
		t.Fatal(err)
	}

	sv, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, names, values)
	if err != nil {
		t.Fatal(err)
	}
	if !sv.TLS || sv.Variable != "POSTGRES_PASSWORD" || sv.Variables["host"] != "POSTGRES_HOST" || sv.Variables["ca"] != "POSTGRES_CA" {
		t.Fatalf("service = %+v", sv)
	}
	got, err := st.GetService(ctx, p.ID, domain.ServicePostgres)
	if err != nil || got.Variables["database"] != "POSTGRES_DB" || !got.TLS {
		t.Fatalf("reloaded = %+v, %v", got, err)
	}
	for role, name := range names {
		v, err := st.GetEnvVariable(ctx, p.ID, name)
		if role == "ca" {
			if !errors.Is(err, store.ErrNotFound) {
				t.Errorf("the CA is delivered at run time and must not be stored: %v", err)
			}
			continue
		}
		if err != nil || v.Source != domain.EnvSourceLinked {
			t.Errorf("%s (%s): %+v, %v", role, name, v, err)
		}
	}

	if _, err := st.RequestDeleteService(ctx, p.ID, own.ID, p.Slug); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if _, err := st.GetEnvVariable(ctx, p.ID, name); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("%s must leave with the service: %v", name, err)
		}
	}
}
