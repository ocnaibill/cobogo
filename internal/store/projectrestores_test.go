package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// goodBackup deixa um backup concluído no projeto.
func goodBackup(t *testing.T, st *store.Store, p domain.Project, ownerID uuid.UUID) domain.ProjectBackup {
	t.Helper()
	b, err := st.CreateProjectBackup(ctx, p.ID, ownerID, domain.BackupManual, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNextProjectBackup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishProjectBackup(ctx, b.ID, domain.BackupSucceeded, 10, domain.BackupManifest{ProjectSlug: p.Slug}, ""); err != nil {
		t.Fatal(err)
	}
	return b
}

// Restauração (DEC-47 C): o pedido cria o backup de segurança junto, só começa depois dele,
// uma por vez, protege o backup de origem e bloqueia backup e apagar enquanto roda.
func TestRestoreIsQueuedBehindItsSafetyBackupAndProtectsTheSource(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	if _, err := st.SetPersistentPaths(ctx, p.ID, own.ID, []string{"/app/data"}); err != nil {
		t.Fatal(err)
	}
	source := goodBackup(t, st, p, own.ID)

	if _, _, err := st.CreateRestore(ctx, p.ID, uuid.New(), source.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner = %v", err)
	}
	if _, _, err := st.CreateRestore(ctx, p.ID, own.ID, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown backup = %v", err)
	}
	r, safety, err := st.CreateRestore(ctx, p.ID, own.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != domain.RestoreQueued || r.BackupID != source.ID || r.SafetyBackupID != safety.ID || safety.Reason != domain.BackupBeforeRestore || !safety.PauseApp || safety.State != domain.BackupQueued {
		t.Fatalf("restore = %+v, safety = %+v", r, safety)
	}
	// Uma por vez; nenhum backup, nem apagar o de origem ou o de segurança, durante a restauração.
	if _, _, err := st.CreateRestore(ctx, p.ID, own.ID, source.ID); !errors.Is(err, store.ErrRestoreActive) {
		t.Errorf("a second restore = %v", err)
	}
	if _, err := st.CreateProjectBackup(ctx, p.ID, own.ID, domain.BackupManual, false); !errors.Is(err, store.ErrRestoreActive) {
		t.Errorf("a backup during a restore = %v", err)
	}
	if _, err := st.RequestDeleteProjectBackup(ctx, p.ID, source.ID, own.ID); !errors.Is(err, store.ErrBackupBusy) {
		t.Errorf("deleting the source of a restore = %v", err)
	}
	prot, _ := st.ProtectedBackupIDs(ctx, p.ID)
	if !prot[source.ID] || !prot[safety.ID] || len(prot) != 2 {
		t.Errorf("protected = %v", prot)
	}

	// Enquanto o backup de segurança não termina, o worker não pega a restauração.
	if got, err := st.ClaimNextReadyRestore(ctx); err != nil || got != nil {
		t.Fatalf("not ready yet = %+v, %v", got, err)
	}
	if _, err := st.ClaimNextProjectBackup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishProjectBackup(ctx, safety.ID, domain.BackupSucceeded, 5, domain.BackupManifest{}, ""); err != nil {
		t.Fatal(err)
	}
	claimed, err := st.ClaimNextReadyRestore(ctx)
	if err != nil || claimed == nil || claimed.ID != r.ID || claimed.State != domain.RestoreRunning || claimed.Step != "iniciando" || claimed.StartedAt == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if again, _ := st.ClaimNextReadyRestore(ctx); again != nil {
		t.Errorf("only one: %+v", again)
	}
	if err := st.SetRestoreStep(ctx, r.ID, "restaurando o banco"); err != nil {
		t.Fatal(err)
	}
	result := domain.RestoreResult{Volumes: []string{"/app/data"}, Database: true, Skipped: []string{"/app/x: não é mais declarado"}, AppRestarted: true}
	if err := st.FinishRestore(ctx, r.ID, domain.RestoreSucceeded, result, ""); err != nil {
		t.Fatal(err)
	}
	list, _ := st.ListRestoresFor(ctx, p.ID, own.ID, 5)
	if len(list) != 1 || list[0].State != domain.RestoreSucceeded || list[0].Step != "" || len(list[0].Result.Volumes) != 1 || !list[0].Result.Database || list[0].Result.Skipped[0] == "" || list[0].FinishedAt == nil {
		t.Fatalf("list = %+v", list)
	}
	if list[0].Result.Warnings == nil {
		t.Error("lists must never be null in JSON")
	}
	// Terminada, libera: backup e apagar voltam.
	if _, err := st.RequestDeleteProjectBackup(ctx, p.ID, source.ID, own.ID); err != nil {
		t.Errorf("after the restore the source can go: %v", err)
	}
}

func TestRestoreFailsWhenItsSafetyBackupFailsAndNothingIsTouched(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	st.SetPersistentPaths(ctx, p.ID, own.ID, []string{"/app/data"})
	source := goodBackup(t, st, p, own.ID)
	r, safety, err := st.CreateRestore(ctx, p.ID, own.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	st.ClaimNextProjectBackup(ctx)
	if err := st.FinishProjectBackup(ctx, safety.ID, domain.BackupFailed, 0, domain.BackupManifest{}, "sem espaço"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ClaimNextReadyRestore(ctx); got != nil {
		t.Fatal("a failed safety backup must not release the restore")
	}
	if n, err := st.FailBlockedRestores(ctx); err != nil || n != 1 {
		t.Fatalf("fail blocked = %d, %v", n, err)
	}
	list, _ := st.ListRestoresFor(ctx, p.ID, own.ID, 5)
	if list[0].ID != r.ID || list[0].State != domain.RestoreFailed || list[0].Error == "" {
		t.Errorf("restore = %+v", list[0])
	}
	// A vaga volta.
	if _, _, err := st.CreateRestore(ctx, p.ID, own.ID, source.ID); err != nil {
		t.Errorf("the slot must be free: %v", err)
	}
}

func TestRestoreInterruptedByAWorkerRestartPointsAtTheSafetyBackup(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	st.SetPersistentPaths(ctx, p.ID, own.ID, []string{"/app/data"})
	source := goodBackup(t, st, p, own.ID)
	r, safety, _ := st.CreateRestore(ctx, p.ID, own.ID, source.ID)
	st.ClaimNextProjectBackup(ctx)
	st.FinishProjectBackup(ctx, safety.ID, domain.BackupSucceeded, 1, domain.BackupManifest{}, "")
	st.ClaimNextReadyRestore(ctx)
	if n, err := st.InterruptRunningRestores(ctx); err != nil || n != 1 {
		t.Fatalf("interrupt = %d, %v", n, err)
	}
	got, _ := st.ListRestoresFor(ctx, p.ID, own.ID, 5)
	if got[0].ID != r.ID || got[0].State != domain.RestoreFailed || got[0].Error == "" || !contains(got[0].Error, safety.ID.String()) {
		t.Errorf("interrupted = %+v", got[0])
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
