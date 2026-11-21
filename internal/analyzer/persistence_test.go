package analyzer

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func suggest(files map[string]string) []domain.PersistentSuggestion {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return suggestPersistence(fsys)
}

func find(list []domain.PersistentSuggestion, path string) *domain.PersistentSuggestion {
	for i := range list {
		if list[i].Path == path {
			return &list[i]
		}
	}
	return nil
}

// Sugestão de caminhos persistentes (DEC-41): cada caso é uma pista que o
// código dá, e o que ela deve (ou não deve) virar.
func TestSuggestPersistence(t *testing.T) {
	t.Run("a SQLite file named in the code is a strong suggestion for its folder", func(t *testing.T) {
		got := suggest(map[string]string{
			"package.json": `{"dependencies":{"hono":"1"}}`,
			"src/db.ts":    "import { Database } from 'bun:sqlite';\nexport const db = new Database(\"data/caderno.sqlite\");\n",
		})
		s := find(got, "/app/data")
		if s == nil || s.Confidence != domain.SuggestStrong || s.Kind != domain.SuggestSQLite || len(s.Evidence) == 0 || !strings.HasPrefix(s.Evidence[0], "src/db.ts:2") {
			t.Fatalf("got %+v", got)
		}
		if find(got, "") != nil {
			t.Error("a known path makes the 'where is the file' note redundant")
		}
	})
	t.Run("an absolute path and a default after ?? are read", func(t *testing.T) {
		got := suggest(map[string]string{"server.js": "const f = process.env.DB ?? './storage/app.db';\nnew Database('/var/lib/app/main.sqlite3');\n"})
		if find(got, "/app/storage") == nil || find(got, "/var/lib/app") == nil {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("a database at the app root cannot become a volume and asks where", func(t *testing.T) {
		got := suggest(map[string]string{"server.js": "const db = new Database('app.sqlite');"})
		if len(got) != 1 || got[0].Path != "" || got[0].Confidence != domain.SuggestWeak {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("a dynamic path with the SQLite import only asks where", func(t *testing.T) {
		got := suggest(map[string]string{"db.ts": "import { Database } from \"bun:sqlite\";\nnew Database(process.env.DB_PATH);"})
		if len(got) != 1 || got[0].Path != "" || got[0].Kind != domain.SuggestSQLite {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("only the dependency, no path found", func(t *testing.T) {
		got := suggest(map[string]string{"package.json": `{"dependencies":{"better-sqlite3":"9"}}`, "index.js": "console.log('oi')"})
		if len(got) != 1 || got[0].Path != "" || got[0].Evidence[0] != "package.json" || !strings.Contains(got[0].Reason, "better-sqlite3") {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("a folder the code writes to is a weak suggestion, and says when the repository has it", func(t *testing.T) {
		got := suggest(map[string]string{
			"src/save.ts":                "import { writeFileSync } from 'node:fs';\nwriteFileSync(`content/folhas/${slug}.md`, text);\n",
			"content/folhas/primeira.md": "x",
		})
		s := find(got, "/app/content")
		if s == nil || s.Confidence != domain.SuggestWeak || s.Kind != domain.SuggestWriteDir || !strings.Contains(s.Reason, "já tem essa pasta") {
			t.Fatalf("got %+v", got)
		}
	})
	t.Run("join with a data folder, in a file that writes", func(t *testing.T) {
		got := suggest(map[string]string{"up.js": "const dir = join(__dirname, \"uploads\");\nfs.mkdirSync(dir);\n"})
		if find(got, "/app/uploads") == nil {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("a relative path with ../ is resolved from the file, and an import is not data", func(t *testing.T) {
		got := suggest(map[string]string{
			"src/content/escritor.ts": "import { getDb } from \"../db/client.ts\";\nimport { writeFileSync } from \"node:fs\";\nconst RAIZ = resolve(import.meta.dir, \"../../content\");\nwriteFileSync(join(RAIZ, 'x'), '');\n",
		})
		if len(got) != 1 || got[0].Path != "/app/content" {
			t.Errorf("got %+v, want only /app/content (the ../db import is code)", got)
		}
		if got := suggest(map[string]string{"a.js": "const p = resolve(__dirname, \"../../../data\"); fs.mkdirSync(p);"}); len(got) != 0 {
			t.Errorf("a path that leaves the app must be ignored: %+v", got)
		}
	})
	t.Run("what must not be suggested", func(t *testing.T) {
		for name, files := range map[string]map[string]string{
			"stream event named data":     {"a.js": "stream.on(\"data\", c => out.write(c));\nfs.writeFileSync('x', 'y');"},
			"a folder name with no write": {"a.js": "const p = 'data/readme.txt'; console.log(p);"},
			"memory database":             {"a.js": "new Database(':memory:')"},
			"templated path":              {"a.js": "new Database(`${dir}/x.sqlite`)"},
			"test code":                   {"tests/db.test.ts": "new Database('data/test.sqlite'); writeFileSync('content/x', '')"},
			"build config":                {"vite.config.ts": "new Database('data/x.sqlite')"},
			"nothing":                     {"index.js": "console.log(1)"},
		} {
			if got := suggest(files); len(got) != 0 {
				t.Errorf("%s: got %+v, want none", name, got)
			}
		}
	})
	t.Run("an absolute path outside the app is only a weak hint", func(t *testing.T) {
		got := suggest(map[string]string{"a.js": "new Database('/artifacts/app.sqlite')"})
		if s := find(got, "/artifacts"); s == nil || s.Confidence != domain.SuggestWeak {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("a database in a system folder is not offered as a path", func(t *testing.T) {
		got := suggest(map[string]string{"a.js": "new Database('/etc/app/x.db')"})
		if len(got) != 1 || got[0].Path != "" {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("strong comes first, the same folder is merged and there is a cap", func(t *testing.T) {
		got := suggest(map[string]string{
			"a.js": "new Database('data/a.sqlite'); fs.writeFileSync('data/b.json', '');",
		})
		if len(got) != 1 || got[0].Confidence != domain.SuggestStrong || got[0].Path != "/app/data" {
			t.Errorf("got %+v", got)
		}
		files := map[string]string{}
		for _, d := range []string{"data", "content", "uploads", "storage", "files", "db", "database"} {
			files[d+".js"] = "fs.writeFileSync('" + d + "/x', '');"
		}
		if got := suggest(files); len(got) != maxSuggestions {
			t.Errorf("len = %d, want the cap of %d", len(got), maxSuggestions)
		}
	})
	t.Run("the analysis always carries a list, never null", func(t *testing.T) {
		res, err := Analyze(fstest.MapFS{"package.json": &fstest.MapFile{Data: []byte(`{"scripts":{"start":"node a.js"}}`)}, "a.js": &fstest.MapFile{Data: []byte("1")}}, Input{})
		if err != nil || res.PersistentSuggestions == nil {
			t.Errorf("suggestions = %v, %v", res.PersistentSuggestions, err)
		}
	})
}
