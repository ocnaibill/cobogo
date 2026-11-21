package collector

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type fakeSampler struct {
	samples []dockerrt.Sample
	errs    []error
}

func (f *fakeSampler) SampleManaged(context.Context, int) ([]dockerrt.Sample, []error) {
	return f.samples, f.errs
}

func f64(v float64) *float64 { return &v }
func i64(v int64) *int64     { return &v }

func TestSlugOfReadsTheApplicationContainerName(t *testing.T) {
	for name, want := range map[string]string{
		"cobogo-caderno-54cf4b71":       "caderno",
		"cobogo-meu-app-teste-0a1b2c3d": "meu-app-teste",
		"cobogo-pg-522f172e4693":        "", // banco: sem sufixo de deploy de 8 caracteres
		"postgres":                      "",
		"cobogo-x-1":                    "",
		"":                              "",
	} {
		if got := slugOf(name); got != want {
			t.Errorf("slugOf(%q) = %q, want %q", name, got, want)
		}
	}
}

// Uma rodada grava as amostras e o batimento; a rodada seguinte, num minuto novo,
// fecha o agregado. Ausência de leitura fica nula.
func TestRoundWritesSamplesHeartbeatAndRollup(t *testing.T) {
	ctx := context.Background()
	st := storetest.New(t)
	pid, dep := uuid.New(), uuid.New()
	fs := &fakeSampler{samples: []dockerrt.Sample{
		{ContainerID: "c1", Name: "cobogo-site-aabbccdd", ProjectID: pid.String(), DeploymentID: dep.String(), Role: "app", State: "running", CPUCores: f64(0.2), MemBytes: i64(50 << 20), MemLimitBytes: i64(1 << 30)},
		{ContainerID: "c2", Name: "cobogo-pg-aabbccddeeff", ProjectID: pid.String(), Role: "service", Service: "postgres", State: "exited"},
	}, errs: []error{io.ErrUnexpectedEOF}}
	c := New(st, fs, quiet, Config{Interval: 5 * time.Second})

	c.Round(ctx)
	status, err := st.GetCollectorStatus(ctx, time.Now())
	if err != nil || status == nil || status.Containers != 2 || status.Errors != 1 || status.LastError == "" || status.IntervalMS != 5000 {
		t.Fatalf("heartbeat = %+v, %v", status, err)
	}
	m, err := st.ProjectMetrics(ctx, pid, "15m", time.Now())
	if err != nil || len(m.Containers) != 2 {
		t.Fatalf("metrics = %+v, %v", m.Containers, err)
	}
	for _, ct := range m.Containers {
		switch ct.ContainerID {
		case "c1":
			if ct.Current.CPUCores == nil || *ct.Current.CPUCores != 0.2 || ct.Role != "app" {
				t.Errorf("c1 = %+v", ct)
			}
		case "c2":
			if ct.Current.CPUCores != nil || ct.Current.MemBytes != nil || ct.Role != "service" || ct.State != "exited" {
				t.Errorf("a stopped container must have no reading: %+v", ct)
			}
		}
	}
	if got := c.slugs["site"]; got != pid {
		t.Errorf("the slug must be learned from the container: %v", c.slugs)
	}
}

// A sonda lê as rotas, faz a requisição pelo Traefik com o Host certo e
// classifica o resultado; uma aplicação parada não é sondada.
func TestProbeRoundClassifiesWhatTheVisitorWouldSee(t *testing.T) {
	ctx := context.Background()
	st := storetest.New(t)
	pids := map[string]uuid.UUID{"ok": uuid.New(), "redir": uuid.New(), "missing": uuid.New(), "broken": uuid.New(), "paused": uuid.New()}

	seen := map[string]int{}
	traefik := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.Host]++
		switch r.Host {
		case "ok.cobogo.test":
			w.WriteHeader(200)
		case "redir.cobogo.test":
			http.Redirect(w, r, "/login", http.StatusFound)
		case "missing.cobogo.test":
			w.WriteHeader(404)
		default:
			w.WriteHeader(502)
		}
	}))
	defer traefik.Close()

	routes := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer segredo" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		cfg := map[string]any{"http": map[string]any{
			"routers": map[string]any{
				"cobogo_panel":   map[string]any{"rule": "Host(`painel.cobogo.test`)", "service": "cobogo_panel"},
				"cobogo-ok":      map[string]any{"rule": "Host(`ok.cobogo.test`)", "service": "cobogo-ok"},
				"cobogo-redir":   map[string]any{"rule": "Host(`redir.cobogo.test`)", "service": "cobogo-redir"},
				"cobogo-missing": map[string]any{"rule": "Host(`missing.cobogo.test`)", "service": "cobogo-missing"},
				"cobogo-broken":  map[string]any{"rule": "Host(`broken.cobogo.test`)", "service": "cobogo-broken"},
				"cobogo-paused":  map[string]any{"rule": "Host(`paused.cobogo.test`)", "service": "cobogo-paused"},
				"cobogo-unknown": map[string]any{"rule": "Host(`unknown.cobogo.test`)", "service": "cobogo-unknown"},
			},
			"services": map[string]any{
				"cobogo_panel":   svc("http://api:8080"),
				"cobogo-ok":      svc("http://cobogo-ok-11111111:8080"),
				"cobogo-redir":   svc("http://cobogo-redir-11111111:8080"),
				"cobogo-missing": svc("http://cobogo-missing-11111111:8080"),
				"cobogo-broken":  svc("http://cobogo-broken-11111111:8080"),
				"cobogo-paused":  svc("http://api:8080"), // parada: aponta para a API (DEC-40)
				"cobogo-unknown": svc("http://x:8080"),
			},
		}}
		_ = json.NewEncoder(w).Encode(cfg)
	}))
	defer routes.Close()

	c := New(st, &fakeSampler{}, quiet, Config{Probe: ProbeConfig{RoutesURL: routes.URL, Token: "segredo", TraefikURL: traefik.URL}})
	for slug, id := range pids {
		c.slugs[slug] = id
	}
	c.ProbeRound(ctx)

	want := map[string]string{"ok": "responded", "redir": "responded", "missing": "client_error", "broken": "failed", "paused": "stopped"}
	ov, err := st.MetricsOverview(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// A visão geral só conta projetos com projeto vivo; confira direto pelas séries.
	for slug, outcome := range want {
		view := probeOf(t, st, pids[slug])
		if view == nil || view.Outcome != outcome {
			t.Errorf("%s: probe = %+v, want %s", slug, view, outcome)
		}
	}
	if seen["paused.cobogo.test"] != 0 {
		t.Error("a stopped application must not be probed")
	}
	if seen["unknown.cobogo.test"] != 0 {
		t.Error("a route with no sampled container (no project id) must be skipped")
	}
	if seen["ok.cobogo.test"] != 1 {
		t.Errorf("the probe must reach the app through Traefik with its Host: %v", seen)
	}
	_ = ov

	// Rotas ilegíveis (token errado) só pulam a rodada.
	bad := New(st, &fakeSampler{}, quiet, Config{Probe: ProbeConfig{RoutesURL: routes.URL, Token: "errado", TraefikURL: traefik.URL}})
	bad.ProbeRound(ctx)
}

func svc(url string) map[string]any {
	return map[string]any{"loadBalancer": map[string]any{"servers": []map[string]string{{"url": url}}}}
}

func probeOf(t *testing.T, st *store.Store, pid uuid.UUID) *store.ProbeView {
	t.Helper()
	m, err := st.ProjectMetrics(context.Background(), pid, "15m", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return m.Probe
}
