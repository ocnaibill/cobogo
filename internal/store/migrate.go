package store

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationLockKey identifica o lock consultivo das migrations do Cobogó.
const migrationLockKey = 7305000001

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrate aplica, em ordem e dentro de transações, as migrations ainda não
// registradas em schema_migrations. Não há rollback automático: cada
// arquivo é aditivo por design, conforme DEC-13.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// API e worker sobem juntos e migram o mesmo banco: um lock consultivo
	// serializa as execuções entre processos. O segundo espera e encontra tudo
	// já aplicado.
	lock, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("store: obter conexão para o lock de migrations: %w", err)
	}
	defer lock.Release()
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
		return fmt.Errorf("store: obter lock de migrations: %w", err)
	}
	defer lock.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockKey)

	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version     TEXT PRIMARY KEY,
			applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("store: preparar schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("store: listar migrations embutidas: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var alreadyApplied bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = $1)`, name).Scan(&alreadyApplied); err != nil {
			return fmt.Errorf("store: verificar migration %s: %w", name, err)
		}
		if alreadyApplied {
			continue
		}

		contents, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("store: ler migration %s: %w", name, err)
		}

		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("store: abrir transação para %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, string(contents)); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("store: aplicar migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, name); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("store: registrar migration %s: %w", name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("store: confirmar migration %s: %w", name, err)
		}
	}
	return nil
}
