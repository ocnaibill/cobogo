package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocnaibill/cobogo/internal/backup"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

func TestHumanBytes(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 5 << 30: "5.0 GiB", 1536 << 20: "1.5 GiB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func docker(t *testing.T, ctx context.Context, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// Backup de verdade dos dados de um projeto (DEC-47 B): um volume com arquivo de outro dono
// e um PostgreSQL gerenciado com dados saem numa pasta do volume de backups, com manifest e
// somas que batem; um caminho sem volume é dito e não falha; a retenção apaga o mais antigo;
// e uma falha não deixa pasta. Precisa de Docker (COBOGO_TEST_DOCKER=1) e do /private/tmp
// compartilhado com o Docker (o volume de backups do teste é um bind de uma pasta do host).
func TestProjectBackupEndToEnd(t *testing.T) {
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes com Docker")
	}
	rt, err := dockerrt.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	st := storetest.New(t)
	admin, err := st.CreateAdmin(ctx, "dono-bk", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, admin.ID, "backup-real", "https://github.com/x/backup-real", "")
	if err != nil {
		t.Fatal(err)
	}
	pid := p.ID.String()

	// O volume de backups: uma pasta do host exposta como volume nomeado do Docker.
	root, err := os.MkdirTemp("/private/tmp", "cobogo-bk-")
	if err != nil {
		t.Skipf("sem /private/tmp: %v", err)
	}
	os.Chmod(root, 0o777)
	t.Cleanup(func() {
		exec.Command("docker", "run", "--rm", "-v", root+":/r", "node:26-slim", "rm", "-rf", "/r/projects").Run()
		os.RemoveAll(root)
	})
	backupVol := "cobogo-test-backups-" + pid[:8]
	docker(t, ctx, "volume", "create", "--driver", "local", "--opt", "type=none", "--opt", "o=bind", "--opt", "device="+root, backupVol)
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", "-f", backupVol).Run() })

	// O volume de dados do projeto, com a posse e as permissões de uma aplicação.
	if _, err := st.SetPersistentPaths(ctx, p.ID, admin.ID, []string{"/app/data", "/app/ainda-nao"}); err != nil {
		t.Fatal(err)
	}
	dataVol := dockerrt.VolumeName(pid, "/app/data")
	docker(t, ctx, "volume", "create", "--label", "cobogo.managed=true", "--label", "cobogo.project="+pid, dataVol)
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", "-f", dataVol).Run() })
	docker(t, ctx, "run", "--rm", "-v", dataVol+":/v", "node:26-slim", "sh", "-c", `echo "caderno-do-usuario" > /v/caderno.txt && chown 1000:1000 /v/caderno.txt && chmod 600 /v/caderno.txt`)

	// O PostgreSQL gerenciado, com uma linha.
	const pass = "senha-do-backup-8821"
	url := "postgres://app:" + pass + "@" + domain.ServiceContainerName(pid) + ":5432/app"
	svc, err := st.ChooseDatabase(ctx, p.ID, admin.ID, domain.ServiceManaged, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": url})
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{store: st, runtime: rt, log: quiet, settings: settings.NewResolver(st, settings.Env{})}
	w.SetBackup(root, backupVol)
	net := dockerrt.ProjectNetwork(pid)
	t.Cleanup(func() {
		_ = rt.RemovePostgres(context.Background(), pid, svc.ContainerName, svc.VolumeName)
		_ = rt.RemoveProjectNetwork(context.Background(), pid)
	})
	if _, err := w.provisionPostgres(ctx, rt, p, svc); err != nil {
		t.Fatal(err)
	}
	docker(t, ctx, "run", "--rm", "--network", net, "postgres:16-alpine", "psql", url, "-c", "create table cadernos(id int, nome text); insert into cadernos values (1, 'meu caderno'), (2, 'outro')")

	run := func(reason string) domain.ProjectBackup {
		t.Helper()
		b, err := st.CreateProjectBackup(ctx, p.ID, admin.ID, reason, false)
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := st.ClaimNextProjectBackup(ctx)
		if err != nil || claimed == nil || claimed.ID != b.ID {
			t.Fatalf("claim = %+v, %v", claimed, err)
		}
		w.executeProjectBackup(ctx, claimed)
		done, err := st.GetProjectBackupFor(ctx, p.ID, b.ID, admin.ID)
		if err != nil {
			t.Fatal(err)
		}
		return done
	}

	first := run(domain.BackupManual)
	if first.State != domain.BackupSucceeded || first.Error != "" {
		t.Fatalf("first backup = %+v", first)
	}
	dir := backup.ProjectDir(root, p.ID, first.ID)
	m, err := backup.ReadManifest(dir)
	if err != nil || m.ProjectSlug != "backup-real" || m.Postgres != "16" || m.Reason != domain.BackupManual || len(m.Items) != 2 {
		t.Fatalf("manifest = %+v, %v", m, err)
	}
	if len(m.Skipped) != 1 || !strings.Contains(m.Skipped[0], "/app/ainda-nao") {
		t.Errorf("a declared path without a volume must be said, not failed: %v", m.Skipped)
	}
	var total int64
	for _, it := range m.Items {
		size, sum, err := backup.FileInfo(filepath.Join(dir, it.File))
		if err != nil || size != it.Size || sum != it.SHA256 || size == 0 {
			t.Errorf("%s: size=%d sum=%s err=%v; manifest says %d %s", it.File, size, sum, err, it.Size, it.SHA256)
		}
		total += it.Size
	}
	if first.SizeBytes != total || first.Manifest.Items[0].Kind != domain.BackupItemVolume {
		t.Errorf("size/manifest in the record = %d / %+v", first.SizeBytes, first.Manifest.Items)
	}
	// O arquivo fechado de outro dono entrou, e o dump abre e tem as linhas.
	if out := docker(t, ctx, "run", "--rm", "-v", root+":/b:ro", "node:26-slim", "sh", "-c", "tar xzOf /b/projects/"+pid+"/"+first.ID.String()+"/"+m.Items[0].File+" ./caderno.txt"); !strings.Contains(out, "caderno-do-usuario") {
		t.Errorf("the closed file must be in the archive: %q", out)
	}
	list := docker(t, ctx, "run", "--rm", "-v", root+":/b:ro", "postgres:16-alpine", "pg_restore", "--list", "/b/projects/"+pid+"/"+first.ID.String()+"/database.dump")
	if !strings.Contains(list, "cadernos") {
		t.Errorf("the dump must have the table:\n%s", list)
	}
	rows := docker(t, ctx, "run", "--rm", "-v", root+":/b:ro", "postgres:16-alpine", "pg_restore", "-a", "-t", "cadernos", "-f", "-", "/b/projects/"+pid+"/"+first.ID.String()+"/database.dump")
	if !strings.Contains(rows, "meu caderno") {
		t.Errorf("the dump must have the data:\n%s", rows)
	}
	// Nenhum contêiner de cópia sobra, e o volume de dados continua como estava.
	left, _ := rt.ListManaged(ctx, pid)
	for _, c := range left {
		if c.Role == dockerrt.RoleBackupCopy {
			t.Errorf("a copy container was left behind")
		}
	}

	// A retenção (padrão 3): com um quarto backup, o mais antigo é marcado e depois apagado.
	second, third := run(domain.BackupBeforeDelete), run(domain.BackupManual)
	fourth := run(domain.BackupManual)
	if second.State != domain.BackupSucceeded || third.State != domain.BackupSucceeded || fourth.State != domain.BackupSucceeded {
		t.Fatalf("backups = %s %s %s", second.State, third.State, fourth.State)
	}
	marked, _ := st.ProjectBackupByID(ctx, first.ID)
	if !marked.DeleteRequested {
		t.Fatal("the oldest backup must be marked for deletion by the retention")
	}
	w.deleteMarkedBackups(ctx)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the oldest backup's folder must be gone")
	}
	if _, err := os.Stat(backup.ProjectDir(root, p.ID, second.ID)); err != nil {
		t.Error("the others must stay")
	}
	if list, _ := st.ListProjectBackupsFor(ctx, p.ID, admin.ID); len(list) != 3 {
		t.Errorf("3 backups must remain: %d", len(list))
	}

	// Uma falha (o banco parou) não deixa pasta nem backup "bom".
	docker(t, ctx, "stop", svc.ContainerName)
	failed := run(domain.BackupManual)
	if failed.State != domain.BackupFailed || !strings.Contains(failed.Error, "banco") {
		t.Errorf("a stopped database must fail the whole backup: %+v", failed)
	}
	if _, err := os.Stat(backup.ProjectDir(root, p.ID, failed.ID)); !os.IsNotExist(err) {
		t.Error("a failed backup must leave no folder")
	}
	if len(failed.Manifest.Items) != 0 {
		t.Errorf("a failed backup must not claim items: %+v", failed.Manifest.Items)
	}
}

// A pasta `projects` é do serviço de backup (outro usuário, na pilha de verdade): o worker
// abre só as pastas que criou, e nunca tenta mudar a `projects` nem a raiz. Foi o que a
// pilha real pegou: um chmod em /backups/projects dava "operation not permitted".
func TestMkdirOpenOnlyOpensWhatTheWorkerCreated(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "projects")
	if err := os.Mkdir(projects, 0o755); err != nil {
		t.Fatal(err)
	}
	os.Chmod(root, 0o755)
	dir := filepath.Join(projects, "p1", "b1")
	if err := mkdirOpen(root, dir); err != nil {
		t.Fatal(err)
	}
	mode := func(p string) os.FileMode {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	if mode(dir) != 0o777 || mode(filepath.Join(projects, "p1")) != 0o777 {
		t.Errorf("the worker's folders must be open: %v %v", mode(dir), mode(filepath.Join(projects, "p1")))
	}
	if mode(projects) != 0o755 || mode(root) != 0o755 {
		t.Errorf("the backup service's folders must be left alone: %v %v", mode(projects), mode(root))
	}
}
