package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Métricas dos contêineres (DEC-43). O coletor escreve (InsertSamples, Rollup,
// Retention, Heartbeat); a API lê (ProjectMetrics, MetricsOverview). Ausência de
// amostra nunca vira zero: os valores sem leitura são nulos.

// MetricSample é uma amostra de um contêiner em um instante.
type MetricSample struct {
	ContainerID   string
	ProjectID     *uuid.UUID
	DeploymentID  *uuid.UUID
	Role          string
	Service       string
	Name          string
	State         string
	Health        string
	Restarts      int
	OOM           bool
	StartedAt     *time.Time
	CPUCores      *float64
	CPULimitCores *float64
	MemBytes      *int64
	MemLimitBytes *int64
}

// InsertSamples grava as amostras de uma rodada, todas com o mesmo instante.
func (s *Store) InsertSamples(ctx context.Context, at time.Time, samples []MetricSample) error {
	if len(samples) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, m := range samples {
		batch.Queue(`
			INSERT INTO metric_samples (container_id, sampled_at, project_id, deployment_id, role, service, name, state, health,
				restart_count, oom_killed, started_at, cpu_cores, cpu_limit_cores, mem_bytes, mem_limit_bytes)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
			ON CONFLICT (container_id, sampled_at) DO NOTHING`,
			m.ContainerID, at, m.ProjectID, m.DeploymentID, m.Role, m.Service, m.Name, m.State, m.Health,
			m.Restarts, m.OOM, m.StartedAt, m.CPUCores, m.CPULimitCores, m.MemBytes, m.MemLimitBytes)
	}
	res := s.pool.SendBatch(ctx, batch)
	defer res.Close()
	for range samples {
		if _, err := res.Exec(); err != nil {
			return fmt.Errorf("store: gravar amostra: %w", err)
		}
	}
	return nil
}

// Probe é o resultado de uma sonda de rota (DEC-43 item 10).
type Probe struct {
	ProjectID  uuid.UUID
	Host       string
	Outcome    string
	Status     int
	DurationMS int
}

const (
	ProbeResponded   = "responded"
	ProbeClientError = "client_error"
	ProbeFailed      = "failed"
	ProbeStopped     = "stopped"
)

// InsertProbes grava as sondas de uma rodada.
func (s *Store) InsertProbes(ctx context.Context, at time.Time, probes []Probe) error {
	if len(probes) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, p := range probes {
		var status, dur *int
		if p.Status > 0 {
			status = &p.Status
		}
		if p.DurationMS >= 0 {
			dur = &p.DurationMS
		}
		batch.Queue(`INSERT INTO metric_probes (project_id, sampled_at, host, outcome, status, duration_ms) VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (project_id, sampled_at) DO NOTHING`, p.ProjectID, at, p.Host, p.Outcome, status, dur)
	}
	res := s.pool.SendBatch(ctx, batch)
	defer res.Close()
	for range probes {
		if _, err := res.Exec(); err != nil {
			return fmt.Errorf("store: gravar sonda: %w", err)
		}
	}
	return nil
}

// Rollup fecha o agregado por minuto das amostras de [from, to). É idempotente:
// refazer um intervalo atualiza as mesmas linhas.
func (s *Store) Rollup(ctx context.Context, from, to time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO metric_rollups (container_id, bucket, project_id, deployment_id, role, service, name,
			samples, running_samples, cpu_avg, cpu_max, mem_avg, mem_max, restarts_max)
		SELECT container_id, date_trunc('minute', sampled_at), (array_agg(project_id))[1], (array_agg(deployment_id))[1],
			(array_agg(role))[1], (array_agg(service))[1], (array_agg(name))[1],
			count(*), count(*) FILTER (WHERE state = 'running'),
			avg(cpu_cores), max(cpu_cores), avg(mem_bytes)::bigint, max(mem_bytes), max(restart_count)
		FROM metric_samples WHERE sampled_at >= $1 AND sampled_at < $2
		GROUP BY container_id, date_trunc('minute', sampled_at)
		ON CONFLICT (container_id, bucket) DO UPDATE SET
			samples = EXCLUDED.samples, running_samples = EXCLUDED.running_samples, cpu_avg = EXCLUDED.cpu_avg,
			cpu_max = EXCLUDED.cpu_max, mem_avg = EXCLUDED.mem_avg, mem_max = EXCLUDED.mem_max, restarts_max = EXCLUDED.restarts_max`, from, to)
	if err != nil {
		return fmt.Errorf("store: agregar amostras: %w", err)
	}
	return nil
}

// Retention apaga o que passou da retenção: o bruto e as sondas antes de
// rawBefore, e o agregado antes de rollupBefore. Devolve as linhas apagadas.
func (s *Store) Retention(ctx context.Context, rawBefore, rollupBefore time.Time) (samples, rollups, probes int64, err error) {
	for _, q := range []struct {
		sql  string
		when time.Time
		out  *int64
	}{
		{`DELETE FROM metric_samples WHERE sampled_at < $1`, rawBefore, &samples},
		{`DELETE FROM metric_probes WHERE sampled_at < $1`, rawBefore, &probes},
		{`DELETE FROM metric_rollups WHERE bucket < $1`, rollupBefore, &rollups},
	} {
		tag, err := s.pool.Exec(ctx, q.sql, q.when)
		if err != nil {
			return 0, 0, 0, fmt.Errorf("store: aplicar a retenção: %w", err)
		}
		*q.out = tag.RowsAffected()
	}
	return samples, rollups, probes, nil
}

// Heartbeat registra uma rodada do coletor.
func (s *Store) Heartbeat(ctx context.Context, at time.Time, duration time.Duration, containers, errCount int, lastError string, interval time.Duration) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO collector_heartbeat (id, last_at, duration_ms, containers, errors, last_error, interval_ms)
		VALUES (1, $1, $2, $3, $4, $5, $6)
		ON CONFLICT (id) DO UPDATE SET last_at = EXCLUDED.last_at, duration_ms = EXCLUDED.duration_ms,
			containers = EXCLUDED.containers, errors = EXCLUDED.errors, last_error = EXCLUDED.last_error, interval_ms = EXCLUDED.interval_ms`,
		at, duration.Milliseconds(), containers, errCount, lastError, interval.Milliseconds())
	if err != nil {
		return fmt.Errorf("store: registrar o batimento do coletor: %w", err)
	}
	return nil
}

// CollectorStatus é o último batimento do coletor. Stale diz que passou de três
// intervalos sem rodada (a saúde atual é então desconhecida).
type CollectorStatus struct {
	LastAt     time.Time `json:"last_at"`
	DurationMS int       `json:"duration_ms"`
	Containers int       `json:"containers"`
	Errors     int       `json:"errors"`
	LastError  string    `json:"last_error,omitempty"`
	IntervalMS int       `json:"interval_ms"`
	Stale      bool      `json:"stale"`
}

// StaleAfter é quanto tempo sem amostra deixa um dado "antigo": três intervalos.
func StaleAfter(interval time.Duration) time.Duration { return 3 * interval }

// GetCollectorStatus lê o batimento, ou nil se o coletor nunca rodou.
func (s *Store) GetCollectorStatus(ctx context.Context, now time.Time) (*CollectorStatus, error) {
	var c CollectorStatus
	err := s.pool.QueryRow(ctx, `SELECT last_at, duration_ms, containers, errors, last_error, interval_ms FROM collector_heartbeat WHERE id = 1`).
		Scan(&c.LastAt, &c.DurationMS, &c.Containers, &c.Errors, &c.LastError, &c.IntervalMS)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: ler o batimento do coletor: %w", err)
	}
	c.Stale = now.Sub(c.LastAt) > StaleAfter(time.Duration(c.IntervalMS)*time.Millisecond)
	return &c, nil
}
