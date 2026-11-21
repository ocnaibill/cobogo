package analyzer

import (
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Perfil estático (DEC-37): o que extrair, quais ferramentas geram site
// estático e como decidir entre estático e servidor.

const extractorStatic = "static"

// decisiveDeps são as dependências que decidem a forma de publicar: a
// ferramenta de build estático ou um framework com renderização no servidor.
var decisiveDeps = map[string]bool{
	"react-scripts": true, "@vue/cli-service": true, "astro": true, "next": true, "vite": true,
	"parcel": true, "@sveltejs/kit": true, "nuxt": true, "@remix-run/react": true, "@remix-run/node": true,
	"@angular/core": true, "gatsby": true,
	// node-sass: descontinuado e nativo; não compila nas versões do catálogo.
	"node-sass": true,
}

// ssrDeps só renderizam no servidor (ou exigem adaptador para não fazê-lo).
var ssrDeps = []string{"@sveltejs/kit", "nuxt", "@remix-run/react", "@remix-run/node"}

var (
	configOutDir   = regexp.MustCompile("\\boutDir\\s*:\\s*['\"`]([^'\"`]+)['\"`]")
	astroSSROutput = regexp.MustCompile(`\boutput\s*:\s*['"](server|hybrid)['"]`)
	astroAdapter   = regexp.MustCompile(`\badapter\s*:`)
	nextExport     = regexp.MustCompile(`\boutput\s*:\s*['"]export['"]`)
)

var configFiles = map[string][]string{
	"vite":  {"vite.config.js", "vite.config.mjs", "vite.config.cjs", "vite.config.ts", "vite.config.mts"},
	"astro": {"astro.config.mjs", "astro.config.js", "astro.config.ts", "astro.config.mts"},
	"next":  {"next.config.js", "next.config.mjs", "next.config.cjs", "next.config.ts"},
}

// extractStatic lê o que decide se o projeto é um site estático: index.html
// na raiz, dependências decisivas e trechos literais das configurações do
// Vite, do Astro e do Next. Configuração calculada em código não é avaliada.
func extractStatic(fsys fs.FS, x *Extraction) error {
	// Dockerfile e compose do repositório: o Cobogó não os usa (DEC-37, HEU-08).
	for _, name := range []string{"Dockerfile", "docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if info, err := fs.Lstat(fsys, name); err == nil && info.Mode().IsRegular() {
			x.emit(extractorStatic, domain.Evidence{Kind: kindDockerArtifact, Name: name, Source: name, Scope: domain.ScopeUnknown})
		}
	}
	if info, err := fs.Lstat(fsys, "index.html"); err == nil && info.Mode().IsRegular() {
		x.emit(extractorStatic, domain.Evidence{Kind: kindStaticIndex, Name: "index.html", Source: "index.html", Scope: domain.ScopeRuntime})
	}

	if data, state, err := readSmallFile(fsys, "package.json"); err == nil && state == fileOK {
		var pkg struct {
			Dependencies    map[string]any `json:"dependencies"`
			DevDependencies map[string]any `json:"devDependencies"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			for _, deps := range []map[string]any{pkg.Dependencies, pkg.DevDependencies} {
				for name := range deps {
					if decisiveDeps[name] {
						x.emit(extractorStatic, domain.Evidence{Kind: kindDependency, Name: name, Source: "package.json", Scope: domain.ScopeBuild})
					}
				}
			}
		}
	}

	for tool, files := range configFiles {
		for _, file := range files {
			data, state, err := readSmallFile(fsys, file)
			if err != nil || state != fileOK {
				continue
			}
			x.filesRead++
			if m := configOutDir.FindSubmatchIndex(data); m != nil && tool != "next" {
				if dir := cleanOutDir(string(data[m[2]:m[3]])); dir != "" {
					x.emit(extractorStatic, domain.Evidence{Kind: kindStaticOutDir, Name: tool, Value: dir, Source: file, Line: lineAt(data, m[0]), Scope: domain.ScopeBuild})
				}
			}
			if tool == "astro" {
				if loc := astroSSROutput.FindIndex(data); loc != nil {
					x.emit(extractorStatic, domain.Evidence{Kind: kindSSRMarker, Name: "astro output", Value: strings.Trim(string(data[loc[0]:loc[1]]), " "), Source: file, Line: lineAt(data, loc[0]), Scope: domain.ScopeBuild})
				}
				if loc := astroAdapter.FindIndex(data); loc != nil {
					x.emit(extractorStatic, domain.Evidence{Kind: kindSSRMarker, Name: "astro adapter", Value: "adapter", Source: file, Line: lineAt(data, loc[0]), Scope: domain.ScopeBuild})
				}
			}
			if tool == "next" {
				if loc := nextExport.FindIndex(data); loc != nil {
					x.emit(extractorStatic, domain.Evidence{Kind: kindNextExport, Name: "next export", Source: file, Line: lineAt(data, loc[0]), Scope: domain.ScopeBuild})
				}
			}
		}
	}
	return nil
}

// cleanOutDir aceita só um caminho relativo dentro do projeto.
func cleanOutDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || strings.HasPrefix(dir, "/") || strings.ContainsAny(dir, "\\$`'\"\n\r\x00 ") {
		return ""
	}
	clean := path.Clean(strings.TrimPrefix(dir, "./"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return ""
	}
	return clean
}

// staticToolSpec é a saída padrão de uma ferramenta e se ela costuma gerar
// uma SPA (rota desconhecida → index.html).
type staticToolSpec struct {
	out string
	spa bool
}

var staticTools = map[string]staticToolSpec{
	"vite": {"dist", true}, "react-scripts": {"build", true}, "vue-cli-service": {"dist", true},
	"parcel": {"dist", true}, "bun": {"", true}, "astro": {"dist", false}, "next": {"out", false},
}

// staticBuild acha, no comando de build, o trecho que gera o site estático e
// o diretório de saída dado por opção. O último trecho que casa vale
// ("tsc -b && vite build").
func staticBuild(command string) (tool, flagOut string) {
	seps := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n")
	for _, segment := range strings.Split(seps.Replace(command), "\n") {
		tokens := strings.Fields(segment)
		for len(tokens) > 0 && (strings.Contains(tokens[0], "=") || tokens[0] == "npx" || tokens[0] == "bunx") {
			tokens = tokens[1:]
		}
		if len(tokens) < 2 || tokens[1] != "build" {
			continue
		}
		switch tokens[0] {
		case "vite":
			tool, flagOut = "vite", flagValue(tokens, "--outDir")
		case "react-scripts", "astro", "next":
			tool, flagOut = tokens[0], ""
		case "vue-cli-service":
			tool, flagOut = "vue-cli-service", flagValue(tokens, "--dest")
		case "parcel":
			tool, flagOut = "parcel", flagValue(tokens, "--dist-dir")
		case "bun":
			// bun build com entrada .html gera um site (DEC-37).
			for _, t := range tokens[2:] {
				if strings.HasSuffix(strings.ToLower(t), ".html") {
					tool, flagOut = "bun", flagValue(tokens, "--outdir")
					break
				}
			}
		}
	}
	return tool, cleanOutDir(flagOut)
}

func flagValue(tokens []string, name string) string {
	for i, t := range tokens {
		if t == name && i+1 < len(tokens) {
			return tokens[i+1]
		}
		if v, ok := strings.CutPrefix(t, name+"="); ok {
			return v
		}
	}
	return ""
}

// staticServeTool diz se um start só serve arquivos ou roda um servidor de
// desenvolvimento ou pré-visualização: nesse caso ele não é o servidor de
// produção, e o site pode ser publicado como estático (DEC-37 item 2).
func staticServeTool(command string) bool {
	tokens := strings.Fields(command)
	for len(tokens) > 0 && (strings.Contains(tokens[0], "=") || tokens[0] == "npx" || tokens[0] == "bunx") {
		tokens = tokens[1:]
	}
	if len(tokens) == 0 {
		return false
	}
	switch tokens[0] {
	case "vite", "serve", "http-server", "live-server", "lite-server", "sirv", "webpack-dev-server", "parcel":
		return true
	case "astro":
		return len(tokens) > 1 && (tokens[1] == "dev" || tokens[1] == "preview")
	case "react-scripts":
		return len(tokens) > 1 && tokens[1] == "start"
	case "vue-cli-service":
		return len(tokens) > 1 && tokens[1] == "serve"
	case "webpack":
		return len(tokens) > 1 && tokens[1] == "serve"
	}
	return false
}

// staticDecision é o que a detecção concluiu sobre um site estático.
type staticDecision struct {
	Tool        string
	NoBuild     bool
	OutDir      string
	OutOrigin   domain.Origin
	OutEvidence []domain.Evidence
	SPA         bool
	// Evidence sustenta a escolha do perfil (o build, ou o index.html).
	Evidence []domain.Evidence
}

// detectStatic aplica o item 2 da DEC-37 às evidências.
func detectStatic(in RuleInput) (staticDecision, bool) {
	for _, e := range in.Of(kindDependency) {
		for _, ssr := range ssrDeps {
			if e.Name == ssr {
				return staticDecision{}, false
			}
		}
	}
	if len(in.Of(kindSSRMarker)) > 0 {
		return staticDecision{}, false
	}
	build := in.Named(kindScript, "build")
	start := in.Named(kindScript, "start")
	if len(build) > 0 {
		tool, flagOut := staticBuild(build[0].Value)
		if tool == "" {
			return staticDecision{}, false
		}
		exported := len(in.Of(kindNextExport)) > 0
		if tool == "next" && !exported {
			return staticDecision{}, false
		}
		// Um start que é servidor de verdade entrega o build (P-NODE/P-BUN);
		// com o Next exportado, next start nem funciona, e é ignorado.
		if len(start) > 0 && !staticServeTool(start[0].Value) && !(tool == "next" && exported) {
			return staticDecision{}, false
		}
		d := staticDecision{Tool: tool, SPA: staticTools[tool].spa, Evidence: build}
		switch declared := in.Named(kindStaticOutDir, tool); {
		case flagOut != "":
			d.OutDir, d.OutOrigin, d.OutEvidence = flagOut, domain.OriginDeclaration, build
		case len(declared) > 0:
			d.OutDir, d.OutOrigin, d.OutEvidence = declared[0].Value, domain.OriginDeclaration, declared
		case staticTools[tool].out != "":
			d.OutDir, d.OutOrigin, d.OutEvidence = staticTools[tool].out, domain.OriginInference, build
		}
		return d, true
	}
	index := in.Of(kindStaticIndex)
	if len(index) > 0 && (len(start) == 0 || staticServeTool(start[0].Value)) {
		return staticDecision{NoBuild: true, OutDir: ".", OutOrigin: domain.OriginInference, OutEvidence: index, Evidence: index}, true
	}
	return staticDecision{}, false
}

// jsProfile decide o perfil do catálogo JavaScript: estático (DEC-37), ou o
// perfil de servidor do gerenciador (P-NODE, P-BUN). O override de profile do
// usuário prevalece sobre a detecção. manager é "" quando o gerenciador ainda
// precisa ser escolhido.
func jsProfile(in RuleInput) (profile, manager string, sd staticDecision) {
	sd, static := detectStatic(in)
	switch in.Overrides[domain.FieldProfile] {
	case domain.ProfileStatic:
		if !static {
			// Estático por escolha do usuário, sem ferramenta reconhecida: o
			// diretório de saída vira pergunta (HEU-03).
			if build := in.Named(kindScript, "build"); len(build) > 0 {
				sd = staticDecision{Evidence: build}
			} else {
				sd = staticDecision{NoBuild: true, OutDir: ".", OutOrigin: domain.OriginDefault, Evidence: in.Of(kindStaticIndex)}
			}
		}
		static = true
	case domain.ProfileNode, domain.ProfileBun:
		static = false
	}
	if static && sd.NoBuild {
		return domain.ProfileStatic, domain.ManagerNone, sd
	}
	manager, _ = jsManager(in)
	switch {
	case static:
		return domain.ProfileStatic, manager, sd
	case manager == domain.ManagerBun:
		return domain.ProfileBun, manager, sd
	case manager == domain.ManagerNPM:
		return domain.ProfileNode, manager, sd
	}
	return "", "", sd
}
