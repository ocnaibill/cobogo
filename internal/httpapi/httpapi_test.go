package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

func TestCreateDeploymentValidatesOverrides(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()

	for name, overrides := range map[string]string{
		"unknown field":      `{"dockerfile":"FROM scratch"}`,
		"port not a number":  `{"port":"exemplo"}`,
		"field not editable": `{"commit_sha":"abc123"}`,
		"unknown profile":    `{"profile":"P-PY"}`,
		"bad manager":        `{"package_manager":"yarn"}`,
		"bad bun version":    `{"bun_version":"2"}`,
		"bad output dir":     `{"output_dir":"../fora"}`,
		"unknown mode":       `{"execution_mode":"staging"}`,
		"command ends in \\": `{"build_command":"npm run build \\"}`,
	} {
		body := `{"idempotency_key":"k-` + name + `","overrides":` + overrides + `}`
		if rec := h.do("POST", "/api/v1/projects/"+project+"/deployments", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}

	// Overrides das DECs 36 e 37 são aceitos quando válidos.
	if rec := h.do("POST", "/api/v1/projects/"+project+"/deployments", `{"idempotency_key":"k-static","overrides":{"profile":"P-STATIC","output_dir":"dist","spa_fallback":"false","package_manager":"bun","bun_version":"1.2"}}`); rec.Code != http.StatusAccepted {
		t.Errorf("valid DEC-36/37 overrides: status %d, want 202 (%s)", rec.Code, rec.Body)
	}

	dep := h.deployment(project, `{"idempotency_key":"ok","overrides":{"port":"3000"}}`)
	if dep.Overrides["port"] != "3000" {
		t.Errorf("overrides = %#v", dep.Overrides)
	}
}

func TestAnswerDeployment(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	dep := h.deployment(project, `{"idempotency_key":"k1"}`)
	path := "/api/v1/deployments/" + dep.ID.String() + "/answers"

	// Ainda em QUEUED: nada a responder.
	if rec := h.do("POST", path, `{"overrides":{"port":"3000"}}`); rec.Code != http.StatusConflict {
		t.Errorf("answer to a queued deployment: %d, want 409", rec.Code)
	}

	// O worker deixou o deploy em NEEDS_INPUT.
	job, _, err := h.st.ClaimNextJob(t.Context(), time.Minute, 3)
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	owned := h.st.Owned(store.LeaseOf(job))
	for _, to := range []domain.DeploymentState{domain.DeploymentFetching, domain.DeploymentAnalyzing, domain.DeploymentNeedsInput} {
		if err := owned.Transition(t.Context(), dep.ID, to, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := owned.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}

	for name, body := range map[string]string{
		"empty body":    `{}`,
		"not json":      `oi`,
		"bad port":      `{"overrides":{"port":"exemplo"}}`,
		"unknown field": `{"overrides":{"image":"x"}}`,
	} {
		if rec := h.do("POST", path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
	if got, _ := h.st.GetDeployment(t.Context(), dep.ID); got.State != domain.DeploymentNeedsInput {
		t.Errorf("rejected answers must not change the state, got %s", got.State)
	}

	rec := h.do("POST", path, `{"overrides":{"port":"3000","start_command":"node server.js"}}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("answer: %d %s", rec.Code, rec.Body)
	}
	var answered domain.Deployment
	if err := json.Unmarshal(rec.Body.Bytes(), &answered); err != nil {
		t.Fatal(err)
	}
	if answered.State != domain.DeploymentQueued || answered.Overrides["port"] != "3000" || answered.Overrides["start_command"] != "node server.js" {
		t.Errorf("answered = %+v", answered)
	}

	if rec := h.do("POST", path, `{"overrides":{"port":"3001"}}`); rec.Code != http.StatusConflict {
		t.Errorf("second answer: %d, want 409", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/deployments/00000000-0000-0000-0000-000000000000/answers", `{"overrides":{"port":"3000"}}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown deployment: %d, want 404", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/deployments/nao-e-uuid/answers", `{"overrides":{"port":"3000"}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d, want 400", rec.Code)
	}
}

func (h *harness) envPath(project, name string) string {
	return "/api/v1/projects/" + project + "/env/" + name
}

func TestEnvVariablesNeverReturnTheirValue(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	const canary = "valor-canario-via-api-77c1"

	rec := h.do("PUT", h.envPath(project, "DB_PASSWORD"), `{"value":"`+canary+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), canary) {
		t.Errorf("PUT echoed the value: %s", rec.Body)
	}
	var put struct {
		Name             string `json:"name"`
		Version          int    `json:"version"`
		Source           string `json:"source"`
		RedeployRequired bool   `json:"redeploy_required"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &put); err != nil {
		t.Fatal(err)
	}
	if put.Name != "DB_PASSWORD" || put.Version != 1 || put.Source != "user" || put.RedeployRequired {
		t.Errorf("first put = %+v", put)
	}

	rec = h.do("PUT", h.envPath(project, "DB_PASSWORD"), `{"value":"outro-valor"}`)
	json.Unmarshal(rec.Body.Bytes(), &put)
	if put.Version != 2 || !put.RedeployRequired {
		t.Errorf("a replaced value must ask for a new deploy: %+v", put)
	}

	list := h.do("GET", "/api/v1/projects/"+project+"/env", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), canary) || strings.Contains(list.Body.String(), "outro-valor") ||
		!strings.Contains(list.Body.String(), "DB_PASSWORD") {
		t.Errorf("list: %d %s", list.Code, list.Body)
	}

	if rec := h.do("DELETE", h.envPath(project, "DB_PASSWORD"), ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := h.do("DELETE", h.envPath(project, "DB_PASSWORD"), ""); rec.Code != http.StatusNotFound {
		t.Errorf("second delete: %d, want 404", rec.Code)
	}
}

func TestPutEnvValidation(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	tooBig := strings.Repeat("a", 32<<10+1)

	for name, c := range map[string]struct{ name, body string }{
		"missing value":        {"A", `{}`},
		"empty value":          {"A", `{"value":""}`},
		"not json":             {"A", `oi`},
		"value too large":      {"A", `{"value":"` + tooBig + `"}`},
		"nul in value":         {"A", `{"value":"a\u0000b"}`},
		"name with a dash":     {"MY-VAR", `{"value":"x"}`},
		"name starts a digit":  {"1VAR", `{"value":"x"}`},
		"platform variable":    {"PORT", `{"value":"3000"}`},
		"platform, lower case": {"port", `{"value":"3000"}`},
	} {
		if rec := h.do("PUT", h.envPath(project, c.name), c.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
	if rec := h.do("PUT", h.envPath("00000000-0000-0000-0000-000000000000", "A"), `{"value":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rec.Code)
	}
	if rec := h.do("PUT", h.envPath("nao-e-uuid", "A"), `{"value":"x"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad project id: %d, want 400", rec.Code)
	}
	if rec := h.do("PUT", h.envPath(project, "MULTILINE"), `{"value":"-----BEGIN KEY-----\nabc\n-----END KEY-----"}`); rec.Code != http.StatusOK {
		t.Errorf("a multi-line value is valid: %d %s", rec.Code, rec.Body)
	}
}

func TestGenerateEnvSecret(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	projectID, _ := uuid.Parse(project)

	// Só segredos que o próprio app inventa.
	for _, name := range []string{"STRIPE_SECRET_KEY", "DB_PASSWORD", "GITHUB_TOKEN", "DATABASE_URL"} {
		if rec := h.do("POST", h.envPath(project, name)+"/generate", ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (a third-party credential must never be generated)", name, rec.Code)
		}
	}
	if rec := h.do("POST", h.envPath(project, "PORT")+"/generate", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("PORT: %d, want 400", rec.Code)
	}

	rec := h.do("POST", h.envPath(project, "JWT_SECRET")+"/generate", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body)
	}
	values, _, err := h.st.RevealEnvValues(t.Context(), projectID)
	if err != nil {
		t.Fatal(err)
	}
	first := values["JWT_SECRET"]
	if len(first) < 40 {
		t.Fatalf("generated secret is too short: %d chars", len(first))
	}
	if strings.Contains(rec.Body.String(), first) {
		t.Error("the generated value must never be returned")
	}
	var meta struct {
		Source string `json:"source"`
	}
	json.Unmarshal(rec.Body.Bytes(), &meta)
	if meta.Source != "generated" {
		t.Errorf("source = %q", meta.Source)
	}

	// Gerar de novo sem pedir substituição preserva o valor (sessões já assinadas).
	if rec := h.do("POST", h.envPath(project, "JWT_SECRET")+"/generate", ""); rec.Code != http.StatusConflict {
		t.Errorf("second generate: %d, want 409", rec.Code)
	}
	if values, _, _ := h.st.RevealEnvValues(t.Context(), projectID); values["JWT_SECRET"] != first {
		t.Error("a refused generation must keep the previous value")
	}

	rec = h.do("POST", h.envPath(project, "JWT_SECRET")+"/generate", `{"replace":true}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"redeploy_required":true`) {
		t.Errorf("replace: %d %s", rec.Code, rec.Body)
	}
	if values, _, _ := h.st.RevealEnvValues(t.Context(), projectID); values["JWT_SECRET"] == first || values["JWT_SECRET"] == "" {
		t.Error("replace must store a new value")
	}

	// Um valor informado pelo usuário também conta como "já tem valor".
	h.do("PUT", h.envPath(project, "SESSION_SECRET"), `{"value":"meu-proprio"}`)
	if rec := h.do("POST", h.envPath(project, "SESSION_SECRET")+"/generate", ""); rec.Code != http.StatusConflict {
		t.Errorf("generate over a user value: %d, want 409", rec.Code)
	}
}
