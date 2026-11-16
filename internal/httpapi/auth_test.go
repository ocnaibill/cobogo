package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/auth"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/httpapi"
)

func TestEveryProtectedRouteRejectsAnonymousRequests(t *testing.T) {
	h := newHarness(t)
	admin := h.admin()
	project, err := h.st.CreateProject(context.Background(), admin.ID, "existente", "https://github.com/a/b", "")
	if err != nil {
		t.Fatal(err)
	}
	dep, _, err := h.st.CreateDeploymentWithJob(context.Background(), project.ID, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	p, d := project.ID.String(), dep.ID.String()

	routes := []struct{ method, path, body string }{
		{"POST", "/api/v1/auth/logout", ""},
		{"GET", "/api/v1/auth/me", ""},
		{"POST", "/api/v1/auth/password", `{"current_password":"x","new_password":"yyyyyyyyyyyyyyy"}`},
		{"POST", "/api/v1/projects", `{"slug":"novo","repository_url":"https://github.com/a/b"}`},
		{"GET", "/api/v1/projects/" + p, ""},
		{"POST", "/api/v1/projects/" + p + "/deployments", `{"idempotency_key":"outra"}`},
		{"GET", "/api/v1/deployments/" + d, ""},
		{"POST", "/api/v1/deployments/" + d + "/answers", `{"overrides":{"port":"3000"}}`},
		{"GET", "/api/v1/projects/" + p + "/env", ""},
		{"PUT", "/api/v1/projects/" + p + "/env/SEGREDO", `{"value":"valor-que-nao-pode-entrar"}`},
		{"DELETE", "/api/v1/projects/" + p + "/env/SEGREDO", ""},
		{"POST", "/api/v1/projects/" + p + "/env/JWT_SECRET/generate", ""},
	}
	cookies := map[string]*http.Cookie{
		"no cookie":      nil,
		"garbage cookie": {Name: cookieSecure, Value: "isto-nao-e-uma-sessao"},
		"empty cookie":   {Name: cookieSecure, Value: ""},
		"huge cookie":    {Name: cookieSecure, Value: strings.Repeat("a", 5000)},
		"wrong name":     {Name: "session", Value: "x"},
	}
	for name, cookie := range cookies {
		for _, rt := range routes {
			r := h.newReq(rt.method, rt.path, rt.body)
			if cookie != nil {
				r.AddCookie(cookie)
			}
			rec := h.serve(r)
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s %s: status %d, want 401: %s", name, rt.method, rt.path, rec.Code, rec.Body)
				continue
			}
			if e := decode[apiError](t, rec); e.Error.Code != "unauthenticated" {
				t.Errorf("%s %s: code %q", name, rt.path, e.Error.Code)
			}
		}
	}

	// Nada foi alterado pelas chamadas sem sessão.
	var projects, deployments, variables int
	_ = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM projects`).Scan(&projects)
	_ = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM deployments`).Scan(&deployments)
	_ = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM env_variables`).Scan(&variables)
	if projects != 1 || deployments != 1 || variables != 0 {
		t.Errorf("anonymous calls changed data: %d projects, %d deployments, %d variables", projects, deployments, variables)
	}
	if got, _ := h.st.GetDeployment(context.Background(), dep.ID); got.State != domain.DeploymentQueued {
		t.Errorf("deployment state = %s", got.State)
	}
}

func TestOnlyTheDocumentedRoutesArePublic(t *testing.T) {
	h := newHarness(t)
	if rec := h.do("GET", "/healthz", ""); rec.Code != http.StatusOK {
		t.Errorf("healthz: %d", rec.Code)
	}
	if rec := h.do("GET", "/api/v1/setup/status", ""); rec.Code != http.StatusOK || !decode[map[string]bool](t, rec)["needs_setup"] {
		t.Errorf("setup status: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("POST", "/api/v1/auth/login", `{"login":"x","password":"y"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("login must be reachable and refuse bad credentials: %d", rec.Code)
	}
	if rec := h.do("GET", "/api/v1/nada", ""); rec.Code != http.StatusNotFound || decode[apiError](t, rec).Error.Code != "not_found" {
		t.Errorf("unknown route: %d %s (want the JSON error format)", rec.Code, rec.Body)
	}
}

func TestSetupCreatesTheAdministratorOnce(t *testing.T) {
	token, plain, err := auth.NewSetupToken()
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, func(o *options) { o.setupToken = token })

	setup := func(tok, login, password string) *httptest.ResponseRecorder {
		return h.do("POST", "/api/v1/setup", `{"token":"`+tok+`","login":"`+login+`","password":"`+password+`"}`)
	}

	if rec := setup("token-errado", "admin", adminPass); rec.Code != http.StatusForbidden || decode[apiError](t, rec).Error.Code != "invalid_setup_token" {
		t.Errorf("wrong token: %d %s", rec.Code, rec.Body)
	}
	if n, _ := h.st.CountUsers(context.Background()); n != 0 {
		t.Fatalf("a wrong token created %d users", n)
	}
	if rec := setup(plain, "admin", "curta"); rec.Code != http.StatusBadRequest || decode[apiError](t, rec).Error.Code != "weak_password" {
		t.Errorf("weak password: %d %s", rec.Code, rec.Body)
	}
	for _, login := range []string{"ab", "Admin Com Espaço", "-admin", "admin@dominio", strings.Repeat("a", 65)} {
		if rec := setup(plain, login, adminPass); rec.Code != http.StatusBadRequest {
			t.Errorf("login %q: %d, want 400", login, rec.Code)
		}
	}
	if n, _ := h.st.CountUsers(context.Background()); n != 0 {
		t.Fatalf("invalid inputs created %d users", n)
	}

	rec := setup(plain, "  Admin ", adminPass) // o login é normalizado
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}
	c := sessionCookie(rec)
	if c == nil {
		t.Fatal("setup must leave the administrator signed in")
	}
	if strings.Contains(rec.Body.String(), adminPass) || strings.Contains(rec.Body.String(), "argon2") {
		t.Errorf("the response leaked secrets: %s", rec.Body)
	}
	h.cookie = &http.Cookie{Name: c.Name, Value: c.Value}
	if me := h.do("GET", "/api/v1/auth/me", ""); me.Code != http.StatusOK || !strings.Contains(me.Body.String(), `"login":"admin"`) {
		t.Errorf("me: %d %s", me.Code, me.Body)
	}

	// A rota fecha: nem o mesmo token abre de novo.
	if rec := setup(plain, "outro", adminPass); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "setup_closed" {
		t.Errorf("second setup: %d %s", rec.Code, rec.Body)
	}
	if n, _ := h.st.CountUsers(context.Background()); n != 1 {
		t.Errorf("users = %d", n)
	}
	if st := h.do("GET", "/api/v1/setup/status", ""); decode[map[string]bool](t, st)["needs_setup"] {
		t.Error("setup status must say the setup is done")
	}
}

func TestSetupIsClosedWithoutAToken(t *testing.T) {
	h := newHarness(t) // sem SetupToken: por exemplo, a API reiniciou depois de criarem o administrador
	rec := h.do("POST", "/api/v1/setup", `{"token":"qualquer","login":"admin","password":"`+adminPass+`"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("setup without a configured token: %d %s", rec.Code, rec.Body)
	}
	if n, _ := h.st.CountUsers(context.Background()); n != 0 {
		t.Errorf("users = %d", n)
	}
}

func TestSetupAttemptsAreLimited(t *testing.T) {
	token, _, _ := auth.NewSetupToken()
	h := newHarness(t, func(o *options) { o.setupToken = token })
	for i := 0; i < auth.PairFailures; i++ {
		if rec := h.do("POST", "/api/v1/setup", `{"token":"palpite","login":"admin","password":"`+adminPass+`"}`); rec.Code != http.StatusForbidden {
			t.Fatalf("attempt %d: %d", i+1, rec.Code)
		}
	}
	rec := h.do("POST", "/api/v1/setup", `{"token":"palpite","login":"admin","password":"`+adminPass+`"}`)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("after %d failures: %d, Retry-After %q", auth.PairFailures, rec.Code, rec.Header().Get("Retry-After"))
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	h := newHarness(t)
	h.admin()
	rec := h.do("POST", "/api/v1/auth/login", `{"login":"admin","password":"`+adminPass+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	c := sessionCookie(rec)
	if c == nil || c.Name != "__Host-cobogo_session" {
		t.Fatalf("cookie = %+v", c)
	}
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Domain != "" {
		t.Errorf("cookie attributes = %+v: want HttpOnly, Secure, SameSite=Strict, Path=/ and no Domain (the __Host- rules)", c)
	}
	if c.MaxAge != int(httpapi.SessionAbsolute.Seconds()) {
		t.Errorf("MaxAge = %d, want %d", c.MaxAge, int(httpapi.SessionAbsolute.Seconds()))
	}
	if len(c.Value) < 40 || strings.Contains(c.Value, "admin") {
		t.Errorf("token = %q", c.Value)
	}
	if strings.Contains(rec.Body.String(), c.Value) {
		t.Error("the token must not appear in the response body")
	}
}

func TestInsecureCookieModeIsOnlyForDevelopment(t *testing.T) {
	h := newHarness(t, func(o *options) { o.cookieInsecure = true })
	h.admin()
	rec := h.do("POST", "/api/v1/auth/login", `{"login":"admin","password":"`+adminPass+`"}`)
	c := sessionCookie(rec)
	if c == nil || c.Name != "cobogo_session" || c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("insecure-mode cookie = %+v: it may drop Secure and the prefix, never HttpOnly or SameSite", c)
	}
}

// Login inexistente e senha errada são indistinguíveis.
func TestLoginFailuresAreIndistinguishable(t *testing.T) {
	h := newHarness(t)
	h.admin()
	wrongPassword := h.do("POST", "/api/v1/auth/login", `{"login":"admin","password":"senha-errada-mas-longa"}`)
	unknownLogin := h.do("POST", "/api/v1/auth/login", `{"login":"ninguem","password":"senha-errada-mas-longa"}`)
	for name, rec := range map[string]*httptest.ResponseRecorder{"wrong password": wrongPassword, "unknown login": unknownLogin} {
		if rec.Code != http.StatusUnauthorized || decode[apiError](t, rec).Error.Code != "invalid_credentials" {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if sessionCookie(rec) != nil {
			t.Errorf("%s set a cookie", name)
		}
	}
	if wrongPassword.Body.String() != unknownLogin.Body.String() {
		t.Errorf("the bodies differ, so the response reveals which logins exist:\n%s\n%s", wrongPassword.Body, unknownLogin.Body)
	}
	var sessions int
	_ = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM sessions`).Scan(&sessions)
	if sessions != 0 {
		t.Errorf("failed logins created %d sessions", sessions)
	}
	// Uma senha absurdamente longa é recusada igual, sem custar um argon2id.
	huge := h.do("POST", "/api/v1/auth/login", `{"login":"admin","password":"`+strings.Repeat("a", 5000)+`"}`)
	if huge.Code != http.StatusUnauthorized || huge.Body.String() != wrongPassword.Body.String() {
		t.Errorf("huge password: %d %s", huge.Code, huge.Body)
	}
}

func TestLoginIsRateLimitedWithoutLockingOutOpenSessions(t *testing.T) {
	h := newHarness(t)
	h.login() // sessão já aberta, de outro "dispositivo"
	open := h.cookie
	h.cookie = nil

	for i := 0; i < auth.PairFailures; i++ {
		if rec := h.do("POST", "/api/v1/auth/login", `{"login":"admin","password":"senha-errada-mas-longa"}`); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i+1, rec.Code)
		}
	}
	// Agora até a senha certa é recusada, com Retry-After.
	rec := h.do("POST", "/api/v1/auth/login", `{"login":"admin","password":"`+adminPass+`"}`)
	if rec.Code != http.StatusTooManyRequests || decode[apiError](t, rec).Error.Code != "rate_limited" || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked login: %d %s", rec.Code, rec.Body)
	}
	// Outro IP não é afetado.
	h.remote = "198.51.100.9:1111"
	if rec := h.do("POST", "/api/v1/auth/login", `{"login":"admin","password":"`+adminPass+`"}`); rec.Code != http.StatusOK {
		t.Errorf("another IP: %d %s", rec.Code, rec.Body)
	}
	// E a sessão que já estava aberta continua valendo.
	h.remote = "203.0.113.7:4321"
	h.cookie = open
	if rec := h.do("GET", "/api/v1/auth/me", ""); rec.Code != http.StatusOK {
		t.Errorf("an open session must survive a login block: %d", rec.Code)
	}
}

func TestEachLoginStartsAFreshSession(t *testing.T) {
	h := newHarness(t)
	h.admin()
	first := sessionCookie(h.do("POST", "/api/v1/auth/login", `{"login":"admin","password":"`+adminPass+`"}`))
	// Entrar com um cookie antigo na requisição não reaproveita o token.
	r := h.newReq("POST", "/api/v1/auth/login", `{"login":"admin","password":"`+adminPass+`"}`)
	r.AddCookie(&http.Cookie{Name: first.Name, Value: first.Value})
	second := sessionCookie(h.serve(r))
	if first == nil || second == nil || first.Value == second.Value {
		t.Errorf("tokens %v / %v: a login must never reuse a token", first, second)
	}
}

func TestLogoutEndsTheSession(t *testing.T) {
	h := newHarness(t)
	h.login()
	rec := h.do("POST", "/api/v1/auth/logout", "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("logout: %d", rec.Code)
	}
	if c := sessionCookie(rec); c == nil || c.MaxAge >= 0 {
		t.Errorf("logout must clear the cookie, got %+v", c)
	}
	if rec := h.do("GET", "/api/v1/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the token must stop working server-side, got %d", rec.Code)
	}
}

func TestSessionsExpireByInactivityAndByAge(t *testing.T) {
	h := newHarness(t)
	h.login()
	if rec := h.do("GET", "/api/v1/auth/me", ""); rec.Code != http.StatusOK {
		t.Fatal("fresh session must work")
	}
	if _, err := h.pool.Exec(context.Background(), `UPDATE sessions SET last_seen_at = now() - interval '2 hours 1 minute'`); err != nil {
		t.Fatal(err)
	}
	if rec := h.do("GET", "/api/v1/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("idle for over 2h: %d, want 401", rec.Code)
	}

	h.cookie = nil
	h.signIn(adminLogin, adminPass)
	if _, err := h.pool.Exec(context.Background(), `UPDATE sessions SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if rec := h.do("GET", "/api/v1/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("past the 12h limit: %d, want 401", rec.Code)
	}
}

func TestChangePassword(t *testing.T) {
	h := newHarness(t)
	h.login()
	other := *h.cookie
	h.cookie = nil
	h.signIn(adminLogin, adminPass) // segunda sessão, que deve cair na troca
	current := h.cookie
	const newPass = "outra-senha-de-teste-bem-longa"

	if rec := h.do("POST", "/api/v1/auth/password", `{"current_password":"errada-errada-errada","new_password":"`+newPass+`"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("wrong current password: %d", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/auth/password", `{"current_password":"`+adminPass+`","new_password":"curta"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("weak new password: %d", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/auth/password", `{"current_password":"`+adminPass+`","new_password":"`+newPass+`"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}

	if rec := h.do("GET", "/api/v1/auth/me", ""); rec.Code != http.StatusOK {
		t.Errorf("the session that changed the password stays: %d", rec.Code)
	}
	h.cookie = &other
	if rec := h.do("GET", "/api/v1/auth/me", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("the other session must end: %d", rec.Code)
	}
	h.cookie = current
	h.remote = "198.51.100.20:1"
	if rec := h.do("POST", "/api/v1/auth/login", `{"login":"admin","password":"`+adminPass+`"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("the old password must stop working: %d", rec.Code)
	}
	if rec := h.do("POST", "/api/v1/auth/login", `{"login":"admin","password":"`+newPass+`"}`); rec.Code != http.StatusOK {
		t.Errorf("the new password must work: %d", rec.Code)
	}
}

func TestStateChangingRequestsMustComeFromThePanelOrigin(t *testing.T) {
	h := newHarness(t)
	h.login()
	const body = `{"slug":"novo-projeto","repository_url":"https://github.com/a/b"}`

	cases := map[string]struct {
		headers map[string]string
		allowed bool
	}{
		"panel origin":                 {map[string]string{"Origin": publicURL}, true},
		"panel origin, upper case":     {map[string]string{"Origin": "HTTPS://COBOGO.TEST"}, true},
		"same-origin fetch metadata":   {map[string]string{"Origin": "", "Sec-Fetch-Site": "same-origin"}, true},
		"another site":                 {map[string]string{"Origin": "https://evil.example"}, false},
		"null origin":                  {map[string]string{"Origin": "null"}, false},
		"same host, http":              {map[string]string{"Origin": "http://cobogo.test"}, false},
		"subdomain":                    {map[string]string{"Origin": "https://app.cobogo.test"}, false},
		"suffix trick":                 {map[string]string{"Origin": "https://cobogo.test.evil.example"}, false},
		"other port":                   {map[string]string{"Origin": "https://cobogo.test:8443"}, false},
		"origin with a path":           {map[string]string{"Origin": publicURL + "/x"}, false},
		"origin with userinfo":         {map[string]string{"Origin": "https://cobogo.test@evil.example"}, false},
		"no origin, no fetch metadata": {map[string]string{"Origin": ""}, false},
		"cross-site fetch metadata":    {map[string]string{"Origin": "", "Sec-Fetch-Site": "cross-site"}, false},
		"same-site fetch metadata":     {map[string]string{"Origin": "", "Sec-Fetch-Site": "same-site"}, false},
		"valid origin wins over a lying fetch metadata": {map[string]string{"Origin": "https://evil.example", "Sec-Fetch-Site": "same-origin"}, false},
	}
	n := 0
	for name, c := range cases {
		n++
		r := h.newReq("POST", "/api/v1/projects", strings.Replace(body, "novo-projeto", "p"+strings.Repeat("x", n), 1))
		for k, v := range c.headers {
			if v == "" {
				r.Header.Del(k)
			} else {
				r.Header.Set(k, v)
			}
		}
		rec := h.serve(r)
		want := http.StatusForbidden
		if c.allowed {
			want = http.StatusCreated
		}
		if rec.Code != want {
			t.Errorf("%s: status %d, want %d (%s)", name, rec.Code, want, rec.Body)
		}
		if !c.allowed && decode[apiError](t, rec).Error.Code != "csrf" {
			t.Errorf("%s: code %q", name, decode[apiError](t, rec).Error.Code)
		}
	}
	var created int
	_ = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM projects`).Scan(&created)
	allowed := 0
	for _, c := range cases {
		if c.allowed {
			allowed++
		}
	}
	if created != allowed {
		t.Errorf("%d projects created, want exactly the %d allowed requests", created, allowed)
	}

	// Leituras não exigem origem, mas continuam exigindo sessão.
	r := h.newReq("GET", "/api/v1/auth/me", "")
	r.Header.Set("Origin", "https://evil.example")
	if rec := h.serve(r); rec.Code != http.StatusOK {
		t.Errorf("a safe method does not need the panel origin: %d", rec.Code)
	}
}

// Login CSRF: um site hostil não pode logar o navegador da vítima numa conta.
func TestLoginAndSetupAreAlsoProtectedFromOtherOrigins(t *testing.T) {
	h := newHarness(t)
	h.admin()
	for _, path := range []string{"/api/v1/auth/login", "/api/v1/setup"} {
		r := h.newReq("POST", path, `{"login":"admin","password":"`+adminPass+`","token":"x"}`)
		r.Header.Set("Origin", "https://evil.example")
		rec := h.serve(r)
		if rec.Code != http.StatusForbidden || sessionCookie(rec) != nil {
			t.Errorf("%s from another origin: %d, cookie %v", path, rec.Code, sessionCookie(rec))
		}
	}
}

func TestBodiesMustBeJSON(t *testing.T) {
	h := newHarness(t)
	h.login()
	n := 0
	for ct, want := range map[string]int{
		"text/plain":                        http.StatusUnsupportedMediaType,
		"application/x-www-form-urlencoded": http.StatusUnsupportedMediaType,
		"multipart/form-data; boundary=x":   http.StatusUnsupportedMediaType,
		"":                                  http.StatusUnsupportedMediaType,
		"application/json":                  http.StatusCreated,
		"application/json; charset=utf-8":   http.StatusCreated,
		"APPLICATION/JSON":                  http.StatusCreated,
	} {
		n++
		r := h.newReq("POST", "/api/v1/projects", `{"slug":"ct-`+strings.Repeat("x", n)+`","repository_url":"https://github.com/a/b"}`)
		if ct == "" {
			r.Header.Del("Content-Type")
		} else {
			r.Header.Set("Content-Type", ct)
		}
		if rec := h.serve(r); rec.Code != want {
			t.Errorf("Content-Type %q: %d, want %d (%s)", ct, rec.Code, want, rec.Body)
		}
	}
}

func TestJSONIsStrictAndErrorsNeverEchoTheBody(t *testing.T) {
	h := newHarness(t)
	h.admin()
	const canary = "senha-canario-que-nao-pode-vazar-9f2"

	for name, body := range map[string]string{
		"unknown field":     `{"login":"admin","password":"x","extra":"` + canary + `"}`,
		"trailing document": `{"login":"admin","password":"` + canary + `"}{"login":"x"}`,
		"trailing garbage":  `{"login":"admin","password":"` + canary + `"} lixo`,
		"array":             `["` + canary + `"]`,
		"malformed":         `{"login":"admin","password":"` + canary,
		"wrong type":        `{"login":123,"password":"` + canary + `"}`,
		"null":              `null`,
	} {
		rec := h.do("POST", "/api/v1/auth/login", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), canary) {
			t.Errorf("%s: the error echoed the request body: %s", name, rec.Body)
		}
	}
	if rec := h.do("POST", "/api/v1/auth/login", ""); rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("empty body: %d", rec.Code)
	}

	big := `{"login":"admin","password":"` + strings.Repeat("a", 2<<20) + `"}`
	if rec := h.do("POST", "/api/v1/auth/login", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a 2 MiB body: %d, want 413", rec.Code)
	}
	if strings.Contains(h.logs.b.String(), canary) {
		t.Error("a request body reached the log")
	}
}

func TestSecurityHeadersAndRequestIDs(t *testing.T) {
	h := newHarness(t)
	h.login()
	seen := map[string]bool{}
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"ok":      h.do("GET", "/api/v1/auth/me", ""),
		"healthz": h.do("GET", "/healthz", ""),
		"unknown": h.do("GET", "/api/v1/nada", ""),
		"unauth": func() *httptest.ResponseRecorder {
			c := h.cookie
			h.cookie = nil
			defer func() { h.cookie = c }()
			return h.do("GET", "/api/v1/auth/me", "")
		}(),
		"bad request": h.do("POST", "/api/v1/projects", `{`),
		"csrf": h.serve(func() *http.Request {
			r := h.newReq("POST", "/api/v1/projects", `{}`)
			r.Header.Set("Origin", "https://evil.example")
			return r
		}()),
	} {
		for k, want := range map[string]string{
			"Cache-Control": "no-store", "X-Content-Type-Options": "nosniff",
			"Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY",
		} {
			if got := rec.Header().Get(k); got != want {
				t.Errorf("%s: %s = %q, want %q", name, k, got, want)
			}
		}
		id := rec.Header().Get("X-Request-Id")
		if _, err := uuid.Parse(id); err != nil || seen[id] {
			t.Errorf("%s: request id %q (repeated: %v)", name, id, seen[id])
		}
		seen[id] = true
	}
	// Um X-Request-Id enviado pelo cliente não é aproveitado.
	r := h.newReq("GET", "/healthz", "")
	r.Header.Set("X-Request-Id", "forjado-pelo-cliente")
	if got := h.serve(r).Header().Get("X-Request-Id"); got == "forjado-pelo-cliente" {
		t.Error("a client-supplied request id must be ignored")
	}
}

func TestEveryAdministratorOnlySeesTheirOwnResources(t *testing.T) {
	h := newHarness(t)
	admin := h.login()
	projectID := h.project()
	dep := h.deployment(projectID, `{"idempotency_key":"k1"}`)
	if rec := h.do("PUT", "/api/v1/projects/"+projectID+"/env/CHAVE", `{"value":"valor-do-primeiro"}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}

	// Um segundo usuário, com sessão própria.
	second := uuid.New()
	if _, err := h.pool.Exec(context.Background(), `INSERT INTO users (id, login, password_hash) VALUES ($1, 'segundo', 'x')`, second); err != nil {
		t.Fatal(err)
	}
	token, hash, _ := auth.NewSessionToken()
	if _, err := h.st.CreateSession(context.Background(), second, hash, time.Hour, "", ""); err != nil {
		t.Fatal(err)
	}
	h.cookie = &http.Cookie{Name: cookieSecure, Value: token}

	d := dep.ID.String()
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/projects/" + projectID, ""},
		{"POST", "/api/v1/projects/" + projectID + "/deployments", `{"idempotency_key":"intruso"}`},
		{"GET", "/api/v1/deployments/" + d, ""},
		{"POST", "/api/v1/deployments/" + d + "/answers", `{"overrides":{"port":"3000"}}`},
		{"GET", "/api/v1/projects/" + projectID + "/env", ""},
		{"PUT", "/api/v1/projects/" + projectID + "/env/CHAVE", `{"value":"valor-do-intruso"}`},
		{"DELETE", "/api/v1/projects/" + projectID + "/env/CHAVE", ""},
		{"POST", "/api/v1/projects/" + projectID + "/env/JWT_SECRET/generate", ""},
	} {
		rec := h.do(c.method, c.path, c.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s by another user: %d, want 404 (not revealed): %s", c.method, c.path, rec.Code, rec.Body)
		}
	}

	// O dono continua com tudo intacto.
	values, _, err := h.st.RevealEnvValues(context.Background(), dep.ProjectID)
	if err != nil || values["CHAVE"] != "valor-do-primeiro" || len(values) != 1 {
		t.Errorf("owner's variables = %v, %v", values, err)
	}
	var deployments int
	_ = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM deployments`).Scan(&deployments)
	if deployments != 1 {
		t.Errorf("deployments = %d: the intruder created one", deployments)
	}
	_ = admin
}

func TestProjectInputIsValidatedAndNormalized(t *testing.T) {
	h := newHarness(t)
	h.login()
	post := func(slug, url, root string) *httptest.ResponseRecorder {
		return h.do("POST", "/api/v1/projects", `{"slug":"`+slug+`","repository_url":"`+url+`","root_directory":"`+root+`"}`)
	}

	rec := post("normalizado", "https://GitHub.com/exemplo/repositorio/", "./apps/web/")
	if rec.Code != http.StatusCreated {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	p := decode[domain.Project](t, rec)
	if p.RepositoryURL != "https://github.com/exemplo/repositorio" || p.RootDirectory != "apps/web" {
		t.Errorf("stored %q and %q: the API must store the normalized forms", p.RepositoryURL, p.RootDirectory)
	}
	if rec := post("outro-ok", "https://gitlab.com/grupo/sub/repo", "/"); rec.Code != http.StatusCreated {
		t.Errorf("gitlab with subgroup: %d %s", rec.Code, rec.Body)
	}

	for name, c := range map[string][3]string{
		"escaping root":    {"s1", "https://github.com/a/b", "../../etc"},
		"nested escape":    {"s2", "https://github.com/a/b", "a/../../b"},
		"backslash root":   {"s3", "https://github.com/a/b", `a\\b`},
		"git directory":    {"s4", "https://github.com/a/b", ".git/hooks"},
		"http url":         {"s5", "http://github.com/a/b", ""},
		"unknown host":     {"s6", "https://example.com/a/b", ""},
		"internal address": {"s7", "https://127.0.0.1/a/b", ""},
		"metadata address": {"s8", "https://169.254.169.254/latest", ""},
		"credentials":      {"s9", "https://user:pass@github.com/a/b", ""},
		"ssh":              {"s10", "ssh://git@github.com/a/b", ""},
		"file":             {"s11", "file:///etc/passwd", ""},
		"no repository":    {"s12", "https://github.com/owner", ""},
		"query":            {"s13", "https://github.com/a/b?x=1", ""},
		"bad slug":         {"Slug_Inválido", "https://github.com/a/b", ""},
	} {
		if rec := post(c[0], c[1], c[2]); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
	if rec := post("normalizado", "https://github.com/a/b", ""); rec.Code != http.StatusConflict {
		t.Errorf("duplicate slug: %d", rec.Code)
	}
	var n int
	_ = h.pool.QueryRow(context.Background(), `SELECT count(*) FROM projects`).Scan(&n)
	if n != 2 {
		t.Errorf("%d projects stored, want 2: refused inputs must not create anything", n)
	}
}

// O que a auditoria e o log registram nunca inclui senha, token nem valor.
func TestNoSecretReachesTheAuditTrailOrTheLog(t *testing.T) {
	h := newHarness(t)
	h.admin()
	const typedInTheWrongField = "senha-digitada-no-campo-do-login-77"
	const envCanary = "valor-de-variavel-canario-55"

	h.do("POST", "/api/v1/auth/login", `{"login":"`+typedInTheWrongField+`","password":"x"}`)
	h.signIn(adminLogin, adminPass)
	token := h.cookie.Value
	projectID := h.project()
	h.do("PUT", "/api/v1/projects/"+projectID+"/env/CHAVE_SECRETA", `{"value":"`+envCanary+`"}`)
	h.do("POST", "/api/v1/projects/"+projectID+"/env/JWT_SECRET/generate", "")
	h.do("POST", "/api/v1/auth/password", `{"current_password":"`+adminPass+`","new_password":"nova-senha-de-teste-bem-longa"}`)
	h.do("GET", "/api/v1/auth/me?token="+token+"&senha=consulta-canario", "")
	h.do("POST", "/api/v1/auth/logout", "")

	events, err := h.st.ListAudit(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	var trail strings.Builder
	for _, e := range events {
		trail.WriteString(e.Action + "|" + e.Target + "|" + e.IP + "|" + e.Outcome + "\n")
	}
	actions := trail.String()
	for _, want := range []string{"login|(desconhecido)|", "login|admin|", "project.create|", "env.set|", "env.generate|", "password.change|", "logout|"} {
		if !strings.Contains(actions, want) {
			t.Errorf("audit trail missing %q:\n%s", want, actions)
		}
	}
	forbidden := []string{typedInTheWrongField, envCanary, adminPass, "nova-senha-de-teste-bem-longa", token, "consulta-canario"}
	for _, secret := range forbidden {
		if strings.Contains(actions, secret) {
			t.Errorf("the audit trail contains %q", secret)
		}
		if strings.Contains(h.logs.b.String(), secret) {
			t.Errorf("the log contains %q", secret)
		}
	}
	if !strings.Contains(h.logs.b.String(), "/api/v1/auth/me") {
		t.Error("the access log must still record the path")
	}
}

func TestLimiterUsesTheRealClientIPOnlyBehindATrustedProxy(t *testing.T) {
	failures := func(h *harness, ipHeader func(i int) string) {
		for i := 0; i < auth.PairFailures; i++ {
			r := h.newReq("POST", "/api/v1/auth/login", `{"login":"admin","password":"senha-errada-mas-longa"}`)
			r.Header.Set("CF-Connecting-IP", ipHeader(i))
			if rec := h.serve(r); rec.Code != http.StatusUnauthorized {
				h.t.Fatalf("failure %d: %d", i+1, rec.Code)
			}
		}
	}
	login := func(h *harness, clientIP string) int {
		r := h.newReq("POST", "/api/v1/auth/login", `{"login":"admin","password":"`+adminPass+`"}`)
		r.Header.Set("CF-Connecting-IP", clientIP)
		return h.serve(r).Code
	}

	t.Run("behind the tunnel, an attacker blocks only their own address", func(t *testing.T) {
		h := newHarness(t, func(o *options) { o.trusted = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")} })
		h.admin()
		failures(h, func(int) string { return "198.51.100.66" })
		if got := login(h, "198.51.100.66"); got != http.StatusTooManyRequests {
			t.Errorf("the attacker's address: %d, want 429", got)
		}
		if got := login(h, "198.51.100.77"); got != http.StatusOK {
			t.Errorf("a different real client: %d, want 200: one attacker must not lock the administrator out", got)
		}
	})

	t.Run("without trusted proxies the header is ignored, so forging it buys nothing", func(t *testing.T) {
		h := newHarness(t)
		h.admin()
		failures(h, func(i int) string { return "198.51.100." + string(rune('1'+i)) }) // 5 IPs "diferentes"
		if got := login(h, "198.51.100.200"); got != http.StatusTooManyRequests {
			t.Errorf("forged addresses must not evade the limit: %d, want 429", got)
		}
	})
}

func TestAWarningIsLoggedWhenProxyHeadersArriveFromAnUntrustedPeer(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < 3; i++ {
		r := h.newReq("GET", "/healthz", "")
		r.Header.Set("CF-Connecting-IP", "198.51.100.66")
		h.serve(r)
	}
	logs := h.logs.b.String()
	if n := strings.Count(logs, "COBOGO_TRUSTED_PROXIES"); n != 1 {
		t.Errorf("the warning appeared %d times, want exactly once per hour:\n%s", n, logs)
	}

	trusted := newHarness(t, func(o *options) { o.trusted = []netip.Prefix{netip.MustParsePrefix("203.0.113.0/24")} })
	r := trusted.newReq("GET", "/healthz", "")
	r.Header.Set("CF-Connecting-IP", "198.51.100.66")
	trusted.serve(r)
	if strings.Contains(trusted.logs.b.String(), "COBOGO_TRUSTED_PROXIES") {
		t.Error("a correctly configured proxy must not trigger the warning")
	}
}

func TestRouterRefusesToStartWithoutWhatSecurityNeeds(t *testing.T) {
	h := newHarness(t)
	for name, o := range map[string]httpapi.Options{
		"no store":       {Log: nil, PublicURL: publicURL},
		"no hasher":      {Store: h.st, PublicURL: publicURL, Limiter: auth.NewLoginLimiter()},
		"no limiter":     {Store: h.st, PublicURL: publicURL, Hasher: h.hasher},
		"no public url":  {Store: h.st, Hasher: h.hasher, Limiter: auth.NewLoginLimiter()},
		"bad public url": {Store: h.st, PublicURL: "://sem-esquema", Hasher: h.hasher, Limiter: auth.NewLoginLimiter()},
	} {
		if _, err := httpapi.NewRouter(o); err == nil {
			t.Errorf("%s: NewRouter must refuse", name)
		}
	}
}
