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
	// Collector é o role do coletor de métricas (DEC-43). É opcional: se não
	// existe, os privilégios dele não são aplicados (desenvolvimento).
	Collector string
}

// DefaultRoles são os nomes que o script de criação dos roles usa.
var DefaultRoles = Roles{API: "cobogo_api", Worker: "cobogo_worker", Collector: "cobogo_collector"}

// Privileges descreve o que um role pode fazer numa tabela: privilégios da
// tabela inteira e privilégios por coluna.
type Privileges struct {
	Table   []string
	Columns map[string][]string
}

// TableAccess é o acesso de cada role a uma tabela.
type TableAccess struct {
	API       Privileges
	Worker    Privileges
	Collector Privileges
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
	"schema_migrations": {API: tbl("SELECT"), Worker: tbl("SELECT"), Collector: tbl("SELECT")},
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
	// DEC-43: só o coletor escreve as amostras (e as apaga na retenção); a API as lê.
	"metric_samples":      {API: tbl("SELECT"), Collector: tbl("SELECT", "INSERT", "DELETE")},
	"metric_rollups":      {API: tbl("SELECT"), Collector: tbl("SELECT", "INSERT", "UPDATE", "DELETE")},
	"metric_probes":       {API: tbl("SELECT"), Collector: tbl("SELECT", "INSERT", "DELETE")},
	"collector_heartbeat": {API: tbl("SELECT"), Collector: tbl("SELECT", "INSERT", "UPDATE")},
	// DEC-45: a API cria o pedido e só pode marcar o cancelamento; o worker executa
	// e grava o desfecho.
	"project_commands": {
		API:    Privileges{Table: []string{"SELECT", "INSERT"}, Columns: map[string][]string{"UPDATE": {"cancel_requested"}}},
		Worker: tbl("SELECT", "UPDATE"),
	},
	// DEC-46: o worker captura e apaga o que passa da retenção; a API só lê.
	"runtime_logs": {API: tbl("SELECT"), Worker: tbl("SELECT", "INSERT", "DELETE")},
	// DEC-47 parte B: a API pede o backup e só marca o pedido de apagar; o worker copia,
	// grava o desfecho e apaga os arquivos.
	"project_backups": {
		API:    Privileges{Table: []string{"SELECT", "INSERT"}, Columns: map[string][]string{"UPDATE": {"delete_requested"}}},
		Worker: tbl("SELECT", "UPDATE"),
	},
	// DEC-47 parte C: a API cria o pedido de restauração; o worker o executa e grava o desfecho.
	"project_restores": {API: tbl("SELECT", "INSERT"), Worker: tbl("SELECT", "UPDATE")},
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

// sequenceAccess é o que cada role pode fazer nas sequências (`BIGSERIAL`): inserir
// numa tabela cuja chave é uma sequência exige USAGE nela. Só as listadas valem.
var sequenceAccess = map[string]struct{ API, Worker, Collector bool }{
	// DEC-46: o worker insere os logs de execução.
	"runtime_logs_id_seq": {Worker: true},
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

	// O coletor é opcional: só entra se o role existe.
	granted := map[string]func(TableAccess) Privileges{
		roles.API:    func(a TableAccess) Privileges { return a.API },
		roles.Worker: func(a TableAccess) Privileges { return a.Worker },
	}
	if roles.Collector != "" {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, roles.Collector).Scan(&exists); err != nil {
			return false, fmt.Errorf("store: procurar o role do coletor: %w", err)
		}
		if exists {
			granted[roles.Collector] = func(a TableAccess) Privileges { return a.Collector }
		}
	}
	for role := range granted {
		if err := exec(fmt.Sprintf("GRANT CONNECT ON DATABASE %s TO %s", id(database), id(role))); err != nil {
			return false, err
		}
		if err := exec(fmt.Sprintf("GRANT USAGE ON SCHEMA %s TO %s", id(schema), id(role))); err != nil {
			return false, err
		}
		if err := exec(fmt.Sprintf("REVOKE ALL ON ALL TABLES IN SCHEMA %s FROM %s", id(schema), id(role))); err != nil {
			return false, err
		}
		if err := exec(fmt.Sprintf("REVOKE ALL ON ALL SEQUENCES IN SCHEMA %s FROM %s", id(schema), id(role))); err != nil {
			return false, err
		}
	}
	for seq, who := range sequenceAccess {
		for role := range granted {
			allowed := (role == roles.API && who.API) || (role == roles.Worker && who.Worker) || (roles.Collector != "" && role == roles.Collector && who.Collector)
			if allowed {
				if err := exec(fmt.Sprintf("GRANT USAGE ON SEQUENCE %s TO %s", id(seq), id(role))); err != nil {
					return false, err
				}
			}
		}
	}
	for _, table := range tables {
		access, ok := accessMatrix[table]
		if !ok {
			continue
		}
		for role, pick := range granted {
			p := pick(access)
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
