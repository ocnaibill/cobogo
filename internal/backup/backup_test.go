package backup

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func file(when, label string) File {
	t := at(when)
	return File{Name: Name(t, label), At: t, Label: label}
}

func names(files []File) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Name
	}
	sort.Strings(out)
	return out
}

func TestNamesRoundTripAndRefuseAnythingElse(t *testing.T) {
	n := Name(at("2026-11-22 03:00"), "pre-f5379cc")
	if n != "cobogo-core-20261122-030000-pre-f5379cc.dump" {
		t.Fatalf("name = %s", n)
	}
	when, label, ok := ParseName(n)
	if !ok || !when.Equal(at("2026-11-22 03:00")) || label != "pre-f5379cc" {
		t.Errorf("parse = %v %q %v", when, label, ok)
	}
	for _, bad := range []string{"", "status.json", "../cobogo-core-20261122-030000.dump", "cobogo-core-20261122-030000.dump/../x", "cobogo-core-2026-030000.dump",
		"cobogo-core-20261122-030000-Maiusc.dump", "cobogo-core-20261122-030000.dump.sha256", "/etc/passwd", "cobogo-core-20261122-030000-a b.dump"} {
		if ValidName(bad) {
			t.Errorf("%q must not be a valid backup name", bad)
		}
	}
	if !ValidLabel("") || !ValidLabel("pre-abc") || ValidLabel("a/b") || ValidLabel("A") || ValidLabel(strings.Repeat("a", 60)) {
		t.Error("label validation")
	}
}

// A retenção guarda o mais novo de cada dia (7 dias) e de cada semana (4), os
// rótulos recentes, e nunca o mais novo de todos.
func TestPlanKeepsDailyWeeklyLabeledAndNeverTheNewest(t *testing.T) {
	now := at("2026-11-22 10:00") // domingo
	var files []File
	// Dois por dia nos últimos 35 dias: às 03:00 e às 15:00.
	for d := 0; d < 35; d++ {
		day := now.AddDate(0, 0, -d).Format("2006-01-02")
		files = append(files, file(day+" 03:00", ""), file(day+" 15:00", ""))
	}
	for i := 0; i < 12; i++ {
		files = append(files, file(now.AddDate(0, 0, -i).Format("2006-01-02")+" 09:00", "pre-x"+string(rune('a'+i))))
	}
	keep, remove := Plan(files, now, 7, 4)
	if len(keep)+len(remove) != len(files) {
		t.Fatalf("keep+remove = %d, want %d", len(keep)+len(remove), len(files))
	}
	kept := map[string]bool{}
	for _, f := range keep {
		kept[f.Name] = true
	}
	// Um por dia nos 7 últimos dias, sempre o mais novo do dia (15:00).
	for d := 0; d < 7; d++ {
		day := now.AddDate(0, 0, -d).Format("2006-01-02")
		if !kept[Name(at(day+" 15:00"), "")] || kept[Name(at(day+" 03:00"), "")] && d > 0 {
			// 03:00 só fica se for o mais novo da semana, o que nunca é com 15:00 no mesmo dia
			t.Errorf("day %s: 15:00 must be kept (%v)", day, kept[Name(at(day+" 15:00"), "")])
		}
	}
	// Um por semana nas 4 últimas (a de hoje, que começa na segunda 16/11, já está coberta pelos dias).
	for _, sunday := range []string{"2026-11-15", "2026-11-08", "2026-11-01"} {
		// o mais novo da semana é o do domingo, 15:00
		if !kept[Name(at(sunday+" 15:00"), "")] {
			t.Errorf("the newest backup of the week ending %s must be kept", sunday)
		}
	}
	if kept[Name(at("2026-10-25 15:00"), "")] || kept[Name(at("2026-10-20 15:00"), "")] {
		t.Error("a backup from the fifth week back must go")
	}
	// Rótulos: os 10 mais novos ficam.
	labeled := 0
	for _, f := range keep {
		if f.Labeled() {
			labeled++
		}
	}
	if labeled != KeepLabeled {
		t.Errorf("labeled kept = %d, want %d", labeled, KeepLabeled)
	}
	// Nunca fica sem nada, nem se a política for mínima.
	only := []File{file("2026-01-01 03:00", "")}
	if keep, remove := Plan(only, now, 1, 0); len(keep) != 1 || len(remove) != 0 {
		t.Errorf("the newest backup must never go: keep=%v remove=%v", names(keep), names(remove))
	}
	if keep, remove := Plan(nil, now, 7, 4); len(keep)+len(remove) != 0 {
		t.Error("nothing to plan")
	}
}

func TestRunWritesAtomicallyVerifiesAndNeverLeavesAPartialFile(t *testing.T) {
	dir := t.TempDir()
	clock := at("2026-11-22 03:00")
	good := Config{Dir: dir, Now: func() time.Time { return clock },
		Dump:   func(_ context.Context, w io.Writer) error { _, err := io.WriteString(w, "PGDMP-conteudo"); return err },
		Verify: func(_ context.Context, path string) error { return nil }}
	f, err := Run(context.Background(), good, "pre-abc")
	if err != nil || f.Name != "cobogo-core-20261122-030000-pre-abc.dump" || f.Size != int64(len("PGDMP-conteudo")) || len(f.SHA256) != 64 {
		t.Fatalf("run = %+v, %v", f, err)
	}
	sum, _ := Hash(filepath.Join(dir, f.Name))
	if sum != f.SHA256 {
		t.Error("the stored sum must match the file")
	}
	listed, _ := List(dir)
	if len(listed) != 1 || listed[0].SHA256 != sum || listed[0].Label != "pre-abc" {
		t.Errorf("list = %+v", listed)
	}

	// Falhas: o dump que erra, o que sai vazio e o que não abre não deixam nada (nem temporário).
	for name, cfg := range map[string]Config{
		"dump error": {Dir: dir, Now: good.Now, Dump: func(context.Context, io.Writer) error { return errors.New("sem conexão") }},
		"empty":      {Dir: dir, Now: good.Now, Dump: func(context.Context, io.Writer) error { return nil }},
		"unreadable": {Dir: dir, Now: good.Now, Dump: good.Dump, Verify: func(context.Context, string) error { return errors.New("truncado") }},
	} {
		clock = clock.Add(time.Minute)
		if _, err := Run(context.Background(), cfg, ""); err == nil {
			t.Errorf("%s must fail", name)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 { // o dump bom e a soma dele
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		t.Errorf("a failed backup must leave nothing behind: %v", got)
	}
	if _, err := Run(context.Background(), good, "Rótulo Inválido"); err == nil {
		t.Error("an invalid label must be refused")
	}
}

func TestPruneOnlyTouchesBackupFiles(t *testing.T) {
	dir := t.TempDir()
	now := at("2026-11-22 10:00")
	for _, f := range []File{file("2026-11-22 03:00", ""), file("2026-11-21 03:00", ""), file("2026-09-01 03:00", ""), file("2026-08-01 03:00", "")} {
		os.WriteFile(filepath.Join(dir, f.Name), []byte("x"), 0o644)
		os.WriteFile(filepath.Join(dir, f.Name+".sha256"), []byte("abc  "+f.Name+"\n"), 0o644)
	}
	for _, other := range []string{"status.json", "notas.txt", "cobogo-core-vazio.dump"} {
		os.WriteFile(filepath.Join(dir, other), []byte("meu"), 0o644)
	}
	gone, err := Prune(dir, now, 3, 0)
	if err != nil || len(gone) != 2 {
		t.Fatalf("prune = %v, %v", gone, err)
	}
	for _, other := range []string{"status.json", "notas.txt", "cobogo-core-vazio.dump"} {
		if _, err := os.Stat(filepath.Join(dir, other)); err != nil {
			t.Errorf("%s is not a backup and must stay", other)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, Name(at("2026-09-01 03:00"), "")+".sha256")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the sum goes with its backup")
	}
	if err := Remove(dir, "../x.dump"); err == nil {
		t.Error("Remove must refuse a name that is not a backup")
	}
}

func TestStatusRoundTripAndStale(t *testing.T) {
	dir := t.TempDir()
	if st, err := ReadStatus(dir); err != nil || st.LastOKAt != nil || !st.Stale(time.Now()) {
		t.Fatalf("no status yet = %+v, %v", st, err)
	}
	ok := at("2026-11-22 03:00")
	if err := WriteStatus(dir, Status{LastOKAt: &ok, LastFile: "x.dump", KeepDaily: 7, KeepWeekly: 4}); err != nil {
		t.Fatal(err)
	}
	st, err := ReadStatus(dir)
	if err != nil || st.LastFile != "x.dump" || st.KeepDaily != 7 {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if st.Stale(ok.Add(35 * time.Hour)) {
		t.Error("35 h is not stale")
	}
	if !st.Stale(ok.Add(37 * time.Hour)) {
		t.Error("37 h is stale")
	}
	os.WriteFile(filepath.Join(dir, StatusFile), []byte("{não é json"), 0o644)
	if _, err := ReadStatus(dir); err == nil {
		t.Error("a corrupt status must be an error, not an empty one")
	}
}
