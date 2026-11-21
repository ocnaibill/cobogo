package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// Dados persistentes (DEC-41).
func TestPersistentPathsAreValidatedScopedAndNormalized(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)

	if got, _ := st.GetProject(ctx, p.ID); len(got.PersistentPaths) != 0 {
		t.Errorf("a new project has no persistent paths: %v", got.PersistentPaths)
	}
	paths, err := st.SetPersistentPaths(ctx, p.ID, own.ID, []string{"/app/data/", "/app/content"})
	if err != nil || len(paths) != 2 || paths[0] != "/app/content" || paths[1] != "/app/data" {
		t.Fatalf("set = %v, %v", paths, err)
	}
	if got, _ := st.GetProject(ctx, p.ID); len(got.PersistentPaths) != 2 {
		t.Errorf("stored = %v", got.PersistentPaths)
	}
	items, _, _ := st.ListProjectsFor(ctx, own.ID, 10, nil)
	if len(items) != 1 || len(items[0].PersistentPaths) != 2 {
		t.Errorf("listing = %+v", items)
	}
	if _, err := st.SetPersistentPaths(ctx, p.ID, own.ID, []string{"/etc/passwd"}); !errors.Is(err, store.ErrInvalidPaths) {
		t.Errorf("system path = %v, want ErrInvalidPaths", err)
	}
	if got, _ := st.GetProject(ctx, p.ID); len(got.PersistentPaths) != 2 {
		t.Error("a refused list must keep the stored one")
	}
	if _, err := st.SetPersistentPaths(ctx, p.ID, uuid.New(), []string{"/app/x"}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner = %v, want ErrNotFound", err)
	}
	if paths, err := st.SetPersistentPaths(ctx, p.ID, own.ID, []string{}); err != nil || len(paths) != 0 {
		t.Errorf("clearing = %v, %v", paths, err)
	}
}

// Um volume é "sem projeto" quando o projeto foi excluído ou o caminho deixou de
// ser persistente; só esse pode ser apagado, e com o nome como confirmação.
func TestOrphanVolumesAndTheirDeletion(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	if _, err := st.SetPersistentPaths(ctx, p.ID, own.ID, []string{"/app/data", "/app/content"}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct{ name, path string }{{"vol-data", "/app/data"}, {"vol-content", "/app/content"}} {
		if err := st.UpsertProjectVolume(ctx, v.name, p.ID, v.path); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpdateVolumeSizes(ctx, map[string]int64{"vol-data": 4096}); err != nil {
		t.Fatal(err)
	}
	vols, err := st.ListVolumesFor(ctx, p.ID, own.ID)
	if err != nil || len(vols) != 2 {
		t.Fatalf("volumes = %+v, %v", vols, err)
	}
	for _, v := range vols {
		if v.Orphan {
			t.Errorf("%s is in use and must not be an orphan", v.Name)
		}
		if v.Name == "vol-data" && (v.SizeBytes == nil || *v.SizeBytes != 4096 || v.MeasuredAt == nil) {
			t.Errorf("size not recorded: %+v", v)
		}
	}
	if orphans, _ := st.ListOrphanVolumesFor(ctx, own.ID); len(orphans) != 0 {
		t.Errorf("orphans = %+v, want none", orphans)
	}
	if _, err := st.RequestDeleteVolume(ctx, own.ID, "vol-data", "vol-data"); !errors.Is(err, store.ErrVolumeNotOrphan) {
		t.Errorf("deleting a volume in use = %v, want ErrVolumeNotOrphan", err)
	}

	// Tirar o caminho da lista deixa o volume sem projeto; o outro segue em uso.
	if _, err := st.SetPersistentPaths(ctx, p.ID, own.ID, []string{"/app/content"}); err != nil {
		t.Fatal(err)
	}
	orphans, _ := st.ListOrphanVolumesFor(ctx, own.ID)
	if len(orphans) != 1 || orphans[0].Name != "vol-data" || orphans[0].ProjectDeleted {
		t.Fatalf("orphans after removing a path = %+v", orphans)
	}
	if _, err := st.RequestDeleteVolume(ctx, own.ID, "vol-data", "outro"); !errors.Is(err, store.ErrConfirmationMismatch) {
		t.Errorf("wrong confirmation = %v, want ErrConfirmationMismatch", err)
	}
	if _, err := st.RequestDeleteVolume(ctx, uuid.New(), "vol-data", "vol-data"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner = %v, want ErrNotFound", err)
	}
	op, err := st.RequestDeleteVolume(ctx, own.ID, "vol-data", "vol-data")
	if err != nil || op.Kind != domain.OpDeleteVolume || op.Params["name"] != "vol-data" {
		t.Fatalf("delete request = %+v, %v", op, err)
	}
	if got, err := st.GetVolume(ctx, "vol-data"); err != nil || !got.Orphan {
		t.Errorf("worker lookup = %+v, %v", got, err)
	}
	if err := st.MarkVolumeDeleted(ctx, "vol-data"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetVolume(ctx, "vol-data"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a deleted volume = %v, want ErrNotFound", err)
	}
	if orphans, _ := st.ListOrphanVolumesFor(ctx, own.ID); len(orphans) != 0 {
		t.Errorf("a deleted volume must not be listed: %+v", orphans)
	}
	// O mesmo caminho declarado de novo reabre o registro.
	if err := st.UpsertProjectVolume(ctx, "vol-data", p.ID, "/app/data"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.GetVolume(ctx, "vol-data"); err != nil {
		t.Errorf("a reused name must be live again: %v", err)
	}
}

// Excluir o projeto: com ou sem os dados, o pedido leva a escolha ao worker, e os
// volumes de um projeto excluído aparecem como sem projeto.
func TestDeleteProjectCarriesTheDataChoiceAndLeavesOrphans(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	if _, err := st.SetPersistentPaths(ctx, p.ID, own.ID, []string{"/app/data"}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertProjectVolume(ctx, "vol-del", p.ID, "/app/data"); err != nil {
		t.Fatal(err)
	}
	op, err := st.DeleteProject(ctx, p.ID, own.ID, p.Slug, true)
	if err != nil {
		t.Fatal(err)
	}
	if del, _ := op.Params["delete_data"].(bool); !del {
		t.Errorf("params = %v, want delete_data true", op.Params)
	}
	orphans, _ := st.ListOrphanVolumesFor(ctx, own.ID)
	if len(orphans) != 1 || !orphans[0].ProjectDeleted || orphans[0].ProjectSlug != p.Slug {
		t.Errorf("orphans = %+v", orphans)
	}
	names, _ := st.ProjectVolumeNames(ctx, p.ID)
	if len(names) != 1 || names[0] != "vol-del" {
		t.Errorf("names = %v", names)
	}
}
