package analyzer

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Despacho por ecossistema (DEC-48 item 1, HEU-00): antes das regras, o que a pasta
// é. Só a raiz analisada conta (o diretório do projeto): uma subpasta de outro
// ecossistema é outro projeto.

const ecosystemBoth = "both"

const dispatchVersion = "dispatch-0.1"

// Marcadores de cada ecossistema na raiz.
var (
	jsMarkers     = []string{"package.json"}
	pythonMarkers = []string{"requirements.txt", "pyproject.toml", "uv.lock", "poetry.lock", "Pipfile", "Pipfile.lock", "pdm.lock", "setup.py", "manage.py", "runtime.txt", "environment.yml"}
)

func exists(fsys fs.FS, name string) bool {
	info, err := fs.Lstat(fsys, name)
	return err == nil && info.Mode().IsRegular()
}

// detectEcosystem diz o ecossistema da pasta e os marcadores encontrados. Um
// index.html na raiz só conta como JavaScript (site estático) se nada indica Python.
func detectEcosystem(fsys fs.FS) (string, []string) {
	var js, py []string
	for _, m := range jsMarkers {
		if exists(fsys, m) {
			js = append(js, m)
		}
	}
	for _, m := range pythonMarkers {
		if exists(fsys, m) {
			py = append(py, m)
		}
	}
	switch {
	case len(js) > 0 && len(py) > 0:
		return ecosystemBoth, append(js, py...)
	case len(py) > 0:
		return domain.EcosystemPython, py
	}
	return domain.EcosystemJavaScript, js
}

func validateEcosystem(v string) error {
	if v != domain.EcosystemJavaScript && v != domain.EcosystemPython {
		return fmt.Errorf("%q não é um ecossistema (javascript ou python)", v)
	}
	return nil
}

// ecosystemQuestion é a pergunta F-6: os dois ecossistemas na mesma pasta. A
// resposta é o override `ecosystem`, e a análise roda de novo com o catálogo escolhido.
func ecosystemQuestion(markers []string) domain.AnalysisResult {
	slices.Sort(markers)
	return domain.AnalysisResult{
		Outcome:        domain.AnalysisNeedsInput,
		CatalogVersion: dispatchVersion,
		Rules:          []domain.AppliedRule{{ID: "HEU-00", Version: "0.1", State: string(RuleProposed), Status: "applied"}},
		Fields:         map[string]domain.Field{},
		Pending: []domain.Finding{{
			Rule:  "HEU-00",
			Field: domain.FieldEcosystem,
			Message: "HEU-00: esta pasta tem arquivos de JavaScript e de Python (" + strings.Join(markers, ", ") +
				"). Qual é a aplicação que deve ir ao ar? Se forem duas (um frontend e uma API, por exemplo), crie dois projetos, cada um com o diretório da sua parte.",
			Options: []domain.Option{
				{Value: domain.EcosystemPython, Note: "a aplicação é Python (Django, Flask, FastAPI…); o package.json é de ferramentas"},
				{Value: domain.EcosystemJavaScript, Note: "a aplicação é JavaScript (Node, Bun ou site estático); os arquivos Python são de outra coisa"},
			},
		}},
		Conflicts:             []domain.Finding{},
		Unsupported:           []domain.Finding{},
		ConfigVariables:       []domain.ConfigVariable{},
		Warnings:              []domain.Finding{},
		Evidences:             []domain.Evidence{},
		PersistentSuggestions: []domain.PersistentSuggestion{},
		ServiceNeeds:          []domain.ServiceNeed{},
	}
}

// monorepoResult diz que a raiz não é uma aplicação, mas as subpastas são (F-5): cada
// uma vira um projeto com o próprio diretório.
func monorepoResult(found []domain.Component) domain.AnalysisResult {
	var parts []string
	hasPy, hasJS := false, false
	for _, c := range found {
		label := map[string]string{domain.EcosystemPython: "Python", domain.EcosystemJavaScript: "JavaScript", ecosystemBoth: "JavaScript e Python"}[c.Ecosystem]
		parts = append(parts, c.Dir+"/ ("+label+")")
		hasPy = hasPy || c.Ecosystem != domain.EcosystemJavaScript
		hasJS = hasJS || c.Ecosystem != domain.EcosystemPython
	}
	msg := "HEU-00: a raiz do repositório não tem uma aplicação, mas estas pastas têm: " + strings.Join(parts, ", ") +
		". Crie um projeto para cada uma, com o diretório dela (root_directory)."
	if hasPy && hasJS {
		msg += " Depois, ligue as duas: a URL pública da API numa variável pública do build do frontend (VITE_API_URL, por exemplo) e o domínio do frontend no CORS do backend (e, no Django, em ALLOWED_HOSTS e CSRF_TRUSTED_ORIGINS)."
	}
	return domain.AnalysisResult{
		Outcome:               domain.AnalysisUnsupported,
		CatalogVersion:        dispatchVersion,
		Rules:                 []domain.AppliedRule{{ID: "HEU-00", Version: "0.1", State: string(RuleProposed), Status: "applied"}},
		Fields:                map[string]domain.Field{},
		Pending:               []domain.Finding{},
		Conflicts:             []domain.Finding{},
		Unsupported:           []domain.Finding{{Rule: "HEU-00", Message: msg}},
		ConfigVariables:       []domain.ConfigVariable{},
		Warnings:              []domain.Finding{},
		Evidences:             []domain.Evidence{},
		PersistentSuggestions: []domain.PersistentSuggestion{},
		ServiceNeeds:          []domain.ServiceNeed{},
	}
}
