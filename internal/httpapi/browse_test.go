package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

type projectsPage struct {
	Projects []struct {
		ID             uuid.UUID `json:"id"`
		Slug           string    `json:"slug"`
		LastDeployment *struct {
			ID    uuid.UUID `json:"id"`
			State string    `json:"state"`
		} `json:"last_deployment"`
	} `json:"projects"`
	NextBefore string `json:"next_before"`
}

type eventsPage struct {
	Events    []domain.DeploymentEvent `json:"events"`
	NextAfter int                      `json:"next_after"`
	HasMore   bool                     `json:"has_more"`
	State     string                   `json:"state"`
	Settled   bool                     `json:"settled"`
}

// leased reivindica o próximo job e devolve a posse, como o worker.
func (h *harness) leased() *store.Owned {
	h.t.Helper()
	job, _, err := h.st.ClaimNextJob(h.t.Context(), time.Minute, 3)
	if err != nil || job == nil {
		h.t.Fatalf("claim: %v %v", job, err)
	}
	return h.st.Owned(store.LeaseOf(job))
}

func TestListProjectsPaginatesAndShowsTheLastDeployment(t *testing.T) {
	h := newHarness(t)
	h.login()

	for _, slug := range []string{"um", "dois", "tres"} {
		rec := h.do("POST", "/api/v1/projects", `{"slug":"`+slug+`","repository_url":"https://github.com/exemplo/`+slug+`"}`)
		if rec.Code != http.StatusCreated {
			t.Fatal(rec.Body)
		}
	}
	dois := ""
	first := decode[projectsPage](t, h.do("GET", "/api/v1/projects?limit=2", ""))
	if len(first.Projects) != 2 || first.NextBefore == "" || first.Projects[0].Slug != "tres" || first.Projects[1].Slug != "dois" {
		t.Fatalf("first page = %+v", first)
	}
	dois = first.Projects[1].ID.String()

	second := decode[projectsPage](t, h.do("GET", "/api/v1/projects?limit=2&before="+url.QueryEscape(first.NextBefore), ""))
	if len(second.Projects) != 1 || second.Projects[0].Slug != "um" || second.NextBefore != "" {
		t.Fatalf("second page = %+v", second)
	}
	if second.Projects[0].LastDeployment != nil {
		t.Error("a project without deploys must have last_deployment null")
	}

	dep := h.deployment(dois, `{"idempotency_key":"k1"}`)
	all := decode[projectsPage](t, h.do("GET", "/api/v1/projects", ""))
	for _, p := range all.Projects {
		if p.Slug == "dois" && (p.LastDeployment == nil || p.LastDeployment.ID != dep.ID || p.LastDeployment.State != "QUEUED") {
			t.Errorf("last_deployment of dois = %+v", p.LastDeployment)
		}
	}
	// A lista vazia é [], não null: o painel não precisa tratar os dois.
	empty := newHarness(t)
	empty.login()
	if body := empty.do("GET", "/api/v1/projects", "").Body.String(); !strings.Contains(body, `"projects":[]`) {
		t.Errorf("empty list = %s", body)
	}
}

func TestListParametersAreValidated(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	dep := h.deployment(project, `{"idempotency_key":"k"}`).ID.String()

	for _, path := range []string{
		"/api/v1/projects?limit=0",
		"/api/v1/projects?limit=101",
		"/api/v1/projects?limit=abc",
		"/api/v1/projects?before=ontem",
		"/api/v1/projects/" + project + "/deployments?limit=-1",
		"/api/v1/projects/" + project + "/deployments?before=" + url.QueryEscape("2026-11-17T00:00:00Z_nao-uuid"),
		"/api/v1/deployments/" + dep + "/events?after=-1",
		"/api/v1/deployments/" + dep + "/events?after=x",
		"/api/v1/deployments/" + dep + "/events?limit=0",
		"/api/v1/deployments/" + dep + "/events?limit=1001",
	} {
		rec := h.do("GET", path, "")
		if rec.Code != http.StatusBadRequest || decode[apiError](t, rec).Error.Code != "invalid_request" {
			t.Errorf("%s: %d %s, want 400 invalid_request", path, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{"/api/v1/projects?limit=100", "/api/v1/deployments/" + dep + "/events?limit=1000&after=0"} {
		if rec := h.do("GET", path, ""); rec.Code != http.StatusOK {
			t.Errorf("%s: %d, want 200", path, rec.Code)
		}
	}
}

func TestListDeploymentsOfAProject(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	var ids []string
	for i := 0; i < 3; i++ {
		ids = append(ids, h.deployment(project, `{"idempotency_key":"k`+strconv.Itoa(i)+`"}`).ID.String())
	}

	type page struct {
		Deployments []domain.Deployment `json:"deployments"`
		NextBefore  string              `json:"next_before"`
	}
	first := decode[page](t, h.do("GET", "/api/v1/projects/"+project+"/deployments?limit=2", ""))
	if len(first.Deployments) != 2 || first.NextBefore == "" || first.Deployments[0].ID.String() != ids[2] {
		t.Fatalf("first page = %+v", first)
	}
	second := decode[page](t, h.do("GET", "/api/v1/projects/"+project+"/deployments?limit=2&before="+url.QueryEscape(first.NextBefore), ""))
	if len(second.Deployments) != 1 || second.Deployments[0].ID.String() != ids[0] || second.NextBefore != "" {
		t.Fatalf("second page = %+v", second)
	}

	if rec := h.do("GET", "/api/v1/projects/"+uuid.NewString()+"/deployments", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rec.Code)
	}
}

func TestDeploymentDetailIsASummaryWithoutEvents(t *testing.T) {
	h := newHarness(t)
	h.login()
	dep := h.deployment(h.project(), `{"idempotency_key":"k"}`)
	o := h.leased()
	for i := 0; i < 3; i++ {
		if err := o.AppendEvent(t.Context(), dep.ID, "building", "log", map[string]any{"line": i}); err != nil {
			t.Fatal(err)
		}
	}

	rec := h.do("GET", "/api/v1/deployments/"+dep.ID.String(), "")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, has := raw["events"]; has {
		t.Error("the detail must not embed events (DEC-28 item 1)")
	}
	if _, has := raw["release"]; has {
		t.Error("no release yet: the field must be omitted")
	}
	var last int
	if err := json.Unmarshal(raw["last_event_sequence"], &last); err != nil || last != 3 {
		t.Errorf("last_event_sequence = %s, %v", raw["last_event_sequence"], err)
	}
	if got := decode[struct {
		Deployment domain.Deployment `json:"deployment"`
	}](t, rec).Deployment; got.ID != dep.ID {
		t.Errorf("deployment = %+v", got)
	}

	if _, err := o.CreateRelease(t.Context(), dep.ID, "img", "ctr", "active", "healthy", 41234, "https://app.cobogo.test/"); err != nil {
		t.Fatal(err)
	}
	withRelease := decode[struct {
		Release *domain.Release `json:"release"`
	}](t, h.do("GET", "/api/v1/deployments/"+dep.ID.String(), ""))
	if withRelease.Release == nil || withRelease.Release.HostPort != 41234 || withRelease.Release.URL != "https://app.cobogo.test/" {
		t.Errorf("release = %+v", withRelease.Release)
	}
}

func TestEventsAreIncrementalAndTellWhenToStop(t *testing.T) {
	h := newHarness(t)
	h.login()
	dep := h.deployment(h.project(), `{"idempotency_key":"k"}`)
	path := "/api/v1/deployments/" + dep.ID.String() + "/events"

	none := decode[eventsPage](t, h.do("GET", path, ""))
	if len(none.Events) != 0 || none.NextAfter != 0 || none.Settled || none.State != "QUEUED" {
		t.Errorf("no events yet = %+v", none)
	}
	if body := h.do("GET", path, "").Body.String(); !strings.Contains(body, `"events":[]`) {
		t.Errorf("an empty page must be [], got %s", body)
	}

	o := h.leased()
	for i := 1; i <= 5; i++ {
		if err := o.AppendEvent(t.Context(), dep.ID, "building", "log", map[string]any{"line": "l" + strconv.Itoa(i)}); err != nil {
			t.Fatal(err)
		}
	}

	page := decode[eventsPage](t, h.do("GET", path+"?limit=2", ""))
	if len(page.Events) != 2 || !page.HasMore || page.NextAfter != 2 || page.Settled {
		t.Fatalf("page 1 = %+v", page)
	}
	page = decode[eventsPage](t, h.do("GET", path+"?after=2&limit=10", ""))
	if len(page.Events) != 3 || page.HasMore || page.NextAfter != 5 || page.Events[0].Sequence != 3 {
		t.Fatalf("page 2 = %+v", page)
	}
	if got := page.Events[0].Payload["line"]; got != "l3" {
		t.Errorf("payload = %v", got)
	}
	// Sem novidade, o cursor não anda.
	page = decode[eventsPage](t, h.do("GET", path+"?after=5", ""))
	if len(page.Events) != 0 || page.NextAfter != 5 {
		t.Errorf("up to date = %+v", page)
	}

	// O estado final chega antes do último evento: só `settled` (job encerrado)
	// garante que nada mais virá (DEC-28 item 3).
	if err := o.Transition(t.Context(), dep.ID, domain.DeploymentFetching, ""); err != nil {
		t.Fatal(err)
	}
	if err := o.Transition(t.Context(), dep.ID, domain.DeploymentFailed, "falhou"); err != nil {
		t.Fatal(err)
	}
	mid := decode[eventsPage](t, h.do("GET", path+"?after=5", ""))
	if mid.State != "FAILED" || mid.Settled {
		t.Errorf("failed but job still leased must not be settled: %+v", mid)
	}
	if err := o.AppendEvent(t.Context(), dep.ID, "pipeline", "failed", map[string]any{"error": "falhou"}); err != nil {
		t.Fatal(err)
	}
	if err := o.Finish(t.Context()); err != nil {
		t.Fatal(err)
	}
	end := decode[eventsPage](t, h.do("GET", path+"?after=5", ""))
	if !end.Settled || len(end.Events) != 1 || end.Events[0].EventType != "failed" || end.HasMore {
		t.Errorf("end = %+v", end)
	}
}

func TestAnalysisAndPlanOfADeployment(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()

	queued := h.deployment(project, `{"idempotency_key":"antes"}`)
	for _, suffix := range []string{"analysis", "plan"} {
		rec := h.do("GET", "/api/v1/deployments/"+queued.ID.String()+"/"+suffix, "")
		if rec.Code != http.StatusNotFound || decode[apiError](t, rec).Error.Code != "analysis_unavailable" {
			t.Errorf("%s before analysis: %d %s", suffix, rec.Code, rec.Body)
		}
	}

	// Um deploy com análise pronta.
	ready := domain.AnalysisResult{Outcome: domain.AnalysisReady, Fields: map[string]domain.Field{
		domain.FieldProfile:      {Value: "P-NODE", Origin: domain.OriginInference},
		domain.FieldStartCommand: {Value: "node server.js", Origin: domain.OriginDeclaration},
		domain.FieldPort:         {Value: "3000", Origin: domain.OriginDefault},
	}}
	dep := h.deployment(project, `{"idempotency_key":"pronta"}`)
	o := h.leased()
	if _, err := o.SaveAnalysis(t.Context(), dep.ID, dep.ProjectID, "abc123", ready); err != nil {
		t.Fatal(err)
	}

	analysis := decode[domain.Analysis](t, h.do("GET", "/api/v1/deployments/"+dep.ID.String()+"/analysis", ""))
	if analysis.CommitSHA != "abc123" || analysis.Result.Outcome != domain.AnalysisReady || analysis.Result.Fields[domain.FieldPort].Value != "3000" {
		t.Errorf("analysis = %+v", analysis)
	}

	plan := decode[domain.Plan](t, h.do("GET", "/api/v1/deployments/"+dep.ID.String()+"/plan", ""))
	if plan.Port != 3000 || plan.StartCommand != "node server.js" || !strings.Contains(plan.Dockerfile, "FROM node:") {
		t.Errorf("plan = %+v", plan)
	}

	// Uma análise que pede informação não tem plano.
	pending := h.deployment(project, `{"idempotency_key":"pendente"}`)
	o.Finish(t.Context())
	o = h.leased()
	needs := domain.AnalysisResult{Outcome: domain.AnalysisNeedsInput, Fields: map[string]domain.Field{}}
	if _, err := o.SaveAnalysis(t.Context(), pending.ID, pending.ProjectID, "abc123", needs); err != nil {
		t.Fatal(err)
	}
	rec := h.do("GET", "/api/v1/deployments/"+pending.ID.String()+"/plan", "")
	if rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "plan_unavailable" {
		t.Errorf("plan of a pending analysis: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("GET", "/api/v1/deployments/"+pending.ID.String()+"/analysis", ""); rec.Code != http.StatusOK {
		t.Errorf("a pending analysis is still readable: %d", rec.Code)
	}
}

func TestCancelDeploymentOverHTTP(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	dep := h.deployment(project, `{"idempotency_key":"k"}`)
	path := "/api/v1/deployments/" + dep.ID.String() + "/cancel"

	rec := h.do("POST", path, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}
	if got := decode[domain.Deployment](t, rec); got.State != domain.DeploymentCancelled {
		t.Errorf("state = %s", got.State)
	}
	rec = h.do("POST", path, "")
	if rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "deployment_finished" {
		t.Errorf("second cancel: %d %s", rec.Code, rec.Body)
	}
	rec = h.do("POST", "/api/v1/deployments/"+dep.ID.String()+"/answers", `{"overrides":{"port":"3000"}}`)
	if rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "not_awaiting_input" {
		t.Errorf("answer after cancel: %d %s", rec.Code, rec.Body)
	}

	// Um deploy que o worker já tem não se cancela.
	running := h.deployment(project, `{"idempotency_key":"em-andamento"}`)
	o := h.leased()
	if err := o.Transition(t.Context(), running.ID, domain.DeploymentFetching, ""); err != nil {
		t.Fatal(err)
	}
	rec = h.do("POST", "/api/v1/deployments/"+running.ID.String()+"/cancel", "")
	if rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "deployment_started" {
		t.Errorf("cancel while running: %d %s", rec.Code, rec.Body)
	}

	if rec := h.do("POST", "/api/v1/deployments/"+uuid.NewString()+"/cancel", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown deployment: %d, want 404", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/deployments/xx/cancel", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d, want 400", rec.Code)
	}

	var audited int
	if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM audit_events WHERE action = 'deployment.cancel' AND outcome = 'ok'`).Scan(&audited); err != nil || audited != 1 {
		t.Errorf("audited cancellations = %d, %v", audited, err)
	}
}

func TestNewRoutesRequireASession(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	dep := h.deployment(project, `{"idempotency_key":"k"}`).ID.String()
	h.cookie = nil

	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/projects"},
		{"GET", "/api/v1/projects/" + project + "/deployments"},
		{"GET", "/api/v1/deployments/" + dep + "/events"},
		{"GET", "/api/v1/deployments/" + dep + "/analysis"},
		{"GET", "/api/v1/deployments/" + dep + "/plan"},
		{"POST", "/api/v1/deployments/" + dep + "/cancel"},
	} {
		rec := h.do(c.method, c.path, "")
		if rec.Code != http.StatusUnauthorized || decode[apiError](t, rec).Error.Code != "unauthenticated" {
			t.Errorf("%s %s without a session: %d %s", c.method, c.path, rec.Code, rec.Body)
		}
	}
}

func TestReservedSlugsCannotBeUsed(t *testing.T) {
	h := newHarness(t)
	h.login()
	for _, slug := range []string{"www", "api", "admin", "cobogo", "painel", "traefik", "mail", "docs"} {
		rec := h.do("POST", "/api/v1/projects", `{"slug":"`+slug+`","repository_url":"https://github.com/exemplo/repo"}`)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "reservado") {
			t.Errorf("%s: %d %s, want 400 reserved", slug, rec.Code, rec.Body)
		}
	}
	if rec := h.do("POST", "/api/v1/projects", `{"slug":"api-de-pagamentos","repository_url":"https://github.com/exemplo/repo"}`); rec.Code != http.StatusCreated {
		t.Errorf("a name that merely contains a reserved word is fine: %d %s", rec.Code, rec.Body)
	}
}
