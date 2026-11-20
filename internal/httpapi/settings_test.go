package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/auth"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/settings"
)

type settingView struct {
	Key     string  `json:"key"`
	Kind    string  `json:"kind"`
	Value   any     `json:"value"`
	Source  string  `json:"source"`
	Default any     `json:"default"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
}

func (h *harness) settingsByKey() map[string]settingView {
	h.t.Helper()
	rec := h.do("GET", "/api/v1/settings", "")
	if rec.Code != http.StatusOK {
		h.t.Fatalf("list settings: %d %s", rec.Code, rec.Body)
	}
	out := map[string]settingView{}
	for _, it := range decode[struct {
		Settings []settingView `json:"settings"`
	}](h.t, rec).Settings {
		out[it.Key] = it
	}
	return out
}

func TestSettingsListUpdateAndReset(t *testing.T) {
	h := newHarness(t)
	h.login()

	by := h.settingsByKey()
	if len(by) != len(settings.Defs) {
		t.Fatalf("listed %d settings, want %d", len(by), len(settings.Defs))
	}
	if m := by["app_memory_mb"]; m.Value != 1024.0 || m.Source != "default" || m.Min != 64 || m.Kind != "integer" {
		t.Errorf("app_memory_mb = %+v", m)
	}

	rec := h.do("PUT", "/api/v1/settings/app_memory_mb", `{"value":2048}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	if got := decode[settingView](t, rec); got.Value != 2048.0 || got.Source != "database" {
		t.Errorf("put response = %+v", got)
	}
	if m := h.settingsByKey()["app_memory_mb"]; m.Value != 2048.0 || m.Source != "database" || m.Default != 1024.0 {
		t.Errorf("after put = %+v", m)
	}

	rec = h.do("DELETE", "/api/v1/settings/app_memory_mb", "")
	if rec.Code != http.StatusOK || decode[settingView](t, rec).Source != "default" {
		t.Errorf("reset: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("DELETE", "/api/v1/settings/app_memory_mb", ""); rec.Code != http.StatusOK {
		t.Errorf("resetting twice must be harmless: %d", rec.Code)
	}

	var audited []string
	rows, err := h.pool.Query(context.Background(), `SELECT action || ' ' || target FROM audit_events WHERE action LIKE 'setting.%' ORDER BY at`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		audited = append(audited, s)
	}
	if strings.Join(audited, ",") != "setting.update app_memory_mb,setting.reset app_memory_mb,setting.reset app_memory_mb" {
		t.Errorf("audit = %v", audited)
	}
}

func TestSettingsValidation(t *testing.T) {
	h := newHarness(t)
	h.login()

	for name, c := range map[string]struct{ key, body string }{
		"too low":          {"app_memory_mb", `{"value":8}`},
		"too high":         {"app_memory_mb", `{"value":999999999}`},
		"as text":          {"app_memory_mb", `{"value":"2048"}`},
		"fraction":         {"app_memory_mb", `{"value":1024.5}`},
		"missing value":    {"app_memory_mb", `{}`},
		"null value":       {"app_memory_mb", `{"value":null}`},
		"extra field":      {"app_memory_mb", `{"value":2048,"outro":1}`},
		"cpus zero":        {"app_cpus", `{"value":0}`},
		"host with scheme": {"extra_git_hosts", `{"value":["https://git.exemplo.com"]}`},
		"host as text":     {"extra_git_hosts", `{"value":"git.exemplo.com"}`},
		"ip as host":       {"extra_git_hosts", `{"value":["10.0.0.1"]}`},
		"domain scheme":    {"apps_domain", `{"value":"https://cobogo.dev"}`},
		"domain as list":   {"apps_domain", `{"value":["cobogo.dev"]}`},
		"scheme unknown":   {"apps_scheme", `{"value":"ftp"}`},
		"not json":         {"app_memory_mb", `oi`},
	} {
		if rec := h.do("PUT", "/api/v1/settings/"+c.key, c.body); rec.Code != http.StatusBadRequest || decode[apiError](t, rec).Error.Code != "invalid_request" {
			t.Errorf("%s: %d %s, want 400 invalid_request", name, rec.Code, rec.Body)
		}
	}
	for _, key := range []string{"cookie_insecure", "public_url", "database_url", "COBOGO_SECRET_KEY", "x"} {
		if rec := h.do("PUT", "/api/v1/settings/"+key, `{"value":1}`); rec.Code != http.StatusNotFound {
			t.Errorf("%s is not editable: %d, want 404", key, rec.Code)
		}
		if rec := h.do("DELETE", "/api/v1/settings/"+key, ""); rec.Code != http.StatusNotFound {
			t.Errorf("reset %s: %d, want 404", key, rec.Code)
		}
	}
	if m := h.settingsByKey()["app_memory_mb"]; m.Source != "default" {
		t.Errorf("a rejected value must not be stored: %+v", m)
	}
}

// Um host extra vale na hora, sem reiniciar (DEC-30 item 6).
func TestExtraGitHostsTakeEffectImmediately(t *testing.T) {
	h := newHarness(t)
	h.login()
	create := func(slug string) *httptest.ResponseRecorder {
		return h.do("POST", "/api/v1/projects", `{"slug":"`+slug+`","repository_url":"https://git.exemplo.com/grupo/repo"}`)
	}

	if rec := create("antes"); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unlisted host must be refused: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("PUT", "/api/v1/settings/extra_git_hosts", `{"value":["Git.Exemplo.com"]}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if rec := create("depois"); rec.Code != http.StatusCreated {
		t.Fatalf("a host added in the panel must work at once: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("DELETE", "/api/v1/settings/extra_git_hosts", ""); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if rec := create("de-novo"); rec.Code != http.StatusBadRequest {
		t.Errorf("after the reset the host is refused again: %d", rec.Code)
	}
}

func TestProjectLimitsEndpoints(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	path := "/api/v1/projects/" + project + "/limits"

	type view struct {
		Overrides domain.Limits `json:"overrides"`
		Effective map[string]struct {
			Value  float64 `json:"value"`
			Source string  `json:"source"`
		} `json:"effective"`
	}

	v := decode[view](t, h.do("GET", path, ""))
	if v.Overrides != (domain.Limits{}) || v.Effective["app_memory_mb"].Value != 1024 || v.Effective["app_memory_mb"].Source != "default" {
		t.Errorf("initial = %+v", v)
	}

	// Global primeiro, depois o projeto sobrepõe só um campo.
	if rec := h.do("PUT", "/api/v1/settings/app_pids", `{"value":512}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	rec := h.do("PUT", path, `{"app_memory_mb":2048,"build_cpus":4}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("put limits: %d %s", rec.Code, rec.Body)
	}
	v = decode[view](t, rec)
	if v.Overrides.AppMemoryMB != 2048 || v.Overrides.BuildCPUs != 4 || v.Overrides.AppPids != 0 {
		t.Errorf("overrides = %+v", v.Overrides)
	}
	for key, want := range map[string]struct {
		value  float64
		source string
	}{
		"app_memory_mb": {2048, "project"}, "build_cpus": {4, "project"},
		"app_pids": {512, "database"}, "app_cpus": {1, "default"},
	} {
		if got := v.Effective[key]; got.Value != want.value || got.Source != want.source {
			t.Errorf("effective %s = %+v, want %+v", key, got, want)
		}
	}

	// O projeto também mostra suas sobreposições.
	if p := decode[domain.Project](t, h.do("GET", "/api/v1/projects/"+project, "")); p.Limits.AppMemoryMB != 2048 {
		t.Errorf("project.limits = %+v", p.Limits)
	}

	// Uma troca substitui tudo: `{}` limpa.
	v = decode[view](t, h.do("PUT", path, `{}`))
	if v.Overrides != (domain.Limits{}) || v.Effective["app_memory_mb"].Source != "default" {
		t.Errorf("cleared = %+v", v)
	}

	for name, body := range map[string]string{
		"too low":       `{"app_memory_mb":8}`,
		"negative":      `{"app_pids":-5}`,
		"as text":       `{"app_memory_mb":"2048"}`,
		"unknown field": `{"disco_mb":100}`,
		"a list":        `[1]`,
		"cpus too big":  `{"app_cpus":9999}`,
	} {
		if rec := h.do("PUT", path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s, want 400", name, rec.Code, rec.Body)
		}
	}
	if rec := h.do("GET", "/api/v1/projects/"+uuid.NewString()+"/limits", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d", rec.Code)
	}
	if rec := h.do("GET", "/api/v1/projects/xx/limits", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", rec.Code)
	}
}

func TestSettingsAndLimitsAreForTheirOwnerOnly(t *testing.T) {
	h := newHarness(t)
	h.login()
	project := h.project()
	if rec := h.do("PUT", "/api/v1/projects/"+project+"/limits", `{"app_memory_mb":2048}`); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}

	second := uuid.New()
	if _, err := h.pool.Exec(context.Background(), `INSERT INTO users (id, login, password_hash) VALUES ($1, 'segundo', 'x')`, second); err != nil {
		t.Fatal(err)
	}
	token, hash, _ := auth.NewSessionToken()
	if _, err := h.st.CreateSession(context.Background(), second, hash, time.Hour, "", ""); err != nil {
		t.Fatal(err)
	}
	h.cookie = &http.Cookie{Name: cookieSecure, Value: token}

	for _, c := range []struct{ method, body string }{{"GET", ""}, {"PUT", `{"app_memory_mb":128}`}} {
		if rec := h.do(c.method, "/api/v1/projects/"+project+"/limits", c.body); rec.Code != http.StatusNotFound {
			t.Errorf("%s another user's limits: %d, want 404", c.method, rec.Code)
		}
	}
	h.cookie = nil
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/settings", ""},
		{"PUT", "/api/v1/settings/app_pids", `{"value":512}`},
		{"DELETE", "/api/v1/settings/app_pids", ""},
		{"GET", "/api/v1/projects/" + project + "/limits", ""},
		{"PUT", "/api/v1/projects/" + project + "/limits", `{}`},
	} {
		if rec := h.do(c.method, c.path, c.body); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: %d, want 401", c.method, c.path, rec.Code)
		}
	}
}

func TestAppsRoutingSettings(t *testing.T) {
	h := newHarness(t)
	h.login()
	by := h.settingsByKey()
	if by["apps_domain"].Value != "" || by["apps_domain"].Source != "default" || by["apps_scheme"].Value != "https" {
		t.Errorf("defaults = %+v / %+v", by["apps_domain"], by["apps_scheme"])
	}
	if rec := h.do("PUT", "/api/v1/settings/apps_domain", `{"value":"Cobogo.DEV"}`); rec.Code != http.StatusOK || decode[settingView](t, rec).Value != "cobogo.dev" {
		t.Fatalf("put domain: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("PUT", "/api/v1/settings/apps_scheme", `{"value":"http"}`); rec.Code != http.StatusOK {
		t.Fatalf("put scheme: %d %s", rec.Code, rec.Body)
	}
	by = h.settingsByKey()
	if by["apps_domain"].Value != "cobogo.dev" || by["apps_domain"].Source != "database" || by["apps_scheme"].Value != "http" {
		t.Errorf("after put = %+v / %+v", by["apps_domain"], by["apps_scheme"])
	}
	// Um valor vazio desliga o roteamento e continua sendo do banco.
	if rec := h.do("PUT", "/api/v1/settings/apps_domain", `{"value":""}`); rec.Code != http.StatusOK || decode[settingView](t, rec).Source != "database" {
		t.Errorf("switching routing off: %d %s", rec.Code, rec.Body)
	}
}
