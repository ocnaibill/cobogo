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
)

const routerToken = "um-token-de-teste-com-mais-de-24-caracteres"

type traefikView struct {
	HTTP struct {
		Routers map[string]struct {
			Rule        string   `json:"rule"`
			EntryPoints []string `json:"entryPoints"`
			Service     string   `json:"service"`
		} `json:"routers"`
		Services map[string]struct {
			LoadBalancer struct {
				Servers []struct {
					URL string `json:"url"`
				} `json:"servers"`
			} `json:"loadBalancer"`
		} `json:"services"`
	} `json:"http"`
}

func (h *harness) routing() http.Handler {
	h.t.Helper()
	handler, err := httpapi.NewRoutingHandler(h.st, routerToken, quietLogger(h), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	return handler
}

func (h *harness) fetchRoutes(handler http.Handler, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/traefik/config", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	return rec
}

// promoted leva um deploy novo de um projeto até a promoção, com a rota dada.
func (h *harness) promoted(slug, host, container string) {
	h.t.Helper()
	rec := h.do("POST", "/api/v1/projects", `{"slug":"`+slug+`","repository_url":"https://github.com/exemplo/`+slug+`"}`)
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("project %s: %d %s", slug, rec.Code, rec.Body)
	}
	project := decode[domain.Project](h.t, rec)
	dep := h.deployment(project.ID.String(), `{"idempotency_key":"k-`+slug+`"}`)
	o := h.leased()
	for _, to := range []domain.DeploymentState{
		domain.DeploymentFetching, domain.DeploymentAnalyzing, domain.DeploymentGenerating, domain.DeploymentBuilding,
		domain.DeploymentProvisioning, domain.DeploymentStarting, domain.DeploymentVerifying,
	} {
		if err := o.Transition(h.t.Context(), dep.ID, to, ""); err != nil {
			h.t.Fatal(err)
		}
	}
	if _, err := o.PromoteRelease(h.t.Context(), dep.ID, store.ReleaseSpec{
		ImageID: "img", ContainerID: container, Health: "healthy", HostPort: 1,
		URL: "https://" + host + "/", RouteHost: host, RouteTarget: "http://" + container + ":3000",
	}); err != nil {
		h.t.Fatal(err)
	}
	if err := o.Finish(h.t.Context()); err != nil {
		h.t.Fatal(err)
	}
}

func TestRoutingHandlerNeedsAStrongToken(t *testing.T) {
	h := newHarness(t)
	for _, token := range []string{"", "curto", strings.Repeat("a", 23)} {
		if _, err := httpapi.NewRoutingHandler(h.st, token, quietLogger(h), nil); err == nil {
			t.Errorf("token %q (%d chars) must be refused", token, len(token))
		}
	}
	if _, err := httpapi.NewRoutingHandler(h.st, strings.Repeat("a", 24), quietLogger(h), nil); err != nil {
		t.Errorf("a 24-char token must be accepted: %v", err)
	}
	if _, err := httpapi.NewRoutingHandler(nil, routerToken, quietLogger(h), nil); err == nil {
		t.Error("a nil store must be refused")
	}
}

func TestRoutingHandlerRequiresTheBearerToken(t *testing.T) {
	h := newHarness(t)
	handler := h.routing()
	for name, headers := range map[string]map[string]string{
		"no header":      nil,
		"wrong token":    {"Authorization": "Bearer " + strings.Repeat("x", len(routerToken))},
		"prefix only":    {"Authorization": "Bearer " + routerToken[:10]},
		"token + suffix": {"Authorization": "Bearer " + routerToken + "x"},
		"wrong scheme":   {"Authorization": "Basic " + routerToken},
		"no scheme":      {"Authorization": routerToken},
		"empty bearer":   {"Authorization": "Bearer "},
		"cookie instead": {"Cookie": cookieSecure + "=" + routerToken},
		"other header":   {"X-Router-Token": routerToken},
	} {
		rec := h.fetchRoutes(handler, headers)
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: %d %s, want 401", name, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "routers") {
			t.Errorf("%s: the table leaked: %s", name, rec.Body)
		}
	}
	// A query string não vale como credencial.
	r := httptest.NewRequest("GET", "/traefik/config?token="+routerToken, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("token in the query string: %d, want 401", rec.Code)
	}
	for _, scheme := range []string{"Bearer", "bearer", "BEARER"} {
		if rec := h.fetchRoutes(handler, map[string]string{"Authorization": scheme + " " + routerToken}); rec.Code != http.StatusOK {
			t.Errorf("scheme %q with the right token: %d", scheme, rec.Code)
		}
	}
}

func TestRoutingHandlerMethodsAndPaths(t *testing.T) {
	h := newHarness(t)
	handler := h.routing()
	auth := map[string]string{"Authorization": "Bearer " + routerToken}

	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		r := httptest.NewRequest(method, "/traefik/config", nil)
		r.Header.Set("Authorization", auth["Authorization"])
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		// Só GET existe; qualquer outro método cai na rota padrão e dá 404, sem
		// dizer o que há ali.
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "routers") {
			t.Errorf("%s: %d %s, want 404", method, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{"/", "/api/v1/projects", "/traefik", "/traefik/config/", "/healthz"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Authorization", auth["Authorization"])
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, r)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404 (the internal listener serves one route only)", path, rec.Code)
		}
	}
}

// A API pública não serve a tabela de rotas, nem com o token certo (DEC-31 item 8).
func TestPublicAPIDoesNotServeTheRoutes(t *testing.T) {
	h := newHarness(t)
	h.login()
	for _, path := range []string{"/traefik/config", "/api/v1/traefik/config", "/internal/traefik/config"} {
		r := h.newReq("GET", path, "")
		r.Header.Set("Authorization", "Bearer "+routerToken)
		if rec := h.serve(r); rec.Code != http.StatusNotFound {
			t.Errorf("%s on the public API: %d, want 404", path, rec.Code)
		}
	}
}

func TestRoutingHandlerEmitsTheTable(t *testing.T) {
	h := newHarness(t)
	h.login()
	handler := h.routing()
	auth := map[string]string{"Authorization": "Bearer " + routerToken}

	// Tabela vazia: `{}`. O Traefik recusa mapas vazios, então a seção http some.
	rec := h.fetchRoutes(handler, auth)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{}` {
		t.Fatalf("empty table: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("headers = %v", rec.Header())
	}

	h.promoted("app-um", "app-um.cobogo.test", "cobogo-app-um-1a2b3c4d")
	h.promoted("app-dois", "app-dois.cobogo.test", "cobogo-app-dois-5e6f7a8b")

	var got traefikView
	if err := json.Unmarshal(h.fetchRoutes(handler, auth).Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.HTTP.Routers) != 2 || len(got.HTTP.Services) != 2 {
		t.Fatalf("table = %+v", got)
	}
	r := got.HTTP.Routers["cobogo-app-um"]
	if r.Rule != "Host(`app-um.cobogo.test`)" || r.Service != "cobogo-app-um" || len(r.EntryPoints) != 1 || r.EntryPoints[0] != "web" {
		t.Errorf("router = %+v", r)
	}
	if s := got.HTTP.Services["cobogo-app-dois"].LoadBalancer.Servers; len(s) != 1 || s[0].URL != "http://cobogo-app-dois-5e6f7a8b:3000" {
		t.Errorf("service = %+v", s)
	}
}

// O host entra numa regra textual do Traefik: um valor fora do padrão (que
// não deveria existir no banco) é omitido, nunca emitido (DEC-31 item 9).
func TestRoutingHandlerOmitsValuesOutsideThePattern(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.promoted("bom", "bom.cobogo.test", "cobogo-bom-11111111")
	for slug, c := range map[string]struct{ host, target string }{
		"mal-host":    {"x.dev`) || Host(`outro.dev", "http://ctr:3000"},
		"mal-espaco":  {"a b.dev", "http://ctr:3000"},
		"mal-maiusc":  {"Maiusc.dev", "http://ctr:3000"},
		"mal-alvo":    {"alvo.dev", "http://ctr:3000/x"},
		"mal-esquema": {"esq.dev", "file:///etc/passwd"},
		"mal-porta":   {"porta.dev", "http://ctr:99999"},
		"mal-externo": {"ext.dev", "http://evil.example.com:80@ctr:80"},
		"mal-vazio":   {"vazio.dev", "http://:3000"},
	} {
		h.promoted(slug, "ok-"+slug+".cobogo.test", "ctr-"+slug)
		if _, err := h.pool.Exec(t.Context(), `
			UPDATE releases SET route_host = $1, route_target = $2
			WHERE deployment_id IN (SELECT d.id FROM deployments d JOIN projects p ON p.id = d.project_id WHERE p.slug = $3)`,
			c.host, c.target, slug); err != nil {
			t.Fatal(err)
		}
	}
	rec := h.fetchRoutes(h.routing(), map[string]string{"Authorization": "Bearer " + routerToken})
	var got traefikView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.HTTP.Routers) != 1 || got.HTTP.Routers["cobogo-bom"].Rule != "Host(`bom.cobogo.test`)" {
		t.Errorf("only the well-formed route may be emitted, got %s", rec.Body)
	}
	for _, bad := range []string{"outro.dev", "evil.example.com", "passwd", "99999"} {
		if strings.Contains(rec.Body.String(), bad) {
			t.Errorf("%q leaked into the table: %s", bad, rec.Body)
		}
	}
}

func quietLogger(h *harness) *slog.Logger {
	return slog.New(slog.NewTextHandler(io.MultiWriter(h.logs, io.Discard), nil))
}

func TestPanelRouteIsEmittedAndNeverOverridden(t *testing.T) {
	h := newHarness(t)
	h.login()
	panel := &httpapi.PanelRoute{Host: "cobogo.ocnaibill.dev", Target: "http://api:8080"}
	handler, err := httpapi.NewRoutingHandler(h.st, routerToken, quietLogger(h), panel)
	if err != nil {
		t.Fatal(err)
	}
	auth := map[string]string{"Authorization": "Bearer " + routerToken}

	// Sem projeto nenhum, a tabela já leva o painel.
	var got traefikView
	if err := json.Unmarshal(h.fetchRoutes(handler, auth).Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	r := got.HTTP.Routers["cobogo_panel"]
	if len(got.HTTP.Routers) != 1 || r.Rule != "Host(`cobogo.ocnaibill.dev`)" || r.Service != "cobogo_panel" || r.EntryPoints[0] != "web" {
		t.Fatalf("panel router = %+v (all: %+v)", r, got.HTTP.Routers)
	}
	if s := got.HTTP.Services["cobogo_panel"].LoadBalancer.Servers; len(s) != 1 || s[0].URL != "http://api:8080" {
		t.Errorf("panel service = %+v", s)
	}

	// Um projeto com o mesmo host do painel é omitido; os outros seguem.
	h.promoted("normal", "normal.cobogo.dev", "cobogo-normal-11111111")
	h.promoted("intruso", "cobogo.ocnaibill.dev", "cobogo-intruso-22222222")
	got = traefikView{}
	if err := json.Unmarshal(h.fetchRoutes(handler, auth).Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, has := got.HTTP.Routers["cobogo-intruso"]; has {
		t.Error("a project must never take the panel's host")
	}
	if got.HTTP.Services["cobogo_panel"].LoadBalancer.Servers[0].URL != "http://api:8080" || got.HTTP.Routers["cobogo-normal"].Rule != "Host(`normal.cobogo.dev`)" {
		t.Errorf("table = %+v", got)
	}
	if !strings.Contains(h.logs.b.String(), "host do painel") {
		t.Error("the omission must be logged")
	}
}

func TestPanelRouteIsValidated(t *testing.T) {
	h := newHarness(t)
	for name, p := range map[string]httpapi.PanelRoute{
		"host with a rule closer": {Host: "x.dev`) || Host(`y.dev", Target: "http://api:8080"},
		"host in capitals":        {Host: "Painel.dev", Target: "http://api:8080"},
		"empty host":              {Host: "", Target: "http://api:8080"},
		"target with a path":      {Host: "p.dev", Target: "http://api:8080/x"},
		"target over https":       {Host: "p.dev", Target: "https://api:8080"},
		"target without a port":   {Host: "p.dev", Target: "http://api"},
	} {
		p := p
		if _, err := httpapi.NewRoutingHandler(h.st, routerToken, quietLogger(h), &p); err == nil {
			t.Errorf("%s must be refused", name)
		}
	}
}
