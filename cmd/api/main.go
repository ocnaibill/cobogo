package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/config"
	"github.com/ocnaibill/cobogo/internal/httpapi"
	"github.com/ocnaibill/cobogo/internal/store"
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

	st := store.New(pool)
	router := httpapi.NewRouter(st, log)

	srv := &http.Server{Addr: cfg.APIAddr, Handler: router}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Info("API Cobogó no ar", "endereco", cfg.APIAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("servidor http", "erro", err)
		os.Exit(1)
	}
}
