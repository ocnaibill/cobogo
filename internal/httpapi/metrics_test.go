package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/store"
)

// Métricas (DEC-43): a API só lê; ausência de dado e dado antigo chegam como são.
func TestMetricsAPIReadsWhatTheCollectorWrote(t *testing.T) {
	h := newHarness(t)
	h.login()
	p := h.createProject("com-metricas")
	base := "/api/v1/projects/" + p.ID.String() + "/metrics"

	// Sem coletor: vazio, e a falta do coletor é dita (collector nulo), não inventada.
	rec := h.do("GET", base, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"collector":null`) || !strings.Contains(rec.Body.String(), `"containers":[]`) {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("metrics must not be cached")
	}
	for _, w := range []string{"2h", "abc", "1d"} {
		if rec := h.do("GET", base+"?window="+w, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("window %s: %d, want 400", w, rec.Code)
		}
	}

	now := time.Now().UTC()
	pid := p.ID
	cpu, mem, limit := 0.4, int64(64<<20), int64(512<<20)
	ctx := t.Context()
	for i := 0; i < 6; i++ {
		var c *float64
		var m *int64
		if i != 3 { // uma leitura sem valor no meio
			c, m = &cpu, &mem
		}
		if err := h.st.InsertSamples(ctx, now.Add(time.Duration(i-5)*5*time.Second), []store.MetricSample{{
			ContainerID: "ctr-api", ProjectID: &pid, Role: "app", Name: "cobogo-com-metricas-aabbccdd", State: "running", Restarts: 0,
			CPUCores: c, MemBytes: m, MemLimitBytes: &limit,
		}}); err != nil {
			t.Fatal(err)
		}
	}
	_ = h.st.Heartbeat(ctx, now, 700*time.Millisecond, 1, 0, "", 5*time.Second)

	var got struct {
		Window     string `json:"window"`
		Containers []struct {
			ContainerID string `json:"container_id"`
			Role        string `json:"role"`
			Stale       bool   `json:"stale"`
			Current     *struct {
				CPUCores *float64 `json:"cpu_cores"`
			} `json:"current"`
			Series []struct {
				CPUAvg *float64 `json:"cpu_avg"`
			} `json:"series"`
		} `json:"containers"`
		Collector *struct {
			Stale bool `json:"stale"`
		} `json:"collector"`
	}
	rec = h.do("GET", base+"?window=15m", "")
	if rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Window != "15m" || len(got.Containers) != 1 || got.Containers[0].ContainerID != "ctr-api" || got.Collector == nil || got.Collector.Stale {
		t.Fatalf("metrics = %s", rec.Body)
	}
	c := got.Containers[0]
	if len(c.Series) != 6 || c.Series[3].CPUAvg != nil || c.Series[0].CPUAvg == nil {
		t.Errorf("the reading with no value must be null, not zero: %+v", c.Series)
	}
	if strings.Contains(rec.Body.String(), "Config") || strings.Contains(rec.Body.String(), "Env") {
		t.Error("the Docker inspect object must never reach the browser")
	}

	// Outro dono não vê; o painel todo exige sessão.
	if rec := h.do("GET", "/api/v1/projects/"+uuid.NewString()+"/metrics", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d, want 404", rec.Code)
	}
	ov := h.do("GET", "/api/v1/metrics/overview", "")
	if ov.Code != http.StatusOK || !strings.Contains(ov.Body.String(), `"by_profile"`) || !strings.Contains(ov.Body.String(), `"running":1`) {
		t.Errorf("overview: %d %s", ov.Code, ov.Body)
	}
	h.cookie = nil
	for _, path := range []string{base, "/api/v1/metrics/overview"} {
		if rec := h.do("GET", path, ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", path, rec.Code)
		}
	}
}
