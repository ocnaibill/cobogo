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
			"another database":      {"a.js": "require('mysql2')"},
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
