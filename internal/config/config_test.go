package config

import (
	"net/netip"
	"strings"
	"testing"
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
