package store_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

func logLines(p domain.Project, container, origin string, from time.Time, n int) []domain.LogLine {
	out := make([]domain.LogLine, n)
	for i := range out {
		stream := domain.LogStdout
		if i%5 == 4 {
			stream = domain.LogStderr
		}
		out[i] = domain.LogLine{ProjectID: p.ID, ContainerID: container, ContainerName: "cobogo-" + container, Origin: origin, Stream: stream,
			At: from.Add(time.Duration(i) * time.Millisecond), Line: fmt.Sprintf("%s linha %d", container, i)}
	}
	return out
}

// Logs de execução (DEC-46): o cursor por id, a ordem, o filtro de origem, o dono
// e a retenção por idade, por quantidade e por projeto excluído.
func TestRuntimeLogsCursorOrderOwnerAndRetention(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	now := time.Now().UTC()

	if _, ok, err := st.LastRuntimeLogAt(ctx, "ctr-a"); err != nil || ok {
		t.Fatalf("a container without logs = %v, %v", ok, err)
	}
	if err := st.AppendRuntimeLogs(ctx, logLines(p, "ctr-a", domain.LogOriginApp, now.Add(-time.Minute), 10)); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendRuntimeLogs(ctx, logLines(p, "ctr-pg", domain.LogOriginPostgres, now.Add(-30*time.Second), 4)); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendRuntimeLogs(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if at, ok, err := st.LastRuntimeLogAt(ctx, "ctr-a"); err != nil || !ok || at.Before(now.Add(-time.Minute)) {
		t.Errorf("last = %v %v %v", at, ok, err)
	}

	// Sem cursor: as últimas, em ordem crescente.
	page, err := st.ListRuntimeLogsFor(ctx, p.ID, own.ID, "", 0, 0, 6)
	if err != nil || len(page.Lines) != 6 || !page.HasOlder || page.HasMore {
		t.Fatalf("latest = %+v, %v", page, err)
	}
	for i := 1; i < len(page.Lines); i++ {
		if page.Lines[i].ID <= page.Lines[i-1].ID {
			t.Fatalf("not ascending: %+v", page.Lines)
		}
	}
	if last := page.Lines[5]; last.Origin != domain.LogOriginPostgres || last.Line != "ctr-pg linha 3" {
		t.Errorf("last line = %+v", last)
	}

	// before: as imediatamente anteriores; after: as seguintes, com HasMore.
	older, _ := st.ListRuntimeLogsFor(ctx, p.ID, own.ID, "", 0, page.Lines[0].ID, 100)
	if len(older.Lines) != 8 || older.HasOlder || older.Lines[len(older.Lines)-1].ID != page.Lines[0].ID-1 {
		t.Errorf("before = %d lines, hasOlder=%v", len(older.Lines), older.HasOlder)
	}
	first := older.Lines[0].ID
	next, _ := st.ListRuntimeLogsFor(ctx, p.ID, own.ID, "", first, 0, 5)
	if len(next.Lines) != 5 || !next.HasMore || next.Lines[0].ID != first+1 {
		t.Errorf("after = %d lines, hasMore=%v", len(next.Lines), next.HasMore)
	}
	rest, _ := st.ListRuntimeLogsFor(ctx, p.ID, own.ID, "", page.Lines[5].ID, 0, 5)
	if len(rest.Lines) != 0 || rest.HasMore {
		t.Errorf("nothing after the last line: %+v", rest)
	}

	// Filtro de origem, e a pergunta de linhas anteriores respeita o filtro.
	pg, _ := st.ListRuntimeLogsFor(ctx, p.ID, own.ID, domain.LogOriginPostgres, 0, 0, 100)
	if len(pg.Lines) != 4 || pg.HasOlder {
		t.Errorf("postgres only = %d lines, hasOlder=%v", len(pg.Lines), pg.HasOlder)
	}

	// Só o dono lê.
	if _, err := st.ListRuntimeLogsFor(ctx, p.ID, uuid.New(), "", 0, 0, 10); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner = %v, want ErrNotFound", err)
	}

	// Retenção por idade: uma linha de ontem sai; as de agora ficam.
	if err := st.AppendRuntimeLogs(ctx, logLines(p, "ctr-old", domain.LogOriginApp, now.Add(-48*time.Hour), 3)); err != nil {
		t.Fatal(err)
	}
	if n, err := st.PruneRuntimeLogs(ctx, domain.LogRetention, 1000); err != nil || n != 3 {
		t.Fatalf("age prune = %d, %v", n, err)
	}
	// Por quantidade: fica só o final.
	if n, err := st.PruneRuntimeLogs(ctx, domain.LogRetention, 5); err != nil || n != 9 {
		t.Fatalf("count prune = %d, %v", n, err)
	}
	left, _ := st.ListRuntimeLogsFor(ctx, p.ID, own.ID, "", 0, 0, 100)
	if len(left.Lines) != 5 || left.Lines[4].Line != "ctr-pg linha 3" {
		t.Errorf("after the count prune = %+v", left.Lines)
	}

	// Projeto excluído perde os logs na varredura.
	if _, err := st.DeleteProject(ctx, p.ID, own.ID, p.Slug, false); err != nil {
		t.Fatal(err)
	}
	if n, err := st.PruneRuntimeLogs(ctx, domain.LogRetention, 1000); err != nil || n != 5 {
		t.Fatalf("deleted project prune = %d, %v", n, err)
	}
}
