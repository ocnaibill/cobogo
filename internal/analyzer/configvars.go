package analyzer

import (
	"bytes"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

const (
	extractorSource = "source"
)

var sourceExtensions = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".jsx": true,
	".ts": true, ".mts": true, ".cts": true, ".tsx": true,
}

var skippedDirs = map[string]bool{"node_modules": true, ".git": true}

var testDirs = map[string]bool{
	"test": true, "tests": true, "__tests__": true, "spec": true, "specs": true,
	"__mocks__": true, "e2e": true, "cypress": true,
}

var (
	envAssignment = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)
	// process.env e Bun.env (DEC-36), com ou sem encadeamento opcional.
	envDotAccess   = regexp.MustCompile(`(?:process|Bun)\.env(?:\?\.|\.)([A-Za-z_][A-Za-z0-9_]*)`)
	envLiteralKey  = regexp.MustCompile("(?:process|Bun)\\.env(?:\\?\\.)?\\[\\s*[\"'`]([A-Za-z_][A-Za-z0-9_]*)[\"'`]\\s*\\]")
	envDestructure = regexp.MustCompile(`\{([^{}]*)\}\s*=\s*(?:process|Bun)\.env\b`)
	// Acesso por chave que não é um nome literal: só executando para saber.
	envDynamic = regexp.MustCompile("(?:process|Bun)\\.env(?:\\?\\.)?\\[\\s*[^\"'`\\s]")

	// import.meta.env.X: configuração de build no Vite e afins (DEC-37).
	importMetaEnv = regexp.MustCompile(`import\.meta\.env\.([A-Za-z_][A-Za-z0-9_]*)`)
	// Variável de build com um endereço local como padrão: VITE_API_URL || 'http://localhost:8000/api'.
	localhostDefault = regexp.MustCompile("(?:import\\.meta\\.env|process\\.env)\\.([A-Za-z_][A-Za-z0-9_]*)\\s*(?:\\|\\||\\?\\?)\\s*[\"'`](https?://(?:localhost|127\\.0\\.0\\.1|0\\.0\\.0\\.0|\\[::1\\])(?::\\d+)?[^\"'`\\s]*)[\"'`]")

	// Servidor do Bun sem porta explícita (DEC-36 item 5).
	bunServeCall  = regexp.MustCompile(`\bBun\.serve\s*\(`)
	defaultExport = regexp.MustCompile(`(?m)^\s*export\s+default\b`)
	portWord      = regexp.MustCompile(`\bport\b`)
	identifier    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

	// Porta escrita no código (DEC-53 B): `.listen(8000)`, `.listen(PORT)` com PORT uma
	// constante numérica do mesmo arquivo, `.listen({ port: 8000 })` e `serve({ port: 8000 })`.
	listenLiteral = regexp.MustCompile(`\.listen\(\s*(\d{2,5})\s*[,)]`)
	listenIdent   = regexp.MustCompile(`\.listen\(\s*([A-Za-z_][A-Za-z0-9_]*)\s*[,)]`)
	listenOptions = regexp.MustCompile(`(?:\.listen|\bserve)\(\s*\{[^{}]*?\bport\s*:\s*(\d{2,5})\b`)
	// Variáveis lidas por um schema que recebe o ambiente inteiro (zod, envalid, t3-env):
	// `schema.parse(process.env)`. As chaves do schema são os nomes (DEC-53 B).
	envPassedWhole = regexp.MustCompile(`\(\s*(?:process|Bun)\.env\s*[,)]`)
	schemaPortKey  = regexp.MustCompile(`(?m)^\s*["']?PORT["']?\s*:`)
)

// numericConst acha `const NAME = 8000` num arquivo e devolve o número.
func numericConst(data []byte, name string) string {
	re := regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:const|let|var)\s+` + regexp.QuoteMeta(name) + `\s*=\s*(\d{2,5})\s*;?\s*(?://.*)?$`)
	if m := re.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

// validPort diz se o texto é uma porta TCP.
func validPort(s string) bool {
	n, err := strconv.Atoi(s)
	return err == nil && n >= 1 && n <= 65535
}

// listenPorts devolve cada porta escrita no código de um arquivo e a linha dela.
func listenPorts(data []byte) map[string]int {
	out := map[string]int{}
	add := func(port string, offset int) {
		if validPort(port) {
			if _, ok := out[port]; !ok {
				out[port] = lineAt(data, offset)
			}
		}
	}
	for _, m := range listenLiteral.FindAllSubmatchIndex(data, -1) {
		add(string(data[m[2]:m[3]]), m[0])
	}
	for _, m := range listenOptions.FindAllSubmatchIndex(data, -1) {
		add(string(data[m[2]:m[3]]), m[0])
	}
	for _, m := range listenIdent.FindAllSubmatchIndex(data, -1) {
		if port := numericConst(data, string(data[m[2]:m[3]])); port != "" {
			add(port, m[0])
		}
	}
	return out
}

// extractSource procura referências a variáveis de ambiente no código. Acesso
// dinâmico (`process.env[x]`) não pode ser resolvido sem executar o código e
// vira lacuna de cobertura.
func extractSource(fsys fs.FS, x *Extraction) error {
	entries := entryFiles(x)
	return fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			x.gap(name, "unreadable")
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !sourceExtensions[strings.ToLower(path.Ext(name))] {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			x.gap(name, "symlink")
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		data, state, err := readSmallFile(fsys, name)
		switch {
		case err != nil:
			x.gap(name, "unreadable")
			return nil
		case state == fileTooLarge:
			x.gap(name, "too_large")
			return nil
		case state != fileOK:
			return nil
		}
		x.filesRead++

		if envDynamic.Match(data) {
			x.gap(name, "dynamic_env_access")
		}
		scope := classifyScope(name)
		for varName, line := range envReferences(data) {
			x.emit(extractorSource, domain.Evidence{Kind: kindEnvReference, Name: varName, Source: name, Line: line, Scope: scope})
		}
		// import.meta.env é resolvido no build, não na execução; os nomes
		// embutidos do Vite não são configuração do projeto.
		for _, m := range importMetaEnv.FindAllSubmatchIndex(data, -1) {
			varName := string(data[m[2]:m[3]])
			if !viteBuiltins[varName] && scope != domain.ScopeTest {
				x.emit(extractorSource, domain.Evidence{Kind: kindEnvReference, Name: varName, Source: name, Line: lineAt(data, m[0]), Scope: domain.ScopeBuild})
			}
		}
		if scope != domain.ScopeTest {
			for _, m := range localhostDefault.FindAllSubmatchIndex(data, -1) {
				x.emit(extractorSource, domain.Evidence{Kind: kindEnvLocalhostDefault, Name: string(data[m[2]:m[3]]), Value: string(data[m[4]:m[5]]), Source: name, Line: lineAt(data, m[0]), Scope: scope})
			}
		}
		if scope == domain.ScopeRuntime {
			for port, line := range listenPorts(data) {
				x.emit(extractorSource, domain.Evidence{Kind: kindListenPort, Name: "listen", Value: port, Source: name, Line: line, Scope: scope})
			}
			if envPassedWhole.Match(data) {
				if loc := schemaPortKey.FindIndex(data); loc != nil {
					x.emit(extractorSource, domain.Evidence{Kind: kindEnvReference, Name: "PORT", Source: name, Line: lineAt(data, loc[0]), Scope: scope})
				}
			}
		}
		if scope == domain.ScopeRuntime && !portWord.Match(data) {
			if loc := bunServeCall.FindIndex(data); loc != nil {
				x.emit(extractorSource, domain.Evidence{Kind: kindBunDefaultPort, Name: "Bun.serve", Source: name, Line: lineAt(data, loc[0]), Scope: scope})
			}
			if loc := defaultExport.FindIndex(data); loc != nil && entries[name] {
				x.emit(extractorSource, domain.Evidence{Kind: kindBunDefaultPort, Name: "export default", Source: name, Line: lineAt(data, loc[0]), Scope: scope})
			}
		}
		return nil
	})
}

// envReferences devolve cada variável referenciada no arquivo e a primeira
// linha em que aparece, por acesso direto ou desestruturação.
func envReferences(data []byte) map[string]int {
	refs := map[string]int{}
	add := func(name string, offset int) {
		line := 1 + bytes.Count(data[:offset], []byte{'\n'})
		if current, ok := refs[name]; !ok || line < current {
			refs[name] = line
		}
	}
	for _, re := range []*regexp.Regexp{envDotAccess, envLiteralKey} {
		for _, m := range re.FindAllSubmatchIndex(data, -1) {
			add(string(data[m[2]:m[3]]), m[0])
		}
	}
	for _, m := range envDestructure.FindAllSubmatchIndex(data, -1) {
		for _, name := range destructuredNames(string(data[m[2]:m[3]])) {
			add(name, m[0])
		}
	}
	return refs
}

// destructuredNames extrai os nomes de `{ A, B: alias, C = 1, ...rest }`.
func destructuredNames(inner string) []string {
	var names []string
	for _, part := range strings.Split(inner, ",") {
		part = strings.TrimSpace(part)
		if part == "" || strings.HasPrefix(part, "...") {
			continue
		}
		part, _, _ = strings.Cut(part, "=")
		part, _, _ = strings.Cut(part, ":")
		if part = strings.TrimSpace(part); identifier.MatchString(part) {
			names = append(names, part)
		}
	}
	return names
}

// classifyScope classifica um arquivo de código pelo caminho. Código não
// reconhecido como teste ou ferramenta de build é runtime por convenção do
// perfil P-NODE, o que é uma inferência (DEC-23).
func classifyScope(name string) domain.Scope {
	segments := strings.Split(strings.ToLower(name), "/")
	base := segments[len(segments)-1]
	for _, dir := range segments[:len(segments)-1] {
		if testDirs[dir] {
			return domain.ScopeTest
		}
	}
	stem := strings.TrimSuffix(base, path.Ext(base))
	switch {
	case stem == "test" || stem == "tests" || stem == "spec" || strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec"):
		return domain.ScopeTest
	case strings.HasSuffix(stem, ".config"):
		return domain.ScopeBuild
	}
	return domain.ScopeRuntime
}

func lineAt(data []byte, offset int) int { return 1 + bytes.Count(data[:offset], []byte{'\n'}) }

// entryFiles devolve os arquivos que o Bun executaria: o do scripts.start
// (`bun [run] [opções] arquivo`) e os campos module e main. O export default
// de um deles é servido pelo próprio Bun (DEC-36 item 5).
func entryFiles(x *Extraction) map[string]bool {
	entries := map[string]bool{}
	addPath := func(p string) {
		p = path.Clean(strings.TrimPrefix(p, "./"))
		if sourceExtensions[strings.ToLower(path.Ext(p))] && !strings.HasPrefix(p, "../") {
			entries[p] = true
		}
	}
	for _, e := range x.of(kindEntryField) {
		addPath(e.Value)
	}
	for _, e := range x.of(kindScript) {
		if e.Name == "start" {
			if file := bunEntry(e.Value); file != "" {
				addPath(file)
			}
		}
	}
	return entries
}

// bunEntry acha o arquivo de `bun [run] [--opções] arquivo`, ignorando
// atribuições de ambiente na frente (NODE_ENV=production bun run x.ts).
func bunEntry(command string) string {
	tokens := strings.Fields(command)
	for len(tokens) > 0 && strings.Contains(tokens[0], "=") {
		tokens = tokens[1:]
	}
	if len(tokens) < 2 || tokens[0] != "bun" {
		return ""
	}
	for _, t := range tokens[1:] {
		switch {
		case t == "run" || strings.HasPrefix(t, "-"):
			continue
		case sourceExtensions[strings.ToLower(path.Ext(t))]:
			return t
		default:
			return ""
		}
	}
	return ""
}

var viteBuiltins = map[string]bool{"MODE": true, "DEV": true, "PROD": true, "SSR": true, "BASE_URL": true}
