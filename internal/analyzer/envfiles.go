package analyzer

import (
	"io/fs"
	"regexp"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

const extractorEnvFiles = "env-files"

// Evidências dos arquivos de ambiente. Nenhuma carrega valor de variável,
// exceto env.default (valor não secreto de arquivo de exemplo).
const (
	kindEnvExampleFile = "env.example_file" // um arquivo de exemplo foi lido
	kindEnvDefault     = "env.default"      // valor padrão não secreto
	kindEnvExposure    = "env.exposure"     // Value = tipo da exposição, nunca o valor
)

const exposureCommittedFile = "committed_env_file"

var (
	envFileName   = regexp.MustCompile(`^\.env(\..+)?$`)
	commentedVar  = regexp.MustCompile(`^(?:export\s+)?([A-Z][A-Z0-9_]*)\s*=(.*)$`)
	exampleSuffix = []string{".example", ".sample", ".template"}
)

func isExampleEnvFile(name string) bool {
	for _, suffix := range exampleSuffix {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

type envLine struct {
	Name      string
	Value     string
	Line      int
	Commented bool
}

// parseEnvLines lê atribuições NAME=valor sem executar nem expandir nada
// (RN-08). Linhas comentadas só contam no formato `# NOME_EM_MAIUSCULAS=`;
// prosa em comentário é ignorada.
func parseEnvLines(data []byte) []envLine {
	var out []envLine
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "#") {
			body := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if m := commentedVar.FindStringSubmatch(body); m != nil {
				out = append(out, envLine{Name: m[1], Value: cleanEnvValue(m[2]), Line: i + 1, Commented: true})
			}
			continue
		}
		if m := envAssignment.FindStringSubmatch(line); m != nil {
			out = append(out, envLine{Name: m[1], Value: cleanEnvValue(line[len(m[0]):]), Line: i + 1})
		}
	}
	return out
}

// cleanEnvValue tira aspas externas e comentário final de valor sem aspas.
func cleanEnvValue(raw string) string {
	v := strings.TrimSpace(raw)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
		if end := strings.IndexByte(v[1:], v[0]); end >= 0 {
			return v[1 : end+1]
		}
		return strings.Trim(v, `"'`)
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// extractEnvFiles lê, na raiz, os arquivos de exemplo (inventário e estado de
// cada variável) e os arquivos de ambiente reais (só para avisar exposição).
// Os valores são lidos em memória para classificar e descartados: só o tipo
// da exposição e, para valores padrão não secretos, o padrão saem daqui
// (DEC-24 itens 5 e 8). Valores de arquivo real nunca são aproveitados.
func extractEnvFiles(fsys fs.FS, x *Extraction) error {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !envFileName.MatchString(name) {
			continue
		}
		data, state, err := readSmallFile(fsys, name)
		if err != nil {
			return err
		}
		switch state {
		case fileMissing:
			continue
		case fileSymlink:
			x.gap(name, "symlink")
			continue
		case fileTooLarge:
			x.gap(name, "too_large")
			continue
		}
		x.filesRead++

		example := isExampleEnvFile(name)
		if example {
			x.emit(extractorEnvFiles, domain.Evidence{Kind: kindEnvExampleFile, Name: name, Source: name, Scope: domain.ScopeUnknown})
		} else {
			x.emit(extractorEnvFiles, domain.Evidence{Kind: kindEnvExposure, Name: name, Value: exposureCommittedFile, Source: name, Scope: domain.ScopeUnknown})
		}

		for _, l := range parseEnvLines(data) {
			kind := credentialKind(l.Name, l.Value)
			if kind != "" {
				x.emit(extractorEnvFiles, domain.Evidence{Kind: kindEnvExposure, Name: l.Name, Value: kind, Source: name, Line: l.Line, Scope: domain.ScopeUnknown})
			}
			if !example {
				continue
			}
			state := exampleState(l.Name, l.Value, l.Commented)
			x.emit(extractorEnvFiles, domain.Evidence{Kind: kindEnvDeclared, Name: l.Name, Value: string(state), Source: name, Line: l.Line, Scope: domain.ScopeUnknown})
			if state == domain.ExampleDefault {
				x.emit(extractorEnvFiles, domain.Evidence{Kind: kindEnvDefault, Name: l.Name, Value: l.Value, Source: name, Line: l.Line, Scope: domain.ScopeUnknown})
			}
		}
	}
	return nil
}
