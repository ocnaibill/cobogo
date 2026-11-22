package dockerrt

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/volume"
)

// sh roda um comando numa imagem descartável com os volumes montados, para preparar e conferir.
func shWith(t *testing.T, rt *Runtime, mounts []mount.Mount, script string) string {
	t.Helper()
	ctx := context.Background()
	created, err := rt.cli.ContainerCreate(ctx, &container.Config{Image: "node:26-slim", Entrypoint: []string{"/bin/sh", "-c"}, Cmd: []string{script}, User: "0"},
		&container.HostConfig{Mounts: mounts}, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer rt.cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true})
	if err := rt.cli.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		t.Fatal(err)
	}
	st, errCh := rt.cli.ContainerWait(ctx, created.ID, container.WaitConditionNotRunning)
	select {
	case s := <-st:
		out, _ := rt.LogTail(ctx, created.ID, 50)
		if s.StatusCode != 0 {
			t.Fatalf("%q exited %d: %s", script, s.StatusCode, out)
		}
		return out
	case err := <-errCh:
		t.Fatal(err)
	}
	return ""
}

// Com Docker de verdade (DEC-47 B): o volume do projeto é copiado somente leitura, para a
// subpasta de backups e só ela; arquivos de outros donos e com permissão fechada entram;
// uma cópia que falha devolve o motivo e não deixa contêiner.
func TestTarVolumeCopiesReadOnlyIntoTheSubpathOnly(t *testing.T) {
	rt := dockerForTest(t)
	ctx := context.Background()
	project, backupID := "projeto-backup-teste", "backup-1"
	src, dst := "cobogo-test-bk-src", "cobogo-test-bk-dst"
	for _, n := range []string{src, dst} {
		opts := volume.CreateOptions{Name: n}
		if n == src {
			opts.Labels = map[string]string{LabelManaged: "true", LabelProject: project}
		}
		if _, err := rt.cli.VolumeCreate(ctx, opts); err != nil {
			t.Fatal(err)
		}
		n := n
		t.Cleanup(func() { _ = rt.cli.VolumeRemove(context.Background(), n, true) })
	}
	// Dados do projeto: um arquivo de outro dono com permissão fechada, e um aninhado.
	shWith(t, rt, []mount.Mount{{Type: mount.TypeVolume, Source: src, Target: "/v"}},
		`mkdir -p /v/sub && echo "dado-secreto" > /v/fechado.txt && chown 1000:1000 /v/fechado.txt && chmod 600 /v/fechado.txt && echo aninhado > /v/sub/a.txt && chown -R 1000:1000 /v/sub`)
	// A pasta do backup (feita pelo worker, em produção) e uma vizinha que a cópia não pode ver.
	shWith(t, rt, []mount.Mount{{Type: mount.TypeVolume, Source: dst, Target: "/b"}},
		`mkdir -p /b/projects/p/b1 /b/projects/p/vizinho && chmod -R 777 /b/projects && echo "do-vizinho" > /b/projects/p/vizinho/x`)

	sub := "projects/p/b1"
	if err := rt.TarVolume(ctx, project, backupID, src, dst, sub, "volume-app-data-aaaaaa.tar.gz", time.Minute); err != nil {
		t.Fatal(err)
	}
	listing := shWith(t, rt, []mount.Mount{{Type: mount.TypeVolume, Source: dst, Target: "/b", ReadOnly: true}},
		`tar tzf /b/projects/p/b1/volume-app-data-aaaaaa.tar.gz | sort; tar xzOf /b/projects/p/b1/volume-app-data-aaaaaa.tar.gz ./fechado.txt`)
	for _, want := range []string{"./fechado.txt", "./sub/a.txt", "dado-secreto"} {
		if !strings.Contains(listing, want) {
			t.Errorf("the archive misses %q:\n%s", want, listing)
		}
	}
	// A fonte não foi alterada (somente leitura): nenhum arquivo novo nela.
	if after := shWith(t, rt, []mount.Mount{{Type: mount.TypeVolume, Source: src, Target: "/v", ReadOnly: true}}, `find /v -type f | sort`); strings.Count(after, "\n") != 2 {
		t.Errorf("the source must be untouched: %q", after)
	}

	// A cópia só enxerga a própria subpasta: um tar que tenta ler a vizinha falha.
	err := rt.RunCopy(ctx, CopySpec{ProjectID: project, BackupID: backupID, Image: PostgresImage, Command: []string{"cat", "/out/../vizinho/x"}, User: "0",
		BackupVolume: dst, Subpath: sub, MemoryMB: 64}, time.Minute)
	if err == nil {
		t.Error("the copy container must not see the neighbouring folder")
	}
	// Um volume que não existe NÃO é criado vazio (o Docker faria isso e o backup pareceria bom),
	// e um volume de outro projeto é recusado.
	if err := rt.TarVolume(ctx, project, backupID, "cobogo-test-bk-nao-existe-aqui", dst, sub, "volume-x-bbbbbb.tar.gz", time.Minute); err == nil || !strings.Contains(err.Error(), "não existe") {
		t.Errorf("missing volume = %v", err)
	}
	if _, err := rt.cli.VolumeInspect(ctx, "cobogo-test-bk-nao-existe-aqui"); err == nil {
		t.Error("a missing source volume must not be created by the copy")
	}
	if err := rt.TarVolume(ctx, "outro-projeto", backupID, src, dst, sub, "volume-x-bbbbbb.tar.gz", time.Minute); err == nil || !strings.Contains(err.Error(), "não é deste projeto") {
		t.Errorf("another project's volume = %v", err)
	}
	// Falha devolve o motivo; e nenhum contêiner de cópia sobra.
	err = rt.RunCopy(ctx, CopySpec{ProjectID: project, BackupID: backupID, Image: PostgresImage, Command: []string{"false"}, BackupVolume: dst, Subpath: sub, MemoryMB: 64}, time.Minute)
	if err == nil || !strings.Contains(err.Error(), "código 1") {
		t.Errorf("a failing copy must say why: %v", err)
	}
	left, _ := rt.ListManaged(ctx, project)
	for _, c := range left {
		if c.Role == RoleBackupCopy {
			t.Errorf("a copy container was left behind: %s", c.Name)
		}
	}
	// E um prazo curto mata a cópia.
	err = rt.RunCopy(ctx, CopySpec{ProjectID: project, BackupID: backupID, Image: PostgresImage, Command: []string{"sleep", "60"}, BackupVolume: dst, Subpath: sub, MemoryMB: 64}, 3*time.Second)
	if err == nil || !strings.Contains(err.Error(), "prazo") {
		t.Errorf("timeout = %v", err)
	}
}
