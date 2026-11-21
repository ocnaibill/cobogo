package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

func pgNeed(level string) domain.AnalysisResult {
	return domain.AnalysisResult{ServiceNeeds: []domain.ServiceNeed{{
		Engine: domain.ServicePostgres, Level: level, Variable: "DATABASE_URL", Reason: "r", Evidence: []string{"src/db.js:1"},
	}}}
}

// A pergunta do banco (DEC-42): só com evidência forte, sem escolha e sem valor
// definido. Nada é criado aqui.
func TestDatabaseFindingOnlyAsksWhenItShould(t *testing.T) {
	ctx := context.Background()
	st := storetest.New(t)
	admin, err := st.CreateAdmin(ctx, "dono", "hash")
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{store: st, log: quiet}
	newProject := func(slug string) domain.Project {
		p, err := st.CreateProject(ctx, admin.ID, slug, "https://github.com/x/"+slug, "")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	p := newProject("pergunta")
	f, err := w.databaseFinding(ctx, p, pgNeed(domain.NeedRequired))
	if err != nil || f == nil || f.Field != domain.FieldDatabase || f.Rule != "HEU-06.6" || len(f.Options) != 3 {
		t.Fatalf("strong evidence, no choice: %+v, %v", f, err)
	}
	if !strings.Contains(f.Message, "src/db.js:1") || !strings.Contains(f.Message, "DATABASE_URL") {
		t.Errorf("the question must name the evidence and the variable: %q", f.Message)
	}
	values := map[string]bool{}
	for _, o := range f.Options {
		values[o.Value] = true
	}
	if !values["managed"] || !values["external"] || !values["none"] {
		t.Errorf("options = %+v", f.Options)
	}

	if f, _ := w.databaseFinding(ctx, p, pgNeed(domain.NeedPossible)); f != nil {
		t.Errorf("weak evidence must only suggest, not ask: %+v", f)
	}
	if f, _ := w.databaseFinding(ctx, p, domain.AnalysisResult{}); f != nil {
		t.Errorf("no need, no question: %+v", f)
	}
	if _, err := st.GetService(ctx, p.ID, domain.ServicePostgres); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("asking must not register any choice: %v", err)
	}

	// Quem já definiu a variável conectou o banco por conta própria.
	q := newProject("ja-definiu")
	if _, err := st.PutEnvVariable(ctx, q.ID, "DATABASE_URL", "postgres://meu/banco", domain.EnvSourceUser); err != nil {
		t.Fatal(err)
	}
	if f, _ := w.databaseFinding(ctx, q, pgNeed(domain.NeedRequired)); f != nil {
		t.Errorf("a defined variable means no question: %+v", f)
	}

	// "Não preciso de banco" não pergunta de novo; "externo" sem valor lembra a URL.
	r := newProject("sem-banco")
	if _, err := st.ChooseDatabase(ctx, r.ID, admin.ID, domain.ServiceNone, "DATABASE_URL", ""); err != nil {
		t.Fatal(err)
	}
	if f, _ := w.databaseFinding(ctx, r, pgNeed(domain.NeedRequired)); f != nil {
		t.Errorf("'none' must not be asked again: %+v", f)
	}
	e := newProject("externo")
	if _, err := st.ChooseDatabase(ctx, e.ID, admin.ID, domain.ServiceExternal, "DATABASE_URL", ""); err != nil {
		t.Fatal(err)
	}
	if f, _ := w.databaseFinding(ctx, e, pgNeed(domain.NeedRequired)); f == nil || !strings.Contains(f.Message, "Variáveis") {
		t.Errorf("'external' without a value must remind the URL: %+v", f)
	}
	// Gerenciado: a variável vinculada já tem valor, então não há pergunta.
	m := newProject("gerenciado")
	if _, err := st.ChooseDatabase(ctx, m.ID, admin.ID, domain.ServiceManaged, "DATABASE_URL", "postgres://app:x@h:5432/app"); err != nil {
		t.Fatal(err)
	}
	if f, _ := w.databaseFinding(ctx, m, pgNeed(domain.NeedRequired)); f != nil {
		t.Errorf("a managed database must not be asked about: %+v", f)
	}
}

// O PostgreSQL gerenciado de verdade: criado uma vez, reaproveitado, protegido e
// apagado só quando pedido. Precisa de Docker (COBOGO_TEST_DOCKER=1) e PostgreSQL.
func TestManagedPostgresLifecycle(t *testing.T) {
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes com Docker")
	}
	rt, err := dockerrt.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	st := storetest.New(t)
	admin, err := st.CreateAdmin(ctx, "dono-pg", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, admin.ID, "banco-real", "https://github.com/x/banco-real", "")
	if err != nil {
		t.Fatal(err)
	}
	const pass = "senha-do-canario-pg-4471"
	url := "postgres://app:" + pass + "@" + domain.ServiceContainerName(p.ID.String()) + ":5432/app"
	svc, err := st.ChooseDatabase(ctx, p.ID, admin.ID, domain.ServiceManaged, "DATABASE_URL", url)
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{store: st, runtime: rt, log: quiet}
	net := dockerrt.ProjectNetwork(p.ID.String())
	t.Cleanup(func() {
		_ = rt.RemovePostgres(context.Background(), p.ID.String(), svc.ContainerName, svc.VolumeName)
		_ = rt.RemoveProjectNetwork(context.Background(), p.ID.String())
	})

	created, err := w.provisionPostgres(ctx, rt, p, svc)
	if err != nil || !created {
		t.Fatalf("first provision = %v, %v", created, err)
	}
	if got, _ := st.GetService(ctx, p.ID, domain.ServicePostgres); got.Status != domain.ServiceReady {
		t.Errorf("status = %q, want ready", got.Status)
	}

	// Alcançável pela rede do projeto, com a senha certa; a errada é recusada.
	psql := func(connection string) (string, error) {
		out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", net, "postgres:16-alpine", "psql", connection, "-tAc", "select 1").CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}
	if out, err := psql(url); err != nil || out != "1" {
		t.Errorf("connect with the right password: %q, %v", out, err)
	}
	if _, err := psql(strings.Replace(url, pass, "errada", 1)); err == nil {
		t.Error("a wrong password must be refused")
	}

	// Sem porta publicada e com os limites e labels do Cobogó.
	out, err := exec.CommandContext(ctx, "docker", "inspect", svc.ContainerName, "--format",
		"{{json .NetworkSettings.Ports}}|{{.HostConfig.Memory}}|{{index .Config.Labels \"cobogo.managed\"}}|{{index .Config.Labels \"cobogo.service\"}}|{{.HostConfig.RestartPolicy.Name}}").Output()
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(f) != 5 || strings.Contains(f[0], "HostPort") || f[1] != "268435456" || f[2] != "true" || f[3] != "postgres" || f[4] != "unless-stopped" {
		t.Errorf("inspect = %v", f)
	}

	// Criar de novo reaproveita: nada duplica, os dados continuam.
	if _, err := psql(url); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", net, "postgres:16-alpine", "psql", url, "-tAc", "create table marca(x int); insert into marca values (42)").CombinedOutput(); err != nil {
		t.Fatalf("write: %s %v", out, err)
	}
	created, err = w.provisionPostgres(ctx, rt, p, svc)
	if err != nil || created {
		t.Fatalf("second provision = %v, %v; want the existing one reused", created, err)
	}
	rows, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--network", net, "postgres:16-alpine", "psql", url, "-tAc", "select x from marca").Output()
	if err != nil || strings.TrimSpace(string(rows)) != "42" {
		t.Errorf("the data must survive a repeated provision: %q, %v", rows, err)
	}
	// Parado, é iniciado de novo.
	_ = exec.CommandContext(ctx, "docker", "stop", svc.ContainerName).Run()
	if created, err := w.provisionPostgres(ctx, rt, p, svc); err != nil || created {
		t.Fatalf("provision with a stopped database = %v, %v", created, err)
	}

	// Um volume do banco aparece com o caminho certo e não é "sem projeto".
	vols, _ := st.ListVolumesFor(ctx, p.ID, admin.ID)
	if len(vols) != 1 || vols[0].Path != domain.PostgresDataPath || vols[0].Orphan {
		t.Errorf("volumes = %+v", vols)
	}

	// Exclusão: pede, o worker remove contêiner e volume.
	op, err := st.RequestDeleteService(ctx, p.ID, admin.ID, p.Slug)
	if err != nil {
		t.Fatal(err)
	}
	res, err := w.deleteService(ctx, &op)
	if err != nil || res["container"] != svc.ContainerName {
		t.Fatalf("deleteService = %v, %v", res, err)
	}
	if out, _ := exec.CommandContext(ctx, "docker", "ps", "-a", "-q", "-f", "name="+svc.ContainerName).Output(); strings.TrimSpace(string(out)) != "" {
		t.Error("the database container must be gone")
	}
	if sizes, _ := rt.VolumeSizes(ctx); func() bool { _, ok := sizes[svc.VolumeName]; return ok }() {
		t.Error("the data volume must be gone")
	}
	if _, err := st.GetService(ctx, p.ID, domain.ServicePostgres); err == nil {
		t.Error("the service must be closed")
	}
	if again, err := w.deleteService(ctx, &op); err != nil || again["already_gone"] != true {
		t.Errorf("deleting twice is a no-op: %v, %v", again, err)
	}
}
