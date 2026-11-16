package analyzer

import (
	"bytes"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

const (
	extractorEnvExample = "env-example"
	extractorSource     = "source"
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
	envAssignment  = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)
	envDotAccess   = regexp.MustCompile(`process\.env\.([A-Za-z_][A-Za-z0-9_]*)`)
	envDestructure = regexp.MustCompile(`\{([^{}]*)\}\s*=\s*process\.env\b`)
	envDynamic     = regexp.MustCompile(`process\.env\[`)
	identifier     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// extractEnvExample lê só os nomes de .env.example; valores nunca saem do
// arquivo (RN-15) e nada é executado ou expandido (RN-08).
func extractEnvExample(fsys fs.FS, x *Extraction) error {
	const name = ".env.example"
	data, state, err := readSmallFile(fsys, name)
	if err != nil {
		return err
	}
	switch state {
	case fileMissing:
		return nil
	case fileSymlink:
		x.gap(name, "symlink")
		return nil
	case fileTooLarge:
		x.gap(name, "too_large")
		return nil
	}
	x.filesRead++
	for _, varName := range parseEnvNames(data) {
		x.emit(extractorEnvExample, domain.Evidence{Kind: kindEnvDeclared, Name: varName, Source: name, Scope: domain.ScopeUnknown})
	}
	return nil
}

func parseEnvNames(data []byte) []string {
	var names []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		m := envAssignment.FindStringSubmatch(line)
		if m == nil || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		names = append(names, m[1])
	}
	return names
}

// extractSource procura referências a variáveis de ambiente no código. Acesso
// dinâmico (`process.env[x]`) não pode ser resolvido sem executar o código e
// vira lacuna de cobertura.
func extractSource(fsys fs.FS, x *Extraction) error {
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
	for _, m := range envDotAccess.FindAllSubmatchIndex(data, -1) {
		add(string(data[m[2]:m[3]]), m[0])
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
