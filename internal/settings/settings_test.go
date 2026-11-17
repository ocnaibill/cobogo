package settings

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

type fakeDB struct {
	values map[string]string
	err    error
}

func (f fakeDB) ListSettings(context.Context) (map[string]string, error) { return f.values, f.err }

var ctx = context.Background()

func TestNormalizeRejectsOutOfRangeAndNonsense(t *testing.T) {
	for _, c := range []struct {
		key, raw string
		ok       bool
		want     string
	}{
		{AppMemoryMB, "1024", true, "1024"},
		{AppMemoryMB, " 2048 ", true, "2048"},
		{AppMemoryMB, "63", false, ""},
		{AppMemoryMB, "262145", false, ""},
		{AppMemoryMB, "1.5", false, ""},
		{AppMemoryMB, "muita", false, ""},
		{AppMemoryMB, "", false, ""},
		{AppPids, "15", false, ""},
		{AppPids, "16", true, "16"},
		{AppCPUs, "0.5", true, "0.5"},
		{AppCPUs, "2.0", true, "2"},
		{AppCPUs, "0.05", false, ""},
		{AppCPUs, "NaN", false, ""},
		{AppCPUs, "Inf", false, ""},
		{BuildMemoryMB, "255", false, ""},
		{BuildMemoryMB, "4096", true, "4096"},
		{BuildCPUs, "257", false, ""},
		{ExtraGitHosts, "", true, ""},
		{ExtraGitHosts, "Git.Exemplo.com, gitea.internal", true, "git.exemplo.com,gitea.internal"},
		{ExtraGitHosts, "a.com,A.com", true, "a.com"},
		{ExtraGitHosts, "git.exemplo.com:8080", false, ""},
		{ExtraGitHosts, "https://git.exemplo.com", false, ""},
		{ExtraGitHosts, "git.exemplo.com/repo", false, ""},
		{ExtraGitHosts, "10.0.0.1", false, ""},
		{ExtraGitHosts, "::1", false, ""},
		{ExtraGitHosts, "localhost", false, ""},
		{ExtraGitHosts, "-ruim.com", false, ""},
		{ExtraGitHosts, "user@git.com", false, ""},
	} {
		d, _ := Lookup(c.key)
		got, err := d.Normalize(c.raw)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%s %q = %q, %v; want ok=%v %q", c.key, c.raw, got, err, c.ok, c.want)
		}
		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%s %q: the error must wrap ErrInvalid: %v", c.key, c.raw, err)
		}
	}

	var many []string
	for i := 0; i < 21; i++ {
		many = append(many, "h"+string(rune('a'+i))+".exemplo.com")
	}
	d, _ := Lookup(ExtraGitHosts)
	raw, _ := json.Marshal(many)
	if _, err := d.FromJSON(raw); err == nil {
		t.Error("more than 20 hosts must be refused")
	}
}

func TestFromJSONIsStrictAboutTypes(t *testing.T) {
	mem, _ := Lookup(AppMemoryMB)
	hosts, _ := Lookup(ExtraGitHosts)
	for _, c := range []struct {
		def  Def
		raw  string
		ok   bool
		want string
	}{
		{mem, `1024`, true, "1024"},
		{mem, `"1024"`, false, ""},
		{mem, `1024.5`, false, ""},
		{mem, `1e3`, false, ""},
		{mem, `true`, false, ""},
		{mem, `[1024]`, false, ""},
		{mem, `{"a":1}`, false, ""},
		{mem, `10`, false, ""},
		{hosts, `["a.exemplo.com","B.exemplo.com"]`, true, "a.exemplo.com,b.exemplo.com"},
		{hosts, `[]`, true, ""},
		{hosts, `"a.exemplo.com"`, false, ""},
		{hosts, `[1]`, false, ""},
		{hosts, `["a.exemplo.com:80"]`, false, ""},
	} {
		got, err := c.def.FromJSON(json.RawMessage(c.raw))
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("%s %s = %q, %v; want ok=%v %q", c.def.Key, c.raw, got, err, c.ok, c.want)
		}
	}
}

func TestParseEnv(t *testing.T) {
	if got, err := ParseEnv(AppMemoryMB, ""); got != "" || err != nil {
		t.Errorf("empty = %q, %v", got, err)
	}
	if got, err := ParseEnv(AppMemoryMB, " 2048 "); got != "2048" || err != nil {
		t.Errorf("valid = %q, %v", got, err)
	}
	if _, err := ParseEnv(AppMemoryMB, "8"); err == nil || !errors.Is(err, ErrInvalid) || len(err.Error()) < 20 {
		t.Errorf("out of range = %v", err)
	}
	if err := func() error { _, e := ParseEnv(AppMemoryMB, "8"); return e }(); err == nil || err.Error()[:len("COBOGO_APP_MEMORY_MB")] != "COBOGO_APP_MEMORY_MB" {
		t.Errorf("the error must name the environment variable: %v", err)
	}
}

// Precedência por campo: projeto, banco, ambiente, padrão (DEC-30 item 3).
func TestPrecedenceProjectDatabaseEnvDefault(t *testing.T) {
	env := Env{Limits: domain.Limits{AppMemoryMB: 2048, AppPids: 512}}
	db := fakeDB{values: map[string]string{AppMemoryMB: "3072", AppCPUs: "2"}}
	r := NewResolver(db, env)

	eff, err := r.EffectiveLimits(ctx, domain.Limits{AppMemoryMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Effective{
		AppMemoryMB:   {128, SourceProject},  // projeto vence o banco
		AppCPUs:       {2, SourceDatabase},   // banco vence o padrão
		AppPids:       {512, SourceEnv},      // ambiente vence o padrão
		BuildMemoryMB: {4096, SourceDefault}, // ninguém definiu
		BuildCPUs:     {2, SourceDefault},
	}
	for k, w := range want {
		if eff[k] != w {
			t.Errorf("%s = %+v, want %+v", k, eff[k], w)
		}
	}

	// Sem sobreposição do projeto, o banco vale.
	got, err := r.Limits(ctx, domain.Limits{})
	if err != nil || got != (domain.Limits{AppMemoryMB: 3072, AppCPUs: 2, AppPids: 512, BuildMemoryMB: 4096, BuildCPUs: 2}) {
		t.Errorf("Limits = %+v, %v", got, err)
	}

	// Sem banco, só ambiente e padrão.
	got, _ = NewResolver(nil, env).Limits(ctx, domain.Limits{})
	if got.AppMemoryMB != 2048 || got.AppPids != 512 || got.AppCPUs != 1 {
		t.Errorf("without a database = %+v", got)
	}

	// Tudo vazio dá os padrões de fábrica.
	if got, _ := NewResolver(nil, Env{}).Limits(ctx, domain.Limits{}); got != domain.DefaultLimits() {
		t.Errorf("defaults = %+v, want %+v", got, domain.DefaultLimits())
	}
}

func TestResolverPropagatesDatabaseErrors(t *testing.T) {
	r := NewResolver(fakeDB{err: errors.New("banco fora")}, Env{})
	if _, err := r.Limits(ctx, domain.Limits{}); err == nil {
		t.Error("Limits must fail when the settings cannot be read, not fall back silently")
	}
	if _, err := r.GitHosts(ctx); err == nil {
		t.Error("GitHosts must fail when the settings cannot be read")
	}
	if _, err := r.List(ctx); err == nil {
		t.Error("List must fail when the settings cannot be read")
	}
}

func TestGitHostsAddsExtrasToTheDefaults(t *testing.T) {
	has := func(hosts []string, h string) bool { return slices.Contains(hosts, h) }

	hosts, err := NewResolver(nil, Env{}).GitHosts(ctx)
	if err != nil || !has(hosts, "github.com") || !has(hosts, "git.sr.ht") || len(hosts) != 5 {
		t.Errorf("defaults = %v, %v", hosts, err)
	}
	hosts, _ = NewResolver(nil, Env{GitHosts: []string{"gitea.interno.com"}}).GitHosts(ctx)
	if !has(hosts, "gitea.interno.com") || !has(hosts, "github.com") {
		t.Errorf("env extras = %v", hosts)
	}
	// O banco sobrepõe o ambiente, inclusive com uma lista vazia.
	hosts, _ = NewResolver(fakeDB{values: map[string]string{ExtraGitHosts: "git.outro.com"}}, Env{GitHosts: []string{"gitea.interno.com"}}).GitHosts(ctx)
	if has(hosts, "gitea.interno.com") || !has(hosts, "git.outro.com") || !has(hosts, "github.com") {
		t.Errorf("database extras = %v", hosts)
	}
	hosts, _ = NewResolver(fakeDB{values: map[string]string{ExtraGitHosts: ""}}, Env{GitHosts: []string{"gitea.interno.com"}}).GitHosts(ctx)
	if has(hosts, "gitea.interno.com") || len(hosts) != 5 {
		t.Errorf("an empty stored list must clear the environment's extras: %v", hosts)
	}
}

func TestListShowsValueSourceDefaultAndRange(t *testing.T) {
	r := NewResolver(fakeDB{values: map[string]string{AppCPUs: "0.5", ExtraGitHosts: "a.exemplo.com"}}, Env{Limits: domain.Limits{AppPids: 300}})
	items, err := r.List(ctx)
	if err != nil || len(items) != len(Defs) {
		t.Fatalf("items = %d, %v", len(items), err)
	}
	by := map[string]Item{}
	for _, it := range items {
		by[it.Key] = it
	}
	if it := by[AppCPUs]; it.Value != 0.5 || it.Source != SourceDatabase || it.Default != 1.0 || it.Min == nil || *it.Min != 0.1 || *it.Max != 256 {
		t.Errorf("app_cpus = %+v", it)
	}
	if it := by[AppPids]; it.Value != 300 || it.Source != SourceEnv || it.Default != 256 {
		t.Errorf("app_pids = %+v", it)
	}
	if it := by[AppMemoryMB]; it.Value != 1024 || it.Source != SourceDefault {
		t.Errorf("app_memory_mb = %+v", it)
	}
	if it := by[ExtraGitHosts]; !slices.Equal(it.Value.([]string), []string{"a.exemplo.com"}) || it.Source != SourceDatabase || it.Min != nil {
		t.Errorf("extra_git_hosts = %+v", it)
	}
	if it := by[BuildCPUs]; it.Kind != KindFloat {
		t.Errorf("build_cpus kind = %s", it.Kind)
	}
}

func TestValidateLimits(t *testing.T) {
	if err := ValidateLimits(domain.Limits{}); err != nil {
		t.Errorf("empty = %v", err)
	}
	if err := ValidateLimits(domain.Limits{AppMemoryMB: 2048, AppCPUs: 0.5, AppPids: 64, BuildMemoryMB: 8192, BuildCPUs: 4}); err != nil {
		t.Errorf("valid = %v", err)
	}
	for name, l := range map[string]domain.Limits{
		"memory low":     {AppMemoryMB: 8},
		"memory high":    {AppMemoryMB: 1 << 30},
		"negative":       {AppMemoryMB: -1},
		"negative cpus":  {AppCPUs: -0.5},
		"cpus too small": {AppCPUs: 0.01},
		"pids low":       {AppPids: 2},
		"build low":      {BuildMemoryMB: 100},
		"build cpus":     {BuildCPUs: 1000},
	} {
		if err := ValidateLimits(l); err == nil || !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v, want ErrInvalid", name, err)
		}
	}
}
