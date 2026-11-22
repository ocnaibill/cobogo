package backup

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func TestVolumeFileNamesAreDerivedFromThePathAndNeverCollide(t *testing.T) {
	a, b := VolumeFileName("/app/data"), VolumeFileName("/app/data/")
	if a == b || a == VolumeFileName("/app/da-ta") {
		t.Errorf("different paths must give different files: %s %s", a, b)
	}
	for _, p := range []string{"/app/data", "/var/lib/Meu App/dados", "/", "/a/b/c/d/e/f/g/h/i/j/k/l/m/n/o/p/q/r/s/t/u/v/w/x/y/z/long", "/ação/ü"} {
		n := VolumeFileName(p)
		if !ValidArtifact(n) || strings.Contains(n, "/") || strings.Contains(n, "..") {
			t.Errorf("%q -> %q is not a valid artifact name", p, n)
		}
	}
	if VolumeFileName("/app/data") != a {
		t.Error("the name must be stable")
	}
	for _, bad := range []string{"", "../x", "volume-x.tar.gz", "database.dump.sha256", "manifest.json/x", "outro.txt", "volume-a-zzzzzz.tar.gz"} {
		if ValidArtifact(bad) {
			t.Errorf("%q must not be a valid artifact", bad)
		}
	}
	for _, ok := range []string{"database.dump", "manifest.json", "sha256sums.txt"} {
		if !ValidArtifact(ok) {
			t.Errorf("%q must be valid", ok)
		}
	}
}

func pb(state, reason string, age time.Duration, deleteRequested bool) domain.ProjectBackup {
	return domain.ProjectBackup{ID: uuid.New(), State: state, Reason: reason, DeleteRequested: deleteRequested, CreatedAt: time.Now().Add(-age)}
}

// A retenção por projeto tira só os bons mais antigos; o de segurança da restauração
// conta no limite e nunca sai.
func TestPrunePlanKeepsTheNewestAndNeverTheBeforeRestoreOnes(t *testing.T) {
	oldest := pb(domain.BackupSucceeded, domain.BackupManual, 5*time.Hour, false)
	second := pb(domain.BackupSucceeded, domain.BackupManual, 4*time.Hour, false)
	safety := pb(domain.BackupSucceeded, domain.BackupBeforeRestore, 3*time.Hour, false)
	fourth := pb(domain.BackupSucceeded, domain.BackupBeforeDelete, 2*time.Hour, false)
	newest := pb(domain.BackupSucceeded, domain.BackupManual, time.Hour, false)
	failed := pb(domain.BackupFailed, domain.BackupManual, 6*time.Hour, false)
	running := pb(domain.BackupRunning, domain.BackupManual, 30*time.Minute, false)
	pending := pb(domain.BackupSucceeded, domain.BackupManual, 7*time.Hour, true)
	all := []domain.ProjectBackup{newest, oldest, failed, safety, running, fourth, second, pending}

	got := PrunePlan(all, 3, nil)
	if len(got) != 2 || got[0] != oldest.ID || got[1] != second.ID {
		t.Fatalf("keep 3 of 5 good: remove the 2 oldest (not the safety one) = %v", got)
	}
	if got := PrunePlan(all, 10, nil); len(got) != 0 {
		t.Errorf("under the limit nothing goes: %v", got)
	}
	// Se só sobram os de segurança, nada sai, mesmo acima do limite.
	onlySafety := []domain.ProjectBackup{safety, pb(domain.BackupSucceeded, domain.BackupBeforeRestore, time.Hour, false)}
	if got := PrunePlan(onlySafety, 1, nil); len(got) != 0 {
		t.Errorf("a before_restore backup is never pruned: %v", got)
	}
	// Falhos, em andamento e já pedidos para apagar não contam.
	if got := PrunePlan([]domain.ProjectBackup{failed, running, pending}, 1, nil); len(got) != 0 {
		t.Errorf("not good backups are not counted: %v", got)
	}
}

func TestManifestAndArchiveRoundTripAndOnlyCarryBackupFiles(t *testing.T) {
	dir := t.TempDir()
	vol := VolumeFileName("/app/data")
	os.WriteFile(filepath.Join(dir, vol), []byte("conteudo-do-volume"), 0o644)
	os.WriteFile(filepath.Join(dir, DatabaseFile), []byte("PGDMP-dados"), 0o644)
	os.WriteFile(filepath.Join(dir, "estranho.txt"), []byte("nao entra"), 0o644)
	outside := filepath.Join(t.TempDir(), "fora")
	os.WriteFile(outside, []byte("fora da pasta"), 0o644)
	os.Symlink(outside, filepath.Join(dir, "database.dump.bak"))
	if err := os.Symlink(outside, filepath.Join(dir, "volume-link-aaaaaa.tar.gz")); err != nil {
		t.Fatal(err)
	}

	var items []domain.BackupItem
	for _, it := range []struct{ kind, path, file string }{{domain.BackupItemVolume, "/app/data", vol}, {domain.BackupItemPostgres, "", DatabaseFile}} {
		size, sum, err := FileInfo(filepath.Join(dir, it.file))
		if err != nil || len(sum) != 64 {
			t.Fatal(err)
		}
		items = append(items, domain.BackupItem{Kind: it.kind, Path: it.path, File: it.file, Size: size, SHA256: sum})
	}
	m := domain.BackupManifest{ProjectID: "p", ProjectSlug: "meu-app", CreatedAt: time.Now().UTC(), Reason: domain.BackupManual, Items: items, Skipped: []string{"/app/x: não existe"}}
	if err := WriteManifest(dir, m); err != nil {
		t.Fatal(err)
	}
	back, err := ReadManifest(dir)
	if err != nil || back.Version != ManifestVersion || back.ProjectSlug != "meu-app" || len(back.Items) != 2 || back.Skipped[0] != "/app/x: não existe" {
		t.Fatalf("manifest = %+v, %v", back, err)
	}
	sums, _ := os.ReadFile(filepath.Join(dir, SumsFile))
	if !strings.Contains(string(sums), items[1].SHA256+"  database.dump") {
		t.Errorf("sums = %q", sums)
	}

	var buf bytes.Buffer
	if err := WriteArchive(&buf, dir, "meu-app-backup"); err != nil {
		t.Fatal(err)
	}
	size, err := ArchiveSize(dir, "meu-app-backup")
	if err != nil || size != int64(buf.Len()) {
		t.Errorf("ArchiveSize = %d, %v; archive = %d", size, err, buf.Len())
	}
	tr := tar.NewReader(&buf)
	var names []string
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(tr)
		names = append(names, h.Name)
		if h.Name == "meu-app-backup/database.dump" && string(body) != "PGDMP-dados" {
			t.Errorf("content = %q", body)
		}
		if strings.Contains(string(body), "fora da pasta") || strings.Contains(string(body), "nao entra") {
			t.Errorf("%s carries what it must not", h.Name)
		}
	}
	if len(names) != 4 || names[0] != "meu-app-backup/manifest.json" {
		t.Errorf("archive = %v (manifest first; the link, the stray file and the symlinks stay out)", names)
	}
}

func TestRemoveProjectDirOnlyRemovesTheBackupFolder(t *testing.T) {
	root := t.TempDir()
	p, b := uuid.New(), uuid.New()
	dir := ProjectDir(root, p, b)
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, DatabaseFile), []byte("x"), 0o644)
	other := ProjectDir(root, p, uuid.New())
	os.MkdirAll(other, 0o755)
	if err := RemoveProjectDir(root, p, b); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the backup folder must go")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("another backup of the project must stay")
	}
	if free, err := FreeBytes(root); err != nil || free == 0 {
		t.Errorf("free = %d, %v", free, err)
	}
}

// O backup que uma restauração em andamento restaura não sai da retenção, mesmo sendo o mais antigo.
func TestPrunePlanNeverRemovesTheBackupBeingRestored(t *testing.T) {
	source := pb(domain.BackupSucceeded, domain.BackupManual, 9*time.Hour, false)
	b2 := pb(domain.BackupSucceeded, domain.BackupManual, 8*time.Hour, false)
	b3 := pb(domain.BackupSucceeded, domain.BackupManual, 7*time.Hour, false)
	b4 := pb(domain.BackupSucceeded, domain.BackupManual, 6*time.Hour, false)
	all := []domain.ProjectBackup{source, b2, b3, b4}
	if got := PrunePlan(all, 3, nil); len(got) != 1 || got[0] != source.ID {
		t.Fatalf("without protection the oldest goes: %v", got)
	}
	got := PrunePlan(all, 3, map[uuid.UUID]bool{source.ID: true})
	if len(got) != 1 || got[0] != b2.ID {
		t.Errorf("the protected backup stays and the next oldest goes: %v", got)
	}
}
