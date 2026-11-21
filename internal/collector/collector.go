// Package collector lê o estado e as estatísticas dos contêineres do Cobogó e as
// guarda (DEC-43). Roda em processo próprio, só lê o Docker (por um proxy de
// leitura) e só escreve nas tabelas de amostras, com o seu role do banco.
package collector

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Sampler lê os contêineres gerenciados. O Runtime do Docker o implementa.
type Sampler interface {
	SampleManaged(ctx context.Context, parallel int) ([]dockerrt.Sample, []error)
}

// Config são os parâmetros do coletor (DEC-43 item 3).
type Config struct {
	// Interval entre rodadas de amostras (padrão 5 s).
	Interval time.Duration
	// Parallel é quantas estatísticas se pedem ao mesmo tempo.
	Parallel int
	// RawRetention e RollupRetention: quanto o bruto e o agregado por minuto
	// ficam (24 h e 7 dias).
	RawRetention    time.Duration
	RollupRetention time.Duration
	// Probe configura a sonda de rota; vazia, ela fica desligada.
	Probe ProbeConfig
}

// DefaultConfig são os valores iniciais da DEC-43.
func DefaultConfig() Config {
	return Config{Interval: 5 * time.Second, Parallel: 8, RawRetention: 24 * time.Hour, RollupRetention: 7 * 24 * time.Hour}
}

// Collector é o laço de coleta.
type Collector struct {
	store   *store.Store
	sampler Sampler
	log     *slog.Logger
	cfg     Config
	probe   *Prober

	lastRollup    time.Time
	lastRetention time.Time
	// slugs liga o slug de um projeto ao seu id, aprendido dos próprios
	// contêineres (o coletor não lê projetos): a sonda precisa dele.
	slugs map[string]uuid.UUID
}

// New monta o coletor.
func New(st *store.Store, sampler Sampler, log *slog.Logger, cfg Config) *Collector {
	def := DefaultConfig()
	if cfg.Interval <= 0 {
		cfg.Interval = def.Interval
	}
	if cfg.Parallel <= 0 {
		cfg.Parallel = def.Parallel
	}
	if cfg.RawRetention <= 0 {
		cfg.RawRetention = def.RawRetention
	}
	if cfg.RollupRetention <= 0 {
		cfg.RollupRetention = def.RollupRetention
	}
	c := &Collector{store: st, sampler: sampler, log: log, cfg: cfg, slugs: map[string]uuid.UUID{}}
	if cfg.Probe.RoutesURL != "" {
		c.probe = NewProber(cfg.Probe)
	}
	return c
}

// Run coleta até o contexto acabar. Uma rodada que passa do intervalo não gera
// rodadas atrasadas para recuperar: a cadência real aparece no batimento.
func (c *Collector) Run(ctx context.Context) error {
	c.log.Info("coletor no ar", "intervalo", c.cfg.Interval, "bruto", c.cfg.RawRetention, "agregado", c.cfg.RollupRetention, "sonda", c.probe != nil)
	ticker := time.NewTicker(c.cfg.Interval)
	defer ticker.Stop()
	var lastProbe time.Time
	for {
		c.Round(ctx)
		if c.probe != nil && time.Since(lastProbe) >= probeEvery {
			lastProbe = time.Now()
			c.ProbeRound(ctx)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Round faz uma rodada: amostra, grava, registra o batimento e, de tempos em
// tempos, fecha o agregado e aplica a retenção.
func (c *Collector) Round(ctx context.Context) {
	start := time.Now().UTC()
	samples, errs := c.sampler.SampleManaged(ctx, c.cfg.Parallel)

	rows := make([]store.MetricSample, 0, len(samples))
	for _, s := range samples {
		rows = append(rows, toRow(s))
		if s.Role == "app" && s.ProjectID != "" {
			if id, err := uuid.Parse(s.ProjectID); err == nil {
				if slug := slugOf(s.Name); slug != "" {
					c.slugs[slug] = id
				}
			}
		}
	}
	lastErr := ""
	if err := c.store.InsertSamples(ctx, start, rows); err != nil {
		errs = append(errs, err)
	}
	for _, err := range errs {
		lastErr = err.Error()
		c.log.Warn("coleta", "erro", err)
	}
	if err := c.store.Heartbeat(ctx, start, time.Since(start), len(rows), len(errs), lastErr, c.cfg.Interval); err != nil {
		c.log.Error("registrar o batimento", "erro", err)
	}

	// Fecha o agregado dos minutos completos (refaz os dois últimos: idempotente).
	if minute := start.Truncate(time.Minute); minute.After(c.lastRollup) {
		if err := c.store.Rollup(ctx, minute.Add(-2*time.Minute), minute); err != nil {
			c.log.Warn("agregar amostras", "erro", err)
		} else {
			c.lastRollup = minute
		}
	}
	if time.Since(c.lastRetention) >= time.Hour {
		c.lastRetention = time.Now()
		raw, roll, probes, err := c.store.Retention(ctx, start.Add(-c.cfg.RawRetention), start.Add(-c.cfg.RollupRetention))
		if err != nil {
			c.log.Warn("aplicar a retenção", "erro", err)
		} else if raw+roll+probes > 0 {
			c.log.Info("retenção aplicada", "amostras", raw, "agregados", roll, "sondas", probes)
		}
	}
}

func toRow(s dockerrt.Sample) store.MetricSample {
	row := store.MetricSample{
		ContainerID: s.ContainerID, Role: s.Role, Service: s.Service, Name: s.Name, State: s.State, Health: s.Health,
		Restarts: s.Restarts, OOM: s.OOM, StartedAt: s.Started,
		CPUCores: s.CPUCores, CPULimitCores: s.CPULimitCores, MemBytes: s.MemBytes, MemLimitBytes: s.MemLimitBytes,
	}
	if id, err := uuid.Parse(s.ProjectID); err == nil {
		row.ProjectID = &id
	}
	if id, err := uuid.Parse(s.DeploymentID); err == nil {
		row.DeploymentID = &id
	}
	return row
}

// slugOf tira o prefixo e o sufixo do nome de um contêiner de aplicação
// (`cobogo-<slug>-<8 hex>`). Devolve "" se o nome não tem esse formato.
func slugOf(name string) string {
	rest, ok := strings.CutPrefix(name, "cobogo-")
	if !ok || len(rest) < 10 {
		return ""
	}
	cut := len(rest) - 9
	if rest[cut] != '-' {
		return ""
	}
	return rest[:cut]
}
