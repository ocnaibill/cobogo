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

// Restaurar de verdade (DEC-47 C): o volume e o banco voltam ao estado do backup, o backup de
// segurança guarda o estado de antes, um backup corrompido é recusado sem tocar em nada, e o
// que o projeto não declara mais é ignorado e dito. Precisa de Docker e de /private/tmp.
func TestProjectRestoreEndToEnd(t *testing.T) {
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes com Docker")
	}
	rt, err := dockerrt.New()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	st := storetest.New(t)
	admin, err := st.CreateAdmin(ctx, "dono-rs", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, admin.ID, "restaura-real", "https://github.com/x/restaura-real", "")
	if err != nil {
		t.Fatal(err)
	}
	pid := p.ID.String()
	root, err := os.MkdirTemp("/private/tmp", "cobogo-rs-")
	if err != nil {
		t.Skipf("sem /private/tmp: %v", err)
	}
	os.Chmod(root, 0o777)
	os.MkdirAll(filepath.Join(root, "projects"), 0o777)
	os.Chmod(filepath.Join(root, "projects"), 0o777)
	t.Cleanup(func() {
		exec.Command("docker", "run", "--rm", "-v", root+":/r", "node:26-slim", "rm", "-rf", "/r/projects").Run()
		os.RemoveAll(root)
	})
	backupVol := "cobogo-test-restores-" + pid[:8]
	docker(t, ctx, "volume", "create", "--driver", "local", "--opt", "type=none", "--opt", "o=bind", "--opt", "device="+root, backupVol)
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", "-f", backupVol).Run() })

	if _, err := st.SetPersistentPaths(ctx, p.ID, admin.ID, []string{"/app/data"}); err != nil {
		t.Fatal(err)
	}
	dataVol := dockerrt.VolumeName(pid, "/app/data")
	docker(t, ctx, "volume", "create", "--label", "cobogo.managed=true", "--label", "cobogo.project="+pid, dataVol)
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", "-f", dataVol).Run() })
	vol := func(script string) string {
		return docker(t, ctx, "run", "--rm", "-v", dataVol+":/v", "node:26-slim", "sh", "-c", script)
	}
	vol(`echo "caderno-de-antes" > /v/caderno.txt && chown 1000:1000 /v/caderno.txt && chmod 600 /v/caderno.txt && echo oculto > /v/.oculto`)

	const pass = "senha-da-restauracao-3344"
	url := "postgres://app:" + pass + "@" + domain.ServiceContainerName(pid) + ":5432/app"
	svc, err := st.ChooseDatabase(ctx, p.ID, admin.ID, domain.ServiceManaged, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": url})
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{store: st, runtime: rt, log: quiet, settings: settings.NewResolver(st, settings.Env{})}
	w.SetBackup(root, backupVol)
	net := dockerrt.ProjectNetwork(pid)
	t.Cleanup(func() {
		_ = rt.RemoveService(context.Background(), pid, svc.ContainerName, svc.VolumeName)
		_ = rt.RemoveProjectNetwork(context.Background(), pid)
	})
	if _, err := w.provisionPostgres(ctx, rt, p, svc); err != nil {
		t.Fatal(err)
	}
	psql := func(sql string) string {
		return docker(t, ctx, "run", "--rm", "--network", net, "postgres:16-alpine", "psql", url, "-tAc", sql)
	}
	psql("create table cadernos(id int, nome text); insert into cadernos values (1,'primeiro'),(2,'segundo'),(3,'terceiro')")

	runBackup := func() domain.ProjectBackup {
		t.Helper()
		b, err := st.CreateProjectBackup(ctx, p.ID, admin.ID, domain.BackupManual, false)
		if err != nil {
			t.Fatal(err)
		}
		claimed, _ := st.ClaimNextProjectBackup(ctx)
		w.executeProjectBackup(ctx, claimed)
		got, _ := st.GetProjectBackupFor(ctx, p.ID, b.ID, admin.ID)
		if got.State != domain.BackupSucceeded {
			t.Fatalf("backup = %+v", got)
		}
		return got
	}
	// restore pede a restauração e anda o laço do worker à mão: o backup de segurança, depois a restauração.
	restore := func(source domain.ProjectBackup) (domain.ProjectRestore, domain.ProjectBackup) {
		t.Helper()
		r, safety, err := st.CreateRestore(ctx, p.ID, admin.ID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		claimed, _ := st.ClaimNextProjectBackup(ctx)
		w.executeProjectBackup(ctx, claimed)
		ready, err := st.ClaimNextReadyRestore(ctx)
		if err != nil || ready == nil || ready.ID != r.ID {
			t.Fatalf("restore not ready: %+v %v", ready, err)
		}
		w.executeRestore(ctx, ready)
		list, _ := st.ListRestoresFor(ctx, p.ID, admin.ID, 5)
		sb, _ := st.GetProjectBackupFor(ctx, p.ID, safety.ID, admin.ID)
		return list[0], sb
	}

	source := runBackup()

	// O estado muda depois do backup: o volume e o banco.
	vol(`rm -f /v/caderno.txt /v/.oculto && echo "depois-do-backup" > /v/novo.txt`)
	psql("delete from cadernos; insert into cadernos values (9,'depois'); create table extra(x int)")

	res, safety := restore(source)
	if res.State != domain.RestoreSucceeded || res.Error != "" || !res.Result.Database || len(res.Result.Volumes) != 1 || res.Result.Volumes[0] != "/app/data" || res.Step != "" {
		t.Fatalf("restore = %+v", res)
	}
	got := vol(`ls -A /v | sort; stat -c '%n %u:%g %a' /v/caderno.txt; cat /v/caderno.txt`)
	if !strings.Contains(got, "caderno.txt 1000:1000 600") || !strings.Contains(got, "caderno-de-antes") || !strings.Contains(got, ".oculto") || strings.Contains(got, "novo.txt") {
		t.Errorf("the volume must be back to the backup's state:\n%s", got)
	}
	if rows := psql("select string_agg(nome, ',' order by id) from cadernos"); rows != "primeiro,segundo,terceiro" {
		t.Errorf("the database must be back: %q", rows)
	}
	if extra := psql("select to_regclass('public.extra') is null"); extra != "t" {
		t.Errorf("a table created after the backup must be gone (the schema is replaced): %q", extra)
	}
	// O backup de segurança guarda o estado de ANTES da restauração (o "depois").
	if safety.State != domain.BackupSucceeded || safety.Reason != domain.BackupBeforeRestore {
		t.Fatalf("safety backup = %+v", safety)
	}
	rows := docker(t, ctx, "run", "--rm", "-v", root+":/b:ro", "postgres:16-alpine", "pg_restore", "-a", "-t", "cadernos", "-f", "-", "/b/projects/"+pid+"/"+safety.ID.String()+"/database.dump")
	if !strings.Contains(rows, "depois") {
		t.Errorf("the safety backup must hold the state from before the restore:\n%s", rows)
	}

	// Um backup corrompido: recusado antes de tocar em qualquer coisa.
	vol(`echo "estado-intacto" > /v/intacto.txt`)
	archive := filepath.Join(backup.ProjectDir(root, p.ID, source.ID), source.Manifest.Items[0].File)
	if f, err := os.OpenFile(archive, os.O_WRONLY|os.O_APPEND, 0); err != nil {
		t.Fatal(err)
	} else {
		f.WriteString("lixo")
		f.Close()
	}
	bad, _ := restore(source)
	if bad.State != domain.RestoreFailed || !strings.Contains(bad.Error, "corrompido") || !strings.Contains(bad.Error, "Nada foi alterado") {
		t.Fatalf("a corrupt backup must be refused untouched: %+v", bad)
	}
	if v := vol(`ls -A /v | sort`); !strings.Contains(v, "intacto.txt") {
		t.Errorf("a refused restore must not touch the volume:\n%s", v)
	}

	// Um caminho que o projeto não declara mais é ignorado e dito; o banco ainda é restaurado.
	second := runBackup() // backup bom com o estado atual
	if _, err := st.SetPersistentPaths(ctx, p.ID, admin.ID, []string{"/app/outro"}); err != nil {
		t.Fatal(err)
	}
	psql("delete from cadernos")
	skip, _ := restore(second)
	if skip.State != domain.RestoreSucceeded || len(skip.Result.Volumes) != 0 || !skip.Result.Database || len(skip.Result.Skipped) != 1 || !strings.Contains(skip.Result.Skipped[0], "/app/data") {
		t.Fatalf("an undeclared path must be skipped and said: %+v", skip)
	}
	if n := psql("select count(*) from cadernos"); n != "3" {
		t.Errorf("the database restore still works: %q", n)
	}
	// Nenhum contêiner de cópia sobra.
	left, _ := rt.ListManaged(ctx, pid)
	for _, c := range left {
		if c.Role == dockerrt.RoleBackupCopy {
			t.Errorf("a copy container was left behind")
		}
	}
}
