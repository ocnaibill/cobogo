package analyzer

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// HEU-09: descoberta de variáveis de configuração. Só o `requires` é
// implementado aqui — encontrar nomes e suas origens. Classificar
// obrigatoriedade, origem do valor e exposição (o `provides`) pertence ao
// fluxo de configuração guiada do §17.1/TEL-05, que ainda não existe; por
// isso o resultado vira evidência, nunca um PendingField que bloqueie o
// plano (ver ficha HEU-09 em docs/MOTOR-DE-HEURISTICAS.md §6.2).

var envAssignmentPattern = regexp.MustCompile(`^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=`)

var processEnvPattern = regexp.MustCompile(`process\.env\.([A-Za-z_][A-Za-z0-9_]*)`)

// discoverConfigVariables aplica HEU-09: cruza nomes declarados em
// `.env.example` com referências literais `process.env.NOME` no código.
// Nunca executa ou expande o arquivo (RN-08); linhas com sintaxe não
// reconhecida são simplesmente ignoradas, não um erro de análise.
func discoverConfigVariables(rootDir string) ([]domain.ConfigVariable, error) {
	declared, err := parseEnvExample(rootDir)
	if err != nil {
		return nil, err
	}
	referenced, err := scanEnvReferences(rootDir)
	if err != nil {
		return nil, err
	}

	names := make(map[string]struct{}, len(declared)+len(referenced))
	for name := range declared {
		names[name] = struct{}{}
	}
	for name := range referenced {
		names[name] = struct{}{}
	}

	variables := make([]domain.ConfigVariable, 0, len(names))
	for name := range names {
		sources := referenced[name]
		sort.Strings(sources)
		variables = append(variables, domain.ConfigVariable{
			Name:               name,
			DeclaredInExample:  declared[name],
			ReferencedInSource: sources,
		})
	}
	sort.Slice(variables, func(i, j int) bool { return variables[i].Name < variables[j].Name })
	return variables, nil
}

// parseEnvExample lê `.env.example` linha a linha: nome antes do `=`,
// ignorando comentários, linhas vazias e qualquer sintaxe que não seja uma
// atribuição simples (sem expandir `$VAR`, sem interpretar multiline).
func parseEnvExample(rootDir string) (map[string]bool, error) {
	path := filepath.Join(rootDir, ".env.example")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, fmt.Errorf("analyzer: ler .env.example: %w", err)
	}
	defer f.Close()

	names := map[string]bool{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := envAssignmentPattern.FindStringSubmatch(line); m != nil {
			names[m[1]] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("analyzer: ler .env.example: %w", err)
	}
	return names, nil
}

// scanEnvReferences varre os mesmos arquivos-fonte de scanPortContract
// procurando toda referência literal `process.env.NOME`. Acesso dinâmico
// (`process.env[x]`) não é reconhecido — não é possível resolvê-lo sem
// executar o código (RN-08).
func scanEnvReferences(rootDir string) (map[string][]string, error) {
	refs := map[string][]string{}

	walkErr := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !sourceExtensions[filepath.Ext(d.Name())] {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxSourceFileSize {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		matches := processEnvPattern.FindAllSubmatch(content, -1)
		if len(matches) == 0 {
			return nil
		}
		rel, relErr := filepath.Rel(rootDir, path)
		if relErr != nil {
			rel = path
		}
		for _, m := range matches {
			name := string(m[1])
			if !containsString(refs[name], rel) {
				refs[name] = append(refs[name], rel)
			}
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("analyzer: varrer referências de configuração: %w", walkErr)
	}
	return refs, nil
}

func containsString(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}
