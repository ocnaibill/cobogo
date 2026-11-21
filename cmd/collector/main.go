// Comando collector: o coletor de métricas do Cobogó (DEC-43). Só lê o Docker,
// por um proxy de leitura, e só escreve as amostras, com o seu role no banco.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/collector"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL é obrigatória (o role do coletor)")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Error("conectar ao postgres", "erro", err)
		os.Exit(1)
	}
	defer pool.Close()
	autoMigrate := strings.TrimSpace(os.Getenv("COBOGO_AUTO_MIGRATE")) != "0"
	if err := store.Prepare(ctx, pool, autoMigrate); err != nil {
		log.Error("preparar o esquema do banco", "erro", err)
		os.Exit(1)
	}
	rt, err := dockerrt.New()
	if err != nil {
		log.Error("conectar ao docker", "erro", err)
		os.Exit(1)
	}

	cfg := collector.DefaultConfig()
	if v := strings.TrimSpace(os.Getenv("COBOGO_COLLECTOR_INTERVAL")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Second || d > 5*time.Minute {
			log.Error("COBOGO_COLLECTOR_INTERVAL inválida: use uma duração de 1s a 5m, como 5s", "valor", v)
			os.Exit(1)
		}
		cfg.Interval = d
	}
	if v := strings.TrimSpace(os.Getenv("COBOGO_COLLECTOR_RAW_RETENTION")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Hour {
			log.Error("COBOGO_COLLECTOR_RAW_RETENTION inválida: use uma duração de 1h ou mais, como 24h", "valor", v)
			os.Exit(1)
		}
		cfg.RawRetention = d
	}
	if v := strings.TrimSpace(os.Getenv("COBOGO_COLLECTOR_PARALLEL")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 && n <= 64 {
			cfg.Parallel = n
		}
	}
	cfg.Probe = collector.ProbeConfig{
		RoutesURL:  strings.TrimSpace(os.Getenv("COBOGO_ROUTES_URL")),
		Token:      strings.TrimSpace(os.Getenv("COBOGO_ROUTER_TOKEN")),
		TraefikURL: strings.TrimSpace(os.Getenv("COBOGO_PROBE_URL")),
	}
	if cfg.Probe.RoutesURL != "" && (cfg.Probe.Token == "" || cfg.Probe.TraefikURL == "") {
		log.Error("a sonda de rota precisa de COBOGO_ROUTES_URL, COBOGO_ROUTER_TOKEN e COBOGO_PROBE_URL juntos")
		os.Exit(1)
	}

	c := collector.New(store.New(pool), rt, log, cfg)
	if err := c.Run(ctx); err != nil {
		log.Error("coletor", "erro", err)
		os.Exit(1)
	}
}
