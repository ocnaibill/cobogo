package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/httpapi"
)

func (h *harness) projectID(slug string) uuid.UUID {
	h.t.Helper()
	for _, p := range decode[projectsPage](h.t, h.do("GET", "/api/v1/projects", "")).Projects {
		if p.Slug == slug {
			return p.ID
		}
	}
	h.t.Fatalf("project %s not found", slug)
	return uuid.Nil
}

// Parar, iniciar e reiniciar (DEC-40): a API só enfileira, e recusa o que não
// muda nada ou não tem release.
func TestAppOperationsAreQueuedAndRefuseWhatChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.login()
	idle := h.createProject("app-sem-release")
	for _, action := range []string{"stop", "start", "restart"} {
		rec := h.do("POST", "/api/v1/projects/"+idle.ID.String()+"/app/"+action, "")
		if rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "no_active_release" {
			t.Errorf("%s without a release: %d %s, want 409 no_active_release", action, rec.Code, rec.Body)
		}
	}

	h.promoted("app-no-ar", "app-no-ar.cobogo.test", "cobogo-app-no-ar-1")
	id := h.projectID("app-no-ar").String()
	base := "/api/v1/projects/" + id + "/app/"

	if rec := h.do("POST", base+"start", ""); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "app_already_running" {
		t.Errorf("start while running: %d %s", rec.Code, rec.Body)
	}
	for action, kind := range map[string]domain.OperationKind{"stop": domain.OpStopApp, "restart": domain.OpRestartApp} {
		rec := h.do("POST", base+action, "")
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s: %d %s", action, rec.Code, rec.Body)
		}
		if op := decode[domain.Operation](t, rec); op.Kind != kind || op.State != domain.OpPending {
			t.Errorf("%s operation = %+v", action, op)
		}
	}

	// Parado, parar de novo é recusado; iniciar vale.
	pid := uuid.MustParse(id)
	active, err := h.st.ActiveRelease(t.Context(), pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.st.SetAppStopped(t.Context(), active.ID, true); err != nil {
		t.Fatal(err)
	}
	if rec := h.do("POST", base+"stop", ""); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "app_already_stopped" {
		t.Errorf("stop while stopped: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("POST", base+"start", ""); rec.Code != http.StatusAccepted {
		t.Errorf("start while stopped: %d %s", rec.Code, rec.Body)
	}
	if got := decode[domain.Project](t, h.do("GET", "/api/v1/projects/"+id, "")); got.AppState != domain.AppStopped {
		t.Errorf("app_state = %q, want stopped", got.AppState)
	}

	if rec := h.do("POST", "/api/v1/projects/"+uuid.NewString()+"/app/stop", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rec.Code)
	}
	h.cookie = nil
	if rec := h.do("POST", base+"stop", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("without a session: %d, want 401", rec.Code)
	}
}

// Um projeto parado aponta o host dele para a API, que serve a página de inativo.
func TestStoppedProjectRoutesToTheAPIAndTheInactivePageAnswers(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.promoted("meu-site", "meu-site.cobogo.test", "cobogo-meu-site-1")
	h.promoted("outro", "outro.cobogo.test", "cobogo-outro-1")
	if rec := h.do("PUT", "/api/v1/settings/apps_domain", `{"value":"cobogo.test"}`); rec.Code != http.StatusOK {
		t.Fatalf("apps_domain: %d %s", rec.Code, rec.Body)
	}
	stopID := h.projectID("meu-site")
	active, _ := h.st.ActiveRelease(t.Context(), stopID)
	if err := h.st.SetAppStopped(t.Context(), active.ID, true); err != nil {
		t.Fatal(err)
	}

	// A tabela de rotas: o parado vai para a API; o outro segue no contêiner.
	panel := &httpapi.PanelRoute{Host: "painel.cobogo.test", Target: "http://api:8080"}
	routing, err := httpapi.NewRoutingHandler(h.st, routerToken, quietLogger(h), panel)
	if err != nil {
		t.Fatal(err)
	}
	var got traefikView
	if err := json.Unmarshal(h.fetchRoutes(routing, map[string]string{"Authorization": "Bearer " + routerToken}).Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if s := got.HTTP.Services["cobogo-meu-site"].LoadBalancer.Servers; len(s) != 1 || s[0].URL != "http://api:8080" {
		t.Errorf("stopped service = %+v, want the API", s)
	}
	if s := got.HTTP.Services["cobogo-outro"].LoadBalancer.Servers; len(s) != 1 || s[0].URL != "http://cobogo-outro-1:3000" {
		t.Errorf("running service = %+v, want the container", s)
	}
	if got.HTTP.Routers["cobogo-meu-site"].Rule != "Host(`meu-site.cobogo.test`)" {
		t.Errorf("stopped router = %+v", got.HTTP.Routers["cobogo-meu-site"])
	}

	// A página de inativo: 503, o nome, sem cache e sem nada além disso.
	get := func(host, path string) *httptest.ResponseRecorder {
		r := h.newReq("GET", path, "")
		r.Host = host
		r.Header.Del("Origin")
		return h.serve(r)
	}
	page := get("meu-site.cobogo.test", "/")
	body := page.Body.String()
	if page.Code != http.StatusServiceUnavailable || !strings.Contains(body, "meu-site") || !strings.Contains(body, "não está ativo") {
		t.Fatalf("inactive page: %d %s", page.Code, body)
	}
	if page.Header().Get("Cache-Control") != "no-store" || page.Header().Get("Retry-After") == "" || !strings.Contains(page.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Errorf("headers = %v", page.Header())
	}
	// Qualquer caminho do host parado mostra a página: a API e o painel não vazam por ele.
	if p := get("meu-site.cobogo.test", "/api/v1/projects"); p.Code != http.StatusServiceUnavailable || strings.Contains(p.Body.String(), "projects") {
		t.Errorf("API path on a stopped host: %d %s", p.Code, p.Body)
	}
	// Host de um projeto no ar, o do painel e hosts desconhecidos não são afetados.
	if p := get("outro.cobogo.test", "/api/v1/auth/me"); p.Code == http.StatusServiceUnavailable {
		t.Error("a running project's host must not show the inactive page")
	}
	if p := get("cobogo.test", "/healthz"); p.Code != http.StatusOK {
		t.Errorf("panel host: %d", p.Code)
	}
	if p := get("inexistente.cobogo.test", "/healthz"); p.Code == http.StatusServiceUnavailable {
		t.Error("an unknown project must not show the inactive page")
	}
	if p := get("meu-site.outro-dominio.test", "/healthz"); p.Code == http.StatusServiceUnavailable {
		t.Error("a host outside the apps domain must not show the inactive page")
	}

	// Iniciar de novo: o host volta ao normal.
	if err := h.st.SetAppStopped(t.Context(), active.ID, false); err != nil {
		t.Fatal(err)
	}
	if p := get("meu-site.cobogo.test", "/healthz"); p.Code != http.StatusOK {
		t.Errorf("after start: %d, want the normal response", p.Code)
	}
}
