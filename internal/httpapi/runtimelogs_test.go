package httpapi_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ocnaibill/cobogo/internal/domain"
)

type logsBody struct {
	Lines []struct {
		ID     int64  `json:"id"`
		Origin string `json:"origin"`
		Stream string `json:"stream"`
		Line   string `json:"line"`
	} `json:"lines"`
	HasOlder       bool `json:"has_older"`
	HasMore        bool `json:"has_more"`
	RetentionHours int  `json:"retention_hours"`
	RetentionLines int  `json:"retention_lines"`
}

// Logs de execução (DEC-46): leitura por cursor, filtro de origem, validação e dono.
func TestRuntimeLogsAPIReadsByCursorAndValidates(t *testing.T) {
	h := newHarness(t)
	h.login()
	p := h.createProject("com-logs")
	base := "/api/v1/projects/" + p.ID.String() + "/logs"
	now := time.Now().UTC()

	empty := decode[logsBody](t, h.do("GET", base, ""))
	if len(empty.Lines) != 0 || empty.HasOlder || empty.RetentionHours != 24 || empty.RetentionLines != 20000 {
		t.Errorf("empty = %+v", empty)
	}

	var lines []domain.LogLine
	for i := 0; i < 12; i++ {
		origin := domain.LogOriginApp
		if i%4 == 3 {
			origin = domain.LogOriginPostgres
		}
		lines = append(lines, domain.LogLine{ProjectID: p.ID, ContainerID: "ctr", ContainerName: "cobogo-x", Origin: origin, Stream: domain.LogStdout,
			At: now.Add(time.Duration(i) * time.Millisecond), Line: "linha " + strconv.Itoa(i)})
	}
	if err := h.st.AppendRuntimeLogs(t.Context(), lines); err != nil {
		t.Fatal(err)
	}

	latest := decode[logsBody](t, h.do("GET", base+"?limit=5", ""))
	if len(latest.Lines) != 5 || !latest.HasOlder || latest.Lines[4].Line != "linha 11" || latest.Lines[0].Line != "linha 7" {
		t.Fatalf("latest = %+v", latest)
	}
	older := decode[logsBody](t, h.do("GET", base+"?limit=100&before="+strconv.FormatInt(latest.Lines[0].ID, 10), ""))
	if len(older.Lines) != 7 || older.HasOlder {
		t.Errorf("before = %d lines hasOlder=%v", len(older.Lines), older.HasOlder)
	}
	next := decode[logsBody](t, h.do("GET", base+"?limit=3&after="+strconv.FormatInt(older.Lines[0].ID, 10), ""))
	if len(next.Lines) != 3 || !next.HasMore || next.Lines[0].Line != "linha 1" {
		t.Errorf("after = %+v", next)
	}
	pg := decode[logsBody](t, h.do("GET", base+"?origin=postgres", ""))
	if len(pg.Lines) != 3 || pg.Lines[0].Origin != "postgres" {
		t.Errorf("origin = %+v", pg)
	}

	for name, query := range map[string]string{
		"bad origin":  "?origin=oracle",
		"bad after":   "?after=abc",
		"negative":    "?before=-1",
		"both":        "?after=1&before=5",
		"limit zero":  "?limit=0",
		"limit large": "?limit=501",
	} {
		if rec := h.do("GET", base+query, ""); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
	if rec := h.do("GET", "/api/v1/projects/00000000-0000-0000-0000-000000000000/logs", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown project: %d", rec.Code)
	}
	if rec := h.do("GET", base, ""); rec.Header().Get("Cache-Control") != "no-store" || !strings.Contains(rec.Body.String(), `"lines"`) {
		t.Errorf("headers/body: %v", rec.Header())
	}
}
