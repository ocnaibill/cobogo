package httpapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/httpapi"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

type harness struct {
	t      *testing.T
	st     *store.Store
	router http.Handler
}

func newHarness(t *testing.T) *harness {
	st := storetest.New(t)
	return &harness{t: t, st: st, router: httpapi.NewRouter(st, slog.New(slog.NewTextHandler(io.Discard, nil)))}
}

func (h *harness) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, req)
	return rec
}

func (h *harness) project() string {
	rec := h.do("POST", "/projects", `{"slug":"app-teste","repository_url":"https://example.com/r.git"}`)
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("create project: %d %s", rec.Code, rec.Body)
	}
	var p domain.Project
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		h.t.Fatal(err)
	}
	return p.ID.String()
}

func (h *harness) deployment(projectID, body string) domain.Deployment {
	rec := h.do("POST", "/projects/"+projectID+"/deployments", body)
	if rec.Code != http.StatusAccepted {
		h.t.Fatalf("create deployment: %d %s", rec.Code, rec.Body)
	}
	var d domain.Deployment
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		h.t.Fatal(err)
	}
	return d
}

func TestCreateDeploymentValidatesOverrides(t *testing.T) {
	h := newHarness(t)
	project := h.project()

	for name, overrides := range map[string]string{
		"unknown field":      `{"dockerfile":"FROM scratch"}`,
		"port not a number":  `{"port":"exemplo"}`,
		"field not editable": `{"profile":"P-NODE"}`,
	} {
		body := `{"idempotency_key":"k-` + name + `","overrides":` + overrides + `}`
		if rec := h.do("POST", "/projects/"+project+"/deployments", body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}

	dep := h.deployment(project, `{"idempotency_key":"ok","overrides":{"port":"3000"}}`)
	if dep.Overrides["port"] != "3000" {
		t.Errorf("overrides = %#v", dep.Overrides)
	}
}

func TestAnswerDeployment(t *testing.T) {
	h := newHarness(t)
	project := h.project()
	dep := h.deployment(project, `{"idempotency_key":"k1"}`)
	path := "/deployments/" + dep.ID.String() + "/answers"

	// Ainda em QUEUED: nada a responder.
	if rec := h.do("POST", path, `{"overrides":{"port":"3000"}}`); rec.Code != http.StatusConflict {
		t.Errorf("answer to a queued deployment: %d, want 409", rec.Code)
	}

	// O worker deixou o deploy em NEEDS_INPUT.
	if err := h.st.UpdateDeploymentState(t.Context(), dep.ID, domain.DeploymentNeedsInput, ""); err != nil {
		t.Fatal(err)
	}
	job, _, err := h.st.ClaimNextJob(t.Context(), 1)
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	if err := h.st.MarkJobDone(t.Context(), job.ID); err != nil {
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
	if rec := h.do("POST", "/deployments/00000000-0000-0000-0000-000000000000/answers", `{"overrides":{"port":"3000"}}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown deployment: %d, want 404", rec.Code)
	}
	if rec := h.do("POST", "/deployments/nao-e-uuid/answers", `{"overrides":{"port":"3000"}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d, want 400", rec.Code)
	}
}
