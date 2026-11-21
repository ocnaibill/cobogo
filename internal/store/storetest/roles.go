package storetest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/secrets"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Roled é um schema de teste com os privilégios da DEC-33 aplicados e um pool
// conectado como cada role de aplicação. Owner é o pool do usuário dos testes
// (que tem de poder criar roles), usado para preparar e para conferir.
type Roled struct {
	Owner  *pgxpool.Pool
	API    *pgxpool.Pool
	Worker *pgxpool.Pool
	// Collector é o pool do role do coletor de métricas (DEC-43).
	Collector *pgxpool.Pool
	// APIStore e WorkerStore são Stores sobre os pools restritos, com a cifra
	// dos testes (o worker decifra, a API só escreve o cifrado).
	APIStore    *store.Store
	WorkerStore *store.Store
	Roles       store.Roles
	Schema      string
}

// NewWithRoles cria o schema, aplica as migrations e a matriz de privilégios
// com roles de nome único (os roles são do cluster, então cada teste cria os
// seus e os remove ao fim).
func NewWithRoles(t *testing.T) Roled {
	t.Helper()
	owner := Pool(t)
	ctx := context.Background()
	if err := store.Migrate(ctx, owner); err != nil {
		t.Fatalf("migrar schema de teste: %v", err)
	}

	suffix := make([]byte, 4)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatal(err)
	}
	roles := store.Roles{
		API:       "cobogo_api_" + hex.EncodeToString(suffix),
		Worker:    "cobogo_worker_" + hex.EncodeToString(suffix),
		Collector: "cobogo_collector_" + hex.EncodeToString(suffix),
	}
	const password = "senha-de-teste"

	var schema, database string
	if err := owner.QueryRow(ctx, `SELECT current_schema(), current_database()`).Scan(&schema, &database); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{roles.API, roles.Worker, roles.Collector} {
		sql := fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s'", pgx.Identifier{role}.Sanitize(), password)
		if _, err := owner.Exec(ctx, sql); err != nil {
			t.Fatalf("criar role de teste (o usuário dos testes precisa de CREATEROLE): %v", err)
		}
	}
	t.Cleanup(func() {
		for _, role := range []string{roles.API, roles.Worker, roles.Collector} {
			id := pgx.Identifier{role}.Sanitize()
			_, _ = owner.Exec(context.Background(), "DROP OWNED BY "+id)
			_, _ = owner.Exec(context.Background(), "DROP ROLE IF EXISTS "+id)
		}
	})
	if applied, err := store.ApplyGrants(ctx, owner, roles); err != nil || !applied {
		t.Fatalf("aplicar privilégios: applied=%v err=%v", applied, err)
	}

	connect := func(role string) *pgxpool.Pool {
		cfg, err := pgxpool.ParseConfig(os.Getenv(EnvVar))
		if err != nil {
			t.Fatal(err)
		}
		cfg.ConnConfig.User, cfg.ConnConfig.Password = role, password
		cfg.ConnConfig.RuntimeParams["search_path"] = schema
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("conectar como %s: %v", role, err)
		}
		t.Cleanup(pool.Close)
		return pool
	}
	box, err := secrets.NewBox(Key, 1)
	if err != nil {
		t.Fatal(err)
	}
	api, worker, collector := connect(roles.API), connect(roles.Worker), connect(roles.Collector)
	return Roled{
		Owner: owner, API: api, Worker: worker, Collector: collector,
		APIStore:    store.New(api).WithCipher(box),
		WorkerStore: store.New(worker).WithCipher(box),
		Roles:       roles, Schema: schema,
	}
}
