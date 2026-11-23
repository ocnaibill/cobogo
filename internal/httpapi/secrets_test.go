package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// importBody monta o corpo de uma importação com o texto escapado em JSON.
func importBody(t *testing.T, text, mode string, names ...string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"text": text, "mode": mode, "names": names})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type previewBody struct {
	Variables []struct {
		Name            string `json:"name"`
		Action          string `json:"action"`
		DefaultSelected bool   `json:"default_selected"`
		Warnings        []struct {
			Code string `json:"code"`
		} `json:"warnings"`
	} `json:"variables"`
	Diagnostics []struct {
		Line int    `json:"line"`
		Code string `json:"code"`
	} `json:"diagnostics"`
}

func (p previewBody) item(name string) (action string, selected bool, codes []string) {
	for _, v := range p.Variables {
		if v.Name == name {
			for _, w := range v.Warnings {
				codes = append(codes, w.Code)
			}
			return v.Action, v.DefaultSelected, codes
		}
	}
	return "", false, nil
}

func TestImportPreviewNeverReturnsValuesAndFlagsRisks(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	path := "/api/v1/projects/" + project + "/env/import"

	const canary = "canario-do-env-colado-5d21"
	if rec := h.do("PUT", h.envPath(project, "EXISTENTE"), `{"value":"antigo"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	text := strings.Join([]string{
		"# comentário",
		"NOVA=" + canary,
		"EXISTENTE=valor-novo",
		"export COM_EXPORT=ok",
		"PORT=9999",
		"VAZIA=",
		"PREENCHIMENTO=changeme",
		"TOKEN_GH=ghp_" + strings.Repeat("a", 36),
		"EXPANDE=$HOME/x",
		"DUP=1",
		"DUP=2",
		"1INVALIDA=x",
		"isto nao e atribuicao " + canary,
	}, "\n")

	rec := h.do("POST", path, importBody(t, text, "preview"))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), canary) || strings.Contains(rec.Body.String(), "ghp_") || strings.Contains(rec.Body.String(), "valor-novo") {
		t.Fatalf("a prévia devolveu um valor: %s", rec.Body)
	}
	var p previewBody
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		action   string
		selected bool
		code     string
	}{
		"NOVA":          {"create", true, ""},
		"COM_EXPORT":    {"create", true, ""},
		"EXISTENTE":     {"replace", false, "replaces_existing"},
		"PORT":          {"blocked", false, "platform"},
		"VAZIA":         {"blocked", false, "empty"},
		"PREENCHIMENTO": {"create", false, "placeholder"},
		"TOKEN_GH":      {"create", true, "credential"},
		"EXPANDE":       {"create", true, "not_expanded"},
		"DUP":           {"create", true, "duplicate"},
	}
	for name, w := range want {
		action, selected, codes := p.item(name)
		if action != w.action || selected != w.selected {
			t.Errorf("%s: action=%q selected=%v, want %q/%v", name, action, selected, w.action, w.selected)
		}
		if w.code != "" && !contains(codes, w.code) {
			t.Errorf("%s: warnings %v, want %q", name, codes, w.code)
		}
	}
	if len(p.Diagnostics) != 2 {
		t.Errorf("diagnostics = %+v, want 2 (invalid name and not an assignment)", p.Diagnostics)
	}

	// A prévia não grava nada.
	if vars, _ := h.st.ListEnvVariables(t.Context(), uuid.MustParse(project)); len(vars) != 1 {
		t.Errorf("a prévia gravou variáveis: %+v", vars)
	}
	if strings.Contains(h.logs.b.String(), canary) {
		t.Error("o log da API contém o valor colado")
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestImportApplyOnlyWritesTheChosenNames(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	pid := uuid.MustParse(project)
	path := "/api/v1/projects/" + project + "/env/import"
	text := "A=1\nB=2\nPORT=3000\nC=\n"

	if rec := h.do("POST", path, importBody(t, text, "apply")); rec.Code != http.StatusBadRequest {
		t.Errorf("apply without names: %d, want 400", rec.Code)
	}
	if rec := h.do("POST", path, importBody(t, "", "preview")); rec.Code != http.StatusBadRequest {
		t.Errorf("empty text: %d, want 400", rec.Code)
	}
	if rec := h.do("POST", path, importBody(t, text, "talvez")); rec.Code != http.StatusBadRequest {
		t.Errorf("bad mode: %d, want 400", rec.Code)
	}
	if vars, _ := h.st.ListEnvVariables(t.Context(), pid); len(vars) != 0 {
		t.Fatalf("pedidos recusados gravaram: %+v", vars)
	}

	rec := h.do("POST", path, importBody(t, text, "apply", "A", "PORT", "C", "NAO_ESTA_NO_TEXTO"))
	if rec.Code != http.StatusOK {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	var res struct {
		Applied []struct {
			Name    string `json:"name"`
			Version int    `json:"version"`
		} `json:"applied"`
		Skipped []struct{ Name, Reason string } `json:"skipped"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) != 1 || res.Applied[0].Name != "A" || res.Applied[0].Version != 1 {
		t.Errorf("applied = %+v, want only A v1", res.Applied)
	}
	reasons := map[string]string{}
	for _, s := range res.Skipped {
		reasons[s.Name] = s.Reason
	}
	if reasons["PORT"] != "blocked" || reasons["C"] != "blocked" || reasons["NAO_ESTA_NO_TEXTO"] != "not_in_text" {
		t.Errorf("skipped = %+v", reasons)
	}
	vars, _ := h.st.ListEnvVariables(t.Context(), pid)
	if len(vars) != 1 || vars[0].Name != "A" || vars[0].Source != domain.EnvSourceImported {
		t.Errorf("variables = %+v, want only A with source imported", vars)
	}

	// Importar de novo uma variável existente cria versão nova e pede novo deploy.
	rec = h.do("POST", path, importBody(t, text, "apply", "A"))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"redeploy_required":true`) {
		t.Errorf("reimport: %d %s", rec.Code, rec.Body)
	}
}

func TestImportRespectsItsLimits(t *testing.T) {
	h := newHarness(t)
	h.login()
	path := "/api/v1/projects/" + h.project() + "/env/import"

	var many strings.Builder
	for i := range 201 {
		many.WriteString("V" + strings.Repeat("X", 1) + string(rune('A'+i%26)) + strings.Repeat("_", i/26) + "=1\n")
	}
	if rec := h.do("POST", path, importBody(t, many.String(), "preview")); rec.Code != http.StatusBadRequest {
		t.Errorf("201 variables: %d, want 400 (%s)", rec.Code, rec.Body)
	}
	if rec := h.do("POST", path, importBody(t, strings.Repeat("A=1\n", 70<<10), "preview")); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("text over 256 KiB: %d, want 413", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/projects/00000000-0000-0000-0000-000000000000/env/import", importBody(t, "A=1", "preview")); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rec.Code)
	}
}

func TestRequiredEnvAndHistory(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	base := "/api/v1/projects/" + project

	if rec := h.do("PUT", base+"/required-env", `{}`); rec.Code != http.StatusBadRequest {
		t.Errorf("missing names: %d, want 400", rec.Code)
	}
	if rec := h.do("PUT", base+"/required-env", `{"names":["PORT"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("platform variable: %d, want 400", rec.Code)
	}
	if rec := h.do("PUT", base+"/required-env", `{"names":["MINHA-VAR"]}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad name: %d, want 400", rec.Code)
	}
	rec := h.do("PUT", base+"/required-env", `{"names":["B","A","B"]}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"names":["A","B"]`) {
		t.Fatalf("put required: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("GET", base+"/required-env", ""); !strings.Contains(rec.Body.String(), `"names":["A","B"]`) {
		t.Errorf("get required: %s", rec.Body)
	}

	// Histórico: digitada, gerada, importada, exigida; nenhum valor.
	const canary = "canario-do-historico-91ab"
	h.do("PUT", h.envPath(project, "API_URL"), `{"value":"`+canary+`"}`)
	h.do("POST", h.envPath(project, "JWT_SECRET")+"/generate", "")
	h.do("POST", base+"/env/import", importBody(t, "API_URL="+canary+"\n", "apply", "API_URL"))
	h.do("DELETE", h.envPath(project, "JWT_SECRET"), "")

	rec = h.do("GET", base+"/env-history", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), canary) {
		t.Fatalf("history: %d %s", rec.Code, rec.Body)
	}
	var hist struct {
		Changes []struct {
			Name    string   `json:"name"`
			Action  string   `json:"action"`
			Version int      `json:"version"`
			Source  string   `json:"source"`
			Actor   string   `json:"actor"`
			Names   []string `json:"names"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &hist); err != nil {
		t.Fatal(err)
	}
	type row struct {
		name, action, source string
		version              int
	}
	var got []row
	for _, c := range hist.Changes {
		if c.Actor != adminLogin {
			t.Errorf("actor = %q", c.Actor)
		}
		got = append(got, row{c.Name, c.Action, c.Source, c.Version})
	}
	// Do mais novo para o mais antigo.
	want := []row{
		{"", "env.import", "", 0},
		{"API_URL", "env.set", "imported", 2},
		{"JWT_SECRET", "env.generate", "generated", 1},
		{"API_URL", "env.set", "user", 1},
		{"", "env.required", "", 0},
	}
	if len(got) != len(want)+1 { // + o env.delete
		t.Fatalf("history = %+v", got)
	}
	got = got[1:] // pula o env.delete (o mais novo)
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("history[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestEnvSuggestionsAreClosedAndNeverApplied(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	pid := uuid.MustParse(project)
	base := "/api/v1/projects/" + project

	// Sem domínio para as aplicações e sem análise: nada a sugerir.
	rec := h.do("GET", base+"/env/suggestions", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"domain_configured":false`) || !strings.Contains(rec.Body.String(), `"suggestions":[]`) {
		t.Fatalf("no domain: %d %s", rec.Code, rec.Body)
	}

	if rec := h.do("PUT", "/api/v1/settings/apps_domain", `{"value":"apps.cobogo.test"}`); rec.Code != http.StatusOK {
		t.Fatalf("set domain: %d %s", rec.Code, rec.Body)
	}
	dep := h.deployment(project, `{"idempotency_key":"s1"}`)
	result := domain.AnalysisResult{Outcome: domain.AnalysisReady, ConfigVariables: []domain.ConfigVariable{
		{Name: "APP_URL", Class: domain.ClassUser},
		{Name: "APP_TESTE_URL", Class: domain.ClassUser}, // <SLUG>_URL de app-teste
		{Name: "DATABASE_URL", Class: domain.ClassUser},  // fora da lista fechada
		{Name: "SITE_URL", Class: domain.ClassUser},      // já definida abaixo
		{Name: "PUBLIC_URL", Class: domain.ClassUser},
	}}
	if _, err := h.leased().SaveAnalysis(t.Context(), dep.ID, pid, "abc123", result); err != nil {
		t.Fatal(err)
	}
	if rec := h.do("PUT", h.envPath(project, "SITE_URL"), `{"value":"https://meu.exemplo"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}

	rec = h.do("GET", base+"/env/suggestions", "")
	var out struct {
		Suggestions []struct{ Name, Value string } `json:"suggestions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range out.Suggestions {
		got[s.Name] = s.Value
	}
	url := "https://app-teste.apps.cobogo.test"
	if len(got) != 3 || got["APP_URL"] != url || got["APP_TESTE_URL"] != url || got["PUBLIC_URL"] != url {
		t.Errorf("suggestions = %+v, want APP_URL, APP_TESTE_URL and PUBLIC_URL with %s", got, url)
	}
	// Oferecer não grava.
	if vars, _ := h.st.ListEnvVariables(t.Context(), pid); len(vars) != 1 {
		t.Errorf("as sugestões gravaram algo: %+v", vars)
	}
}

// DEC-49: as sugestões entre projetos do mesmo repositório (os pares de um monorepo).
func TestPairSuggestions(t *testing.T) {
	h := newHarness(t)
	h.login()
	backend := h.project() // slug app-teste, repositório exemplo/repositorio, raiz
	if rec := h.do("PUT", "/api/v1/settings/apps_domain", `{"value":"apps.cobogo.test"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	rec := h.do("POST", "/api/v1/projects", `{"slug":"app-teste-frontend","repository_url":"https://github.com/exemplo/repositorio","root_directory":"frontend"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create frontend: %d %s", rec.Code, rec.Body)
	}
	frontend := decode[domain.Project](t, rec).ID.String()
	// Um projeto de outro repositório nunca é par.
	if rec := h.do("POST", "/api/v1/projects", `{"slug":"outro","repository_url":"https://github.com/exemplo/outro"}`); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}

	// O backend sabe, pelo mapa dele, que frontend/ é um frontend.
	depB := h.deployment(backend, `{"idempotency_key":"b1"}`)
	resB := domain.AnalysisResult{Outcome: domain.AnalysisReady,
		Components: []domain.Component{
			{Dir: ".", Role: domain.CompBackend, Label: "Django REST"},
			{Dir: "frontend", Role: domain.CompFrontend, Label: "React + Vite", Sibling: true},
		},
		ConfigVariables: []domain.ConfigVariable{
			{Name: "FRONTEND_URL", Class: domain.ClassUser}, {Name: "ALLOWED_HOSTS", Class: domain.ClassUser},
			{Name: "CSRF_TRUSTED_ORIGINS", Class: domain.ClassUser}, {Name: "VITE_API_URL", Class: domain.ClassUser},
		}}
	if _, err := h.leased().SaveAnalysis(t.Context(), depB.ID, uuid.MustParse(backend), "abc", resB); err != nil {
		t.Fatal(err)
	}
	// O frontend foi analisado como frontend, com o padrão de localhost no código.
	depF := h.deployment(frontend, `{"idempotency_key":"f1"}`)
	resF := domain.AnalysisResult{Outcome: domain.AnalysisReady,
		Components:      []domain.Component{{Dir: ".", Role: domain.CompFrontend, Label: "React + Vite"}},
		Evidences:       []domain.Evidence{{Kind: "env.localhost_default", Name: "VITE_API_URL", Value: "http://localhost:8000/api", Source: "src/api.js", Line: 3}},
		ConfigVariables: []domain.ConfigVariable{{Name: "VITE_API_URL", Class: domain.ClassPublic}, {Name: "FRONTEND_URL", Class: domain.ClassUser}},
	}
	if _, err := h.leased().SaveAnalysis(t.Context(), depF.ID, uuid.MustParse(frontend), "abc", resF); err != nil {
		t.Fatal(err)
	}

	get := func(project string) map[string]string {
		rec := h.do("GET", "/api/v1/projects/"+project+"/env/suggestions", "")
		var out struct {
			Suggestions []struct{ Name, Value string } `json:"suggestions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		m := map[string]string{}
		for _, s := range out.Suggestions {
			m[s.Name] = s.Value
		}
		return m
	}
	b := get(backend)
	if b["FRONTEND_URL"] != "https://app-teste-frontend.apps.cobogo.test" {
		t.Errorf("backend FRONTEND_URL = %q (%v)", b["FRONTEND_URL"], b)
	}
	// O endereço do próprio backend já é aplicado pela plataforma ao pedir o deploy
	// (DEC-52 parte B), então não sobra como sugestão.
	if _, ok := b["ALLOWED_HOSTS"]; ok {
		t.Errorf("ALLOWED_HOSTS já é da plataforma: %v", b)
	}
	vals, _, _ := h.st.RevealEnvValues(t.Context(), uuid.MustParse(backend))
	if vals["ALLOWED_HOSTS"] != "app-teste.apps.cobogo.test" || !strings.HasPrefix(vals["CSRF_TRUSTED_ORIGINS"], "https://app-teste.apps.cobogo.test") {
		t.Errorf("backend, valores da plataforma = %q %q", vals["ALLOWED_HOSTS"], vals["CSRF_TRUSTED_ORIGINS"])
	}
	if _, ok := b["VITE_API_URL"]; ok {
		t.Errorf("o par do backend é um frontend: não há API a sugerir: %v", b)
	}
	f := get(frontend)
	if f["VITE_API_URL"] != "https://app-teste.apps.cobogo.test/api" {
		t.Errorf("frontend VITE_API_URL = %q (%v)", f["VITE_API_URL"], f)
	}
	if _, ok := f["FRONTEND_URL"]; ok {
		t.Errorf("o par do frontend é um backend, não há frontend a sugerir: %v", f)
	}
	// Variável já definida não é sugerida.
	if rec := h.do("PUT", h.envPath(frontend, "VITE_API_URL"), `{"value":"https://x.exemplo/api"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if _, ok := get(frontend)["VITE_API_URL"]; ok {
		t.Error("sugestão de uma variável já definida")
	}
}
