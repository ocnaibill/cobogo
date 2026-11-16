// Package storetest abre um PostgreSQL isolado por teste (um schema próprio)
// para os testes de integração. Sem COBOGO_TEST_DATABASE_URL os testes são
// pulados, então `go test ./...` continua funcionando sem banco.
package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/store"
)

// EnvVar nomeia a variável com a URL do PostgreSQL usado nos testes.
const EnvVar = "COBOGO_TEST_DATABASE_URL"

// Pool devolve um pool preso a um schema vazio, removido ao fim do teste.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(EnvVar)
	if url == "" {
		t.Skipf("defina %s para rodar testes de integração com PostgreSQL", EnvVar)
	}
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("conectar ao banco de teste: %v", err)
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(suffix)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("criar schema de teste: %v", err)
	}

	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("conectar ao schema de teste: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		if _, err := admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Errorf("remover schema de teste: %v", err)
		}
		admin.Close()
	})
	return pool
}

// New devolve um Store sobre um schema novo com todas as migrations aplicadas.
func New(t *testing.T) *store.Store {
	t.Helper()
	pool := Pool(t)
	if err := store.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrar schema de teste: %v", err)
	}
	return store.New(pool)
}
