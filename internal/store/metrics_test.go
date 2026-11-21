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

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func sampleOf(project uuid.UUID, id, role string, state string, cpu *float64, mem *int64) store.MetricSample {
	m := store.MetricSample{ContainerID: id, ProjectID: &project, Role: role, Name: "cobogo-" + id, State: state, MemLimitBytes: i64(1 << 30), CPULimitCores: f64(1)}
	m.CPUCores, m.MemBytes = cpu, mem
	return m
}

// Métricas (DEC-43): séries com lacuna, valor nulo que não vira zero, saúde antiga
// por três intervalos, marcadores de reinício e de deploy, e o agregado.
func TestProjectMetricsKeepGapsAndNeverTurnAbsenceIntoZero(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	now := time.Now().UTC().Truncate(time.Second)

	// Sem coletor ainda: a janela vem vazia, sem batimento.
	empty, err := st.ProjectMetrics(ctx, p.ID, "15m", now)
	if err != nil || len(empty.Containers) != 0 || empty.Collector != nil {
		t.Fatalf("empty = %+v, %v", empty, err)
	}
	if _, err := st.ProjectMetrics(ctx, p.ID, "2h", now); !errors.Is(err, store.ErrBadWindow) {
		t.Errorf("unknown window = %v, want ErrBadWindow", err)
	}

	// Um contêiner amostrado a cada 5 s por 1 min, com uma lacuna de 30 s no meio,
	// e depois parado (sem leitura: valores nulos).
	const app = "app-aaaa"
	for i := 0; i < 60; i += 5 {
		if i >= 20 && i < 50 { // lacuna: o coletor não amostrou
			continue
		}
		at := now.Add(time.Duration(i-120) * time.Second)
		cpu, mem := f64(0.25), i64(100<<20)
		if err := st.InsertSamples(ctx, at, []store.MetricSample{sampleOf(p.ID, app, "app", "running", cpu, mem)}); err != nil {
			t.Fatal(err)
		}
	}
	stopped := sampleOf(p.ID, app, "app", "exited", nil, nil)
	if err := st.InsertSamples(ctx, now.Add(-30*time.Second), []store.MetricSample{stopped}); err != nil {
		t.Fatal(err)
	}
	if err := st.Heartbeat(ctx, now.Add(-2*time.Second), 800*time.Millisecond, 1, 0, "", 5*time.Second); err != nil {
		t.Fatal(err)
	}

	m, err := st.ProjectMetrics(ctx, p.ID, "15m", now)
	if err != nil || len(m.Containers) != 1 {
		t.Fatalf("metrics = %+v, %v", m, err)
	}
	c := m.Containers[0]
	if c.State != "exited" || c.Current.CPUCores != nil || c.Current.MemBytes != nil {
		t.Errorf("a stopped container has no reading, and absence must not become zero: %+v", c.Current)
	}
	if !c.Stale {
		t.Error("a reading 30 s old with a 5 s interval (stale after 15 s) must be flagged as old")
	}
	// A série tem só os instantes amostrados: a lacuna não vira pontos.
	if len(c.Series) != 7 || c.Series[0].CPUAvg == nil || *c.Series[0].CPUAvg != 0.25 {
		t.Errorf("series = %d points, first %+v; want 7 sampled points and no fabricated ones", len(c.Series), c.Series[0])
	}
	last := c.Series[len(c.Series)-1]
	if last.CPUAvg != nil || last.MemAvg != nil {
		t.Errorf("the stopped reading must be a null point, not zero: %+v", last)
	}
	if m.Collector == nil || m.Collector.Stale || m.Collector.Containers != 1 {
		t.Errorf("collector = %+v", m.Collector)
	}
	if m.StepSeconds != 5 || m.Source != "raw" {
		t.Errorf("step = %d source = %s", m.StepSeconds, m.Source)
	}
	_ = own
}

func TestStaleAfterThreeIntervalsAndTheCollectorGoingSilent(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	now := time.Now().UTC().Truncate(time.Second)
	_ = st.InsertSamples(ctx, now.Add(-10*time.Second), []store.MetricSample{sampleOf(p.ID, "fresh", "app", "running", f64(0.1), i64(1<<20))})
	_ = st.InsertSamples(ctx, now.Add(-40*time.Second), []store.MetricSample{sampleOf(p.ID, "old", "app", "running", f64(0.1), i64(1<<20))})
	_ = st.Heartbeat(ctx, now.Add(-40*time.Second), time.Second, 2, 0, "", 5*time.Second)

	m, _ := st.ProjectMetrics(ctx, p.ID, "15m", now)
	by := map[string]store.ContainerMetrics{}
	for _, c := range m.Containers {
		by[c.ContainerID] = c
	}
	if by["fresh"].Stale || !by["old"].Stale {
		t.Errorf("stale flags: fresh=%v old=%v", by["fresh"].Stale, by["old"].Stale)
	}
	if m.Collector == nil || !m.Collector.Stale {
		t.Errorf("a collector silent for 40 s with a 5 s interval is stale: %+v", m.Collector)
	}
	ov, err := st.MetricsOverview(ctx, now)
	if err != nil || ov.Running != 1 || ov.Unknown != 1 {
		t.Errorf("overview = %+v, %v; want 1 running (fresh) and 1 unknown (stale)", ov, err)
	}
}

func TestMetricMarkersCoverRestartsOOMAndDeploys(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	now := time.Now().UTC().Truncate(time.Second)

	o, dep := verifying(t, st, p)
	if _, err := o.PromoteRelease(ctx, dep.ID, routed("ctr-m", 1)); err != nil {
		t.Fatal(err)
	}
	started := now.Add(-time.Hour)
	for i, restarts := range []int{0, 0, 1, 1, 2} {
		s := sampleOf(p.ID, "ctr-m", "app", "running", f64(0.1), i64(1<<20))
		s.Restarts = restarts
		s.OOM = i == 4
		// Um início manual muda a hora de início sem mexer na contagem do Docker.
		if i == 3 {
			started = now.Add(-time.Minute)
		}
		s.StartedAt = &started
		if err := st.InsertSamples(ctx, now.Add(time.Duration(i-10)*5*time.Second), []store.MetricSample{s}); err != nil {
			t.Fatal(err)
		}
	}
	m, err := st.ProjectMetrics(ctx, p.ID, "15m", now)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, mk := range m.Markers {
		kinds[mk.Kind]++
	}
	if kinds["deploy"] != 1 || kinds["restart"] != 2 || kinds["oom"] != 1 || kinds["start"] != 1 {
		t.Errorf("markers = %+v, want 1 deploy, 2 restarts, 1 manual start and 1 oom", m.Markers)
	}
}

func TestRollupRetentionAndTheLongWindowComeFromTheAggregate(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	now := time.Now().UTC().Truncate(time.Minute)

	// Duas horas atrás: 12 amostras em um minuto (o agregado as resume em uma linha).
	base := now.Add(-2 * time.Hour)
	for i := 0; i < 12; i++ {
		_ = st.InsertSamples(ctx, base.Add(time.Duration(i)*5*time.Second), []store.MetricSample{sampleOf(p.ID, "ctr-r", "app", "running", f64(float64(i)/10), i64(int64(i+1)<<20))})
	}
	if err := st.Rollup(ctx, base, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.Rollup(ctx, base, base.Add(time.Minute)); err != nil { // idempotente
		t.Fatal(err)
	}
	// Retenção: o bruto de ontem sai, o agregado fica.
	old := now.Add(-30 * time.Hour)
	_ = st.InsertSamples(ctx, old, []store.MetricSample{sampleOf(p.ID, "ctr-old", "app", "running", f64(1), i64(1<<20))})
	samples, rollups, _, err := st.Retention(ctx, now.Add(-24*time.Hour), now.Add(-7*24*time.Hour))
	if err != nil || samples != 1 || rollups != 0 {
		t.Fatalf("retention = %d samples, %d rollups, %v", samples, rollups, err)
	}

	week, err := st.ProjectMetrics(ctx, p.ID, "7d", now)
	if err != nil || week.Source != "rollup" || week.StepSeconds < 60 {
		t.Fatalf("7d = %+v, %v", week, err)
	}
	// O contêiner da janela de 7 dias aparece pelos dados brutos recentes (2 h atrás),
	// e a série vem do agregado.
	if len(week.Containers) != 1 || len(week.Containers[0].Series) != 1 {
		t.Fatalf("containers = %+v", week.Containers)
	}
	pt := week.Containers[0].Series[0]
	if pt.CPUMax == nil || *pt.CPUMax < 1.0 || pt.MemMax == nil || *pt.MemMax != 12<<20 {
		t.Errorf("rollup point = %+v; max cpu should be 1.1 and max mem 12 MiB", pt)
	}
	if _, rollups, _, _ := st.Retention(ctx, now, now); rollups != 1 {
		t.Errorf("rollups older than the cutoff must go: %d", rollups)
	}
}

func TestOverviewAggregatesByProfileAndProbes(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	node, bun := newProject(t, st), newProject(t, st)
	now := time.Now().UTC().Truncate(time.Second)
	for p, profile := range map[uuid.UUID]string{node.ID: "P-NODE", bun.ID: "P-BUN"} {
		dep, _, _ := st.CreateDeploymentWithJob(ctx, p, "k-"+profile, nil)
		o, _, _ := claim(t, st)
		_ = o.Transition(ctx, dep.ID, domain.DeploymentFetching, "")
		_ = o.Transition(ctx, dep.ID, domain.DeploymentAnalyzing, "")
		res := domain.AnalysisResult{Outcome: domain.AnalysisReady, Fields: map[string]domain.Field{domain.FieldProfile: {Value: profile}}}
		if _, err := o.SaveAnalysis(ctx, dep.ID, p, "sha", res); err != nil {
			t.Fatal(err)
		}
		o.Finish(ctx)
	}
	_ = st.InsertSamples(ctx, now.Add(-2*time.Second), []store.MetricSample{
		sampleOf(node.ID, "n1", "app", "running", f64(0.5), i64(200<<20)),
		sampleOf(node.ID, "n-db", "service", "running", f64(0.1), i64(100<<20)),
		sampleOf(bun.ID, "b1", "app", "exited", nil, nil),
	})
	_ = st.InsertProbes(ctx, now.Add(-3*time.Second), []store.Probe{
		{ProjectID: node.ID, Host: "node.cobogo.test", Outcome: store.ProbeResponded, Status: 200, DurationMS: 40},
		{ProjectID: bun.ID, Host: "bun.cobogo.test", Outcome: store.ProbeStopped, Status: 503, DurationMS: 5},
	})
	ov, err := st.MetricsOverview(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if ov.Running != 2 || ov.NotRunning != 1 || ov.Apps != 2 || ov.Services != 1 || ov.MemBytes != 300<<20 || ov.CPUCores < 0.59 || ov.CPUCores > 0.61 {
		t.Errorf("overview = %+v", ov)
	}
	got := map[string]store.ProfileUsage{}
	for _, u := range ov.ByProfile {
		got[u.Profile] = u
	}
	if got["P-NODE"].Containers != 2 || got["P-NODE"].MemBytes != 300<<20 || got["P-BUN"].Containers != 1 || got["P-BUN"].MemBytes != 0 {
		t.Errorf("by profile = %+v", got)
	}
	if ov.Probes["responded"] != 1 || ov.Probes["stopped"] != 1 {
		t.Errorf("probes = %+v", ov.Probes)
	}
	_ = own
}

// O coletor só escreve as amostras: o banco recusa o resto, de verdade.
func TestCollectorRoleCanWriteSamplesAndNothingElse(t *testing.T) {
	r := storetest.NewWithRoles(t)
	collector := store.New(r.Collector)
	now := time.Now().UTC()
	pid := uuid.New()
	if err := collector.InsertSamples(ctx, now, []store.MetricSample{sampleOf(pid, "c1", "app", "running", f64(0.1), i64(1<<20))}); err != nil {
		t.Fatalf("the collector must write samples: %v", err)
	}
	if err := collector.Heartbeat(ctx, now, time.Second, 1, 0, "", 5*time.Second); err != nil {
		t.Fatalf("the collector must write its heartbeat: %v", err)
	}
	if err := collector.Rollup(ctx, now.Add(-time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatalf("the collector must roll up: %v", err)
	}
	if _, _, _, err := collector.Retention(ctx, now.Add(-24*time.Hour), now.Add(-7*24*time.Hour)); err != nil {
		t.Fatalf("the collector must apply the retention: %v", err)
	}
	for name, sql := range map[string]string{
		"read projects":      `SELECT * FROM projects`,
		"read releases":      `SELECT * FROM releases`,
		"read the env table": `SELECT name FROM env_variables`,
		"read users":         `SELECT * FROM users`,
		"write a project":    `UPDATE projects SET slug = 'x'`,
		"update samples":     `UPDATE metric_samples SET name = 'x'`,
		"create a table":     `CREATE TABLE intruso (x int)`,
		"read audit":         `SELECT * FROM audit_events`,
		"read operations":    `SELECT * FROM operations`,
	} {
		if _, err := r.Collector.Exec(ctx, sql); !denied(err) {
			t.Errorf("%s: err = %v, want permission denied", name, err)
		}
	}
	// A API lê as amostras, não as escreve; o worker não toca nelas.
	if _, err := r.API.Exec(ctx, `SELECT * FROM metric_samples`); err != nil {
		t.Errorf("the API must read samples: %v", err)
	}
	if _, err := r.API.Exec(ctx, `DELETE FROM metric_samples`); !denied(err) {
		t.Errorf("the API must not delete samples: %v", err)
	}
	if _, err := r.Worker.Exec(ctx, `SELECT * FROM metric_samples`); !denied(err) {
		t.Errorf("the worker must not read samples: %v", err)
	}
}
