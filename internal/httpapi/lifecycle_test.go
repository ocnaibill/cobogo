package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// createProject cria um projeto e devolve o id; o slug é o dado.
func (h *harness) createProject(slug string) domain.Project {
	h.t.Helper()
	rec := h.do("POST", "/api/v1/projects", `{"slug":"`+slug+`","repository_url":"https://github.com/exemplo/`+slug+`"}`)
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("project %s: %d %s", slug, rec.Code, rec.Body)
	}
	return decode[domain.Project](h.t, rec)
}

func TestDeleteProjectNeedsTheNameAndHidesTheProject(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.promoted("app-excluir", "app-excluir.cobogo.test", "cobogo-app-excluir-1")
	var id string
	for _, p := range decode[projectsPage](t, h.do("GET", "/api/v1/projects", "")).Projects {
		if p.Slug == "app-excluir" {
			id = p.ID.String()
		}
	}
	if id == "" {
		t.Fatal("project not listed")
	}
	path := "/api/v1/projects/" + id

	for name, body := range map[string]string{
		"no body":    ``,
		"empty name": `{"confirm_slug":""}`,
		"wrong name": `{"confirm_slug":"outro-nome"}`,
		"unknown":    `{"confirm_slug":"app-excluir","extra":1}`,
	} {
		rec := h.do("DELETE", path, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
	if rec := h.do("GET", path, ""); rec.Code != http.StatusOK {
		t.Fatalf("a refused delete must leave the project alone: %d", rec.Code)
	}
	if rec := h.do("DELETE", path, `{"confirm_slug":"outro-nome"}`); decode[apiError](t, rec).Error.Code != "confirmation_mismatch" {
		t.Errorf("the code must be confirmation_mismatch: %s", rec.Body)
	}

	rec := h.do("DELETE", path, `{"confirm_slug":"app-excluir"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	op := decode[domain.Operation](t, rec)
	if op.Kind != domain.OpDeleteProject || op.State != domain.OpPending {
		t.Errorf("operation = %+v", op)
	}

	// Some da API, da lista e das rotas; a operação continua visível.
	for _, p := range []string{path, path + "/deployments", path + "/limits", path + "/env"} {
		if rec := h.do("GET", p, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s after delete: %d, want 404", p, rec.Code)
		}
	}
	for _, p := range decode[projectsPage](t, h.do("GET", "/api/v1/projects", "")).Projects {
		if p.Slug == "app-excluir" {
			t.Error("a deleted project must not be listed")
		}
	}
	if rec := h.do("DELETE", path, `{"confirm_slug":"app-excluir"}`); rec.Code != http.StatusNotFound {
		t.Errorf("deleting twice: %d, want 404", rec.Code)
	}
	got := h.do("GET", "/api/v1/operations/"+op.ID.String(), "")
	if got.Code != http.StatusOK || decode[domain.Operation](t, got).ID != op.ID {
		t.Errorf("the operation must be readable: %d %s", got.Code, got.Body)
	}
	list := h.do("GET", path+"/operations", "")
	var page struct {
		Operations []domain.Operation `json:"operations"`
	}
	if list.Code != http.StatusOK {
		t.Fatalf("list operations: %d", list.Code)
	}
	page = decode[struct {
		Operations []domain.Operation `json:"operations"`
	}](t, list)
	if len(page.Operations) != 1 || page.Operations[0].ID != op.ID {
		t.Errorf("operations of the deleted project = %+v, want the delete", page.Operations)
	}
	// A auditoria registra o pedido e a recusa.
	var audited int
	if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action = 'project.delete' AND outcome = 'ok'`).Scan(&audited); err != nil || audited != 1 {
		t.Errorf("audit rows for the delete = %d, %v", audited, err)
	}
}

func TestDeleteProjectIsRefusedWhileADeployRuns(t *testing.T) {
	h := newHarness(t)
	h.login()
	p := h.createProject("app-rodando")
	dep := h.deployment(p.ID.String(), `{"idempotency_key":"k1"}`)
	o := h.leased()
	for _, to := range []domain.DeploymentState{domain.DeploymentFetching, domain.DeploymentAnalyzing, domain.DeploymentGenerating, domain.DeploymentBuilding} {
		if err := o.Transition(t.Context(), dep.ID, to, ""); err != nil {
			t.Fatal(err)
		}
	}
	rec := h.do("DELETE", "/api/v1/projects/"+p.ID.String(), `{"confirm_slug":"app-rodando"}`)
	if rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "deployment_in_progress" {
		t.Errorf("delete while building: %d %s, want 409 deployment_in_progress", rec.Code, rec.Body)
	}
	if rec := h.do("GET", "/api/v1/projects/"+p.ID.String(), ""); rec.Code != http.StatusOK {
		t.Errorf("a refused delete must keep the project: %d", rec.Code)
	}
}

func TestApplyLimitsNeedsALiveReleaseAndAnOwner(t *testing.T) {
	h := newHarness(t)
	h.login()
	idle := h.createProject("app-parado")
	rec := h.do("POST", "/api/v1/projects/"+idle.ID.String()+"/limits/apply", "")
	if rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "no_active_release" {
		t.Errorf("apply without a release: %d %s, want 409 no_active_release", rec.Code, rec.Body)
	}

	h.promoted("app-no-ar", "app-no-ar.cobogo.test", "cobogo-app-no-ar-1")
	var id string
	for _, p := range decode[projectsPage](t, h.do("GET", "/api/v1/projects", "")).Projects {
		if p.Slug == "app-no-ar" {
			id = p.ID.String()
		}
	}
	rec = h.do("POST", "/api/v1/projects/"+id+"/limits/apply", "")
	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
	if op := decode[domain.Operation](t, rec); op.Kind != domain.OpApplyLimits || op.State != domain.OpPending {
		t.Errorf("operation = %+v", op)
	}
	if rec := h.do("POST", "/api/v1/projects/"+uuid.NewString()+"/limits/apply", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rec.Code)
	}
	if rec := h.do("GET", "/api/v1/operations/"+uuid.NewString(), ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown operation: %d, want 404", rec.Code)
	}
	if rec := h.do("GET", "/api/v1/projects/"+id+"/operations?limit=0", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("limit=0: %d, want 400", rec.Code)
	}
}

func TestLifecycleRoutesNeedASession(t *testing.T) {
	h := newHarness(t)
	h.login()
	p := h.createProject("app-sessao")
	h.cookie = nil
	for _, c := range []struct{ method, path, body string }{
		{"DELETE", "/api/v1/projects/" + p.ID.String(), `{"confirm_slug":"app-sessao"}`},
		{"POST", "/api/v1/projects/" + p.ID.String() + "/limits/apply", ""},
		{"GET", "/api/v1/projects/" + p.ID.String() + "/operations", ""},
		{"GET", "/api/v1/operations/" + uuid.NewString(), ""},
	} {
		if rec := h.do(c.method, c.path, c.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", c.method, strings.TrimPrefix(c.path, "/api/v1"), rec.Code)
		}
	}
}
