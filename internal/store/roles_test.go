package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

func columnsOf(t *testing.T, r storetest.Roled, table string) []string {
	t.Helper()
	rows, err := r.Owner.Query(ctx, `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var cols []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, c)
	}
	return cols
}

func tablesOf(t *testing.T, r storetest.Roled) []string {
	t.Helper()
	rows, err := r.Owner.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = current_schema() ORDER BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	return tables
}

// Toda tabela do esquema tem entrada na matriz (ou é a de controle das
// migrations): uma migration nova sem decisão de privilégio não passa.
func TestEveryTableIsInTheAccessMatrix(t *testing.T) {
	r := storetest.NewWithRoles(t)
	matrix := store.AccessMatrix()
	for _, table := range tablesOf(t, r) {
		if _, ok := matrix[table]; !ok {
			t.Errorf("table %s has no entry in the access matrix (DEC-33 item 4)", table)
		}
	}
	for table := range matrix {
		if !slices.Contains(tablesOf(t, r), table) {
			t.Errorf("the matrix lists %s, which does not exist", table)
		}
	}
}

// O privilégio real de cada role, coluna a coluna, é exatamente o da matriz.
func TestPrivilegesMatchTheMatrixExactly(t *testing.T) {
	r := storetest.NewWithRoles(t)
	matrix := store.AccessMatrix()
	for table, access := range matrix {
		for role, p := range map[string]store.Privileges{r.Roles.API: access.API, r.Roles.Worker: access.Worker, r.Roles.Collector: access.Collector} {
			for _, col := range columnsOf(t, r, table) {
				for _, priv := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
					want := slices.Contains(p.Table, priv) || slices.Contains(p.Columns[priv], col)
					var got bool
					if err := r.Owner.QueryRow(ctx, `SELECT has_column_privilege($1, $2, $3, $4)`, role, table, col, priv).Scan(&got); err != nil {
						t.Fatal(err)
					}
					if got != want {
						t.Errorf("%s %s on %s.%s = %v, want %v", role, priv, table, col, got, want)
					}
				}
			}
			for _, priv := range []string{"DELETE", "TRUNCATE", "TRIGGER"} {
				want := slices.Contains(p.Table, priv)
				var got bool
				if err := r.Owner.QueryRow(ctx, `SELECT has_table_privilege($1, $2, $3)`, role, table, priv).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("%s %s on %s = %v, want %v", role, priv, table, got, want)
				}
			}
		}
	}
	for _, role := range []string{r.Roles.API, r.Roles.Worker, r.Roles.Collector} {
		var create bool
		if err := r.Owner.QueryRow(ctx, `SELECT has_schema_privilege($1, current_schema(), 'CREATE')`, role).Scan(&create); err != nil {
			t.Fatal(err)
		}
		if create {
			t.Errorf("%s must not be able to create objects in the schema", role)
		}
	}
}

func denied(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "42501"
}

// O que os roles não podem, o banco recusa de verdade.
func TestRolesAreRefusedWhatTheyMustNotDo(t *testing.T) {
	r := storetest.NewWithRoles(t)
	exec := func(pool interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	}, sql string) error {
		_, err := pool.Exec(ctx, sql)
		return err
	}
	for name, sql := range map[string]string{
		"read the ciphertext":      `SELECT ciphertext FROM env_variables`,
		"select star on env":       `SELECT * FROM env_variables`,
		"create a table":           `CREATE TABLE intruso (x int)`,
		"drop a table":             `DROP TABLE users`,
		"alter a table":            `ALTER TABLE projects ADD COLUMN x int`,
		"truncate":                 `TRUNCATE deployments`,
		"write schema_migrations":  `INSERT INTO schema_migrations (version) VALUES ('x')`,
		"delete schema_migrations": `DELETE FROM schema_migrations`,
		"delete releases":          `DELETE FROM releases`,
		"delete projects":          `DELETE FROM projects`,
		"update audit events":      `UPDATE audit_events SET outcome = 'x'`,
		"delete audit events":      `DELETE FROM audit_events`,
		"insert releases":          `INSERT INTO releases (id, deployment_id, image_id, container_id, operational_state, health, host_port) VALUES (gen_random_uuid(), gen_random_uuid(), '', '', '', '', 0)`,
		"insert analyses":          `INSERT INTO analyses (id, project_id, commit_sha, rules_version, outcome, result) VALUES (gen_random_uuid(), gen_random_uuid(), '', '', '', '{}')`,
		"create a role":            `CREATE ROLE intruso`,
	} {
		if err := exec(r.API, sql); !denied(err) {
			t.Errorf("API %s: %v, want permission denied", name, err)
		}
	}
	for name, sql := range map[string]string{
		"read users":              `SELECT * FROM users`,
		"read sessions":           `SELECT * FROM sessions`,
		"read audit events":       `SELECT * FROM audit_events`,
		"insert a user":           `INSERT INTO users (id, login, password_hash) VALUES (gen_random_uuid(), 'x', 'x')`,
		"insert a project":        `INSERT INTO projects (id, slug, repository_url) VALUES (gen_random_uuid(), 'x', 'x')`,
		"update a project":        `UPDATE projects SET slug = slug`,
		"delete a project":        `DELETE FROM projects`,
		"write env":               `INSERT INTO env_variables (id, project_id, name, version, source, ciphertext, key_version) VALUES (gen_random_uuid(), gen_random_uuid(), 'x', 1, 'user', ''::bytea, 1)`,
		"write settings":          `INSERT INTO settings (key, value) VALUES ('x', 'x')`,
		"create a deployment":     `INSERT INTO deployments (id, project_id, commit_sha, state, idempotency_key) VALUES (gen_random_uuid(), gen_random_uuid(), '', 'QUEUED', 'x')`,
		"create a table":          `CREATE TABLE intruso (x int)`,
		"drop a table":            `DROP TABLE releases`,
		"truncate":                `TRUNCATE jobs`,
		"write schema_migrations": `DELETE FROM schema_migrations`,
		"delete events":           `DELETE FROM deployment_events`,
		"delete releases":         `DELETE FROM releases`,
	} {
		if err := exec(r.Worker, sql); !denied(err) {
			t.Errorf("worker %s: %v, want permission denied", name, err)
		}
	}
	// A API continua lendo os metadados das variáveis.
	if _, err := r.API.Exec(ctx, `SELECT name, version FROM env_variables`); err != nil {
		t.Errorf("the API must read the variables' metadata: %v", err)
	}
}

// A API cria o que o worker processa; o worker conclui e a API lê o resultado.
// Nada além do concedido é necessário (DEC-33 itens 2 e 3).
func TestTheWholeFlowWorksWithOnlyTheGrantedPrivileges(t *testing.T) {
	r := storetest.NewWithRoles(t)
	api, worker := r.APIStore, r.WorkerStore

	// --- API: administrador, sessão, projeto, configuração, variável ---
	admin, err := api.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatalf("CreateAdmin: %v", err)
	}
	if _, _, err := api.GetUserByLogin(ctx, "admin"); err != nil {
		t.Fatalf("GetUserByLogin: %v", err)
	}
	if err := api.SetPasswordHash(ctx, admin.ID, "hash2"); err != nil {
		t.Fatalf("SetPasswordHash: %v", err)
	}
	if _, err := api.CreateSession(ctx, admin.ID, []byte("token-hash-1"), time.Hour, "1.2.3.4", "teste"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, _, err := api.GetSession(ctx, []byte("token-hash-1"), 2*time.Hour); err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if err := api.DeleteOtherSessions(ctx, admin.ID, []byte("outro")); err != nil {
		t.Fatalf("DeleteOtherSessions: %v", err)
	}
	if _, err := api.DeleteExpiredSessions(ctx, time.Hour); err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if err := api.AppendAudit(ctx, domain.AuditEvent{ActorID: &admin.ID, Action: "teste", Target: "x", Outcome: "ok"}); err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	project, err := api.CreateProject(ctx, admin.ID, "app-roles", "https://github.com/exemplo/repo", "")
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if _, err := api.SetProjectLimits(ctx, project.ID, admin.ID, domain.Limits{AppMemoryMB: 2048}); err != nil {
		t.Fatalf("SetProjectLimits: %v", err)
	}
	if err := api.PutSetting(ctx, "apps_domain", "cobogo.test", admin.ID); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}
	if _, err := api.PutEnvVariable(ctx, project.ID, "API_TOKEN", "valor-secreto", domain.EnvSourceUser); err != nil {
		t.Fatalf("PutEnvVariable: %v", err)
	}
	if _, err := api.PutEnvVariable(ctx, project.ID, "API_TOKEN", "valor-novo", domain.EnvSourceUser); err != nil {
		t.Fatalf("PutEnvVariable (replace): %v", err)
	}
	if _, err := api.GetEnvVariable(ctx, project.ID, "API_TOKEN"); err != nil {
		t.Fatalf("GetEnvVariable: %v", err)
	}
	if vars, err := api.ListEnvVariables(ctx, project.ID); err != nil || len(vars) != 1 {
		t.Fatalf("ListEnvVariables = %v, %v", vars, err)
	}
	// A API tem a chave mas não lê o texto cifrado (DEC-33 item 3).
	if _, _, err := api.RevealEnvValues(ctx, project.ID); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("the API must not read the ciphertext: %v", err)
	}
	dep, _, err := api.CreateDeploymentWithJob(ctx, project.ID, "k1", nil)
	if err != nil {
		t.Fatalf("CreateDeploymentWithJob: %v", err)
	}

	// --- worker: reivindica, processa, pede informação, conclui ---
	job, claimed, err := worker.ClaimNextJob(ctx, time.Minute, 3)
	if err != nil || job == nil || claimed.ID != dep.ID {
		t.Fatalf("ClaimNextJob = %v %v %v", job, claimed, err)
	}
	o := worker.Owned(store.LeaseOf(job))
	step := func(to domain.DeploymentState) {
		t.Helper()
		if err := o.Transition(ctx, dep.ID, to, ""); err != nil {
			t.Fatalf("Transition %s: %v", to, err)
		}
	}
	step(domain.DeploymentFetching)
	if err := o.SetCommitSHA(ctx, dep.ID, "abc123"); err != nil {
		t.Fatalf("SetCommitSHA: %v", err)
	}
	step(domain.DeploymentAnalyzing)
	if _, err := o.SaveAnalysis(ctx, dep.ID, project.ID, "abc123", domain.AnalysisResult{Outcome: domain.AnalysisNeedsInput}); err != nil {
		t.Fatalf("SaveAnalysis: %v", err)
	}
	if err := o.AppendEvent(ctx, dep.ID, "analyzing", "completed", nil); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if err := o.Renew(ctx, time.Minute); err != nil {
		t.Fatalf("Renew: %v", err)
	}
	step(domain.DeploymentNeedsInput)
	if err := o.Finish(ctx); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	if _, err := worker.GetProject(ctx, project.ID); err != nil {
		t.Fatalf("worker GetProject: %v", err)
	}
	if _, err := worker.ListSettings(ctx); err != nil {
		t.Fatalf("worker ListSettings: %v", err)
	}
	values, _, err := worker.RevealEnvValues(ctx, project.ID)
	if err != nil || values["API_TOKEN"] != "valor-novo" {
		t.Fatalf("the worker must read and decrypt the variables: %v, %v", values, err)
	}

	// --- API: responde à pendência (reabre o job); o worker segue até a promoção ---
	if _, err := api.AnswerDeployment(ctx, dep.ID, map[string]string{"port": "3000"}); err != nil {
		t.Fatalf("AnswerDeployment: %v", err)
	}
	job, _, err = worker.ClaimNextJob(ctx, time.Minute, 3)
	if err != nil || job == nil {
		t.Fatalf("second claim: %v %v", job, err)
	}
	o = worker.Owned(store.LeaseOf(job))
	for _, to := range []domain.DeploymentState{
		domain.DeploymentFetching, domain.DeploymentAnalyzing, domain.DeploymentGenerating, domain.DeploymentBuilding,
		domain.DeploymentProvisioning, domain.DeploymentStarting, domain.DeploymentVerifying,
	} {
		step(to)
	}
	if _, err := o.PromoteRelease(ctx, dep.ID, store.ReleaseSpec{
		ImageID: "img", ContainerID: "ctr", Health: "healthy", HostPort: 1,
		URL: "https://app-roles.cobogo.test/", RouteHost: "app-roles.cobogo.test", RouteTarget: "http://ctr:3000",
	}); err != nil {
		t.Fatalf("PromoteRelease: %v", err)
	}
	if err := o.Finish(ctx); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	// --- API: lê o resultado ---
	if _, err := api.GetReleaseByDeployment(ctx, dep.ID); err != nil {
		t.Fatalf("GetReleaseByDeployment: %v", err)
	}
	if routes, err := api.ListRoutes(ctx); err != nil || len(routes) != 1 {
		t.Fatalf("ListRoutes = %v, %v", routes, err)
	}
	if page, err := api.ListEventsAfter(ctx, dep.ID, 0, 100); err != nil || len(page.Events) == 0 || !page.Settled {
		t.Fatalf("ListEventsAfter = %+v, %v", page, err)
	}
	if _, err := api.LastEventSequence(ctx, dep.ID); err != nil {
		t.Fatalf("LastEventSequence: %v", err)
	}
	if _, _, err := api.ListProjectsFor(ctx, admin.ID, 10, nil); err != nil {
		t.Fatalf("ListProjectsFor: %v", err)
	}
	if _, _, err := api.ListDeployments(ctx, project.ID, 10, nil); err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if got, err := api.GetDeploymentFor(ctx, dep.ID, admin.ID); err != nil || got.AnalysisID == nil {
		t.Fatalf("GetDeploymentFor = %+v, %v", got, err)
	} else if _, err := api.GetAnalysis(ctx, *got.AnalysisID); err != nil {
		t.Fatalf("GetAnalysis: %v", err)
	}

	// --- API: cancelar e remover variável ---
	second, _, err := api.CreateDeploymentWithJob(ctx, project.ID, "k2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.CancelDeployment(ctx, second.ID); err != nil {
		t.Fatalf("CancelDeployment: %v", err)
	}
	if err := api.DeleteEnvVariable(ctx, project.ID, "API_TOKEN"); err != nil {
		t.Fatalf("DeleteEnvVariable: %v", err)
	}
	if _, err := api.DeleteSetting(ctx, "apps_domain"); err != nil {
		t.Fatalf("DeleteSetting: %v", err)
	}
	if n, err := api.CountUsers(ctx); err != nil || n != 1 {
		t.Fatalf("CountUsers = %d, %v", n, err)
	}

	// --- ciclo de vida (DEC-38): a API pede, o worker executa ---
	if _, err := api.RequestApplyLimits(ctx, project.ID, admin.ID); err != nil {
		t.Fatalf("RequestApplyLimits: %v", err)
	}
	at := time.Now().UTC().Add(-time.Second)
	if _, err := worker.EnqueueOperation(ctx, &project.ID, domain.OpRetireReleases, nil, at, nil); err != nil {
		t.Fatalf("worker EnqueueOperation: %v", err)
	}
	if _, err := worker.EnqueueOperation(ctx, &project.ID, domain.OpRetireReleases, nil, at, nil); err != nil {
		t.Fatalf("worker EnqueueOperation (coalesce): %v", err)
	}
	for i := 0; i < 2; i++ {
		op, err := worker.ClaimNextOperation(ctx, time.Minute, 3)
		if err != nil || op == nil {
			t.Fatalf("worker ClaimNextOperation = %v, %v", op, err)
		}
		if err := worker.FinishOperation(ctx, op.ID, map[string]any{"ok": true}); err != nil {
			t.Fatalf("FinishOperation: %v", err)
		}
	}
	if _, err := worker.ContainerFacts(ctx, []uuid.UUID{dep.ID}); err != nil {
		t.Fatalf("worker ContainerFacts: %v", err)
	}
	if err := worker.MarkReleasesRetired(ctx, []string{"ctr"}); err != nil {
		t.Fatalf("MarkReleasesRetired: %v", err)
	}
	if err := worker.AppendEvent(ctx, dep.ID, "lifecycle", "retired", map[string]any{"x": 1}); err != nil {
		t.Fatalf("worker AppendEvent: %v", err)
	}
	if err := worker.RecordOperation(ctx, nil, domain.OpSweep, map[string]any{"n": 1}); err != nil {
		t.Fatalf("worker RecordOperation: %v", err)
	}
	if _, err := worker.ActiveRelease(ctx, project.ID); err != nil {
		t.Fatalf("worker ActiveRelease: %v", err)
	}
	// Excluir o projeto: só a API, e só com as permissões que tem.
	del, err := api.DeleteProject(ctx, project.ID, admin.ID, "app-roles", false)
	if err != nil {
		t.Fatalf("api DeleteProject: %v", err)
	}
	if _, err := api.GetOperationFor(ctx, del.ID, admin.ID); err != nil {
		t.Fatalf("api GetOperationFor: %v", err)
	}
	if _, err := api.ListOperationsFor(ctx, project.ID, admin.ID, 10); err != nil {
		t.Fatalf("api ListOperationsFor: %v", err)
	}
	claimed2, err := worker.ClaimNextOperation(ctx, time.Minute, 3)
	if err != nil || claimed2 == nil || claimed2.Kind != domain.OpDeleteProject {
		t.Fatalf("the worker must claim the delete: %+v, %v", claimed2, err)
	}
	if err := worker.MarkProjectReleasesRemoved(ctx, project.ID); err != nil {
		t.Fatalf("MarkProjectReleasesRemoved: %v", err)
	}
	if err := worker.FinishOperation(ctx, claimed2.ID, nil); err != nil {
		t.Fatalf("FinishOperation (delete): %v", err)
	}
	if got, err := worker.GetProject(ctx, project.ID); err != nil || got.DeletedAt == nil {
		t.Fatalf("the worker still reads the deleted project: %+v, %v", got, err)
	}

	// O que continua proibido: o histórico não se apaga por nenhum dos dois roles.
	for name, pool := range map[string]interface {
		Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	}{"api": r.API, "worker": r.Worker} {
		for _, table := range []string{"deployment_events", "deployments", "analyses", "releases", "audit_events", "operations", "projects"} {
			if _, err := pool.Exec(ctx, "DELETE FROM "+table); err == nil {
				t.Errorf("the %s role must not delete from %s", name, table)
			}
		}
	}
	if _, err := r.Worker.Exec(ctx, "UPDATE projects SET deleted_at = NULL"); err == nil {
		t.Error("the worker must not touch projects")
	}
	if _, err := r.API.Exec(ctx, "UPDATE operations SET state = 'DONE'"); err == nil {
		t.Error("the API must not conclude operations")
	}
	_ = fmt.Sprint
	_ = uuid.Nil
}

func TestApplyGrantsIsIdempotentAndRevokesWhatIsNotInTheMatrix(t *testing.T) {
	r := storetest.NewWithRoles(t)
	// Um privilégio a mais, dado por fora da matriz, some na reaplicação.
	if _, err := r.Owner.Exec(ctx, fmt.Sprintf(`GRANT DELETE ON releases TO "%s"`, r.Roles.API)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if applied, err := store.ApplyGrants(ctx, r.Owner, r.Roles); err != nil || !applied {
			t.Fatalf("round %d: applied=%v err=%v", i, applied, err)
		}
	}
	var can bool
	if err := r.Owner.QueryRow(ctx, `SELECT has_table_privilege($1, 'releases', 'DELETE')`, r.Roles.API).Scan(&can); err != nil || can {
		t.Errorf("a stray privilege must be revoked on the next run: %v, %v", can, err)
	}
}

func TestApplyGrantsRefusesATableOutsideTheMatrix(t *testing.T) {
	r := storetest.NewWithRoles(t)
	if _, err := r.Owner.Exec(ctx, `CREATE TABLE tabela_nova (x int)`); err != nil {
		t.Fatal(err)
	}
	applied, err := store.ApplyGrants(ctx, r.Owner, r.Roles)
	if err == nil || applied || !strings.Contains(err.Error(), "tabela_nova") {
		t.Fatalf("applied=%v err=%v, want an error naming the table", applied, err)
	}
}

func TestApplyGrantsWithoutRolesIsSkippedAndHalfRolesFail(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	_ = st
	if applied, err := store.ApplyGrants(ctx, pool, store.Roles{API: "nao_existe_a", Worker: "nao_existe_b"}); err != nil || applied {
		t.Errorf("no roles: applied=%v err=%v, want skipped", applied, err)
	}
	r := storetest.NewWithRoles(t)
	if _, err := store.ApplyGrants(ctx, r.Owner, store.Roles{API: r.Roles.API, Worker: "nao_existe_b"}); err == nil {
		t.Error("only one of the two roles existing must be an error")
	}
}

func TestCheckSchemaAndPrepare(t *testing.T) {
	_, pool := storetest.NewWithPool(t)
	if err := store.CheckSchema(ctx, pool); err != nil {
		t.Fatalf("a migrated schema must pass: %v", err)
	}
	var latest string
	if err := pool.QueryRow(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&latest); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, latest); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckSchema(ctx, pool); err == nil || !strings.Contains(err.Error(), latest) || !strings.Contains(err.Error(), "atrasado") {
		t.Errorf("a behind schema = %v, want an error naming %s", err, latest)
	}
	if err := store.Prepare(ctx, pool, false); err == nil {
		t.Error("Prepare without auto-migrate must not accept a behind schema")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, latest); err != nil {
		t.Fatal(err)
	}
	if err := store.Prepare(ctx, pool, false); err != nil {
		t.Errorf("Prepare without auto-migrate on a current schema: %v", err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ('9999_do_futuro.sql')`); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckSchema(ctx, pool); err == nil || !strings.Contains(err.Error(), "mais novo") {
		t.Errorf("a newer schema = %v", err)
	}
}

func TestCheckSchemaOnAnEmptyDatabase(t *testing.T) {
	pool := storetest.Pool(t) // schema novo, sem migrations
	if err := store.CheckSchema(ctx, pool); err == nil || !strings.Contains(err.Error(), "migrate") {
		t.Errorf("an unprepared database = %v, want a hint to run migrate", err)
	}
}

func TestPrepareMigratesOnlyWhenAsked(t *testing.T) {
	pool := storetest.Pool(t)
	if err := store.Prepare(ctx, pool, false); err == nil {
		t.Fatal("without auto-migrate an empty database must be refused, not migrated")
	}
	var tables int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_tables WHERE schemaname = current_schema() AND tablename = 'projects'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("a refused Prepare must not create anything: %d, %v", tables, err)
	}
	if err := store.Prepare(ctx, pool, true); err != nil {
		t.Fatalf("with auto-migrate: %v", err)
	}
	if err := store.CheckSchema(ctx, pool); err != nil {
		t.Errorf("after Prepare: %v", err)
	}
}

// Cada role consegue conferir o esquema ao subir (é o que a API e o worker
// fazem com COBOGO_AUTO_MIGRATE=0), e nenhum consegue migrar.
func TestEachRoleCanCheckTheSchemaButNotMigrate(t *testing.T) {
	r := storetest.NewWithRoles(t)
	for name, pool := range map[string]*pgxpool.Pool{"API": r.API, "worker": r.Worker} {
		if err := store.Prepare(ctx, pool, false); err != nil {
			t.Errorf("%s must be able to check the schema: %v", name, err)
		}
		if err := store.Migrate(ctx, pool); err == nil {
			t.Errorf("%s must not be able to migrate", name)
		}
	}
}

// DEC-46: os logs de execução com os roles de verdade. O worker grava (isso usa a
// sequência da chave, que exige USAGE) e apaga o que passou da retenção; a API só
// lê. Pegou uma falta de privilégio que o teste com um usuário só não via.
func TestRuntimeLogsWorkWithOnlyTheGrantedPrivileges(t *testing.T) {
	r := storetest.NewWithRoles(t)
	api, worker := r.APIStore, r.WorkerStore
	admin, err := api.CreateAdmin(ctx, "admin", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := api.CreateProject(ctx, admin.ID, "logs-roles", "https://github.com/exemplo/repo", "")
	if err != nil {
		t.Fatal(err)
	}
	lines := []domain.LogLine{
		{ProjectID: p.ID, ContainerID: "c1", ContainerName: "n1", Origin: domain.LogOriginApp, Stream: domain.LogStdout, At: time.Now().UTC(), Line: "um"},
		{ProjectID: p.ID, ContainerID: "c1", ContainerName: "n1", Origin: domain.LogOriginApp, Stream: domain.LogStderr, At: time.Now().UTC(), Line: "dois"},
	}
	if err := worker.AppendRuntimeLogs(ctx, lines); err != nil {
		t.Fatalf("the worker must be able to insert logs: %v", err)
	}
	if _, ok, err := worker.LastRuntimeLogAt(ctx, "c1"); err != nil || !ok {
		t.Fatalf("worker read = %v, %v", ok, err)
	}
	page, err := api.ListRuntimeLogsFor(ctx, p.ID, admin.ID, "", 0, 0, 10)
	if err != nil || len(page.Lines) != 2 {
		t.Fatalf("the API must read the logs: %+v, %v", page, err)
	}
	if err := api.AppendRuntimeLogs(ctx, lines); err == nil {
		t.Error("the API must not write logs")
	}
	if _, err := api.PruneRuntimeLogs(ctx, time.Hour, 10); err == nil {
		t.Error("the API must not delete logs")
	}
	if n, err := worker.PruneRuntimeLogs(ctx, time.Hour, 1); err != nil || n != 1 {
		t.Errorf("the worker prune = %d, %v", n, err)
	}

	// A janela de reautenticação (DEC-45) é da API, na própria sessão.
	sess, err := api.CreateSession(ctx, admin.ID, []byte("token-reauth-roles"), time.Hour, "1.2.3.4", "teste")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.MarkReauth(ctx, sess.ID, domain.CommandReauthWindow); err != nil {
		t.Errorf("the API must mark the reauth window: %v", err)
	}
	if _, err := worker.MarkReauth(ctx, sess.ID, time.Minute); err == nil {
		t.Error("the worker must not touch sessions")
	}
}
