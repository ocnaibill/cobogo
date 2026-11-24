package analyzer

import (
	"io/fs"
	"net/url"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Survey lê um repositório inteiro antes de criar o projeto (DEC-52 item 2): a
// aplicação da raiz, se há, e cada aplicação irmã, cada uma com a análise da própria
// pasta (papel, perfil, serviços, processos e perguntas). open abre uma subpasta do
// repositório; quem chama garante que ela não sai do clone.
func Survey(root fs.FS, open func(dir string) (fs.FS, error)) ([]domain.StackScanApp, error) {
	res, err := Analyze(root, Input{})
	if err != nil {
		return nil, err
	}
	out := []domain.StackScanApp{}
	// A raiz mistura Python e JavaScript: cada parte que é uma aplicação vira uma proposta
	// com o ecossistema fixado, em vez de uma pergunta a cada deploy (DEC-53 F).
	if mixed := mixedRootApps(root); len(mixed) > 0 {
		out = append(out, mixed...)
		for _, c := range findSiblings(root) {
			sub, err := open(c.Dir)
			if err != nil {
				continue
			}
			sres, err := Analyze(sub, Input{Repo: root, Dir: c.Dir})
			if err != nil {
				continue
			}
			c := c
			out = append(out, surveyApp(c.Dir, sres, &c))
		}
		return out, nil
	}
	var own *domain.Component
	for i, c := range res.Components {
		if c.Dir == "." && !c.Sibling && isAppRole(c.Role) {
			own = &res.Components[i]
			break
		}
	}
	// A raiz só é uma aplicação quando o mapa a reconhece ou a análise resolveu um perfil sem
	// precisar perguntar se instala sem lockfile: um package.json de ferramentas, sem lockfile,
	// não é uma aplicação (DEC-53 H).
	if own != nil || (res.Outcome != domain.AnalysisUnsupported && !asksUnlocked(res)) {
		out = append(out, surveyApp("", res, own))
	}
	// As irmãs vêm da mesma busca do mapa; a raiz vazia (monorepo) não monta o mapa.
	siblings := findSiblings(root)
	for i, c := range siblings {
		sub, err := open(c.Dir)
		if err != nil {
			continue
		}
		sres, err := Analyze(sub, Input{Repo: root, Dir: c.Dir})
		if err != nil {
			continue
		}
		out = append(out, surveyApp(c.Dir, sres, &siblings[i]))
	}
	return out, nil
}

func isAppRole(role string) bool {
	switch role {
	case domain.CompFrontend, domain.CompBackend, domain.CompFullstack, domain.CompApp:
		return true
	}
	return false
}

// surveyApp descreve uma aplicação proposta a partir da análise da pasta dela e da
// peça do mapa que a achou.
func surveyApp(dir string, res domain.AnalysisResult, comp *domain.Component) domain.StackScanApp {
	a := domain.StackScanApp{
		RootDirectory: dir,
		Role:          domain.CompApp,
		Profile:       res.Fields[domain.FieldProfile].Value,
		Evidence:      []string{},
		Components:    []domain.Component{},
		Questions:     []string{},
		Warnings:      len(res.Warnings),
	}
	if comp != nil {
		a.Role, a.Label, a.Ecosystem = comp.Role, comp.Label, comp.Ecosystem
		a.Evidence = append(a.Evidence, comp.Evidence...)
	}
	// A análise da própria pasta sabe mais do que a peça vista de fora.
	for _, c := range res.Components {
		if c.Dir == "." && !c.Sibling && isAppRole(c.Role) {
			a.Role, a.Label = c.Role, c.Label
			if c.Ecosystem != "" {
				a.Ecosystem = c.Ecosystem
			}
		}
	}
	if a.Profile == domain.ProfileStatic {
		a.Role = domain.CompFrontend
		if a.Label == "" {
			a.Label = "site estático"
		}
	}
	for _, c := range res.Components {
		if c.Dir == "." && !c.Sibling && !isAppRole(c.Role) {
			a.Components = append(a.Components, c)
		}
	}
	for _, f := range res.Pending {
		if f.Field != "" {
			a.Questions = append(a.Questions, f.Field)
		}
	}
	a.Variables = res.ConfigVariables
	a.ServiceNeeds = res.ServiceNeeds
	a.APIPaths = APIPaths(res)
	if res.Outcome == domain.AnalysisUnsupported {
		a.Questions = append(a.Questions, "unsupported")
	}
	return a
}

// APIPaths devolve, por variável, o caminho que o código usa como padrão quando a
// variável falta (`VITE_API_URL || 'http://localhost:8000/api'` dá `/api`).
func APIPaths(res domain.AnalysisResult) map[string]string {
	out := map[string]string{}
	for _, e := range res.Evidences {
		if e.Kind != "env.localhost_default" || out[e.Name] != "" {
			continue
		}
		if u, err := url.Parse(e.Value); err == nil && u.Path != "" && u.Path != "/" {
			out[e.Name] = strings.TrimRight(u.Path, "/")
		}
	}
	return out
}

// asksUnlocked diz se a análise ficou esperando a escolha de instalar sem lockfile.
func asksUnlocked(res domain.AnalysisResult) bool {
	for _, f := range res.Pending {
		if f.Field == domain.FieldAllowUnlocked {
			return true
		}
	}
	return false
}

// mixedRootApps propõe as aplicações de uma raiz que mistura Python e JavaScript: uma por
// ecossistema que o mapa reconhece como aplicação. Vazio quando a raiz não é mista ou
// nenhuma das partes é uma aplicação (aí vale a pergunta de ecossistema de sempre).
func mixedRootApps(root fs.FS) []domain.StackScanApp {
	if eco, _ := detectEcosystem(root); eco != ecosystemBoth {
		return nil
	}
	var out []domain.StackScanApp
	for _, eco := range []string{domain.EcosystemPython, domain.EcosystemJavaScript} {
		comp, ok := describeDirAs(root, ".", eco)
		if !ok {
			continue
		}
		res, err := Analyze(root, Input{Overrides: map[string]string{domain.FieldEcosystem: eco}})
		if err != nil {
			continue
		}
		// Uma parte que o catálogo recusa ainda é proposta, para a pessoa ver o motivo.
		app := surveyApp("", res, &comp)
		app.PinnedEcosystem = eco
		app.Ecosystem = eco
		out = append(out, app)
	}
	return out
}
