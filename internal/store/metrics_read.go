package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Janelas aceitas pelo painel e o que cada uma usa (DEC-43 itens 3 e 7).
var MetricWindows = map[string]time.Duration{
	"15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
}

const (
	rawRetention = 24 * time.Hour
	maxPoints    = 240
	// metricsInterval é o intervalo base das amostras brutas quando o batimento
	// ainda não existe.
	defaultSampleInterval = 5 * time.Second
	rollupStep            = time.Minute
	probeInterval         = 15 * time.Second
)

// MetricPoint é um ponto da série de um contêiner: média e máximo do intervalo.
// Intervalos sem amostra não geram ponto: o gráfico mostra a lacuna.
type MetricPoint struct {
	T      time.Time `json:"t"`
	CPUAvg *float64  `json:"cpu_avg"`
	CPUMax *float64  `json:"cpu_max"`
	MemAvg *int64    `json:"mem_avg"`
	MemMax *int64    `json:"mem_max"`
}

// MetricCurrent é a última leitura de um contêiner.
type MetricCurrent struct {
	At            time.Time `json:"at"`
	CPUCores      *float64  `json:"cpu_cores"`
	CPULimitCores *float64  `json:"cpu_limit_cores"`
	MemBytes      *int64    `json:"mem_bytes"`
	MemLimitBytes *int64    `json:"mem_limit_bytes"`
}

// ContainerMetrics é um contêiner do projeto: identidade, estado, última leitura
// e série. Stale diz que a última leitura passou de três intervalos.
type ContainerMetrics struct {
	ContainerID  string         `json:"container_id"`
	Name         string         `json:"name"`
	Role         string         `json:"role"`
	Service      string         `json:"service,omitempty"`
	DeploymentID *uuid.UUID     `json:"deployment_id,omitempty"`
	ReleaseState string         `json:"release_state,omitempty"`
	State        string         `json:"state,omitempty"`
	Health       string         `json:"health,omitempty"`
	Restarts     int            `json:"restarts"`
	OOMKilled    bool           `json:"oom_killed"`
	StartedAt    *time.Time     `json:"started_at,omitempty"`
	Current      *MetricCurrent `json:"current"`
	Stale        bool           `json:"stale"`
	Series       []MetricPoint  `json:"series"`
}

// MetricMarker é um acontecimento na linha do tempo: deploy, reinício ou morte
// por falta de memória.
type MetricMarker struct {
	At          time.Time `json:"at"`
	Kind        string    `json:"kind"`
	Label       string    `json:"label"`
	ContainerID string    `json:"container_id,omitempty"`
}

// ProbePoint é um intervalo de sondas: quantas responderam e quantas falharam.
type ProbePoint struct {
	T        time.Time `json:"t"`
	OK       int       `json:"ok"`
	Failed   int       `json:"failed"`
	Stopped  int       `json:"stopped"`
	AvgMS    *int      `json:"avg_ms"`
	Total    int       `json:"total"`
	Outcomes int       `json:"-"`
}

// ProbeView é a sonda de rota do projeto (DEC-43 item 10).
type ProbeView struct {
	Host       string       `json:"host"`
	At         time.Time    `json:"at"`
	Outcome    string       `json:"outcome"`
	Status     *int         `json:"status"`
	DurationMS *int         `json:"duration_ms"`
	Stale      bool         `json:"stale"`
	Series     []ProbePoint `json:"series"`
}

// ProjectMetrics é o que o painel mostra na aba de recursos.
type ProjectMetrics struct {
	Window      string             `json:"window"`
	From        time.Time          `json:"from"`
	To          time.Time          `json:"to"`
	StepSeconds int                `json:"step_seconds"`
	Source      string             `json:"source"` // raw ou rollup
	Collector   *CollectorStatus   `json:"collector"`
	Containers  []ContainerMetrics `json:"containers"`
	Markers     []MetricMarker     `json:"markers"`
	Probe       *ProbeView         `json:"probe"`
}

// ErrBadWindow: a janela pedida não é uma das aceitas.
var ErrBadWindow = fmt.Errorf("store: janela de métricas inválida")

// step devolve o passo da série: a janela em até maxPoints pontos, nunca menor
// que a granularidade da fonte.
func metricStep(window, base time.Duration) time.Duration {
	step := window / maxPoints
	if step < base {
		step = base
	}
	return ((step + base - 1) / base) * base
}

// ProjectMetrics monta as métricas de um projeto numa janela. O dono do projeto
// já deve ter sido conferido por quem chama.
func (s *Store) ProjectMetrics(ctx context.Context, projectID uuid.UUID, windowKey string, now time.Time) (ProjectMetrics, error) {
	window, ok := MetricWindows[windowKey]
	if !ok {
		return ProjectMetrics{}, ErrBadWindow
	}
	out := ProjectMetrics{Window: windowKey, From: now.Add(-window), To: now, Containers: []ContainerMetrics{}, Markers: []MetricMarker{}}
	status, err := s.GetCollectorStatus(ctx, now)
	if err != nil {
		return ProjectMetrics{}, err
	}
	out.Collector = status
	interval := defaultSampleInterval
	if status != nil {
		interval = time.Duration(status.IntervalMS) * time.Millisecond
	}
	stale := StaleAfter(interval)

	// Quem entra na lista: os contêineres com amostra na janela (bruta ou agregada).
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (s.container_id) s.container_id, s.name, s.role, s.service, s.deployment_id,
			COALESCE(r.operational_state, ''), s.state, s.health, s.restart_count, s.oom_killed, s.started_at,
			s.sampled_at, s.cpu_cores, s.cpu_limit_cores, s.mem_bytes, s.mem_limit_bytes
		FROM metric_samples s LEFT JOIN releases r ON r.deployment_id = s.deployment_id
		WHERE s.project_id = $1 AND s.sampled_at >= $2
		ORDER BY s.container_id, s.sampled_at DESC`, projectID, out.From)
	if err != nil {
		return ProjectMetrics{}, fmt.Errorf("store: ler contêineres do projeto: %w", err)
	}
	index := map[string]int{}
	for rows.Next() {
		var c ContainerMetrics
		var cur MetricCurrent
		if err := rows.Scan(&c.ContainerID, &c.Name, &c.Role, &c.Service, &c.DeploymentID, &c.ReleaseState, &c.State, &c.Health,
			&c.Restarts, &c.OOMKilled, &c.StartedAt, &cur.At, &cur.CPUCores, &cur.CPULimitCores, &cur.MemBytes, &cur.MemLimitBytes); err != nil {
			rows.Close()
			return ProjectMetrics{}, fmt.Errorf("store: ler contêiner: %w", err)
		}
		c.Current = &cur
		c.Stale = now.Sub(cur.At) > stale
		c.Series = []MetricPoint{}
		index[c.ContainerID] = len(out.Containers)
		out.Containers = append(out.Containers, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ProjectMetrics{}, err
	}

	// A série: bruta até 24 h, agregada por minuto acima disso.
	raw := window <= rawRetention
	base := defaultSampleInterval
	source := "raw"
	if !raw {
		base, source = rollupStep, "rollup"
	}
	step := metricStep(window, base)
	out.StepSeconds, out.Source = int(step.Seconds()), source

	var seriesSQL string
	if raw {
		seriesSQL = `SELECT container_id, date_bin($3::interval, sampled_at, TIMESTAMPTZ '2000-01-01'), avg(cpu_cores), max(cpu_cores), avg(mem_bytes)::bigint, max(mem_bytes)
			FROM metric_samples WHERE project_id = $1 AND sampled_at >= $2 GROUP BY 1, 2 ORDER BY 1, 2`
	} else {
		seriesSQL = `SELECT container_id, date_bin($3::interval, bucket, TIMESTAMPTZ '2000-01-01'), avg(cpu_avg), max(cpu_max), avg(mem_avg)::bigint, max(mem_max)
			FROM metric_rollups WHERE project_id = $1 AND bucket >= $2 GROUP BY 1, 2 ORDER BY 1, 2`
	}
	prows, err := s.pool.Query(ctx, seriesSQL, projectID, out.From, fmt.Sprintf("%d seconds", int(step.Seconds())))
	if err != nil {
		return ProjectMetrics{}, fmt.Errorf("store: ler séries: %w", err)
	}
	for prows.Next() {
		var id string
		var p MetricPoint
		if err := prows.Scan(&id, &p.T, &p.CPUAvg, &p.CPUMax, &p.MemAvg, &p.MemMax); err != nil {
			prows.Close()
			return ProjectMetrics{}, fmt.Errorf("store: ler ponto: %w", err)
		}
		if i, ok := index[id]; ok {
			out.Containers[i].Series = append(out.Containers[i].Series, p)
		}
	}
	prows.Close()
	if err := prows.Err(); err != nil {
		return ProjectMetrics{}, err
	}

	// Marcadores: deploys (releases) e reinícios/mortes por memória.
	mrows, err := s.pool.Query(ctx, `
		SELECT r.created_at, d.commit_sha FROM releases r JOIN deployments d ON d.id = r.deployment_id
		WHERE d.project_id = $1 AND r.created_at >= $2 ORDER BY r.created_at`, projectID, out.From)
	if err != nil {
		return ProjectMetrics{}, fmt.Errorf("store: ler marcadores de deploy: %w", err)
	}
	for mrows.Next() {
		var m MetricMarker
		var sha string
		if err := mrows.Scan(&m.At, &sha); err != nil {
			mrows.Close()
			return ProjectMetrics{}, err
		}
		if len(sha) > 7 {
			sha = sha[:7]
		}
		m.Kind, m.Label = "deploy", "deploy "+sha
		out.Markers = append(out.Markers, m)
	}
	mrows.Close()

	// Um reinício automático aumenta a contagem do Docker; um início manual (ou um
	// "reiniciar" do painel) muda a hora de início do contêiner. Os dois são
	// marcadores, e só o primeiro mexe no número de reinícios.
	changeSQL := `
		SELECT container_id, sampled_at, restart_count - prev,
			oom_killed AND NOT COALESCE(prev_oom, false),
			started_at IS NOT NULL AND prev_started IS NOT NULL AND started_at > prev_started
		FROM (SELECT container_id, sampled_at, restart_count, oom_killed, started_at,
				lag(restart_count) OVER w AS prev, lag(oom_killed) OVER w AS prev_oom, lag(started_at) OVER w AS prev_started
			FROM metric_samples WHERE project_id = $1 AND sampled_at >= $2
			WINDOW w AS (PARTITION BY container_id ORDER BY sampled_at)) t
		WHERE (prev IS NOT NULL AND restart_count > prev) OR (oom_killed AND NOT COALESCE(prev_oom, false))
			OR (started_at IS NOT NULL AND prev_started IS NOT NULL AND started_at > prev_started)`
	if !raw {
		changeSQL = `
			SELECT container_id, bucket, restarts_max - prev, false, false
			FROM (SELECT container_id, bucket, restarts_max, lag(restarts_max) OVER (PARTITION BY container_id ORDER BY bucket) AS prev
				FROM metric_rollups WHERE project_id = $1 AND bucket >= $2) t
			WHERE prev IS NOT NULL AND restarts_max > prev`
	}
	crows, err := s.pool.Query(ctx, changeSQL, projectID, out.From)
	if err != nil {
		return ProjectMetrics{}, fmt.Errorf("store: ler reinícios: %w", err)
	}
	names := map[string]string{}
	for _, c := range out.Containers {
		names[c.ContainerID] = c.Name
	}
	for crows.Next() {
		var m MetricMarker
		var delta *int
		var oom, started bool
		if err := crows.Scan(&m.ContainerID, &m.At, &delta, &oom, &started); err != nil {
			crows.Close()
			return ProjectMetrics{}, err
		}
		// Um reinício e uma morte por falta de memória na mesma leitura são dois fatos.
		if delta != nil && *delta > 0 {
			r := m
			r.Kind, r.Label = "restart", "reinício"
			out.Markers = append(out.Markers, r)
		}
		if started && (delta == nil || *delta <= 0) {
			st := m
			st.Kind, st.Label = "start", "contêiner iniciado de novo"
			out.Markers = append(out.Markers, st)
		}
		if oom {
			m.Kind, m.Label = "oom", "morto por falta de memória"
			out.Markers = append(out.Markers, m)
		}
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return ProjectMetrics{}, err
	}

	probe, err := s.projectProbe(ctx, projectID, out.From, step, now)
	if err != nil {
		return ProjectMetrics{}, err
	}
	out.Probe = probe
	return out, nil
}

// projectProbe lê a sonda de rota do projeto: a última e a série (só até 24 h,
// que é quanto as sondas são guardadas).
func (s *Store) projectProbe(ctx context.Context, projectID uuid.UUID, from time.Time, step time.Duration, now time.Time) (*ProbeView, error) {
	var v ProbeView
	err := s.pool.QueryRow(ctx, `SELECT host, sampled_at, outcome, status, duration_ms FROM metric_probes WHERE project_id = $1 ORDER BY sampled_at DESC LIMIT 1`, projectID).
		Scan(&v.Host, &v.At, &v.Outcome, &v.Status, &v.DurationMS)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("store: ler a sonda: %w", err)
	}
	v.Stale = now.Sub(v.At) > 3*probeInterval
	if from.Before(now.Add(-rawRetention)) {
		from = now.Add(-rawRetention)
	}
	if step < probeInterval {
		step = probeInterval
	}
	rows, err := s.pool.Query(ctx, `
		SELECT date_bin($3::interval, sampled_at, TIMESTAMPTZ '2000-01-01'),
			count(*) FILTER (WHERE outcome IN ('responded', 'client_error')), count(*) FILTER (WHERE outcome = 'failed'),
			count(*) FILTER (WHERE outcome = 'stopped'), avg(duration_ms) FILTER (WHERE outcome IN ('responded', 'client_error'))::int, count(*)
		FROM metric_probes WHERE project_id = $1 AND sampled_at >= $2 GROUP BY 1 ORDER BY 1`, projectID, from, fmt.Sprintf("%d seconds", int(step.Seconds())))
	if err != nil {
		return nil, fmt.Errorf("store: ler a série da sonda: %w", err)
	}
	defer rows.Close()
	v.Series = []ProbePoint{}
	for rows.Next() {
		var p ProbePoint
		if err := rows.Scan(&p.T, &p.OK, &p.Failed, &p.Stopped, &p.AvgMS, &p.Total); err != nil {
			return nil, err
		}
		v.Series = append(v.Series, p)
	}
	return &v, rows.Err()
}

// ProfileUsage é o consumo agregado por perfil tecnológico (RF-25).
type ProfileUsage struct {
	Profile    string  `json:"profile"`
	Projects   int     `json:"projects"`
	Containers int     `json:"containers"`
	MemBytes   int64   `json:"mem_bytes"`
	CPUCores   float64 `json:"cpu_cores"`
}

// MetricsOverview é o que a visão geral mostra: o coletor e o consumo das
// cargas geridas, só com leituras recentes.
type MetricsOverview struct {
	Collector     *CollectorStatus `json:"collector"`
	Running       int              `json:"running"`
	NotRunning    int              `json:"not_running"`
	Unknown       int              `json:"unknown"`
	Apps          int              `json:"apps"`
	Services      int              `json:"services"`
	MemBytes      int64            `json:"mem_bytes"`
	MemLimitBytes int64            `json:"mem_limit_bytes"`
	CPUCores      float64          `json:"cpu_cores"`
	ByProfile     []ProfileUsage   `json:"by_profile"`
	Probes        map[string]int   `json:"probes"`
}

// MetricsOverview agrega a última leitura de cada contêiner. Contêineres vistos
// há mais de três intervalos e menos de dez minutos contam como "desconhecidos":
// é o sinal de um coletor parado, sem fingir que estão bem.
func (s *Store) MetricsOverview(ctx context.Context, now time.Time) (MetricsOverview, error) {
	out := MetricsOverview{ByProfile: []ProfileUsage{}, Probes: map[string]int{}}
	status, err := s.GetCollectorStatus(ctx, now)
	if err != nil {
		return out, err
	}
	out.Collector = status
	interval := defaultSampleInterval
	if status != nil {
		interval = time.Duration(status.IntervalMS) * time.Millisecond
	}
	fresh, seen := now.Add(-StaleAfter(interval)), now.Add(-10*time.Minute)

	rows, err := s.pool.Query(ctx, `
		SELECT l.role, l.state, l.sampled_at, l.cpu_cores, l.mem_bytes, l.mem_limit_bytes, l.project_id,
			COALESCE(a.profile, '')
		FROM (SELECT DISTINCT ON (container_id) * FROM metric_samples WHERE sampled_at >= $1 ORDER BY container_id, sampled_at DESC) l
		JOIN projects p ON p.id = l.project_id AND p.deleted_at IS NULL
		LEFT JOIN LATERAL (SELECT result->'fields'->'profile'->>'value' AS profile FROM analyses WHERE project_id = l.project_id ORDER BY created_at DESC LIMIT 1) a ON true`, seen)
	if err != nil {
		return out, fmt.Errorf("store: ler a visão geral: %w", err)
	}
	defer rows.Close()
	byProfile := map[string]*ProfileUsage{}
	projects := map[string]map[uuid.UUID]bool{}
	for rows.Next() {
		var role, state, profile string
		var at time.Time
		var cpu *float64
		var mem, limit *int64
		var pid uuid.UUID
		if err := rows.Scan(&role, &state, &at, &cpu, &mem, &limit, &pid, &profile); err != nil {
			return out, err
		}
		if at.Before(fresh) {
			out.Unknown++
			continue
		}
		if state == "running" {
			out.Running++
		} else {
			out.NotRunning++
		}
		if role == "service" {
			out.Services++
		} else {
			out.Apps++
		}
		if profile == "" {
			profile = "desconhecido"
		}
		u := byProfile[profile]
		if u == nil {
			u = &ProfileUsage{Profile: profile}
			byProfile[profile] = u
			projects[profile] = map[uuid.UUID]bool{}
		}
		u.Containers++
		projects[profile][pid] = true
		if cpu != nil {
			out.CPUCores += *cpu
			u.CPUCores += *cpu
		}
		if mem != nil {
			out.MemBytes += *mem
			u.MemBytes += *mem
		}
		if limit != nil && mem != nil {
			out.MemLimitBytes += *limit
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	for name, u := range byProfile {
		u.Projects = len(projects[name])
		out.ByProfile = append(out.ByProfile, *u)
	}
	sortProfiles(out.ByProfile)

	prow, err := s.pool.Query(ctx, `SELECT outcome, count(*) FROM (SELECT DISTINCT ON (project_id) project_id, outcome FROM metric_probes WHERE sampled_at >= $1 ORDER BY project_id, sampled_at DESC) t GROUP BY outcome`, now.Add(-3*probeInterval))
	if err != nil {
		return out, fmt.Errorf("store: ler as sondas: %w", err)
	}
	defer prow.Close()
	for prow.Next() {
		var outcome string
		var n int
		if err := prow.Scan(&outcome, &n); err != nil {
			return out, err
		}
		out.Probes[outcome] = n
	}
	return out, prow.Err()
}

func sortProfiles(list []ProfileUsage) {
	for i := 1; i < len(list); i++ {
		for j := i; j > 0 && list[j].Profile < list[j-1].Profile; j-- {
			list[j], list[j-1] = list[j-1], list[j]
		}
	}
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
