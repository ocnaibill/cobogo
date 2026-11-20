package config

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

const goodKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=" // 32 bytes

func setEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for _, k := range []string{"DATABASE_URL", "COBOGO_SECRET_KEY", "COBOGO_PUBLIC_URL", "COBOGO_TRUSTED_PROXIES", "COBOGO_EXTRA_GIT_HOSTS", "COBOGO_COOKIE_INSECURE", "API_ADDR"} {
		t.Setenv(k, "")
	}
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoadReadsEverything(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL": "postgres://x", "COBOGO_SECRET_KEY": goodKey,
		"COBOGO_PUBLIC_URL":      "https://cobogo.example.com/",
		"COBOGO_TRUSTED_PROXIES": "172.18.0.0/16, 10.1.2.3 ,fd00::/8",
		"COBOGO_EXTRA_GIT_HOSTS": "git.example.com, gitea.internal",
	})
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.PublicURL != "https://cobogo.example.com" {
		t.Errorf("PublicURL = %q (trailing slash must go)", c.PublicURL)
	}
	want := []netip.Prefix{netip.MustParsePrefix("172.18.0.0/16"), netip.MustParsePrefix("10.1.2.3/32"), netip.MustParsePrefix("fd00::/8")}
	if len(c.TrustedProxies) != 3 || c.TrustedProxies[0] != want[0] || c.TrustedProxies[1] != want[1] || c.TrustedProxies[2] != want[2] {
		t.Errorf("TrustedProxies = %v", c.TrustedProxies)
	}
	if len(c.ExtraGitHosts) != 2 || c.ExtraGitHosts[1] != "gitea.internal" {
		t.Errorf("ExtraGitHosts = %v", c.ExtraGitHosts)
	}
	if err := c.ValidateForAPI(); err != nil {
		t.Errorf("ValidateForAPI: %v", err)
	}
}

func TestNoTrustedProxiesByDefault(t *testing.T) {
	setEnv(t, map[string]string{"DATABASE_URL": "postgres://x", "COBOGO_SECRET_KEY": goodKey})
	c, err := Load()
	if err != nil || len(c.TrustedProxies) != 0 {
		t.Errorf("trusted proxies = %v, %v: nothing is trusted unless configured", c.TrustedProxies, err)
	}
}

func TestLoadRefusesBadValues(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"no database":      {"COBOGO_SECRET_KEY": goodKey},
		"no key":           {"DATABASE_URL": "x"},
		"bad key":          {"DATABASE_URL": "x", "COBOGO_SECRET_KEY": "curta"},
		"bad proxy":        {"DATABASE_URL": "x", "COBOGO_SECRET_KEY": goodKey, "COBOGO_TRUSTED_PROXIES": "nao-e-ip"},
		"bad proxy prefix": {"DATABASE_URL": "x", "COBOGO_SECRET_KEY": goodKey, "COBOGO_TRUSTED_PROXIES": "10.0.0.0/99"},
	} {
		setEnv(t, env)
		if _, err := Load(); err == nil {
			t.Errorf("%s: Load must fail", name)
		}
	}
}

func TestValidateForAPI(t *testing.T) {
	for name, c := range map[string]Config{
		"missing":             {},
		"path":                {PublicURL: "https://x.com/painel"},
		"credentials":         {PublicURL: "https://u:p@x.com"},
		"other scheme":        {PublicURL: "ftp://x.com"},
		"http without opt-in": {PublicURL: "http://localhost:8080"},
	} {
		if err := c.ValidateForAPI(); err == nil {
			t.Errorf("%s: must be refused", name)
		}
	}
	for name, c := range map[string]Config{
		"https":         {PublicURL: "https://cobogo.example.com"},
		"http dev":      {PublicURL: "http://localhost:8080", CookieInsecure: true},
		"https w/ port": {PublicURL: "https://cobogo.example.com:8443"},
	} {
		if err := c.ValidateForAPI(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	err := Config{PublicURL: "http://localhost:8080"}.ValidateForAPI()
	if err == nil || !strings.Contains(err.Error(), "COBOGO_COOKIE_INSECURE") {
		t.Errorf("the error must say how to opt in for development: %v", err)
	}
}

func TestLoadLimits(t *testing.T) {
	limitVars := []string{"COBOGO_APP_MEMORY_MB", "COBOGO_APP_CPUS", "COBOGO_APP_PIDS", "COBOGO_BUILD_MEMORY_MB", "COBOGO_BUILD_CPUS"}
	base := map[string]string{"DATABASE_URL": "postgres://x", "COBOGO_SECRET_KEY": goodKey}
	load := func(extra map[string]string) (Config, error) {
		setEnv(t, base)
		for _, k := range limitVars {
			t.Setenv(k, "")
		}
		for k, v := range extra {
			t.Setenv(k, v)
		}
		return Load()
	}

	c, err := load(nil)
	if err != nil || c.Limits != (domain.Limits{}) {
		t.Fatalf("no variables must leave the zero value (the adapter's default applies): %+v, %v", c.Limits, err)
	}

	c, err = load(map[string]string{
		"COBOGO_APP_MEMORY_MB": "1024", "COBOGO_APP_CPUS": "0.5", "COBOGO_APP_PIDS": "512",
		"COBOGO_BUILD_MEMORY_MB": "4096", "COBOGO_BUILD_CPUS": "4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (domain.Limits{AppMemoryMB: 1024, AppCPUs: 0.5, AppPids: 512, BuildMemoryMB: 4096, BuildCPUs: 4}); c.Limits != want {
		t.Errorf("Limits = %+v, want %+v", c.Limits, want)
	}

	for name, extra := range map[string]map[string]string{
		"memory too low":       {"COBOGO_APP_MEMORY_MB": "8"},
		"memory not a number":  {"COBOGO_APP_MEMORY_MB": "muita"},
		"negative pids":        {"COBOGO_APP_PIDS": "-1"},
		"pids too low":         {"COBOGO_APP_PIDS": "2"},
		"cpus zero":            {"COBOGO_APP_CPUS": "0"},
		"cpus NaN":             {"COBOGO_APP_CPUS": "NaN"},
		"build memory low":     {"COBOGO_BUILD_MEMORY_MB": "100"},
		"build cpus text":      {"COBOGO_BUILD_CPUS": "dois"},
		"extra host with port": {"COBOGO_EXTRA_GIT_HOSTS": "git.exemplo.com:8080"},
		"extra host is an IP":  {"COBOGO_EXTRA_GIT_HOSTS": "10.0.0.1"},
	} {
		if _, err := load(extra); err == nil || !strings.Contains(err.Error(), "COBOGO_") {
			t.Errorf("%s: err = %v, want an error naming the variable", name, err)
		}
	}
}

func TestLoadAppsRoutingDefaults(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x", "COBOGO_SECRET_KEY": goodKey}
	setEnv(t, base)
	t.Setenv("COBOGO_APPS_DOMAIN", " Cobogo.DEV ")
	t.Setenv("COBOGO_APPS_SCHEME", "HTTP")
	c, err := Load()
	if err != nil || c.AppsDomain != "cobogo.dev" || c.AppsScheme != "http" {
		t.Fatalf("Load = %q %q, %v", c.AppsDomain, c.AppsScheme, err)
	}
	for name, kv := range map[string][2]string{
		"domain with scheme": {"COBOGO_APPS_DOMAIN", "https://cobogo.dev"},
		"scheme unknown":     {"COBOGO_APPS_SCHEME", "ftp"},
	} {
		setEnv(t, base)
		t.Setenv("COBOGO_APPS_DOMAIN", "")
		t.Setenv("COBOGO_APPS_SCHEME", "")
		t.Setenv(kv[0], kv[1])
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), kv[0]) {
			t.Errorf("%s: err = %v, want an error naming %s", name, err, kv[0])
		}
	}
}

func TestLoadRouterSettings(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x", "COBOGO_SECRET_KEY": goodKey}
	load := func(addr, token string) (Config, error) {
		setEnv(t, base)
		t.Setenv("COBOGO_ROUTER_ADDR", addr)
		t.Setenv("COBOGO_ROUTER_TOKEN", token)
		return Load()
	}

	c, err := load("", "")
	if err != nil || c.RouterAddr != ":8081" || c.RouterToken != "" {
		t.Fatalf("defaults = %q %q, %v: no token means the listener stays off", c.RouterAddr, c.RouterToken, err)
	}
	c, err = load("10.0.0.5:9000", "  "+strings.Repeat("t", 32)+"\n")
	if err != nil || c.RouterAddr != "10.0.0.5:9000" || c.RouterToken != strings.Repeat("t", 32) {
		t.Errorf("set = %q %q, %v", c.RouterAddr, c.RouterToken, err)
	}
	if _, err := load("", strings.Repeat("t", 23)); err == nil || !strings.Contains(err.Error(), "COBOGO_ROUTER_TOKEN") {
		t.Errorf("a short token must be refused, naming the variable: %v", err)
	}
}
