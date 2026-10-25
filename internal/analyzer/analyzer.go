// Package analyzer implementa o catálogo inicial de heurísticas (§11.2) do
// perfil P-NODE: HEU-01 (package.json + lockfile npm), HEU-02
// (scripts.start), HEU-05 (contrato de porta) e HEU-09 (descoberta de
// variáveis de configuração, só a camada `requires`: ver ficha em
// docs/MOTOR-DE-HEURISTICAS.md §6.2). Não executa nenhum código do
// repositório (RN-08) — apenas lê arquivos e grifos textuais.
package analyzer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/planner"
)

// RulesVersion identifica a versão do catálogo usado nesta análise (RF-02,
// RNF-01: mesmas regras e arquivos produzem o mesmo plano normalizado).
const RulesVersion = "heuristics-m1-2026.10"

const maxSourceFileSize = 1 << 20 // 1 MiB; arquivos maiores não são lidos.

var sourceExtensions = map[string]bool{
	".js": true, ".mjs": true, ".cjs": true, ".ts": true,
}

var skippedDirs = map[string]bool{
	"node_modules": true, ".git": true,
}

type packageJSON struct {
	Scripts    map[string]string `json:"scripts"`
	Workspaces json.RawMessage   `json:"workspaces"`
}

// Analyze aplica o catálogo P-NODE sobre o diretório raiz do projeto.
// portOverride é o valor explícito do solicitante (RN-03), quando houver.
func Analyze(rootDir string, portOverride *int) (domain.AnalysisResult, error) {
	result := domain.AnalysisResult{RulesVersion: RulesVersion}

	pkgPath := filepath.Join(rootDir, "package.json")
	raw, err := os.ReadFile(pkgPath)
	if err != nil {
		if os.IsNotExist(err) {
			result.Outcome = domain.AnalysisUnsupported
			result.Conflicts = append(result.Conflicts, "package.json ausente no diretório raiz; perfil P-NODE não se aplica")
			return result, nil
		}
		return result, fmt.Errorf("analyzer: ler package.json: %w", err)
	}

	var pkg packageJSON
	if err := json.Unmarshal(raw, &pkg); err != nil {
		result.Outcome = domain.AnalysisUnsupported
		result.Conflicts = append(result.Conflicts, fmt.Sprintf("package.json inválido: %v", err))
		return result, nil
	}

	hasNpmLock := fileExists(filepath.Join(rootDir, "package-lock.json"))
	hasYarnLock := fileExists(filepath.Join(rootDir, "yarn.lock"))
	hasPnpmLock := fileExists(filepath.Join(rootDir, "pnpm-lock.yaml"))

	switch {
	case !hasNpmLock:
		result.Outcome = domain.AnalysisUnsupported
		result.Conflicts = append(result.Conflicts, "HEU-01: package-lock.json ausente; sem lockfile npm coerente não é possível fixar dependências")
	case hasYarnLock || hasPnpmLock:
		result.Outcome = domain.AnalysisUnsupported
		result.Conflicts = append(result.Conflicts, "HEU-01: múltiplos lockfiles detectados (npm + yarn/pnpm); perfil P-NODE exige gerenciador único")
	default:
		result.Evidences = append(result.Evidences, domain.Evidence{
			Rule:        "HEU-01",
			Description: "package.json e package-lock.json coerentes com npm",
			Source:      "package.json, package-lock.json",
		})
	}

	if len(pkg.Workspaces) > 0 {
		result.Outcome = domain.AnalysisUnsupported
		result.Conflicts = append(result.Conflicts, "HEU-01: workspaces declarado; monorepos ficam fora do perfil P-NODE do D0")
	}

	if result.Outcome == domain.AnalysisUnsupported {
		return result, nil
	}

	startCommand, hasStart := pkg.Scripts["start"]
	startCommand = strings.TrimSpace(startCommand)
	if !hasStart || startCommand == "" {
		result.Pending = append(result.Pending, domain.PendingField{
			Field:  "start_command",
			Reason: "HEU-02: scripts.start ausente; não é permitido assumir um entrypoint arbitrário",
		})
	} else {
		result.Evidences = append(result.Evidences, domain.Evidence{
			Rule:        "HEU-02",
			Description: fmt.Sprintf("scripts.start declara o comando de execução: %q", startCommand),
			Source:      "package.json#scripts.start",
		})
	}

	buildCommand := strings.TrimSpace(pkg.Scripts["build"])

	portEvidenceFound, portSource, err := scanPortContract(rootDir)
	if err != nil {
		return result, err
	}
	if portEvidenceFound {
		result.Evidences = append(result.Evidences, domain.Evidence{
			Rule:        "HEU-05",
			Description: "process.env.PORT referenciado no código-fonte",
			Source:      portSource,
		})
	}

	var port int
	var portOrigin string
	switch {
	case portOverride != nil:
		port = *portOverride
		portOrigin = "override explícito do solicitante (RN-03)"
	case portEvidenceFound:
		result.Pending = append(result.Pending, domain.PendingField{
			Field:  "port",
			Reason: "HEU-05: contrato de PORT detectado, mas o valor numérico não é inferido automaticamente (RN-02); informe a porta",
		})
	default:
		result.Pending = append(result.Pending, domain.PendingField{
			Field:  "port",
			Reason: "nenhum contrato de porta identificado nas evidências suportadas; informe a porta",
		})
	}

	configVars, err := discoverConfigVariables(rootDir)
	if err != nil {
		return result, err
	}
	if len(configVars) > 0 {
		result.ConfigVariables = configVars
		result.Evidences = append(result.Evidences, domain.Evidence{
			Rule:        "HEU-09",
			Description: fmt.Sprintf("%d variável(is) de configuração descoberta(s); obrigatoriedade e exposição ainda não classificadas", len(configVars)),
			Source:      ".env.example, process.env.*",
		})
	}

	if len(result.Pending) > 0 {
		result.Outcome = domain.AnalysisNeedsInput
		return result, nil
	}

	result.Outcome = domain.AnalysisReady
	plan := planner.BuildNodePlan(planner.NodePlanInput{
		StartCommand: startCommand,
		BuildCommand: buildCommand,
		Port:         port,
		PortOrigin:   portOrigin,
	})
	result.Plan = &plan
	return result, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// scanPortContract procura por `process.env.PORT` nos arquivos-fonte do
// projeto, sem executar nenhum deles (RN-08). Primeira ocorrência encontrada
// é suficiente como evidência; a leitura é limitada por tamanho e diretório.
func scanPortContract(rootDir string) (found bool, source string, err error) {
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
		if found {
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
		if bytes.Contains(content, []byte("process.env.PORT")) {
			found = true
			rel, relErr := filepath.Rel(rootDir, path)
			if relErr != nil {
				rel = path
			}
			source = rel
		}
		return nil
	})
	if walkErr != nil {
		return false, "", fmt.Errorf("analyzer: varrer contrato de porta: %w", walkErr)
	}
	return found, source, nil
}
