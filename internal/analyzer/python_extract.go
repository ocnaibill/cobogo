package analyzer

import (
	"bufio"
	"bytes"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Extração do catálogo Python (DEC-48). Lê arquivos como texto: nada é importado
// nem executado (HEU-04, RN-08). Cada evidência traz o arquivo e a linha.

const (
	extractorPyManifest = "py.manifest"
	extractorPySource   = "py.source"
)

const (
	kindPyManifest   = "py.manifest"   // um arquivo de dependências (Name = o arquivo)
	kindPyLock       = "py.lock"       // um lockfile (Name = o gerenciador)
	kindPyDependency = "py.dependency" // Name = pacote normalizado, Value = a especificação
	kindPyUnpinned   = "py.unpinned"   // uma linha de requirements sem versão fixada
	kindPySpecial    = "py.special"    // Name = index|editable_outside|vcs|private_index; Value = a linha
	kindPyVersion    = "py.version"    // Name = de onde veio; Value = a declaração
	kindPyProcfile   = "py.procfile"   // Name = tipo do processo; Value = o comando
	kindPyApp        = "py.app"        // Name = framework; Value = modulo:atributo
	kindPyFactory    = "py.factory"    // Name = framework; Value = modulo:funcao()
	kindPyRunTarget  = "py.run_target" // Value = o alvo em texto de uvicorn.run("m:a")
	kindPySrcLayout  = "py.src_layout" // o código fica em src/ (PYTHONPATH=src)
	kindPyManage     = "py.manage"     // Name = nome do comando; Value = a linha que o roda (DEC-45)
	kindDjangoModule = "django.settings_module"
	kindDjangoCand   = "django.settings_candidate" // outro módulo do pacote de settings
	kindDjangoApp    = "django.app"                // Name = wsgi|asgi; Value = modulo:atributo
	kindDjangoSet    = "django.setting"            // Name = configuração; Value = o que se leu
	kindPyPostgres   = "py.postgres"               // Name = driver importado em código de runtime
	kindPyMySQL      = "py.mysql"                  // Name = driver MySQL importado em código de runtime
	kindPyCelery     = "py.celery"                 // Name = worker|beat|beat_setting; Value = o alvo do -A do Celery
	kindPyRedis      = "py.redis"                  // Name = import|url; Value = o endereço redis:// sem credenciais
)

// Pastas que não são código da aplicação.
var pySkippedDirs = map[string]bool{
	".git": true, "node_modules": true, "venv": true, ".venv": true, "env": true, "virtualenv": true,
	"__pycache__": true, "site-packages": true, ".tox": true, ".nox": true, "build": true, "dist": true,
	".mypy_cache": true, ".pytest_cache": true, ".ruff_cache": true, "htmlcov": true, ".eggs": true,
}

const maxPyFiles = 3000

var (
	pepName      = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)`)
	pepNormalize = regexp.MustCompile(`[-_.]+`)
	procfileLine = regexp.MustCompile(`^([A-Za-z0-9_-]+)\s*:\s*(.+?)\s*$`)

	// Atribuição de nível de módulo de uma aplicação (DEC-48 item 5).
	pyAppAssign      = regexp.MustCompile(`(?m)^([A-Za-z_]\w*)\s*(?::\s*[\w.\[\], ]+)?=\s*((?:[A-Za-z_]\w*\.)*)(Flask|FastAPI|Starlette|Quart|Litestar|Sanic|App)\s*\(`)
	pyFactory        = regexp.MustCompile(`(?m)^(?:async\s+)?def\s+(create_app|make_app|app_factory|get_app|build_app)\s*\(`)
	pyCeleryApp      = regexp.MustCompile(`(?m)^([A-Za-z_]\w*)\s*=\s*(?:celery\.)?Celery\s*\(`)
	pyCeleryBeat     = regexp.MustCompile(`CELERY_BEAT_SCHEDULE|\bbeat_schedule\b|django_celery_beat`)
	pyRedisImport    = regexp.MustCompile(`(?m)^\s*(?:import|from)\s+(redis|aioredis|django_redis|rq|channels_redis)\b`)
	djangoRedisCache = regexp.MustCompile(`django_redis|backends\.redis\.RedisCache`)
	pyMySQLImport    = regexp.MustCompile(`(?m)^\s*(?:import|from)\s+(MySQLdb|pymysql|mysql\.connector|aiomysql|asyncmy)\b`)
	pyPgImport       = regexp.MustCompile(`(?m)^\s*(?:import|from)\s+(psycopg2|psycopg|asyncpg|pg8000)\b`)
	pyRunTarget      = regexp.MustCompile(`uvicorn\.run\(\s*["']([A-Za-z_][\w.]*:[A-Za-z_]\w*)["']`)

	pyImport = map[string]*regexp.Regexp{
		domain.FrameworkFlask:     regexp.MustCompile(`(?m)^\s*(?:from\s+flask\b|import\s+flask\b)`),
		domain.FrameworkFastAPI:   regexp.MustCompile(`(?m)^\s*(?:from\s+fastapi\b|import\s+fastapi\b)`),
		domain.FrameworkStarlette: regexp.MustCompile(`(?m)^\s*(?:from\s+starlette\b|import\s+starlette\b)`),
		domain.FrameworkQuart:     regexp.MustCompile(`(?m)^\s*(?:from\s+quart\b|import\s+quart\b)`),
		domain.FrameworkLitestar:  regexp.MustCompile(`(?m)^\s*(?:from\s+litestar\b|import\s+litestar\b)`),
		domain.FrameworkFalcon:    regexp.MustCompile(`(?m)^\s*(?:from\s+falcon\b|import\s+falcon\b)`),
		"sanic":                   regexp.MustCompile(`(?m)^\s*(?:from\s+sanic\b|import\s+sanic\b)`),
	}

	// Variáveis de ambiente lidas por código Python (HEU-09 para Python).
	pyEnvRequired = regexp.MustCompile(`\benviron\[\s*["']([A-Z_][A-Z0-9_]*)["']\s*\]`)
	pyEnvOptional = []*regexp.Regexp{
		regexp.MustCompile(`\benviron\.get\(\s*["']([A-Z_][A-Z0-9_]*)["']`),
		regexp.MustCompile(`\bgetenv\(\s*["']([A-Z_][A-Z0-9_]*)["']`),
		// django-environ (env("X"), env.str("X"), env.db("X")...) e python-decouple (config("X")).
		regexp.MustCompile(`\benv(?:\.(?:str|bool|int|float|list|tuple|dict|json|url|db|db_url|cache|email|path))?\(\s*["']([A-Z_][A-Z0-9_]*)["']`),
		regexp.MustCompile(`\bconfig\(\s*["']([A-Z_][A-Z0-9_]*)["']`),
	}

	djangoSettingsDefault = regexp.MustCompile(`DJANGO_SETTINGS_MODULE["']\s*,\s*["']([A-Za-z_][\w.]*)["']`)
	djangoWSGI            = regexp.MustCompile(`(?m)^WSGI_APPLICATION\s*=\s*["']([A-Za-z_][\w.]*)\.([A-Za-z_]\w*)["']`)
	djangoASGI            = regexp.MustCompile(`(?m)^ASGI_APPLICATION\s*=\s*["']([A-Za-z_][\w.]*)\.([A-Za-z_]\w*)["']`)
	djangoStaticRoot      = regexp.MustCompile(`(?m)^\s*STATIC_ROOT\s*=`)
	djangoDebugTrue       = regexp.MustCompile(`(?m)^DEBUG\s*=\s*True\b`)
	djangoSecretLiteral   = regexp.MustCompile(`(?m)^SECRET_KEY\s*=\s*["']`)
	// SECRET_KEY = os.environ.get('SECRET_KEY', 'literal') e config('SECRET_KEY', default='literal'):
	// a falta da variável não falha, e a chave que vale é a do repositório.
	djangoSecretDefault = regexp.MustCompile(`(?m)^\s*SECRET_KEY\s*=\s*[\w.]+\(\s*["']([A-Z_][A-Z0-9_]*)["']\s*,\s*(?:default\s*=\s*)?["'][^"'\n]+["']`)
	djangoCSRFLiteral   = regexp.MustCompile(`(?m)^\s*CSRF_TRUSTED_ORIGINS\s*=\s*\[([^\]]*)\]`)
	djangoRootURLConf   = regexp.MustCompile(`(?m)^\s*ROOT_URLCONF\s*=\s*["']([A-Za-z_][\w.]*)["']`)
	djangoMediaStatic   = regexp.MustCompile(`\bstatic\(\s*(?:settings\.)?MEDIA_URL\b`)
	djangoSecretEnv     = regexp.MustCompile(`(?m)^SECRET_KEY\s*=.*?["']([A-Z_][A-Z0-9_]*)["']`)
	djangoHostsLiteral  = regexp.MustCompile(`(?m)^ALLOWED_HOSTS\s*=\s*\[([^\]]*)\]`)
	djangoHostsEnv      = regexp.MustCompile(`(?m)^ALLOWED_HOSTS\s*=.*?["']([A-Z_][A-Z0-9_]*)["']`)
	djangoCSRFEnv       = regexp.MustCompile(`(?m)^CSRF_TRUSTED_ORIGINS\s*=.*?["']([A-Z_][A-Z0-9_]*)["']`)
	djangoCSRF          = regexp.MustCompile(`(?m)^CSRF_TRUSTED_ORIGINS\s*=`)
	djangoWhitenoise    = regexp.MustCompile(`whitenoise\.middleware\.WhiteNoiseMiddleware`)
	djangoSQLite        = regexp.MustCompile(`django\.db\.backends\.sqlite3`)
	djangoPostgres      = regexp.MustCompile(`django\.db\.backends\.postgresql`)
	djangoMySQL         = regexp.MustCompile(`django\.db\.backends\.mysql`)
	djangoMediaRoot     = regexp.MustCompile(`(?m)^\s*MEDIA_ROOT\s*=`)
	djangoMediaDir      = regexp.MustCompile(`(?m)^\s*MEDIA_ROOT\s*=.*?["']([\w./-]+)["']`)
	djangoHostsExtended = regexp.MustCompile(`(?m)^\s*ALLOWED_HOSTS\s*(?:\+=|\.append\(|\.extend\()`)
	pyStringValue       = regexp.MustCompile(`["']([^"']*)["']`)
)

// normalizePy normaliza o nome de um pacote (PEP 503): minúsculas e um hífen.
func normalizePy(name string) string {
	return pepNormalize.ReplaceAllString(strings.ToLower(name), "-")
}

func extractPythonManifest(fsys fs.FS, x *Extraction) error {
	for _, lock := range []struct{ file, manager string }{
		{"uv.lock", domain.ManagerUV}, {"poetry.lock", "poetry"}, {"Pipfile.lock", "pipenv"}, {"Pipfile", "pipenv"}, {"pdm.lock", "pdm"}, {"environment.yml", "conda"},
	} {
		if exists(fsys, lock.file) {
			x.emit(extractorPyManifest, domain.Evidence{Kind: kindPyLock, Name: lock.manager, Source: lock.file, Scope: domain.ScopeBuild})
		}
	}
	if exists(fsys, "requirements.txt") {
		x.emit(extractorPyManifest, domain.Evidence{Kind: kindPyManifest, Name: "requirements.txt", Source: "requirements.txt", Scope: domain.ScopeBuild})
		readRequirements(fsys, x, "requirements.txt", 0, map[string]bool{})
	}
	if data, state, err := readSmallFile(fsys, "pyproject.toml"); err == nil && state == fileOK {
		x.emit(extractorPyManifest, domain.Evidence{Kind: kindPyManifest, Name: "pyproject.toml", Source: "pyproject.toml", Scope: domain.ScopeBuild})
		readPyproject(x, data)
	}
	if exists(fsys, "setup.py") {
		x.emit(extractorPyManifest, domain.Evidence{Kind: kindPyManifest, Name: "setup.py", Source: "setup.py", Scope: domain.ScopeBuild})
	}
	for _, f := range []string{".python-version", "runtime.txt"} {
		if data, state, err := readSmallFile(fsys, f); err == nil && state == fileOK {
			if v := strings.TrimSpace(strings.SplitN(string(data), "\n", 2)[0]); v != "" {
				x.emit(extractorPyManifest, domain.Evidence{Kind: kindPyVersion, Name: f, Value: v, Source: f, Line: 1, Scope: domain.ScopeBuild})
			}
		}
	}
	if data, state, err := readSmallFile(fsys, "Procfile"); err == nil && state == fileOK {
		for i, line := range strings.Split(string(data), "\n") {
			if m := procfileLine.FindStringSubmatch(strings.TrimSpace(line)); m != nil && !strings.HasPrefix(strings.TrimSpace(line), "#") {
				x.emit(extractorPyManifest, domain.Evidence{Kind: kindPyProcfile, Name: m[1], Value: m[2], Source: "Procfile", Line: i + 1, Scope: domain.ScopeRuntime})
			}
		}
	}
	// Migrações do SQLAlchemy: o Alembic com a configuração na raiz (DEC-45).
	if exists(fsys, "alembic.ini") {
		x.emit(extractorPyManifest, domain.Evidence{Kind: kindPyManage, Name: "alembic upgrade", Value: "alembic upgrade head", Source: "alembic.ini", Scope: domain.ScopeRuntime})
	}
	for _, name := range []string{"Dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if exists(fsys, name) {
			x.emit(extractorPyManifest, domain.Evidence{Kind: kindDockerArtifact, Name: name, Source: name, Scope: domain.ScopeUnknown})
		}
	}
	return nil
}

// readRequirements lê um requirements.txt, seguindo -r para arquivos do próprio
// repositório (no máximo 4 níveis, sem repetir).
func readRequirements(fsys fs.FS, x *Extraction, file string, depth int, seen map[string]bool) {
	if depth > 4 || seen[file] {
		return
	}
	seen[file] = true
	data, state, err := readSmallFile(fsys, file)
	if err != nil || state != fileOK {
		if state == fileTooLarge {
			x.gap(file, "too_large")
		}
		return
	}
	dir := path.Dir(file)
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), maxFileSize)
	n, carry, start := 0, "", 0
	for sc.Scan() {
		n++
		line := sc.Text()
		if carry == "" {
			start = n
		}
		if strings.HasSuffix(line, "\\") {
			carry += strings.TrimSuffix(line, "\\") + " "
			continue
		}
		line = strings.TrimSpace(carry + line)
		carry = ""
		if i := strings.Index(line, " #"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ev := func(kind, name, value string) {
			x.emit(extractorPyManifest, domain.Evidence{Kind: kind, Name: name, Value: value, Source: file, Line: start, Scope: domain.ScopeBuild})
		}
		fields := strings.Fields(line)
		switch opt := fields[0]; {
		case opt == "-r" || opt == "--requirement" || strings.HasPrefix(opt, "-r") && len(opt) > 2:
			target := strings.TrimPrefix(opt, "-r")
			if target == "" && len(fields) > 1 {
				target = fields[1]
			}
			child := path.Clean(path.Join(dir, target))
			if strings.HasPrefix(child, "..") {
				ev(kindPySpecial, "editable_outside", line)
				continue
			}
			readRequirements(fsys, x, child, depth+1, seen)
		case opt == "-c" || opt == "--constraint":
			continue
		case opt == "-e" || opt == "--editable":
			target := ""
			if len(fields) > 1 {
				target = fields[1]
			}
			if strings.HasPrefix(target, ".") && strings.HasPrefix(path.Clean(path.Join(dir, target)), "..") {
				ev(kindPySpecial, "editable_outside", line)
			} else if strings.Contains(target, "://") || strings.HasPrefix(target, "git+") {
				ev(kindPySpecial, "vcs", line)
			}
		case opt == "-i" || opt == "--index-url" || opt == "--extra-index-url" || strings.HasPrefix(opt, "--index-url=") || strings.HasPrefix(opt, "--extra-index-url="):
			// Credencial na URL (usuario:senha@ ou ${VAR}) é um índice privado: não suportado.
			if strings.Contains(line, "@") || strings.Contains(line, "${") {
				ev(kindPySpecial, "private_index", "(índice com credencial)")
			} else {
				ev(kindPySpecial, "index", line)
			}
		case strings.HasPrefix(opt, "-"):
			continue
		case strings.Contains(line, "://") || strings.HasPrefix(line, "git+"):
			ev(kindPySpecial, "vcs", line)
		case strings.HasPrefix(line, ".") || strings.HasPrefix(line, "/"):
			if strings.HasPrefix(path.Clean(path.Join(dir, fields[0])), "..") || strings.HasPrefix(line, "/") {
				ev(kindPySpecial, "editable_outside", line)
			}
		default:
			m := pepName.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			name := normalizePy(m[1])
			ev(kindPyDependency, name, line)
			if !strings.Contains(line, "==") {
				ev(kindPyUnpinned, name, line)
			}
		}
	}
}

// readPyproject lê o que importa do pyproject.toml: as dependências do projeto,
// o requires-python e se é um projeto Poetry.
func readPyproject(x *Extraction, data []byte) {
	var doc struct {
		Project struct {
			Dependencies   []string `toml:"dependencies"`
			RequiresPython string   `toml:"requires-python"`
		} `toml:"project"`
		Tool map[string]any `toml:"tool"`
	}
	if err := toml.Unmarshal(data, &doc); err != nil {
		x.gap("pyproject.toml", "invalid_toml")
		return
	}
	for _, dep := range doc.Project.Dependencies {
		if m := pepName.FindStringSubmatch(dep); m != nil {
			x.emit(extractorPyManifest, domain.Evidence{Kind: kindPyDependency, Name: normalizePy(m[1]), Value: dep, Source: "pyproject.toml", Scope: domain.ScopeBuild})
		}
	}
	if v := strings.TrimSpace(doc.Project.RequiresPython); v != "" {
		x.emit(extractorPyManifest, domain.Evidence{Kind: kindPyVersion, Name: "requires-python", Value: v, Source: "pyproject.toml", Scope: domain.ScopeBuild})
	}
	if _, ok := doc.Tool["poetry"]; ok {
		x.emit(extractorPyManifest, domain.Evidence{Kind: kindPyLock, Name: "poetry", Source: "pyproject.toml", Scope: domain.ScopeBuild})
	}
}

// pyModule converte um caminho de arquivo num módulo importável a partir da raiz
// (ou de src/, no layout src): "app/main.py" → "app.main"; "pkg/__init__.py" → "pkg".
func pyModule(file string, srcLayout bool) string {
	mod := strings.TrimSuffix(file, ".py")
	mod = strings.TrimSuffix(mod, "/__init__")
	if srcLayout && strings.HasPrefix(mod, "src/") {
		mod = strings.TrimPrefix(mod, "src/")
	}
	return strings.ReplaceAll(mod, "/", ".")
}

func validModulePath(file string) bool {
	for _, seg := range strings.Split(strings.TrimSuffix(file, ".py"), "/") {
		if seg == "" || !pyIdent.MatchString(seg) {
			return false
		}
	}
	return true
}

var pyIdent = regexp.MustCompile(`^[A-Za-z_]\w*$`)

func extractPythonSource(fsys fs.FS, x *Extraction) error {
	// Layout src: uma pasta src/ sem __init__.py, com pacotes dentro.
	srcLayout := false
	if info, err := fs.Stat(fsys, "src"); err == nil && info.IsDir() && !exists(fsys, "src/__init__.py") {
		srcLayout = true
	}
	files := 0
	var pyFiles []string
	_ = fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if name != "." && (pySkippedDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if path.Ext(name) != ".py" || d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		files++
		if files > maxPyFiles {
			x.gap(name, "too_many_files")
			return fs.SkipAll
		}
		pyFiles = append(pyFiles, name)
		return nil
	})
	sort.Strings(pyFiles)
	usedSrc := false
	for _, name := range pyFiles {
		data, state, err := readSmallFile(fsys, name)
		if err != nil || state != fileOK {
			if state == fileTooLarge {
				x.gap(name, "too_large")
			}
			continue
		}
		x.filesRead++
		scope := classifyScope(name)
		if isPyTestFile(name) {
			scope = domain.ScopeTest
		}
		// Variáveis de ambiente: obrigatória quando lida sem padrão (environ["X"]).
		for _, m := range pyEnvRequired.FindAllSubmatchIndex(data, -1) {
			x.emit(extractorPySource, domain.Evidence{Kind: kindEnvReference, Name: string(data[m[2]:m[3]]), Value: "required", Source: name, Line: lineAt(data, m[0]), Scope: scope})
		}
		for _, re := range pyEnvOptional {
			for _, m := range re.FindAllSubmatchIndex(data, -1) {
				x.emit(extractorPySource, domain.Evidence{Kind: kindEnvReference, Name: string(data[m[2]:m[3]]), Source: name, Line: lineAt(data, m[0]), Scope: scope})
			}
		}
		if scope != domain.ScopeRuntime || strings.Contains("/"+name, "/migrations/") || !validModulePath(name) {
			continue
		}
		mod := pyModule(name, srcLayout)
		if srcLayout && strings.HasPrefix(name, "src/") {
			usedSrc = true
		}
		imported := map[string]bool{}
		for fw, re := range pyImport {
			if re.Match(data) {
				imported[fw] = true
			}
		}
		for _, m := range pyAppAssign.FindAllSubmatchIndex(data, -1) {
			attr, prefix, ctor := string(data[m[2]:m[3]]), string(data[m[4]:m[5]]), string(data[m[6]:m[7]])
			fw := ""
			switch ctor {
			case "Flask":
				fw = domain.FrameworkFlask
			case "FastAPI":
				fw = domain.FrameworkFastAPI
			case "Starlette":
				fw = domain.FrameworkStarlette
			case "Quart":
				fw = domain.FrameworkQuart
			case "Litestar":
				fw = domain.FrameworkLitestar
			case "Sanic":
				fw = "sanic"
			case "App":
				if strings.HasPrefix(prefix, "falcon.") {
					fw = domain.FrameworkFalcon
					if strings.Contains(prefix, "asgi") {
						fw = "falcon-asgi"
					}
				}
			}
			base := strings.TrimSuffix(fw, "-asgi")
			// Só conta com o import do framework no arquivo (Flask(...) sem import é outra coisa).
			if fw == "" || !imported[base] {
				continue
			}
			x.emit(extractorPySource, domain.Evidence{Kind: kindPyApp, Name: fw, Value: mod + ":" + attr, Source: name, Line: lineAt(data, m[0]), Scope: scope})
		}
		if loc := pyFactory.FindSubmatchIndex(data); loc != nil {
			for _, fw := range []string{domain.FrameworkFlask, domain.FrameworkFastAPI, domain.FrameworkQuart, domain.FrameworkStarlette, domain.FrameworkLitestar} {
				if imported[fw] {
					x.emit(extractorPySource, domain.Evidence{Kind: kindPyFactory, Name: fw, Value: mod + ":" + string(data[loc[2]:loc[3]]) + "()", Source: name, Line: lineAt(data, loc[0]), Scope: scope})
					break
				}
			}
		}
		// O Celery (DEC-51): o app de módulo (`app = Celery(...)`). Em `<pacote>/celery.py` o `-A <pacote>` basta
		// (o Celery procura `<pacote>.celery:app`); em outro arquivo, o alvo é modulo:atributo.
		if m := pyCeleryApp.FindSubmatchIndex(data); m != nil && strings.Contains(string(data), "celery") {
			attr := string(data[m[2]:m[3]])
			target := mod + ":" + attr
			if path.Base(name) == "celery.py" && attr == "app" {
				target = strings.TrimSuffix(mod, ".celery")
			}
			x.emit(extractorPySource, domain.Evidence{Kind: kindPyCelery, Name: "worker", Value: target, Source: name, Line: lineAt(data, m[0]), Scope: scope})
			if loc := pyCeleryBeat.FindIndex(data); loc != nil {
				x.emit(extractorPySource, domain.Evidence{Kind: kindPyCelery, Name: "beat", Value: target, Source: name, Line: lineAt(data, loc[0]), Scope: scope})
			}
		} else if loc := pyCeleryBeat.FindIndex(data); loc != nil && strings.Contains(name, "settings") {
			x.emit(extractorPySource, domain.Evidence{Kind: kindPyCelery, Name: "beat_setting", Source: name, Line: lineAt(data, loc[0]), Scope: scope})
		}
		if m := pyRedisImport.FindSubmatchIndex(data); m != nil {
			x.emit(extractorPySource, domain.Evidence{Kind: kindPyRedis, Name: "import", Value: string(data[m[2]:m[3]]), Source: name, Line: lineAt(data, m[0]), Scope: scope})
		}
		for _, m := range redisURLLiteral.FindAllSubmatchIndex(data, -1) {
			if display, _, ok := redisAddress(string(data[m[2]:m[3]])); ok {
				x.emit(extractorPySource, domain.Evidence{Kind: kindPyRedis, Name: "url", Value: display, Source: name, Line: lineAt(data, m[0]), Scope: scope})
			}
		}
		if m := pyMySQLImport.FindSubmatchIndex(data); m != nil {
			x.emit(extractorPySource, domain.Evidence{Kind: kindPyMySQL, Name: string(data[m[2]:m[3]]), Source: name, Line: lineAt(data, m[0]), Scope: scope})
		}
		if m := pyPgImport.FindSubmatchIndex(data); m != nil {
			x.emit(extractorPySource, domain.Evidence{Kind: kindPyPostgres, Name: string(data[m[2]:m[3]]), Source: name, Line: lineAt(data, m[0]), Scope: scope})
		}
		if m := pyRunTarget.FindSubmatchIndex(data); m != nil {
			x.emit(extractorPySource, domain.Evidence{Kind: kindPyRunTarget, Value: string(data[m[2]:m[3]]), Source: name, Line: lineAt(data, m[0]), Scope: scope})
		}
		// Comandos de gerenciamento do próprio projeto (app/management/commands/x.py).
		if dir, file := path.Split(name); strings.HasSuffix(dir, "/management/commands/") && file != "__init__.py" && !strings.HasPrefix(file, "_") {
			cmd := strings.TrimSuffix(file, ".py")
			x.emit(extractorPySource, domain.Evidence{Kind: kindPyManage, Name: cmd, Value: "python manage.py " + cmd, Source: name, Scope: domain.ScopeRuntime})
		}
	}
	if usedSrc {
		x.emit(extractorPySource, domain.Evidence{Kind: kindPySrcLayout, Source: "src", Scope: domain.ScopeBuild})
	}
	extractDjango(fsys, x, srcLayout)
	return nil
}

func isPyTestFile(name string) bool {
	base := path.Base(name)
	if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py") || base == "conftest.py" || base == "tests.py" {
		return true
	}
	return strings.HasPrefix(name, "tests/") || strings.Contains(name, "/tests/")
}

// extractDjango lê o contrato do Django: o settings padrão do manage.py, o alvo
// WSGI/ASGI e as configurações que quebram a produção (DEC-48 item 9).
func extractDjango(fsys fs.FS, x *Extraction, srcLayout bool) {
	data, state, err := readSmallFile(fsys, "manage.py")
	if err != nil || state != fileOK {
		return
	}
	m := djangoSettingsDefault.FindSubmatchIndex(data)
	if m == nil {
		return
	}
	module := string(data[m[2]:m[3]])
	x.emit(extractorPySource, domain.Evidence{Kind: kindDjangoModule, Value: module, Source: "manage.py", Line: lineAt(data, m[0]), Scope: domain.ScopeRuntime})
	// O collectstatic roda no build: num contêiner descartável ele não teria efeito.
	for _, cmd := range []string{"migrate", "showmigrations", "check --deploy"} {
		x.emit(extractorPySource, domain.Evidence{Kind: kindPyManage, Name: cmd, Value: "python manage.py " + cmd, Source: "manage.py", Scope: domain.ScopeRuntime})
	}

	// O pacote de settings (settings/ com base.py, production.py...): os irmãos viram candidatos.
	toFile := func(mod string) []string {
		p := strings.ReplaceAll(mod, ".", "/")
		out := []string{p + ".py", p + "/__init__.py"}
		if srcLayout {
			out = append(out, "src/"+p+".py", "src/"+p+"/__init__.py")
		}
		return out
	}
	parent := module[:max(0, strings.LastIndex(module, "."))]
	if parent != "" {
		for _, base := range []string{strings.ReplaceAll(parent, ".", "/"), "src/" + strings.ReplaceAll(parent, ".", "/")} {
			entries, err := fs.ReadDir(fsys, base)
			if err != nil || !exists(fsys, base+"/__init__.py") {
				continue
			}
			for _, e := range entries {
				if e.IsDir() || path.Ext(e.Name()) != ".py" || e.Name() == "__init__.py" {
					continue
				}
				cand := parent + "." + strings.TrimSuffix(e.Name(), ".py")
				if cand != module {
					x.emit(extractorPySource, domain.Evidence{Kind: kindDjangoCand, Value: cand, Source: base + "/" + e.Name(), Scope: domain.ScopeRuntime})
				}
			}
		}
	}

	// Lê o settings padrão e, num pacote, todos os módulos dele (o que um importa do outro).
	var files []string
	files = append(files, toFile(module)...)
	if parent != "" && strings.Contains(module, ".settings.") {
		for _, e := range x.of(kindDjangoCand) {
			files = append(files, toFile(e.Value)...)
		}
	}
	emitted := map[string]bool{}
	set := func(name, value, file string, data []byte, off int) {
		key := name + "|" + value
		if emitted[key] {
			return
		}
		emitted[key] = true
		x.emit(extractorPySource, domain.Evidence{Kind: kindDjangoSet, Name: name, Value: value, Source: file, Line: lineAt(data, off), Scope: domain.ScopeRuntime})
	}
	for _, file := range files {
		data, state, err := readSmallFile(fsys, file)
		if err != nil || state != fileOK {
			continue
		}
		if m := djangoWSGI.FindSubmatchIndex(data); m != nil {
			x.emit(extractorPySource, domain.Evidence{Kind: kindDjangoApp, Name: domain.InterfaceWSGI, Value: string(data[m[2]:m[3]]) + ":" + string(data[m[4]:m[5]]), Source: file, Line: lineAt(data, m[0]), Scope: domain.ScopeRuntime})
		}
		if m := djangoASGI.FindSubmatchIndex(data); m != nil {
			x.emit(extractorPySource, domain.Evidence{Kind: kindDjangoApp, Name: domain.InterfaceASGI, Value: string(data[m[2]:m[3]]) + ":" + string(data[m[4]:m[5]]), Source: file, Line: lineAt(data, m[0]), Scope: domain.ScopeRuntime})
		}
		for name, re := range map[string]*regexp.Regexp{"STATIC_ROOT": djangoStaticRoot, "DEBUG_TRUE": djangoDebugTrue, "SECRET_KEY_LITERAL": djangoSecretLiteral, "WHITENOISE": djangoWhitenoise, "CSRF_TRUSTED_ORIGINS": djangoCSRF, "SQLITE": djangoSQLite, "POSTGRES": djangoPostgres, "MYSQL": djangoMySQL, "REDIS_CACHE": djangoRedisCache, "MEDIA_ROOT": djangoMediaRoot, "ALLOWED_HOSTS_EXTENDED": djangoHostsExtended} {
			if loc := re.FindIndex(data); loc != nil {
				value := ""
				if m := djangoMediaDir.FindSubmatch(data); name == "MEDIA_ROOT" && m != nil {
					value = string(m[1])
				}
				set(name, value, file, data, loc[0])
			}
		}
		if m := djangoSecretEnv.FindSubmatchIndex(data); m != nil && djangoSecretLiteral.Find(data) == nil {
			set("SECRET_KEY_ENV", string(data[m[2]:m[3]]), file, data, m[0])
		}
		if m := djangoHostsEnv.FindSubmatchIndex(data); m != nil {
			set("ALLOWED_HOSTS_ENV", string(data[m[2]:m[3]]), file, data, m[0])
		} else if m := djangoHostsLiteral.FindSubmatchIndex(data); m != nil {
			var hosts []string
			for _, h := range pyStringValue.FindAllSubmatch(data[m[2]:m[3]], -1) {
				hosts = append(hosts, string(h[1]))
			}
			set("ALLOWED_HOSTS_LITERAL", strings.Join(hosts, ","), file, data, m[0])
		}
		if m := djangoCSRFEnv.FindSubmatchIndex(data); m != nil {
			set("CSRF_TRUSTED_ORIGINS_ENV", string(data[m[2]:m[3]]), file, data, m[0])
		} else if m := djangoCSRFLiteral.FindSubmatchIndex(data); m != nil {
			var origins []string
			for _, o := range pyStringValue.FindAllSubmatch(data[m[2]:m[3]], -1) {
				origins = append(origins, string(o[1]))
			}
			set("CSRF_TRUSTED_ORIGINS_LITERAL", strings.Join(origins, ","), file, data, m[0])
		}
		if m := djangoSecretDefault.FindSubmatchIndex(data); m != nil {
			set("SECRET_KEY_DEFAULT", string(data[m[2]:m[3]]), file, data, m[0])
		}
		// O módulo de URLs: a mídia servida só por static(MEDIA_URL…).
		if m := djangoRootURLConf.FindSubmatch(data); m != nil {
			urls := strings.ReplaceAll(string(m[1]), ".", "/")
			for _, f := range []string{urls + ".py", urls + "/__init__.py", "src/" + urls + ".py"} {
				ud, st, err := readSmallFile(fsys, f)
				if err != nil || st != fileOK {
					continue
				}
				if loc := djangoMediaStatic.FindIndex(ud); loc != nil {
					set("MEDIA_URLS_STATIC", "", f, ud, loc[0])
				}
				break
			}
		}
	}
}
