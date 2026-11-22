package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/config"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/worker"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.Load()
	if err != nil {
		log.Error("carregar configuração", "erro", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("conectar ao postgres", "erro", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := store.Prepare(ctx, pool, cfg.AutoMigrate); err != nil {
		log.Error("preparar o esquema do banco", "erro", err)
		os.Exit(1)
	}

	rt, err := dockerrt.New()
	if err != nil {
		log.Error("conectar ao docker", "erro", err)
		os.Exit(1)
	}

	st := store.New(pool).WithCipher(cfg.Keyring)
	w := worker.New(st, rt, log, settings.NewResolver(st, settings.Env{Limits: cfg.Limits, GitHosts: cfg.ExtraGitHosts, AppsDomain: cfg.AppsDomain, AppsScheme: cfg.AppsScheme}))

	w.SetProbeMode(cfg.ProbeMode)
	w.SetBackup(strings.TrimSpace(os.Getenv("COBOGO_BACKUP_DIR")), strings.TrimSpace(os.Getenv("COBOGO_BACKUP_VOLUME")))

	log.Info("worker Cobogó no ar", "sonda", cfg.ProbeMode)
	if err := w.Run(ctx); err != nil && err != context.Canceled {
		log.Error("worker encerrou com erro", "erro", err)
		os.Exit(1)
	}
}
