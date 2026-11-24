package analyzer

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

const extractorManifest = "manifest"

var lockfiles = []struct{ file, manager string }{
	{"package-lock.json", "npm"},
	{"yarn.lock", "yarn"},
	{"pnpm-lock.yaml", "pnpm"},
	{"bun.lock", "bun"},
	{"bun.lockb", "bun"},
}

// extractManifest lê package.json e detecta os lockfiles. Emite todos os
// scripts: start e build alimentam as regras; os demais só servem de opções
// quando o comando de execução precisa ser informado (DEC-24).
func extractManifest(fsys fs.FS, x *Extraction) error {
	for _, lf := range lockfiles {
		if _, err := fs.Lstat(fsys, lf.file); err == nil {
			x.emit(extractorManifest, domain.Evidence{Kind: kindLockfile, Name: lf.manager, Source: lf.file, Scope: domain.ScopeBuild})
		}
	}

	data, state, err := readSmallFile(fsys, "package.json")
	if err != nil {
		return err
	}
	invalid := func(reason string) {
		x.emit(extractorManifest, domain.Evidence{Kind: kindManifestInvalid, Name: "package.json", Value: reason, Source: "package.json", Scope: domain.ScopeUnknown})
	}
	switch state {
	case fileMissing:
		return nil
	case fileSymlink:
		invalid("é um link simbólico")
		return nil
	case fileTooLarge:
		invalid("excede o limite de leitura")
		return nil
	}
	x.filesRead++

	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
		Workspaces      json.RawMessage   `json:"workspaces"`
		Engines         map[string]any    `json:"engines"`
		Module          any               `json:"module"`
		Main            any               `json:"main"`
		PackageManager  any               `json:"packageManager"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		invalid(err.Error())
		return nil
	}

	x.emit(extractorManifest, domain.Evidence{Kind: kindManifest, Source: "package.json", Scope: domain.ScopeUnknown})
	if hasWorkspaces(pkg.Workspaces) {
		x.emit(extractorManifest, domain.Evidence{Kind: kindWorkspaces, Source: "package.json#workspaces", Scope: domain.ScopeUnknown})
	}
	for name, raw := range pkg.Scripts {
		cmd := strings.TrimSpace(raw)
		if cmd == "" {
			continue
		}
		scope := domain.ScopeUnknown
		switch name {
		case "start":
			scope = domain.ScopeRuntime
		case "build":
			scope = domain.ScopeBuild
		}
		x.emit(extractorManifest, domain.Evidence{Kind: kindScript, Name: name, Value: cmd, Source: "package.json#scripts." + name, Scope: scope})
		if name == "start" || name == "build" {
			emitMissingEnvFiles(fsys, x, cmd, "package.json#scripts."+name, scope)
		}
	}

	// Ferramentas de build da geração do webpack 4 (DEC-54 B): lista fechada, porque o
	// contorno do OpenSSL é o que a documentação delas e do webpack indica.
	for _, tool := range legacyBuildTools {
		spec, ok := pkg.Dependencies[tool]
		if !ok {
			spec, ok = pkg.DevDependencies[tool]
		}
		if ok && declaredMajor(spec) > 0 && declaredMajor(spec) < 5 {
			x.emit(extractorManifest, domain.Evidence{Kind: kindLegacyToolchain, Name: tool, Value: spec, Source: "package.json", Scope: domain.ScopeBuild})
		}
	}
	for _, runtime := range []string{"node", "bun"} {
		if v, ok := pkg.Engines[runtime].(string); ok && strings.TrimSpace(v) != "" {
			x.emit(extractorManifest, domain.Evidence{Kind: kindEngines, Name: runtime, Value: strings.TrimSpace(v), Source: "package.json#engines." + runtime, Scope: domain.ScopeBuild})
		}
	}
	// module e main apontam o arquivo de entrada (o bun init cria só module);
	// viram opção de comando, nunca comando escolhido sozinho (DEC-36).
	for _, field := range []struct {
		name  string
		value any
	}{{"module", pkg.Module}, {"main", pkg.Main}} {
		if v, ok := field.value.(string); ok && strings.TrimSpace(v) != "" {
			x.emit(extractorManifest, domain.Evidence{Kind: kindEntryField, Name: field.name, Value: strings.TrimSpace(v), Source: "package.json#" + field.name, Scope: domain.ScopeRuntime})
		}
	}
	if v, ok := pkg.PackageManager.(string); ok && strings.TrimSpace(v) != "" {
		x.emit(extractorManifest, domain.Evidence{Kind: kindPackageManagerField, Name: "packageManager", Value: strings.TrimSpace(v), Source: "package.json#packageManager", Scope: domain.ScopeBuild})
	}
	// Sem nenhum lockfile (DEC-53 H): o gerenciador provável é o declarado em packageManager
	// (Bun) ou o npm, e a evidência diz que não há lockfile. A HEU-01 decide o que fazer com
	// isso (perguntar, recusar ou avisar); as demais regras analisam como se houvesse um,
	// para a pessoa ver todas as perguntas de uma vez.
	if len(x.of(kindLockfile)) == 0 {
		manager := domain.ManagerNPM
		if v, ok := pkg.PackageManager.(string); ok && strings.HasPrefix(strings.TrimSpace(v), "bun@") {
			manager = domain.ManagerBun
		}
		x.emit(extractorManifest, domain.Evidence{Kind: kindLockfile, Name: manager, Value: unlockedMarker, Source: "package.json", Scope: domain.ScopeBuild})
	}
	return extractNvmrc(fsys, x)
}

// unlockedMarker é o Value da evidência de lockfile quando não há lockfile e a pessoa
// escolheu instalar assim.
const unlockedMarker = "unlocked"

// extractNvmrc lê a primeira linha de .nvmrc, que declara a versão de Node
// que a pessoa desenvolvedora usa. Só a linha é guardada, nunca executada.
func extractNvmrc(fsys fs.FS, x *Extraction) error {
	data, state, err := readSmallFile(fsys, ".nvmrc")
	if err != nil {
		return err
	}
	switch state {
	case fileMissing:
		return nil
	case fileSymlink:
		x.gap(".nvmrc", "symlink")
		return nil
	case fileTooLarge:
		x.gap(".nvmrc", "too_large")
		return nil
	}
	x.filesRead++
	first, _, _ := strings.Cut(string(data), "\n")
	if value := strings.TrimSpace(first); value != "" {
		x.emit(extractorManifest, domain.Evidence{Kind: kindNvmrc, Name: ".nvmrc", Value: value, Source: ".nvmrc", Line: 1, Scope: domain.ScopeBuild})
	}
	return nil
}

func hasWorkspaces(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", "[]", "{}":
		return false
	}
	return true
}

// envFileFlags são as opções que apontam um arquivo de variáveis, por ferramenta:
// `node --env-file=x`, `dotenv -e x`, `env-cmd -f x`, `dotenvx run -f x`. A variante
// `--env-file-if-exists` do Node não entra: ela aceita a falta do arquivo.
var envFileFlags = map[string]bool{"--env-file": true, "-e": true, "-f": true, "--file": true, "--env-file-path": true}

// referencedEnvFiles devolve os arquivos de variáveis que um comando manda carregar.
func referencedEnvFiles(command string) []string {
	var out []string
	for _, segment := range commandSegments(command) {
		tokens := strings.Fields(segment)
		for i, t := range tokens {
			flag, value, hasValue := strings.Cut(t, "=")
			if !envFileFlags[flag] {
				continue
			}
			// -e e -f só valem para os carregadores de .env; `node -e` é código.
			if flag == "-e" || flag == "-f" || flag == "--file" {
				if !slices.ContainsFunc(tokens[:i], func(w string) bool {
					return w == "dotenv" || w == "env-cmd" || w == "dotenvx" || strings.HasSuffix(w, "/dotenv") || strings.HasSuffix(w, "/env-cmd")
				}) {
					continue
				}
			}
			if !hasValue && i+1 < len(tokens) {
				value = tokens[i+1]
			}
			value = strings.Trim(value, `"'`)
			if value != "" && !strings.HasPrefix(value, "-") {
				out = append(out, value)
			}
		}
	}
	return out
}

// emitMissingEnvFiles registra, para cada arquivo de variáveis que um script manda
// carregar e que não está no repositório, uma evidência (DEC-53 D). Caminhos que saem
// da pasta ou absolutos não são conferidos. Value é o exemplo vizinho (`.example`,
// `.sample`, `.template`), se houver.
func emitMissingEnvFiles(fsys fs.FS, x *Extraction, command, source string, scope domain.Scope) {
	for _, file := range referencedEnvFiles(command) {
		clean := path.Clean(file)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "$") {
			continue
		}
		if exists(fsys, clean) {
			continue
		}
		example := ""
		for _, suffix := range []string{".example", ".sample", ".template", ".dist"} {
			if exists(fsys, clean+suffix) {
				example = clean + suffix
				break
			}
		}
		x.emit(extractorManifest, domain.Evidence{Kind: kindMissingEnvFile, Name: clean, Value: example, Source: source, Scope: scope})
	}
}

// legacyBuildTools são as ferramentas cuja versão 4 ou menor traz o webpack 4.
var legacyBuildTools = []string{"react-scripts", "@vue/cli-service"}

var firstNumber = regexp.MustCompile(`(\d+)(?:\.|$)`)

// declaredMajor devolve a major do limite inferior de uma faixa do npm (`^4.0.3`, `~3.3.0`,
// `4.0.3`), ou 0 se a faixa não diz (`latest`, `*`).
func declaredMajor(spec string) int {
	m := firstNumber.FindStringSubmatch(spec)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}
