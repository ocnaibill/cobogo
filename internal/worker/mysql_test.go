package worker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// O MySQL gerenciado de verdade (DEC-50 B): sobe com a imagem fixada, aceita o usuário da
// aplicação por mysql_native_password e sem TLS, entra no backup, volta na restauração (com o
// esquema trocado) e some com a exclusão. Precisa de Docker e de /private/tmp.
func TestMySQLManagedEndToEnd(t *testing.T) {
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
	admin, err := st.CreateAdmin(ctx, "dono-my", "hash")
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, admin.ID, "mysql-real", "https://github.com/x/mysql-real", "")
	if err != nil {
		t.Fatal(err)
	}
	pid := p.ID.String()
	root, err := os.MkdirTemp("/private/tmp", "cobogo-my-")
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
	backupVol := "cobogo-test-mysql-" + pid[:8]
	docker(t, ctx, "volume", "create", "--driver", "local", "--opt", "type=none", "--opt", "o=bind", "--opt", "device="+root, backupVol)
	t.Cleanup(func() { exec.Command("docker", "volume", "rm", "-f", backupVol).Run() })

	const pass = "senha-do-mysql-5566"
	host := domain.ServiceContainerNameFor(pid, domain.ServiceMySQL)
	if host != "cobogo-mysql-"+strings.ReplaceAll(pid, "-", "")[:12] {
		t.Fatalf("nome do contêiner = %s", host)
	}
	svc, err := st.ChooseDatabaseFor(ctx, domain.ServiceMySQL, p.ID, admin.ID, domain.ServiceManaged,
		map[string]string{"host": "DB_HOST", "port": "DB_PORT", "user": "DB_USER", "password": "DB_PASSWORD", "database": "DB_NAME"},
		map[string]string{"host": host, "port": "3306", "user": "app", "password": pass, "database": "app"})
	if err != nil {
		t.Fatal(err)
	}
	if svc.Engine != domain.ServiceMySQL || svc.TLS || svc.ContainerName != host || svc.VolumeName != domain.ServiceVolumeNameFor(pid, domain.ServiceMySQL) {
		t.Fatalf("serviço = %+v", svc)
	}
	// O PostgreSQL e o MySQL não coexistem como banco gerenciado do mesmo projeto.
	if _, err := st.ChooseDatabaseFor(ctx, domain.ServicePostgres, p.ID, admin.ID, domain.ServiceManaged, map[string]string{"url": "X_URL"}, map[string]string{"url": "postgres://x"}); err == nil {
		t.Error("um segundo banco gerenciado, de outra engine, foi aceito")
	}

	w := &Worker{store: st, runtime: rt, log: quiet, settings: settings.NewResolver(st, settings.Env{})}
	w.SetBackup(root, backupVol)
	net := dockerrt.ProjectNetwork(pid)
	t.Cleanup(func() {
		_ = rt.RemoveService(context.Background(), pid, svc.ContainerName, svc.VolumeName)
		_ = rt.RemoveProjectNetwork(context.Background(), pid)
	})
	if _, err := w.provisionService(ctx, rt, p, svc); err != nil {
		t.Fatalf("provisionar: %v", err)
	}
	mysql := func(sql string) string {
		return docker(t, ctx, "run", "--rm", "--network", net, "-e", "MYSQL_PWD="+pass, dockerrt.MySQLImage,
			"mysql", "-h", host, "-u", "app", "app", "-N", "-B", "-e", sql)
	}
	if got := mysql("select 1"); strings.TrimSpace(got) != "1" {
		t.Fatalf("o usuário da aplicação não conectou: %q", got)
	}
	// O plugin de autenticação do usuário: o que os drivers sem TLS conseguem usar.
	if got := mysql("show create user current_user()"); !strings.Contains(got, "mysql_native_password") {
		t.Errorf("plugin do usuário = %q", got)
	}
	if got := mysql("select @@character_set_server"); !strings.HasPrefix(strings.TrimSpace(got), "utf8mb4") {
		t.Errorf("charset = %q", got)
	}
	// A senha do administrador é aleatória: a da aplicação não abre o root.
	if out, err := exec.Command("docker", "run", "--rm", "--network", net, "-e", "MYSQL_PWD="+pass, dockerrt.MySQLImage, "mysql", "-h", host, "-u", "root", "-e", "select 1").CombinedOutput(); err == nil {
		t.Errorf("o root abriu com a senha da aplicação: %s", out)
	}

	mysql("create table cadernos(id int primary key, nome varchar(40)) default charset utf8mb4; insert into cadernos values (1,'primeiro'),(2,'segundo'),(3,'terceiro ç')")
	b, err := st.CreateProjectBackup(ctx, p.ID, admin.ID, domain.BackupManual, false)
	if err != nil {
		t.Fatal(err)
	}
	claimed, _ := st.ClaimNextProjectBackup(ctx)
	w.executeProjectBackup(ctx, claimed)
	source, _ := st.GetProjectBackupFor(ctx, p.ID, b.ID, admin.ID)
	if source.State != domain.BackupSucceeded || source.Manifest.MySQL != backupMySQLVersion || source.Manifest.Postgres != "" ||
		len(source.Manifest.Items) != 1 || source.Manifest.Items[0].Kind != domain.BackupItemMySQL || source.Manifest.Items[0].File != "database.sql" {
		t.Fatalf("backup = %+v", source)
	}

	mysql("delete from cadernos; insert into cadernos values (9,'depois'); create table extra(x int)")
	r, safety, err := st.CreateRestore(ctx, p.ID, admin.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	sc, _ := st.ClaimNextProjectBackup(ctx)
	w.executeProjectBackup(ctx, sc)
	ready, err := st.ClaimNextReadyRestore(ctx)
	if err != nil || ready == nil || ready.ID != r.ID {
		t.Fatalf("restauração não está pronta: %+v %v", ready, err)
	}
	w.executeRestore(ctx, ready)
	list, _ := st.ListRestoresFor(ctx, p.ID, admin.ID, 5)
	if list[0].State != domain.RestoreSucceeded || !list[0].Result.Database {
		t.Fatalf("restauração = %+v", list[0])
	}
	if rows := mysql("select group_concat(nome order by id) from cadernos"); strings.TrimSpace(rows) != "primeiro,segundo,terceiro ç" {
		t.Errorf("o banco deve voltar ao estado do backup (com acentos): %q", rows)
	}
	if extra := mysql("select count(*) from information_schema.tables where table_schema = 'app' and table_name = 'extra'"); strings.TrimSpace(extra) != "0" {
		t.Errorf("uma tabela criada depois do backup deve sumir: %q", extra)
	}
	sb, _ := st.GetProjectBackupFor(ctx, p.ID, safety.ID, admin.ID)
	if sb.State != domain.BackupSucceeded || sb.Manifest.MySQL == "" {
		t.Errorf("o backup de segurança do estado de antes: %+v", sb)
	}
	// Um dump incompleto é recusado e o banco não muda: o MySQL não tem transação de esquema.
	if out, err := exec.Command("docker", "run", "--rm", "--network", net, "-e", "MYSQL_PWD="+pass, "-v", backupVol+":/out", dockerrt.MySQLImage,
		"sh", "-c", `printf 'CREATE TABLE lixo (x int);\n' > /out/truncado.sql`).CombinedOutput(); err != nil {
		t.Fatalf("preparar o dump truncado: %v %s", err, out)
	}
	err = rt.RestoreService(ctx, domain.ServiceMySQL, pid, "truncado", host, pass, net, backupVol, "", "truncado.sql", time.Minute)
	if err == nil {
		t.Error("um dump sem a marca de término foi aceito")
	}
	if rows := mysql("select count(*) from cadernos"); strings.TrimSpace(rows) != "3" {
		t.Errorf("o banco mudou com um dump recusado: %q", rows)
	}
}
