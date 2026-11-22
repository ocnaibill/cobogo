package httpapi_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocnaibill/cobogo/internal/backup"
)

type backupsBody struct {
	Configured bool `json:"configured"`
	Stale      bool `json:"stale"`
	Backups    []struct {
		Name   string `json:"name"`
		Size   int64  `json:"size_bytes"`
		Label  string `json:"label"`
		SHA256 string `json:"sha256"`
	} `json:"backups"`
	Status struct {
		LastFile  string `json:"last_file"`
		LastError string `json:"last_error"`
	} `json:"status"`
	TotalBytes int64 `json:"total_bytes"`
}

func writeBackup(t *testing.T, dir string, at time.Time, label, body string) string {
	t.Helper()
	name := backup.Name(at, label)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	sum, _ := backup.Hash(filepath.Join(dir, name))
	if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte(sum+"  "+name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

// Backups da plataforma (DEC-47): o estado, o aviso de atraso, e o download só com a
// senha digitada de novo, só de nome de backup, sem sair da pasta.
func TestBackupsListWarnAndDownloadOnlyWithReauth(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, func(o *options) { o.backupDir = dir })
	h.login()

	// Sem nenhum backup: o aviso vale.
	empty := decode[backupsBody](t, h.do("GET", "/api/v1/backups", ""))
	if !empty.Configured || !empty.Stale || len(empty.Backups) != 0 {
		t.Errorf("empty = %+v", empty)
	}

	fresh := writeBackup(t, dir, time.Now().UTC().Add(-2*time.Hour), "", "PGDMP-conteudo-do-dump")
	writeBackup(t, dir, time.Now().UTC().Add(-50*time.Hour), "pre-abc1234", "velho")
	os.WriteFile(filepath.Join(dir, "notas.txt"), []byte("não é backup"), 0o644)
	ok := time.Now().UTC().Add(-2 * time.Hour)
	if err := backup.WriteStatus(dir, backup.Status{LastOKAt: &ok, LastFile: fresh}); err != nil {
		t.Fatal(err)
	}
	list := decode[backupsBody](t, h.do("GET", "/api/v1/backups", ""))
	if list.Stale || len(list.Backups) != 2 || list.Backups[0].Name != fresh || list.Backups[1].Label != "pre-abc1234" || len(list.Backups[0].SHA256) != 64 || list.Status.LastFile != fresh {
		t.Fatalf("list = %+v", list)
	}
	if list.TotalBytes != int64(len("PGDMP-conteudo-do-dump")+len("velho")) {
		t.Errorf("total = %d", list.TotalBytes)
	}

	// Um último backup velho acende o aviso, mesmo com arquivos.
	old := time.Now().UTC().Add(-40 * time.Hour)
	backup.WriteStatus(dir, backup.Status{LastOKAt: &old})
	if stale := decode[backupsBody](t, h.do("GET", "/api/v1/backups", "")); !stale.Stale {
		t.Error("a last good backup older than 36 h must raise the warning")
	}
	// Um erro do serviço aparece.
	backup.WriteStatus(dir, backup.Status{LastOKAt: &ok, LastError: "pg_dump: sem conexão"})
	if withErr := decode[backupsBody](t, h.do("GET", "/api/v1/backups", "")); withErr.Status.LastError != "pg_dump: sem conexão" {
		t.Errorf("status = %+v", withErr.Status)
	}

	// Download sem a janela: recusado, e nada vaza.
	url := "/api/v1/backups/" + fresh + "/download"
	rec := h.do("GET", url, "")
	if rec.Code != http.StatusForbidden || decode[apiError](t, rec).Error.Code != "reauth_required" || strings.Contains(rec.Body.String(), "PGDMP") {
		t.Fatalf("without the window: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("POST", "/api/v1/auth/reauth", `{"password":"`+adminPass+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("reauth: %d", rec.Code)
	}
	rec = h.do("GET", url, "")
	if rec.Code != http.StatusOK || rec.Body.String() != "PGDMP-conteudo-do-dump" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename="`+fresh+`"` || rec.Header().Get("X-Content-SHA256") != list.Backups[0].SHA256 || rec.Header().Get("Content-Length") != "22" {
		t.Fatalf("download: %d %q %v", rec.Code, rec.Body, rec.Header())
	}

	// Só nome de backup: nada de caminho, de outro arquivo, de soma nem de link.
	for _, bad := range []string{"notas.txt", "status.json", fresh + ".sha256", "..%2F..%2Fetc%2Fpasswd", "cobogo-core-20200101-000000.dump", "cobogo-core-20200101-000000-A.dump"} {
		if rec := h.do("GET", "/api/v1/backups/"+bad+"/download", ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", bad, rec.Code)
		}
	}
	outside := filepath.Join(t.TempDir(), "segredo")
	os.WriteFile(outside, []byte("fora da pasta"), 0o644)
	link := backup.Name(time.Now().UTC().Add(-time.Hour), "link")
	if err := os.Symlink(outside, filepath.Join(dir, link)); err != nil {
		t.Fatal(err)
	}
	if rec := h.do("GET", "/api/v1/backups/"+link+"/download", ""); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "fora da pasta") {
		t.Errorf("a symlink must not be followed: %d %s", rec.Code, rec.Body)
	}

	// A janela é da sessão: outra sessão não baixa.
	h.signIn(adminLogin, adminPass)
	if rec := h.do("GET", url, ""); rec.Code != http.StatusForbidden {
		t.Errorf("a new session must not inherit the window: %d", rec.Code)
	}
}

func TestBackupsWithoutTheVolumeSayTheyAreNotConfigured(t *testing.T) {
	h := newHarness(t)
	h.login()
	v := decode[backupsBody](t, h.do("GET", "/api/v1/backups", ""))
	if v.Configured || !v.Stale || len(v.Backups) != 0 {
		t.Errorf("no volume = %+v", v)
	}
	h.do("POST", "/api/v1/auth/reauth", `{"password":"`+adminPass+`"}`)
	if rec := h.do("GET", "/api/v1/backups/cobogo-core-20261122-030000.dump/download", ""); rec.Code != http.StatusNotFound {
		t.Errorf("download without a volume: %d", rec.Code)
	}
	// /auth/me mostra a janela quando vale.
	if me := h.do("GET", "/api/v1/auth/me", ""); !strings.Contains(me.Body.String(), "reauth_until") {
		t.Errorf("me must show the reauth window: %s", me.Body)
	}
}
