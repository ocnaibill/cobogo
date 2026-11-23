package analyzer

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func needs(files map[string]string, vars ...string) []domain.ServiceNeed {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	var cv []domain.ConfigVariable
	for _, v := range vars {
		cv = append(cv, domain.ConfigVariable{Name: v})
	}
	return detectServiceNeeds(fsys, cv)
}

// Necessidade de banco (HEU-06, DEC-42): o que vira pergunta antes do build, o
// que vira só uma sugestão e o que não vira nada.
func TestDetectServiceNeeds(t *testing.T) {
	t.Run("a driver imported by runtime code is a required need, with its evidence", func(t *testing.T) {
		got := needs(map[string]string{
			"package.json": `{"dependencies":{"pg":"8"}}`,
			"src/db.js":    "const { Pool } = require('pg');\nmodule.exports = new Pool({ connectionString: process.env.DATABASE_URL });\n",
		}, "DATABASE_URL")
		if len(got) != 1 || got[0].Level != domain.NeedRequired || got[0].Variable != "DATABASE_URL" || got[0].Evidence[0] != "src/db.js:1" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("ESM import, postgres.js and drizzle adapters count", func(t *testing.T) {
		for name, body := range map[string]string{
			"import from":    "import { Pool } from 'pg';",
			"postgres.js":    "import postgres from \"postgres\";",
			"drizzle":        "import { drizzle } from 'drizzle-orm/node-postgres';",
			"typeorm config": "new DataSource({ type: 'postgres', url: u })",
			"sequelize":      "new Sequelize(u, { dialect: \"postgres\" })",
			"knex":           "knex({ client: 'pg', connection: u })",
			"dynamic import": "const pg = await import('pg');",
			"pg-promise":     "const pgp = require('pg-promise')();",
		} {
			if got := needs(map[string]string{"src/a.ts": body}); len(got) != 1 || got[0].Level != domain.NeedRequired {
				t.Errorf("%s: got %+v", name, got)
			}
		}
	})
	t.Run("a Prisma schema for PostgreSQL with the client is required and names the variable", func(t *testing.T) {
		got := needs(map[string]string{
			"package.json":         `{"dependencies":{"@prisma/client":"5"}}`,
			"prisma/schema.prisma": "datasource db {\n  provider = \"postgresql\"\n  url      = env(\"POSTGRES_URL\")\n}\n",
		})
		if len(got) != 1 || got[0].Level != domain.NeedRequired || got[0].Variable != "POSTGRES_URL" || got[0].Evidence[0] != "prisma/schema.prisma:2" {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("only the dependency is a possible need", func(t *testing.T) {
		got := needs(map[string]string{"package.json": `{"dependencies":{"pg":"8"}}`, "index.js": "console.log(1)"})
		if len(got) != 1 || got[0].Level != domain.NeedPossible || !strings.Contains(got[0].Reason, "pg") {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("a schema without the Prisma client is only possible", func(t *testing.T) {
		got := needs(map[string]string{"prisma/schema.prisma": "datasource db {\n  provider = \"postgresql\"\n  url = env(\"DATABASE_URL\")\n}"})
		if len(got) != 1 || got[0].Level != domain.NeedPossible {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("the connection variable is the first known one, else DATABASE_URL", func(t *testing.T) {
		files := map[string]string{"a.js": "require('pg')"}
		if got := needs(files, "DB_URL", "OTHER"); got[0].Variable != "DB_URL" {
			t.Errorf("known DB_URL: %+v", got)
		}
		if got := needs(files, "POSTGRES_URL", "DB_URL"); got[0].Variable != "POSTGRES_URL" {
			t.Errorf("preference order: %+v", got)
		}
		if got := needs(files); got[0].Variable != "DATABASE_URL" {
			t.Errorf("default: %+v", got)
		}
	})
	t.Run("what must not be a need", func(t *testing.T) {
		for name, files := range map[string]map[string]string{
			"nothing":               {"index.js": "console.log(1)"},
			"a test using pg":       {"tests/db.test.js": "const pg = require('pg')"},
			"tooling":               {"scripts/seed.js": "const pg = require('pg')"},
			"a sqlite app":          {"package.json": `{"dependencies":{"better-sqlite3":"9"}}`, "a.js": "require('better-sqlite3')"},
			"a mention in a string": {"a.js": "const help = 'install the pg driver first'"},
			"another database":      {"a.js": "require('mongodb')"},
		} {
			if got := needs(files); len(got) != 0 {
				t.Errorf("%s: got %+v, want none", name, got)
			}
		}
	})
	t.Run("the analysis always carries a list, never null", func(t *testing.T) {
		res, err := Analyze(fstest.MapFS{"package.json": &fstest.MapFile{Data: []byte(`{"scripts":{"start":"node a.js"}}`)}, "a.js": &fstest.MapFile{Data: []byte("1")}}, Input{})
		if err != nil || res.ServiceNeeds == nil {
			t.Errorf("needs = %v, %v", res.ServiceNeeds, err)
		}
	})
}

// Estilo da conexão (DEC-44): URL única ou partes em família de variáveis.
func TestConnectionStyle(t *testing.T) {
	code := map[string]string{"package.json": `{"dependencies":{"pg":"8"}}`, "src/db.js": "const { Pool } = require('pg');\n"}
	t.Run("a POSTGRES_ family read in parts is detected, with the CA", func(t *testing.T) {
		got := needs(code, "POSTGRES_HOST", "POSTGRES_PORT", "POSTGRES_USER", "POSTGRES_DB", "POSTGRES_PASSWORD", "POSTGRES_CA", "NODE_ENV")
		if len(got) != 1 || got[0].Style != domain.StyleParts || got[0].Family != "POSTGRES" {
			t.Fatalf("got %+v", got)
		}
		want := map[string]string{"host": "POSTGRES_HOST", "port": "POSTGRES_PORT", "user": "POSTGRES_USER", "database": "POSTGRES_DB", "password": "POSTGRES_PASSWORD", "ca": "POSTGRES_CA"}
		for k, v := range want {
			if got[0].Variables[k] != v {
				t.Errorf("role %s: %q, want %q", k, got[0].Variables[k], v)
			}
		}
	})
	t.Run("PG and DB_ families work, with their suffix variants", func(t *testing.T) {
		got := needs(code, "PGHOST", "PGUSER", "PGPASSWORD", "PGDATABASE")
		if got[0].Style != domain.StyleParts || got[0].Family != "PG" || got[0].Variables["database"] != "PGDATABASE" {
			t.Fatalf("PG: %+v", got)
		}
		got = needs(code, "DB_HOSTNAME", "DB_USERNAME", "DB_PASS", "DB_NAME")
		if got[0].Style != domain.StyleParts || got[0].Family != "DB" || got[0].Variables["host"] != "DB_HOSTNAME" || got[0].Variables["password"] != "DB_PASS" {
			t.Fatalf("DB: %+v", got)
		}
	})
	t.Run("a URL variable wins over parts", func(t *testing.T) {
		got := needs(code, "DATABASE_URL", "POSTGRES_HOST", "POSTGRES_PASSWORD")
		if got[0].Style != domain.StyleURL || got[0].Variables["url"] != "DATABASE_URL" || got[0].Variable != "DATABASE_URL" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("parts without host and password fall back to the default URL", func(t *testing.T) {
		got := needs(code, "POSTGRES_USER", "POSTGRES_DB")
		if got[0].Style != domain.StyleURL || got[0].Variable != "DATABASE_URL" {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("no variables referenced defaults to DATABASE_URL", func(t *testing.T) {
		if got := needs(code); got[0].Style != domain.StyleURL || got[0].Variables["url"] != "DATABASE_URL" {
			t.Fatalf("got %+v", got)
		}
	})
}

// MySQL (DEC-50): o mesmo critério do PostgreSQL, com os nomes de variável do MySQL.
func TestDetectMySQLNeeds(t *testing.T) {
	got := needs(map[string]string{"package.json": `{"dependencies":{"mysql2":"3"}}`, "src/db.js": "const mysql = require('mysql2/promise')\n"}, "MYSQL_URL")
	if len(got) != 1 || got[0].Engine != domain.ServiceMySQL || got[0].Level != domain.NeedRequired || got[0].Variable != "MYSQL_URL" || got[0].Style != domain.StyleURL {
		t.Fatalf("mysql2 em runtime: %+v", got)
	}
	// Só a dependência: sugestão.
	got = needs(map[string]string{"package.json": `{"dependencies":{"mysql":"2"}}`})
	if len(got) != 1 || got[0].Level != domain.NeedPossible {
		t.Errorf("só a dependência: %+v", got)
	}
	// Partes: a família DB_ (como o fec) e a MYSQL_.
	got = needs(map[string]string{"a.js": "require('mysql2')"}, "DB_HOST", "DB_PORT", "DB_USER", "DB_PASSWORD", "DB_NAME")
	if len(got) != 1 || got[0].Style != domain.StyleParts || got[0].Family != "DB" || got[0].Variables[domain.RoleDatabase] != "DB_NAME" || got[0].Variables[domain.RoleCA] != "" {
		t.Errorf("partes DB_: %+v", got)
	}
	got = needs(map[string]string{"a.js": "require('mysql2')"}, "MYSQL_HOST", "MYSQL_PASSWORD", "MYSQL_DATABASE")
	if len(got) != 1 || got[0].Family != "MYSQL" {
		t.Errorf("partes MYSQL_: %+v", got)
	}
	// PostgreSQL e MySQL no mesmo projeto: uma necessidade para cada.
	got = needs(map[string]string{"a.js": "require('pg'); require('mysql2')"})
	if len(got) != 2 {
		t.Errorf("dois bancos: %+v", got)
	}
	// Prisma com provider mysql.
	got = needs(map[string]string{"package.json": `{"dependencies":{"@prisma/client":"5"}}`, "prisma/schema.prisma": "datasource db {\n  provider = \"mysql\"\n  url = env(\"MYSQL_DATABASE_URL\")\n}\n"})
	if len(got) != 1 || got[0].Engine != domain.ServiceMySQL || got[0].Variable != "MYSQL_DATABASE_URL" {
		t.Errorf("prisma: %+v", got)
	}
}

// Redis (DEC-50 C): evidência forte, só a dependência, o endereço escrito no código (que nunca
// guarda a senha) e como o código chega ao serviço.
func TestDetectRedisNeeds(t *testing.T) {
	got := needs(map[string]string{"package.json": `{"dependencies":{"ioredis":"5"}}`, "src/q.js": "const Redis = require('ioredis')\nconst r = new Redis(process.env.REDIS_URL)\n"}, "REDIS_URL")
	if len(got) != 1 || got[0].Engine != domain.ServiceRedis || got[0].Level != domain.NeedRequired || got[0].Variable != "REDIS_URL" || len(got[0].FixedAt) != 0 {
		t.Fatalf("ioredis lendo REDIS_URL: %+v", got)
	}
	// Só a dependência: sugestão.
	got = needs(map[string]string{"package.json": `{"dependencies":{"bullmq":"5"}}`})
	if len(got) != 1 || got[0].Level != domain.NeedPossible {
		t.Errorf("só a dependência: %+v", got)
	}
	// O endereço escrito no código: forte, com o lugar, e sem a senha.
	got = needs(map[string]string{"src/q.js": "const c = createClient({ url: 'redis://:segredo123@cache.interno:6380/2' })\n"})
	if len(got) != 1 || got[0].Level != domain.NeedRequired || len(got[0].FixedAt) != 1 || got[0].FixedAt[0] != "src/q.js:1" {
		t.Fatalf("endereço escrito no código: %+v", got)
	}
	if strings.Contains(strings.Join(got[0].Evidence, " ")+strings.Join(got[0].FixedAt, " ")+got[0].Reason, "segredo123") {
		t.Error("a senha escrita no código apareceu na análise")
	}
	// O endereço como padrão de uma variável: uso do Redis, mas não endereço fixo.
	got = needs(map[string]string{"src/q.js": "const url = process.env.REDIS_URL || 'redis://localhost:6379'\nconst b = process.env['QUEUE_URL'] ?? \"redis://cache:6379/1\"\n"})
	if len(got) != 1 || got[0].Level != domain.NeedRequired || len(got[0].FixedAt) != 0 {
		t.Errorf("padrão de variável no JS: %+v", got)
	}
	// Partes REDIS_*: host e senha, sem usuário nem banco.
	got = needs(map[string]string{"a.js": "require('redis')"}, "REDIS_HOST", "REDIS_PORT", "REDIS_PASSWORD", "REDIS_DB")
	if len(got) != 1 || got[0].Style != domain.StyleParts || got[0].Family != "REDIS" || got[0].Variables[domain.RoleDatabase] != "" || got[0].Variables[domain.RoleUser] != "" || got[0].Variables[domain.RolePassword] != "REDIS_PASSWORD" {
		t.Errorf("partes REDIS_: %+v", got)
	}
	// O broker do Celery como variável.
	got = needs(map[string]string{"a.js": "require('bull')"}, "CELERY_BROKER_URL")
	if len(got) != 1 || got[0].Variable != "CELERY_BROKER_URL" {
		t.Errorf("CELERY_BROKER_URL: %+v", got)
	}
	// Nada: um servidor que só cita a palavra.
	if got := needs(map[string]string{"a.js": "const help = 'use redis for cache'"}); len(got) != 0 {
		t.Errorf("menção em texto: %+v", got)
	}
}
