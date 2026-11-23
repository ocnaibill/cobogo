package analyzer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// O mapa do repositório (DEC-49 itens 1 e 2): cada peça com o papel dela, lida dos
// arquivos, sem executar nada. É inferência: cada peça traz a evidência.

// dirDeps é o que se lê de uma pasta para decidir o papel dela.
type dirDeps struct {
	ecosystem  string // javascript, python ou ""
	deps       map[string]bool
	scripts    map[string]string
	workspaces bool
}

func joinDir(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return dir + "/" + name
}

// readDirDeps lê as dependências declaradas numa pasta (package.json, requirements.txt,
// pyproject.toml). Só a pasta: nada de subpastas.
func readDirDeps(fsys fs.FS, dir string) dirDeps {
	d := dirDeps{deps: map[string]bool{}, scripts: map[string]string{}}
	if data, state, err := readSmallFile(fsys, joinDir(dir, "package.json")); err == nil && state == fileOK {
		var pkg struct {
			Scripts              map[string]string `json:"scripts"`
			Dependencies         map[string]string `json:"dependencies"`
			DevDependencies      map[string]string `json:"devDependencies"`
			OptionalDependencies map[string]string `json:"optionalDependencies"`
			Workspaces           json.RawMessage   `json:"workspaces"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			d.ecosystem = domain.EcosystemJavaScript
			d.scripts = pkg.Scripts
			d.workspaces = len(pkg.Workspaces) > 0 && string(pkg.Workspaces) != "null"
			for _, m := range []map[string]string{pkg.Dependencies, pkg.DevDependencies, pkg.OptionalDependencies} {
				for name := range m {
					d.deps[name] = true
				}
			}
		}
	}
	pyMarker := false
	if data, state, err := readSmallFile(fsys, joinDir(dir, "requirements.txt")); err == nil && state == fileOK {
		pyMarker = true
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(strings.SplitN(line, " #", 2)[0])
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") || strings.Contains(line, "://") {
				continue
			}
			if m := pepName.FindStringSubmatch(line); m != nil {
				d.deps[normalizePy(m[1])] = true
			}
		}
	}
	if data, state, err := readSmallFile(fsys, joinDir(dir, "pyproject.toml")); err == nil && state == fileOK {
		var proj struct {
			Project struct {
				Dependencies []string `toml:"dependencies"`
			} `toml:"project"`
		}
		if toml.Unmarshal(data, &proj) == nil {
			pyMarker = true
			for _, dep := range proj.Project.Dependencies {
				if m := pepName.FindStringSubmatch(dep); m != nil {
					d.deps[normalizePy(m[1])] = true
				}
			}
		}
	}
	if exists(fsys, joinDir(dir, "manage.py")) || exists(fsys, joinDir(dir, "uv.lock")) {
		pyMarker = true
	}
	if pyMarker {
		if d.ecosystem == domain.EcosystemJavaScript {
			d.ecosystem = ecosystemBoth
		} else {
			d.ecosystem = domain.EcosystemPython
		}
	}
	return d
}

func (d dirDeps) has(names ...string) bool {
	for _, n := range names {
		if d.deps[n] {
			return true
		}
	}
	return false
}

func (d dirDeps) first(names ...string) string {
	for _, n := range names {
		if d.deps[n] {
			return n
		}
	}
	return ""
}

var (
	jsServerDeps = []string{"express", "fastify", "koa", "hono", "@nestjs/core", "elysia", "@hapi/hapi", "restify"}
	jsFullDeps   = []string{"next", "nuxt", "@remix-run/node", "@remix-run/react", "@sveltejs/kit", "astro"}
	jsUIDeps     = []string{"react", "vue", "svelte", "solid-js", "preact", "@angular/core", "lit"}
	jsToolDeps   = []string{"vite", "react-scripts", "parcel", "webpack", "@vue/cli-service", "rollup"}
	jsTools      = []string{"vite", "next", "webpack", "react-scripts", "nuxt", "astro", "parcel", "rollup", "tsc", "node ", "bun "}
	pyWebDeps    = []string{"django", "flask", "fastapi", "starlette", "quart", "litestar", "falcon", "sanic"}
	pyLabels     = map[string]string{"django": "Django", "flask": "Flask", "fastapi": "FastAPI", "starlette": "Starlette", "quart": "Quart", "litestar": "Litestar", "falcon": "Falcon", "sanic": "Sanic"}
	jsLabels     = map[string]string{
		"react": "React", "vue": "Vue", "svelte": "Svelte", "solid-js": "Solid", "preact": "Preact", "@angular/core": "Angular", "lit": "Lit",
		"vite": "Vite", "react-scripts": "Create React App", "parcel": "Parcel", "webpack": "Webpack", "@vue/cli-service": "Vue CLI", "rollup": "Rollup",
		"express": "Express", "fastify": "Fastify", "koa": "Koa", "hono": "Hono", "@nestjs/core": "NestJS", "elysia": "Elysia", "@hapi/hapi": "hapi", "restify": "restify",
		"next": "Next.js", "nuxt": "Nuxt", "@remix-run/node": "Remix", "@remix-run/react": "Remix", "@sveltejs/kit": "SvelteKit", "astro": "Astro",
	}
)

func labelsOf(d dirDeps, names ...string) []string {
	var out []string
	for _, n := range names {
		if d.deps[n] {
			l := jsLabels[n]
			if l != "" && !slices.Contains(out, l) {
				out = append(out, l)
			}
		}
	}
	return out
}

// describeDir diz o papel de uma pasta, ou ok=false se ela não parece uma aplicação.
func describeDir(fsys fs.FS, dir string) (domain.Component, bool) {
	d := readDirDeps(fsys, dir)
	c := domain.Component{Dir: dir, Supported: true, Evidence: []string{}}
	switch d.ecosystem {
	case domain.EcosystemJavaScript, ecosystemBoth:
		if d.workspaces {
			return c, false
		}
		hasScript := d.scripts["build"] != "" || d.scripts["start"] != ""
		toolInScript := false
		for _, s := range []string{d.scripts["build"], d.scripts["start"]} {
			for _, t := range jsTools {
				if strings.Contains(s, t) {
					toolInScript = true
				}
			}
		}
		server, full := d.first(jsServerDeps...), d.first(jsFullDeps...)
		ui, tool := d.first(jsUIDeps...), d.first(jsToolDeps...)
		switch {
		case full != "":
			c.Role, c.Label = domain.CompFullstack, strings.Join(labelsOf(d, full), " ")
		case server != "":
			c.Role, c.Label = domain.CompBackend, strings.Join(labelsOf(d, server), " ")
		case ui != "" || tool != "":
			c.Role, c.Label = domain.CompFrontend, strings.Join(labelsOf(d, ui, tool), " + ")
		case hasScript && toolInScript:
			c.Role, c.Label = domain.CompApp, "aplicação JavaScript"
		default:
			return c, false
		}
		if !hasScript && c.Role != domain.CompApp && server == "" {
			return c, false
		}
		c.Ecosystem = domain.EcosystemJavaScript
		c.Evidence = append(c.Evidence, joinDir(dir, "package.json"))
		if d.ecosystem == ecosystemBoth {
			c.Ecosystem = ecosystemBoth
		}
		return c, true
	case domain.EcosystemPython:
		fw := d.first(pyWebDeps...)
		if fw == "" && !exists(fsys, joinDir(dir, "manage.py")) {
			return c, false
		}
		if fw == "" {
			fw = "django"
		}
		c.Ecosystem = domain.EcosystemPython
		c.Label = pyLabels[fw]
		switch {
		case fw == "django" && d.has("djangorestframework", "django-ninja"):
			c.Role, c.Label = domain.CompBackend, "Django REST"
		case fw == "django":
			c.Role = domain.CompFullstack
		default:
			c.Role = domain.CompBackend
		}
		for _, f := range []string{"manage.py", "requirements.txt", "pyproject.toml", "uv.lock"} {
			if exists(fsys, joinDir(dir, f)) {
				c.Evidence = append(c.Evidence, joinDir(dir, f))
				break
			}
		}
		return c, true
	}
	return c, false
}

// Pastas que não são aplicação do repositório.
var siblingSkip = map[string]bool{
	"docs": true, "doc": true, "examples": true, "example": true, "test": true, "tests": true, "e2e": true,
	"scripts": true, "tools": true, "tooling": true, "packages": true, "node_modules": true, "vendor": true,
	"static": true, "media": true, "public": true, "dist": true, "build": true, "assets": true,
}

// findSiblings procura aplicações nas subpastas (até dois níveis), sem entrar numa
// pasta que já é uma aplicação.
func findSiblings(fsys fs.FS) []domain.Component {
	var out []domain.Component
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		entries, err := fs.ReadDir(fsys, dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if !e.IsDir() || strings.HasPrefix(name, ".") || pySkippedDirs[name] || siblingSkip[strings.ToLower(name)] {
				continue
			}
			sub := joinDir(dir, name)
			if c, ok := describeDir(fsys, sub); ok {
				c.Sibling = true
				out = append(out, c)
			} else if depth < 2 {
				walk(sub, depth+1)
			}
			if len(out) >= 6 {
				return
			}
		}
	}
	walk(".", 1)
	return out
}

// Peças de serviço e de processo que o código pede (DEC-49 item 1). O que o Cobogó
// ainda não provê aparece como fora de suporte, com o motivo.
var serviceSignatures = []struct {
	role, label, note string
	pyDeps, jsDeps    []string
}{
	{domain.CompDatabase, "MySQL", "o Cobogó ainda não provê MySQL (DEC-50): use um banco externo nas variáveis de conexão",
		[]string{"mysqlclient", "pymysql", "mysql-connector-python", "aiomysql"}, []string{"mysql2", "mysql"}},
	{domain.CompDatabase, "MongoDB", "o Cobogó ainda não provê MongoDB: use um banco externo",
		[]string{"pymongo", "motor"}, []string{"mongoose", "mongodb"}},
	{domain.CompCache, "Redis", "o Cobogó ainda não provê Redis (DEC-50): use um Redis externo",
		[]string{"redis", "django-redis"}, []string{"ioredis", "redis"}},
	{domain.CompWorker, "Celery", "processos de worker ainda não são suportados (DEC-51)",
		[]string{"celery"}, nil},
	{domain.CompWorker, "RQ", "processos de worker ainda não são suportados (DEC-51)",
		[]string{"rq"}, nil},
	{domain.CompWorker, "BullMQ", "processos de worker ainda não são suportados (DEC-51)",
		nil, []string{"bullmq", "bull"}},
}

// buildComponents monta o mapa: a aplicação desta pasta, os serviços e processos que
// ela pede, e as aplicações irmãs.
func buildComponents(fsys fs.FS, res domain.AnalysisResult) []domain.Component {
	out := []domain.Component{}
	own, ok := describeDir(fsys, ".")
	d := readDirDeps(fsys, ".")
	if ok {
		if res.Fields[domain.FieldProfile].Value == domain.ProfileStatic {
			own.Role = domain.CompFrontend
		}
		out = append(out, own)
	}
	for _, need := range res.ServiceNeeds {
		if need.Engine == domain.ServicePostgres && need.Level == domain.NeedRequired {
			out = append(out, domain.Component{Dir: ".", Role: domain.CompDatabase, Label: "PostgreSQL", Supported: true, Evidence: need.Evidence})
		}
	}
	queue := false
	for _, s := range serviceSignatures {
		if s.role == domain.CompWorker {
			deps := s.pyDeps
			if d.ecosystem == domain.EcosystemJavaScript {
				deps = s.jsDeps
			}
			if d.has(deps...) {
				queue = true
			}
		}
	}
	for _, s := range serviceSignatures {
		deps := s.pyDeps
		if d.ecosystem == domain.EcosystemJavaScript {
			deps = s.jsDeps
		}
		dep := d.first(deps...)
		if dep == "" {
			continue
		}
		role := s.role
		if role == domain.CompCache && queue {
			role = domain.CompQueue
		}
		out = append(out, domain.Component{
			Dir: ".", Role: role, Label: s.label, Supported: false, Note: s.note,
			Evidence: []string{fmt.Sprintf("%s: %s", map[bool]string{true: "package.json", false: "requirements"}[d.ecosystem == domain.EcosystemJavaScript], dep)},
		})
	}
	// O Django com MySQL declarado no settings, mesmo sem o driver nas dependências lidas.
	for _, e := range res.Evidences {
		if e.Kind == kindDjangoSet && e.Name == "MYSQL" && !slices.ContainsFunc(out, func(c domain.Component) bool { return c.Label == "MySQL" }) {
			out = append(out, domain.Component{Dir: ".", Role: domain.CompDatabase, Label: "MySQL", Supported: false,
				Note: serviceSignatures[0].note, Evidence: []string{evidenceRef(e)}})
		}
	}
	return append(out, findSiblings(fsys)...)
}
