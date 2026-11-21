package worker

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// Dados persistentes (DEC-41 item 5): excluir o projeto só apaga os volumes se o
// usuário marcou, e um volume sem projeto só é apagado se continua sem projeto.
// Precisa de Docker (COBOGO_TEST_DOCKER=1) e de PostgreSQL.
func TestDeleteProjectDataFollowsTheUsersChoice(t *testing.T) {
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes com Docker")
	}
	rt, err := dockerrt.New()
	if err != nil {
		t.Fatal(err)
	}
	st := storetest.New(t)
	ctx := context.Background()
	admin, err := st.CreateAdmin(ctx, "dono-volumes", "hash")
	if err != nil {
		t.Fatal(err)
	}
	w := &Worker{store: st, runtime: rt, log: quiet}

	// run cria um projeto com um volume real, exclui e devolve o projeto.
	run := func(slug string, deleteData bool) (domain.Project, string) {
		p, err := st.CreateProject(ctx, admin.ID, slug, "https://github.com/exemplo/"+slug, "")
		if err != nil {
			t.Fatal(err)
		}
		const path = "/app/data"
		name := dockerrt.VolumeName(p.ID.String(), path)
		if _, err := rt.EnsureVolume(ctx, name, p.ID.String(), path); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = rt.RemoveVolume(context.Background(), name) })
		if err := st.UpsertProjectVolume(ctx, name, p.ID, path); err != nil {
			t.Fatal(err)
		}
		op, err := st.DeleteProject(ctx, p.ID, admin.ID, p.Slug, deleteData)
		if err != nil {
			t.Fatal(err)
		}
		deleted, _ := st.GetProject(ctx, p.ID)
		removed, kept, errs := w.deleteProjectData(ctx, deleted, &op)
		if len(errs) != 0 {
			t.Fatalf("errors: %v", errs)
		}
		if deleteData && (len(removed) != 1 || len(kept) != 0) || !deleteData && (len(removed) != 0 || len(kept) != 1) {
			t.Fatalf("deleteData=%v: removed=%v kept=%v", deleteData, removed, kept)
		}
		return p, name
	}
	volumeExists := func(name string) bool {
		sizes, err := rt.VolumeSizes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, ok := sizes[name]
		return ok
	}

	_, keptName := run("guarda-dados-w", false)
	if !volumeExists(keptName) {
		t.Error("without the choice, the volume must stay")
	}
	_, goneName := run("apaga-dados-w", true)
	if volumeExists(goneName) {
		t.Error("with the choice, the volume must be gone")
	}
	if _, err := st.GetVolume(ctx, goneName); err == nil {
		t.Error("the deleted volume's record must be marked as deleted")
	}

	// O volume mantido é sem projeto: apagar pela operação funciona; um volume que
	// voltou a ser usado é recusado.
	op := &domain.Operation{Kind: domain.OpDeleteVolume, Params: map[string]any{"name": keptName}}
	res, err := w.deleteVolume(ctx, op)
	if err != nil || res["volume"] != keptName {
		t.Fatalf("deleteVolume = %v, %v", res, err)
	}
	if volumeExists(keptName) {
		t.Error("the orphan volume must be gone")
	}
	if res, err := w.deleteVolume(ctx, op); err != nil || res["already_gone"] != true {
		t.Errorf("deleting twice must be a no-op: %v, %v", res, err)
	}

	live, err := st.CreateProject(ctx, admin.ID, "ainda-usa", "https://github.com/exemplo/x", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetPersistentPaths(ctx, live.ID, admin.ID, []string{"/app/data"}); err != nil {
		t.Fatal(err)
	}
	inUse := "cobogo-vol-" + uuid.NewString()[:8]
	if err := st.UpsertProjectVolume(ctx, inUse, live.ID, "/app/data"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.deleteVolume(ctx, &domain.Operation{Params: map[string]any{"name": inUse}}); err != errVolumeInUse {
		t.Errorf("a volume in use = %v, want errVolumeInUse", err)
	}
}
