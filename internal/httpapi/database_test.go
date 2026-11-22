package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

type dbBody struct {
	Need *struct {
		Level, Variable string
		Evidence        []string
	} `json:"need"`
	Service *struct {
		Mode, Variable, Status string
	} `json:"service"`
	Variable string `json:"variable"`
	Requeued int    `json:"requeued"`
}

// PostgreSQL auxiliar (DEC-42): a escolha, a senha que nunca volta, o vínculo
// protegido e a exclusão confirmada.
func TestChooseDatabaseThroughTheAPI(t *testing.T) {
	h := newHarness(t)
	h.login()
	p := h.createProject("com-banco")
	base := "/api/v1/projects/" + p.ID.String() + "/database"

	var got dbBody
	if rec := h.do("GET", base, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"service":null`) || !strings.Contains(rec.Body.String(), `"need":null`) {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"missing choice":    `{}`,
		"unknown choice":    `{"choice":"talvez"}`,
		"bad variable":      `{"choice":"managed","variable":"MINHA-VAR"}`,
		"platform variable": `{"choice":"managed","variable":"PORT"}`,
		"not json":          `oi`,
	} {
		if rec := h.do("POST", base, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}

	rec := h.do("POST", base, `{"choice":"managed"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("managed: %d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Service == nil || got.Service.Mode != "managed" || got.Service.Variable != "DATABASE_URL" || got.Service.Status != "pending" {
		t.Fatalf("service = %+v", got.Service)
	}

	// A senha gerada não aparece em nenhuma resposta nem no log; a variável é "linked".
	pw := ""
	vals, _, _ := h.st.RevealEnvValues(t.Context(), p.ID)
	if u := vals["DATABASE_URL"]; strings.HasPrefix(u, "postgres://app:") && strings.Contains(u, "@cobogo-pg-") {
		pw = strings.TrimPrefix(strings.SplitN(u, "@", 2)[0], "postgres://app:")
	} else {
		t.Fatalf("connection = %q", u)
	}
	if len(pw) < 20 {
		t.Errorf("password too short: %d", len(pw))
	}
	list := h.do("GET", "/api/v1/projects/"+p.ID.String()+"/env", "")
	if !strings.Contains(list.Body.String(), `"source":"linked"`) || strings.Contains(list.Body.String(), pw) {
		t.Errorf("env list: %s", list.Body)
	}
	for _, path := range []string{base, "/api/v1/projects/" + p.ID.String() + "/env-history"} {
		if rec := h.do("GET", path, ""); strings.Contains(rec.Body.String(), pw) {
			t.Errorf("%s leaks the password", path)
		}
	}
	if strings.Contains(h.logs.b.String(), pw) {
		t.Error("the API log contains the database password")
	}

	// Escolher o gerenciado de novo mantém a senha; outra escolha é recusada.
	h.do("POST", base, `{"choice":"managed"}`)
	if vals2, _, _ := h.st.RevealEnvValues(t.Context(), p.ID); vals2["DATABASE_URL"] != vals["DATABASE_URL"] {
		t.Error("the password changed")
	}
	if rec := h.do("POST", base, `{"choice":"none"}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "managed_exists" {
		t.Errorf("none over managed: %d %s", rec.Code, rec.Body)
	}

	// A variável vinculada não se edita, remove, gera nem importa.
	envPath := h.envPath(p.ID.String(), "DATABASE_URL")
	if rec := h.do("PUT", envPath, `{"value":"x"}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "variable_linked" {
		t.Errorf("PUT linked: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("DELETE", envPath, ""); rec.Code != http.StatusConflict {
		t.Errorf("DELETE linked: %d", rec.Code)
	}
	imp := h.do("POST", "/api/v1/projects/"+p.ID.String()+"/env/import", importBody(t, "DATABASE_URL=postgres://x\n", "preview"))
	if !strings.Contains(imp.Body.String(), `"action":"blocked"`) || !strings.Contains(imp.Body.String(), `"code":"linked"`) {
		t.Errorf("import preview of a linked variable: %s", imp.Body)
	}
	if rec := h.do("POST", "/api/v1/projects/"+p.ID.String()+"/env/import", importBody(t, "DATABASE_URL=postgres://x\n", "apply", "DATABASE_URL")); !strings.Contains(rec.Body.String(), `"reason":"blocked"`) {
		t.Errorf("import apply of a linked variable: %s", rec.Body)
	}

	// Exclusão: confirmação pelo nome do projeto.
	if rec := h.do("DELETE", base, `{"confirm_slug":"outro"}`); rec.Code != http.StatusBadRequest || decode[apiError](t, rec).Error.Code != "confirmation_mismatch" {
		t.Errorf("wrong confirmation: %d %s", rec.Code, rec.Body)
	}
	rec = h.do("DELETE", base, `{"confirm_slug":"com-banco"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if op := decode[domain.Operation](t, rec); op.Kind != domain.OpDeleteService || op.State != domain.OpPending {
		t.Errorf("operation = %+v", op)
	}
	if rec := h.do("DELETE", base, `{"confirm_slug":"com-banco"}`); rec.Code != http.StatusConflict {
		// o serviço já está sendo apagado
		t.Logf("second delete: %d", rec.Code)
	}

	h.cookie = nil
	for _, c := range []struct{ method, body string }{{"GET", ""}, {"POST", `{"choice":"none"}`}, {"DELETE", `{"confirm_slug":"x"}`}} {
		if rec := h.do(c.method, base, c.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", c.method, rec.Code)
		}
	}
}

// Um valor digitado pela pessoa nunca é sobrescrito, e a pergunta do banco
// reenfileira o deploy que esperava.
func TestChooseDatabaseRespectsTheUsersVariableAndRequeuesTheWaitingDeploy(t *testing.T) {
	h := newHarness(t)
	h.login()
	p := h.createProject("respeita")
	base := "/api/v1/projects/" + p.ID.String() + "/database"
	h.do("PUT", h.envPath(p.ID.String(), "DATABASE_URL"), `{"value":"postgres://meu/banco"}`)
	if rec := h.do("POST", base, `{"choice":"managed"}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "variable_defined" {
		t.Fatalf("managed over a typed value: %d %s", rec.Code, rec.Body)
	}
	if vals, _, _ := h.st.RevealEnvValues(t.Context(), p.ID); vals["DATABASE_URL"] != "postgres://meu/banco" {
		t.Error("the user's value was overwritten")
	}

	// Um deploy parado na pergunta do banco volta à fila com a escolha "none".
	h.do("DELETE", h.envPath(p.ID.String(), "DATABASE_URL"), "")
	dep := h.deployment(p.ID.String(), `{"idempotency_key":"d1"}`)
	o := h.leased()
	for _, to := range []domain.DeploymentState{domain.DeploymentFetching, domain.DeploymentAnalyzing} {
		if err := o.Transition(t.Context(), dep.ID, to, ""); err != nil {
			t.Fatal(err)
		}
	}
	res := domain.AnalysisResult{Outcome: domain.AnalysisNeedsInput, Pending: []domain.Finding{{Field: domain.FieldDatabase, Message: "?"}}}
	if _, err := o.SaveAnalysis(t.Context(), dep.ID, p.ID, "abc", res); err != nil {
		t.Fatal(err)
	}
	if err := o.Transition(t.Context(), dep.ID, domain.DeploymentNeedsInput, ""); err != nil {
		t.Fatal(err)
	}
	o.Finish(t.Context())

	var got dbBody
	rec := h.do("POST", base, `{"choice":"none"}`)
	json.Unmarshal(rec.Body.Bytes(), &got)
	if rec.Code != http.StatusOK || got.Requeued != 1 {
		t.Fatalf("choose none: %d %s", rec.Code, rec.Body)
	}
	if d, _ := h.st.GetDeployment(t.Context(), dep.ID); d.State != domain.DeploymentQueued {
		t.Errorf("the waiting deploy state = %s, want QUEUED", d.State)
	}
}

// Conexão em partes (DEC-44): validação dos papéis e valores entregues por papel.
func TestChooseDatabaseInPartsThroughTheAPI(t *testing.T) {
	h := newHarness(t)
	h.login()
	p := h.createProject("em-partes")
	base := "/api/v1/projects/" + p.ID.String() + "/database"

	for name, body := range map[string]string{
		"unknown role":       `{"choice":"managed","variables":{"senha":"X"}}`,
		"parts without host": `{"choice":"managed","variables":{"password":"POSTGRES_PASSWORD"}}`,
		"parts without pass": `{"choice":"managed","variables":{"host":"POSTGRES_HOST"}}`,
		"url mixed":          `{"choice":"managed","variables":{"url":"DATABASE_URL","host":"POSTGRES_HOST"}}`,
		"duplicate name":     `{"choice":"managed","variables":{"host":"X","password":"X"}}`,
		"platform name":      `{"choice":"managed","variables":{"host":"PORT","password":"POSTGRES_PASSWORD"}}`,
		"bad name":           `{"choice":"managed","variables":{"host":"A-B","password":"POSTGRES_PASSWORD"}}`,
	} {
		if rec := h.do("POST", base, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}

	rec := h.do("POST", base, `{"choice":"managed","variables":{"host":"POSTGRES_HOST","port":"POSTGRES_PORT","user":"POSTGRES_USER","password":"POSTGRES_PASSWORD","database":"POSTGRES_DB","ca":"POSTGRES_CA"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("managed in parts: %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"tls":true`) || strings.Contains(rec.Body.String(), `"url"`) {
		t.Errorf("body: %s", rec.Body)
	}
	vals, _, _ := h.st.RevealEnvValues(t.Context(), p.ID)
	if vals["POSTGRES_HOST"] != domain.ServiceContainerName(p.ID.String()) || vals["POSTGRES_PORT"] != "5432" ||
		vals["POSTGRES_USER"] != "app" || vals["POSTGRES_DB"] != "app" || len(vals["POSTGRES_PASSWORD"]) < 20 {
		t.Errorf("values = %v", vals)
	}
	if _, has := vals["POSTGRES_CA"]; has {
		t.Error("the CA is delivered at run time and must not be stored")
	}
	if strings.Contains(h.logs.b.String(), vals["POSTGRES_PASSWORD"]) || strings.Contains(rec.Body.String(), vals["POSTGRES_PASSWORD"]) {
		t.Error("the password leaked")
	}
}
