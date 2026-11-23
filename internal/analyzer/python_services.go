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
	needs := append(detectPythonPostgresNeeds(fsys, ev, vars), detectPythonMySQLNeeds(ev, vars)...)
	return append(needs, detectPythonRedisNeeds(ev, vars)...)
}

// detectPythonRedisNeeds é a HEU-06 para Redis no Python: o import do cliente, o backend de cache
// do Django ou o endereço `redis://` escrito no código (forte, com arquivo e linha); só o cliente nas
// dependências (possível). O Celery sozinho não é Redis: o broker pode ser outro.
func detectPythonRedisNeeds(ev []domain.Evidence, vars []domain.ConfigVariable) []domain.ServiceNeed {
	var strong, fixed []string
	for _, e := range ev {
		switch {
		case e.Kind == kindPyRedis && e.Scope == domain.ScopeRuntime:
			if len(strong) < 3 {
				strong = append(strong, evidenceRef(e))
			}
			if e.Name == "url" && len(fixed) < 3 {
				fixed = append(fixed, evidenceRef(e))
			}
		case e.Kind == kindDjangoSet && e.Name == "REDIS_CACHE" && len(strong) < 3:
			strong = append(strong, evidenceRef(e))
		}
	}
	dep, celeryDep := "", false
	for _, e := range ev {
		if e.Kind == kindPyDependency && dep == "" && slices.Contains(pyRedisDeps, e.Name) {
			dep = e.Name
		}
		if e.Kind == kindPyDependency && e.Name == "celery" {
			celeryDep = true
		}
	}
	// O Celery com o cliente do Redis nas dependências usa o Redis como broker, ainda que o código só leia
	// a URL de uma variável de ambiente (o caso comum: CELERY_BROKER_URL = os.environ[...]).
	if len(strong) == 0 && celeryDep && dep != "" {
		for _, e := range ev {
			if e.Kind == kindPyCelery && e.Name == "worker" {
				strong = append(strong, evidenceRef(e))
				break
			}
		}
	}
	return buildRedisNeed(strong, dep, "requirements", fixed, vars)
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
