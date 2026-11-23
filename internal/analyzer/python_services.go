package analyzer

import (
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Banco e volumes do catálogo Python (DEC-48 item 10): as mesmas heurísticas do
// JavaScript (HEU-06 e DEC-41), com as pistas do Python.

// pyPgDeps: pacotes Python que falam com PostgreSQL.
var pyPgDeps = []string{"psycopg2", "psycopg2-binary", "psycopg", "psycopg-binary", "asyncpg", "pg8000"}

func evidenceRef(e domain.Evidence) string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d", e.Source, e.Line)
	}
	return e.Source
}

func detectPythonServiceNeeds(fsys fs.FS, ev []domain.Evidence, vars []domain.ConfigVariable) []domain.ServiceNeed {
	return append(detectPythonPostgresNeeds(fsys, ev, vars), detectPythonMySQLNeeds(ev, vars)...)
}

// pyMySQLDeps: pacotes Python que falam com MySQL e MariaDB.
var pyMySQLDeps = []string{"mysqlclient", "pymysql", "mysql-connector-python", "aiomysql", "asyncmy"}

// detectPythonMySQLNeeds é a HEU-06 para MySQL: o ENGINE do Django, o import do driver em runtime
// (necessidade `required`) ou só a dependência (`possible`).
func detectPythonMySQLNeeds(ev []domain.Evidence, vars []domain.ConfigVariable) []domain.ServiceNeed {
	var runtime []string
	for _, e := range ev {
		if len(runtime) >= 3 {
			break
		}
		if (e.Kind == kindPyMySQL && e.Scope == domain.ScopeRuntime) || (e.Kind == kindDjangoSet && e.Name == "MYSQL") {
			runtime = append(runtime, evidenceRef(e))
		}
	}
	dep := ""
	for _, e := range ev {
		if e.Kind == kindPyDependency && dep == "" && slices.Contains(pyMySQLDeps, e.Name) {
			dep = e.Name
		}
	}
	need := domain.ServiceNeed{Engine: domain.ServiceMySQL}
	switch {
	case len(runtime) > 0:
		need.Level, need.Evidence, need.Reason = domain.NeedRequired, runtime, "o código usa MySQL"
	case dep != "":
		need.Level, need.Evidence = domain.NeedPossible, []string{"requirements"}
		need.Reason = "a dependência " + dep + " fala com MySQL, mas nenhum arquivo de runtime a importa"
	default:
		return nil
	}
	need.Style, need.Family, need.Variables = connectionStyleFor(domain.ServiceMySQL, "", vars)
	need.Variable = need.Variables[domain.PrimaryRole(need.Style)]
	return []domain.ServiceNeed{need}
}

func detectPythonPostgresNeeds(_ fs.FS, ev []domain.Evidence, vars []domain.ConfigVariable) []domain.ServiceNeed {
	var runtime []string
	for _, e := range ev {
		if len(runtime) >= 3 {
			break
		}
		if (e.Kind == kindPyPostgres && e.Scope == domain.ScopeRuntime) || (e.Kind == kindDjangoSet && e.Name == "POSTGRES") {
			runtime = append(runtime, evidenceRef(e))
		}
	}
	dep := ""
	for _, e := range ev {
		if e.Kind == kindPyDependency && dep == "" {
			for _, d := range pyPgDeps {
				if e.Name == d {
					dep = d
				}
			}
		}
	}
	need := domain.ServiceNeed{Engine: domain.ServicePostgres}
	switch {
	case len(runtime) > 0:
		need.Level, need.Evidence, need.Reason = domain.NeedRequired, runtime, "o código usa PostgreSQL"
	case dep != "":
		need.Level, need.Evidence = domain.NeedPossible, []string{"requirements"}
		need.Reason = "a dependência " + dep + " fala com PostgreSQL, mas nenhum arquivo de runtime a importa"
	default:
		return []domain.ServiceNeed{}
	}
	need.Style, need.Family, need.Variables = connectionStyle("", vars)
	need.Variable = need.Variables[domain.PrimaryRole(need.Style)]
	return []domain.ServiceNeed{need}
}

var (
	pySQLiteImport = regexp.MustCompile(`(?m)^\s*(?:import\s+sqlite3|from\s+sqlite3\s+import|import\s+aiosqlite)`)
	// Escritas em disco. O .save() fica de fora: no Django é o do ORM.
	pyWriteCall = regexp.MustCompile(`\bopen\([^)\n]*,\s*(?:mode\s*=\s*)?["'][wax][bt+]*["']|\bos\.makedirs?\(|\.mkdir\(|\bshutil\.(?:copy\w*|move)\(|\.write_(?:text|bytes)\(`)
	// Pasta de dados citada como caminho, em aspas do Python (crase é comentário).
	pyDirWithPath = regexp.MustCompile(`["']((?:\.{1,2}/)*|/)(` + dirNames + `)/[^"'\n]*["']`)
	pyDirInJoin   = regexp.MustCompile(`(?:join|Path)\([^()\n]*["']((?:\.{1,2}/)*)(` + dirNames + `)/?["']`)
)

func suggestPythonPersistence(fsys fs.FS, ev []domain.Evidence) []domain.PersistentSuggestion {
	lang := persistenceLang{
		extensions: map[string]bool{".py": true}, skip: pySkippedDirs,
		sqliteImport: pySQLiteImport, writeCall: pyWriteCall,
		dirPatterns: []*regexp.Regexp{pyDirWithPath, pyDirInJoin}, skipFile: isPyTestFile,
	}
	for _, e := range ev {
		if e.Kind != kindDjangoSet || e.Name != "MEDIA_ROOT" || e.Value == "" {
			continue
		}
		dir := e.Value
		if !strings.HasPrefix(dir, "/") {
			dir = path.Join("/app", strings.TrimPrefix(dir, "./"))
		}
		lang.extra = append(lang.extra, domain.PersistentSuggestion{
			Path: dir, Kind: domain.SuggestWriteDir, Confidence: domain.SuggestWeak,
			Reason:   "o Django guarda os arquivos enviados (MEDIA_ROOT) em " + dir + "; sem volume, eles somem a cada deploy",
			Evidence: []string{evidenceRef(e)},
		})
	}
	return suggestPersistenceWith(fsys, lang)
}
