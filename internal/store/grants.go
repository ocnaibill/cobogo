package store

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Roles são os nomes dos roles de aplicação do PostgreSQL (DEC-33). Quem conecta
// como um deles só faz DML nas tabelas da matriz de privilégios.
type Roles struct {
	API    string
	Worker string
}

// DefaultRoles são os nomes que o script de criação dos roles usa.
var DefaultRoles = Roles{API: "cobogo_api", Worker: "cobogo_worker"}

// Privileges descreve o que um role pode fazer numa tabela: privilégios da
// tabela inteira e privilégios por coluna.
type Privileges struct {
	Table   []string
	Columns map[string][]string
}

// TableAccess é o acesso de cada role a uma tabela.
type TableAccess struct {
	API    Privileges
	Worker Privileges
}

func tbl(p ...string) Privileges { return Privileges{Table: p} }

// Colunas de env_variables que a API pode ler: tudo menos o texto cifrado
// (DEC-33 item 3).
var (
	grantEnvMeta      = []string{"id", "project_id", "name", "version", "source", "key_version", "created_at", "updated_at"}
	grantEnvAll       = append(append([]string{}, grantEnvMeta...), "ciphertext")
	grantEnvUpdatable = []string{"version", "source", "ciphertext", "key_version", "updated_at"}
)

// accessMatrix é a matriz de privilégios da DEC-33. Uma tabela do esquema que
// não esteja aqui faz o passo de migração falhar.
var accessMatrix = map[string]TableAccess{
	// Os dois só leem as migrations aplicadas, para conferir o esquema ao subir.
	"schema_migrations": {API: tbl("SELECT"), Worker: tbl("SELECT")},
	"users":             {API: tbl("SELECT", "INSERT", "UPDATE")},
	"sessions":          {API: tbl("SELECT", "INSERT", "UPDATE", "DELETE")},
	"audit_events":      {API: tbl("SELECT", "INSERT")},
	"projects":          {API: tbl("SELECT", "INSERT", "UPDATE"), Worker: tbl("SELECT")},
	"settings":          {API: tbl("SELECT", "INSERT", "UPDATE", "DELETE"), Worker: tbl("SELECT")},
	"deployments":       {API: tbl("SELECT", "INSERT", "UPDATE"), Worker: tbl("SELECT", "UPDATE")},
	"jobs":              {API: tbl("SELECT", "INSERT", "UPDATE"), Worker: tbl("SELECT", "UPDATE")},
	"deployment_events": {API: tbl("SELECT", "INSERT"), Worker: tbl("SELECT", "INSERT")},
	"analyses":          {API: tbl("SELECT"), Worker: tbl("SELECT", "INSERT")},
	"releases":          {API: tbl("SELECT"), Worker: tbl("SELECT", "INSERT", "UPDATE")},
	// DEC-41: quem cria e mede volumes é o worker; a API só lista.
	"project_volumes": {API: tbl("SELECT"), Worker: tbl("SELECT", "INSERT", "UPDATE")},
	// DEC-42: a API registra a escolha do usuário; o worker, o desfecho da criação.
	"project_services": {API: tbl("SELECT", "INSERT", "UPDATE"), Worker: tbl("SELECT", "UPDATE")},
	// A API pede (inserção) e acompanha (leitura); o worker executa e conclui.
	"operations": {API: tbl("SELECT", "INSERT"), Worker: tbl("SELECT", "INSERT", "UPDATE")},
	"env_variables": {
		API: Privileges{
			Table: []string{"DELETE"},
			Columns: map[string][]string{
				"SELECT": grantEnvMeta,
				"INSERT": grantEnvAll,
				"UPDATE": grantEnvUpdatable,
			},
		},
		Worker: tbl("SELECT"),
	},
}

// AccessMatrix devolve uma cópia da matriz, para os testes e para a conferência.
func AccessMatrix() map[string]TableAccess {
	out := make(map[string]TableAccess, len(accessMatrix))
	for k, v := range accessMatrix {
		out[k] = v
	}
	return out
}

// ApplyGrants aplica a matriz aos roles de aplicação, numa transação, com o
// role dono do esquema (DEC-33 item 5). É idempotente: revoga tudo dos dois
// roles nas tabelas do esquema e concede o que a matriz diz. Devolve false, sem
// erro, se os roles não existem (desenvolvimento com um usuário só); se só um
// existe, ou se há uma tabela fora da matriz, devolve erro.
func ApplyGrants(ctx context.Context, pool *pgxpool.Pool, roles Roles) (applied bool, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("store: abrir transação de privilégios: %w", err)
	}
	defer tx.Rollback(ctx)

	var found int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM pg_roles WHERE rolname = ANY($1)`, []string{roles.API, roles.Worker}).Scan(&found); err != nil {
		return false, fmt.Errorf("store: procurar os roles de aplicação: %w", err)
	}
	switch found {
	case 0:
		return false, nil
	case 1:
		return false, fmt.Errorf("store: só um dos roles %s e %s existe; crie os dois", roles.API, roles.Worker)
	}

	var schema, database string
	if err := tx.QueryRow(ctx, `SELECT current_schema(), current_database()`).Scan(&schema, &database); err != nil {
		return false, fmt.Errorf("store: ler o esquema: %w", err)
	}

	rows, err := tx.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname = current_schema()`)
	if err != nil {
		return false, fmt.Errorf("store: listar tabelas: %w", err)
	}
	var tables, unknown []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return false, err
		}
		tables = append(tables, name)
		if _, ok := accessMatrix[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return false, fmt.Errorf("store: tabelas fora da matriz de privilégios (DEC-33 item 4): %s", strings.Join(unknown, ", "))
	}

	id := func(name string) string { return pgx.Identifier{name}.Sanitize() }
	exec := func(sql string) error {
		if _, err := tx.Exec(ctx, sql); err != nil {
			return fmt.Errorf("store: %s: %w", sql, err)
		}
		return nil
	}

	for _, role := range []string{roles.API, roles.Worker} {
		if err := exec(fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s", id(database), id(role))); err != nil {
			return false, err
		}
		if err := exec(fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", id(schema), id(role))); err != nil {
			return false, err
		}
		if err := exec(fmt.Sprintf("REVOKE ALL ON ALL TABLES IN SCHEMA %s FROM %s", id(schema), id(role))); err != nil {
			return false, err
		}
	}
	for _, table := range tables {
		access, ok := accessMatrix[table]
		if !ok {
			continue
		}
		for role, p := range map[string]Privileges{roles.API: access.API, roles.Worker: access.Worker} {
			if len(p.Table) > 0 {
				if err := exec(fmt.Sprintf("GRANT %s ON TABLE %s TO %s", strings.Join(p.Table, ", "), id(table), id(role))); err != nil {
					return false, err
				}
			}
			for priv, cols := range p.Columns {
				quoted := make([]string, len(cols))
				for i, c := range cols {
					quoted[i] = id(c)
				}
				if err := exec(fmt.Sprintf("GRANT %s (%s) ON TABLE %s TO %s", priv, strings.Join(quoted, ", "), id(table), id(role))); err != nil {
					return false, err
				}
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("store: confirmar privilégios: %w", err)
	}
	return true, nil
}

// CheckSchema confere que o esquema aplicado é exatamente o que este binário
// espera: nem atrasado nem mais novo (DEC-33 item 6). Não altera nada, então
// serve a roles sem privilégio de DDL.
func CheckSchema(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("store: o esquema do banco não foi preparado (rode `cobogo-api migrate`): %w", err)
	}
	defer rows.Close()
	applied := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("store: listar migrations embutidas: %w", err)
	}
	expected := map[string]bool{}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			expected[e.Name()] = true
		}
	}
	var missing, extra []string
	for name := range expected {
		if !applied[name] {
			missing = append(missing, name)
		}
	}
	for name := range applied {
		if !expected[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	switch {
	case len(missing) > 0:
		return fmt.Errorf("store: o banco está atrasado (faltam %s); rode `cobogo-api migrate`", strings.Join(missing, ", "))
	case len(extra) > 0:
		return fmt.Errorf("store: o banco é mais novo que este binário (tem %s); atualize o Cobogó", strings.Join(extra, ", "))
	}
	return nil
}

// Prepare deixa o esquema pronto para o processo: migra, ou só confere quando
// as migrations rodam em passo próprio (COBOGO_AUTO_MIGRATE=0).
func Prepare(ctx context.Context, pool *pgxpool.Pool, autoMigrate bool) error {
	if autoMigrate {
		return Migrate(ctx, pool)
	}
	return CheckSchema(ctx, pool)
}
