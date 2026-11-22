package httpapi_test

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/backup"
	"github.com/ocnaibill/cobogo/internal/domain"
)

type pbBody struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Reason     string `json:"reason"`
	PauseApp   bool   `json:"pause_app"`
	Configured bool   `json:"configured"`
	HasContent bool   `json:"has_content"`
	Keep       int    `json:"keep"`
	Backups    []struct {
		ID              string `json:"id"`
		State           string `json:"state"`
		DeleteRequested bool   `json:"delete_requested"`
	} `json:"backups"`
}

// Backup dos dados de um projeto (DEC-47 B): só com o que copiar, um por vez, o download só
// com a senha de novo e só de backup bom e do dono, e o apagar passa pelo worker.
func TestProjectBackupsAPIFlowAndDownload(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, func(o *options) { o.backupDir = dir })
	h.login()
	p := h.createProject("com-dados")
	base := "/api/v1/projects/" + p.ID.String() + "/backups"

	view := decode[pbBody](t, h.do("GET", base, ""))
	if !view.Configured || view.HasContent || view.Keep != 3 || len(view.Backups) != 0 {
		t.Fatalf("empty view = %+v", view)
	}
	if rec := h.do("POST", base, `{}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "nothing_to_back_up" {
		t.Errorf("nothing to back up: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("PUT", "/api/v1/projects/"+p.ID.String()+"/persistent-paths", `{"paths":["/app/data"]}`); rec.Code != http.StatusOK {
		t.Fatalf("paths: %d %s", rec.Code, rec.Body)
	}
	if v := decode[pbBody](t, h.do("GET", base, "")); !v.HasContent {
		t.Error("a declared path is content")
	}
	if rec := h.do("POST", base, `{"reason":"before_restore"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("before_restore is not for the API: %d", rec.Code)
	}
	rec := h.do("POST", base, `{"pause_app":true,"reason":"before_delete"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	b := decode[pbBody](t, rec)
	if b.State != "queued" || !b.PauseApp || b.Reason != "before_delete" {
		t.Errorf("created = %+v", b)
	}
	if rec := h.do("POST", base, `{}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "backup_active" {
		t.Errorf("second backup: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("DELETE", base+"/"+b.ID, ""); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "backup_busy" {
		t.Errorf("deleting a queued backup: %d %s", rec.Code, rec.Body)
	}

	// O worker conclui (aqui, à mão): arquivos no volume e o desfecho no banco.
	bid := uuid.MustParse(b.ID)
	claimed, err := h.st.ClaimNextProjectBackup(t.Context())
	if err != nil || claimed == nil {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	bdir := backup.ProjectDir(dir, p.ID, bid)
	os.MkdirAll(bdir, 0o755)
	volFile := backup.VolumeFileName("/app/data")
	os.WriteFile(filepath.Join(bdir, volFile), []byte("TARGZ-DO-VOLUME"), 0o644)
	os.WriteFile(filepath.Join(bdir, "estranho.txt"), []byte("nao entra"), 0o644)
	m := domain.BackupManifest{ProjectSlug: p.Slug, ProjectID: p.ID.String(), Reason: "before_delete",
		Items: []domain.BackupItem{{Kind: domain.BackupItemVolume, Path: "/app/data", File: volFile, Size: 15, SHA256: "ab"}}}
	if err := backup.WriteManifest(bdir, m); err != nil {
		t.Fatal(err)
	}
	if err := h.st.FinishProjectBackup(t.Context(), bid, domain.BackupSucceeded, 15, m, ""); err != nil {
		t.Fatal(err)
	}

	// Download: sem a senha de novo, não; com ela, um tar com o manifest primeiro.
	url := base + "/" + b.ID + "/download"
	rec = h.do("GET", url, "")
	if rec.Code != http.StatusForbidden || decode[apiError](t, rec).Error.Code != "reauth_required" || strings.Contains(rec.Body.String(), "TARGZ") {
		t.Fatalf("without the window: %d %s", rec.Code, rec.Body)
	}
	h.do("POST", "/api/v1/auth/reauth", `{"password":"`+adminPass+`"}`)
	rec = h.do("GET", url, "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/x-tar" ||
		!strings.HasPrefix(rec.Header().Get("Content-Disposition"), `attachment; filename="com-dados-backup-`) || rec.Header().Get("Content-Length") == "" {
		t.Fatalf("download: %d %v", rec.Code, rec.Header())
	}
	tr := tar.NewReader(bytes.NewReader(rec.Body.Bytes()))
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		names = append(names, strings.TrimPrefix(hdr.Name, strings.SplitN(hdr.Name, "/", 2)[0]+"/"))
		if strings.Contains(string(body), "nao entra") {
			t.Error("a stray file must not be in the archive")
		}
	}
	if len(names) != 3 || names[0] != "manifest.json" {
		t.Errorf("archive = %v", names)
	}
	if got := rec.Header().Get("Content-Length"); got != itoa(rec.Body.Len()) {
		t.Errorf("Content-Length = %s, body = %d", got, rec.Body.Len())
	}

	// O mesmo backup por um id de outro projeto, ou um id que não existe, é 404.
	other := h.createProject("outro-projeto")
	if rec := h.do("GET", "/api/v1/projects/"+other.ID.String()+"/backups/"+b.ID+"/download", ""); rec.Code != http.StatusNotFound {
		t.Errorf("another project's backup: %d", rec.Code)
	}
	if rec := h.do("GET", base+"/"+uuid.NewString()+"/download", ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown backup: %d", rec.Code)
	}
	if rec := h.do("GET", base+"/nao-e-uuid/download", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", rec.Code)
	}

	// Apagar: o pedido marca, e daí em diante o backup não baixa mais.
	rec = h.do("DELETE", base+"/"+b.ID, "")
	if rec.Code != http.StatusAccepted || !decode[pbBody](t, h.do("GET", base, "")).Backups[0].DeleteRequested {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("GET", url, ""); rec.Code != http.StatusNotFound {
		t.Errorf("a backup marked for deletion must not download: %d", rec.Code)
	}
	// A janela é da sessão.
	h.signIn(adminLogin, adminPass)
	if rec := h.do("GET", base+"/"+b.ID+"/download", ""); rec.Code != http.StatusForbidden {
		t.Errorf("a new session must not inherit the window: %d", rec.Code)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestProjectBackupsWithoutTheVolumeAreUnavailable(t *testing.T) {
	h := newHarness(t)
	h.login()
	p := h.createProject("sem-volume")
	h.do("PUT", "/api/v1/projects/"+p.ID.String()+"/persistent-paths", `{"paths":["/app/data"]}`)
	base := "/api/v1/projects/" + p.ID.String() + "/backups"
	if v := decode[pbBody](t, h.do("GET", base, "")); v.Configured {
		t.Error("no backup volume: not configured")
	}
	if rec := h.do("POST", base, `{}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "backups_unavailable" {
		t.Errorf("create without the volume: %d %s", rec.Code, rec.Body)
	}
}

// Restaurar (DEC-47 C): exige a senha de novo e o nome do projeto, só de backup bom, só do que o
// projeto ainda recebe, cria o backup de segurança junto e uma por vez.
func TestRestoreAPIRequiresReauthAndTheSlugAndQueuesItsSafetyBackup(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, func(o *options) { o.backupDir = dir })
	h.login()
	p := h.createProject("restauravel")
	pid := p.ID.String()
	base := "/api/v1/projects/" + pid + "/backups"
	h.do("PUT", "/api/v1/projects/"+pid+"/persistent-paths", `{"paths":["/app/data"]}`)

	// Um backup bom, com um volume e um dump, feito "pelo worker".
	b := decode[pbBody](t, h.do("POST", base, `{}`))
	h.st.ClaimNextProjectBackup(t.Context())
	m := domain.BackupManifest{ProjectSlug: p.Slug, Items: []domain.BackupItem{
		{Kind: domain.BackupItemVolume, Path: "/app/data", File: backup.VolumeFileName("/app/data"), Size: 1, SHA256: "a"},
		{Kind: domain.BackupItemPostgres, File: "database.dump", Size: 1, SHA256: "b"}}}
	if err := h.st.FinishProjectBackup(t.Context(), uuid.MustParse(b.ID), domain.BackupSucceeded, 2, m, ""); err != nil {
		t.Fatal(err)
	}
	url := base + "/" + b.ID + "/restore"

	// Sem a senha de novo e sem o nome certo: recusado, e nada é criado.
	if rec := h.do("POST", url, `{"confirm_slug":"restauravel"}`); rec.Code != http.StatusForbidden || decode[apiError](t, rec).Error.Code != "reauth_required" {
		t.Fatalf("without the window: %d %s", rec.Code, rec.Body)
	}
	h.do("POST", "/api/v1/auth/reauth", `{"password":"`+adminPass+`"}`)
	for name, body := range map[string]string{"wrong name": `{"confirm_slug":"outro"}`, "empty": `{"confirm_slug":""}`, "not json": `oi`} {
		if rec := h.do("POST", url, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400 (%s)", name, rec.Code, rec.Body)
		}
	}
	if v := decode[pbBody](t, h.do("GET", base, "")); len(v.Backups) != 1 {
		t.Fatalf("a refused restore must not create a safety backup: %d backups", len(v.Backups))
	}

	rec := h.do("POST", url, `{"confirm_slug":"restauravel"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	var got struct {
		Restore struct {
			ID, State, BackupID, SafetyBackupID string
			Step                                string
		} `json:"restore"`
		SafetyBackup struct {
			ID       string `json:"id"`
			Reason   string `json:"reason"`
			PauseApp bool   `json:"pause_app"`
			State    string `json:"state"`
		} `json:"safety_backup"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Restore.State != "queued" || got.SafetyBackup.Reason != "before_restore" || !got.SafetyBackup.PauseApp || got.SafetyBackup.State != "queued" {
		t.Errorf("restore = %s", rec.Body)
	}
	// O painel acompanha pela lista: a restauração aparece, e o backup de segurança também.
	var view struct {
		Backups  []struct{ ID, Reason string } `json:"backups"`
		Restores []struct{ ID, State string }  `json:"restores"`
	}
	json.Unmarshal(h.do("GET", base, "").Body.Bytes(), &view)
	if len(view.Backups) != 2 || len(view.Restores) != 1 || view.Restores[0].State != "queued" {
		t.Errorf("view = %+v", view)
	}
	// Uma por vez, e nenhum backup novo nem apagar o de origem enquanto ela está na fila.
	if rec := h.do("POST", url, `{"confirm_slug":"restauravel"}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "restore_active" {
		t.Errorf("a second restore: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("POST", base, `{}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "restore_active" {
		t.Errorf("a backup during a restore: %d %s", rec.Code, rec.Body)
	}
	if rec := h.do("DELETE", base+"/"+b.ID, ""); rec.Code != http.StatusConflict {
		t.Errorf("deleting the source during a restore: %d", rec.Code)
	}
}

func TestRestoreAPIRefusesWhatTheProjectCannotReceive(t *testing.T) {
	dir := t.TempDir()
	h := newHarness(t, func(o *options) { o.backupDir = dir })
	h.login()
	p := h.createProject("sem-destino")
	pid := p.ID.String()
	base := "/api/v1/projects/" + pid + "/backups"
	h.do("PUT", "/api/v1/projects/"+pid+"/persistent-paths", `{"paths":["/app/data"]}`)
	b := decode[pbBody](t, h.do("POST", base, `{}`))
	h.st.ClaimNextProjectBackup(t.Context())
	m := domain.BackupManifest{Items: []domain.BackupItem{{Kind: domain.BackupItemVolume, Path: "/app/data", File: backup.VolumeFileName("/app/data"), Size: 1, SHA256: "a"}}}
	h.st.FinishProjectBackup(t.Context(), uuid.MustParse(b.ID), domain.BackupSucceeded, 1, m, "")
	h.do("POST", "/api/v1/auth/reauth", `{"password":"`+adminPass+`"}`)

	// O projeto deixa de declarar o caminho: o backup não tem mais onde ser restaurado.
	h.do("PUT", "/api/v1/projects/"+pid+"/persistent-paths", `{"paths":["/app/outro"]}`)
	rec := h.do("POST", base+"/"+b.ID+"/restore", `{"confirm_slug":"sem-destino"}`)
	if rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "nothing_to_restore" {
		t.Errorf("nothing to restore: %d %s", rec.Code, rec.Body)
	}
	if v := decode[pbBody](t, h.do("GET", base, "")); len(v.Backups) != 1 {
		t.Errorf("a refused restore must not create a safety backup: %d", len(v.Backups))
	}
	if rec := h.do("POST", base+"/"+uuid.NewString()+"/restore", `{"confirm_slug":"sem-destino"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown backup: %d", rec.Code)
	}
	// Um backup que falhou não se restaura.
	h.do("PUT", "/api/v1/projects/"+pid+"/persistent-paths", `{"paths":["/app/data"]}`)
	failed := decode[pbBody](t, h.do("POST", base, `{}`))
	h.st.ClaimNextProjectBackup(t.Context())
	h.st.FinishProjectBackup(t.Context(), uuid.MustParse(failed.ID), domain.BackupFailed, 0, domain.BackupManifest{}, "x")
	if rec := h.do("POST", base+"/"+failed.ID+"/restore", `{"confirm_slug":"sem-destino"}`); rec.Code != http.StatusConflict || decode[apiError](t, rec).Error.Code != "nothing_to_restore" && decode[apiError](t, rec).Error.Code != "backup_not_usable" {
		t.Errorf("a failed backup: %d %s", rec.Code, rec.Body)
	}
}
