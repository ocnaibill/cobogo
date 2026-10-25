package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/config"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
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

	if err := store.Migrate(ctx, pool); err != nil {
		log.Error("aplicar migrations", "erro", err)
		os.Exit(1)
	}

	rt, err := dockerrt.New()
	if err != nil {
		log.Error("conectar ao docker", "erro", err)
		os.Exit(1)
	}

	st := store.New(pool)
	w := worker.New(st, rt, log)

	log.Info("worker Cobogó no ar")
	if err := w.Run(ctx); err != nil && err != context.Canceled {
		log.Error("worker encerrou com erro", "erro", err)
		os.Exit(1)
	}
}
