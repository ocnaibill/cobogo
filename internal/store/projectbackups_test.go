package store_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// Backup dos dados de um projeto (DEC-47 B): só com o que copiar, um por vez por
// projeto, só o dono, o worker conduz o estado e apagar passa pelo worker.
func TestProjectBackupQueueOwnerAndWorkerFlow(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)

	if _, err := st.CreateProjectBackup(ctx, p.ID, own.ID, domain.BackupManual, false); !errors.Is(err, store.ErrNothingToBackUp) {
		t.Fatalf("nothing declared = %v, want ErrNothingToBackUp", err)
	}
	if _, err := st.SetPersistentPaths(ctx, p.ID, own.ID, []string{"/app/data"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProjectBackup(ctx, p.ID, uuid.New(), domain.BackupManual, false); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner = %v", err)
	}
	b, err := st.CreateProjectBackup(ctx, p.ID, own.ID, domain.BackupManual, true)
	if err != nil || b.State != domain.BackupQueued || !b.PauseApp || b.Reason != domain.BackupManual || len(b.Manifest.Items) != 0 {
		t.Fatalf("create = %+v, %v", b, err)
	}
	if _, err := st.CreateProjectBackup(ctx, p.ID, own.ID, domain.BackupManual, false); !errors.Is(err, store.ErrBackupActive) {
		t.Errorf("a second active backup = %v", err)
	}
	if _, err := st.RequestDeleteProjectBackup(ctx, p.ID, b.ID, own.ID); !errors.Is(err, store.ErrBackupBusy) {
		t.Errorf("deleting a queued backup = %v", err)
	}

	claimed, err := st.ClaimNextProjectBackup(ctx)
	if err != nil || claimed == nil || claimed.ID != b.ID || claimed.State != domain.BackupRunning || claimed.StartedAt == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if again, _ := st.ClaimNextProjectBackup(ctx); again != nil {
		t.Errorf("nothing queued: %+v", again)
	}
	manifest := domain.BackupManifest{ProjectSlug: p.Slug, Items: []domain.BackupItem{{Kind: domain.BackupItemVolume, Path: "/app/data", File: "volume-app-data-abc123.tar.gz", Size: 10, SHA256: "ab"}}, Skipped: []string{"/x: não existe"}}
	if err := st.FinishProjectBackup(ctx, b.ID, domain.BackupSucceeded, 4096, manifest, ""); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetProjectBackupFor(ctx, p.ID, b.ID, own.ID)
	if err != nil || got.State != domain.BackupSucceeded || got.SizeBytes != 4096 || len(got.Manifest.Items) != 1 || got.Manifest.Skipped[0] != "/x: não existe" || got.FinishedAt == nil {
		t.Fatalf("finished = %+v, %v", got, err)
	}
	// Terminado, o desfecho não muda.
	if err := st.FinishProjectBackup(ctx, b.ID, domain.BackupFailed, 0, manifest, "x"); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.GetProjectBackupFor(ctx, p.ID, b.ID, own.ID); again.State != domain.BackupSucceeded {
		t.Errorf("a finished backup must not change: %s", again.State)
	}

	// A vaga volta; apagar é pedido pela API e feito pelo worker.
	second, err := st.CreateProjectBackup(ctx, p.ID, own.ID, domain.BackupBeforeDelete, false)
	if err != nil {
		t.Fatalf("the slot must be free: %v", err)
	}
	if _, err := st.ClaimNextProjectBackup(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := st.InterruptRunningProjectBackups(ctx); err != nil || n != 1 {
		t.Fatalf("interrupt = %d, %v", n, err)
	}
	if s2, _ := st.GetProjectBackupFor(ctx, p.ID, second.ID, own.ID); s2.State != domain.BackupFailed || s2.Error == "" {
		t.Errorf("interrupted = %+v", s2)
	}
	del, err := st.RequestDeleteProjectBackup(ctx, p.ID, b.ID, own.ID)
	if err != nil || !del.DeleteRequested {
		t.Fatalf("request delete = %+v, %v", del, err)
	}
	pending, _ := st.BackupsToDelete(ctx)
	if len(pending) != 1 || pending[0].ID != b.ID {
		t.Errorf("to delete = %+v", pending)
	}
	if good, _ := st.SucceededProjectBackups(ctx, p.ID); len(good) != 0 {
		t.Errorf("a backup marked for deletion is not a good one anymore: %+v", good)
	}
	if err := st.MarkProjectBackupDeleted(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := st.ListProjectBackupsFor(ctx, p.ID, own.ID)
	if len(list) != 1 || list[0].ID != second.ID {
		t.Errorf("list = %+v", list)
	}
	if _, err := st.GetProjectBackupFor(ctx, p.ID, b.ID, own.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a deleted backup is gone: %v", err)
	}
}

// A restauração e o "antes de apagar" também podem pedir backup, e um banco gerenciado
// basta para haver o que copiar.
func TestProjectBackupAcceptsAManagedDatabaseAsContent(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	if _, err := st.ChooseDatabase(ctx, p.ID, own.ID, domain.ServiceManaged, map[string]string{"url": "DATABASE_URL"}, map[string]string{"url": "postgres://app:x@h:5432/app"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateProjectBackup(ctx, p.ID, own.ID, domain.BackupBeforeRestore, false); err != nil {
		t.Errorf("a managed database is content: %v", err)
	}
}

// Excluir o projeto leva os backups dele junto (o worker os apaga): nada de dado sem dono.
func TestProjectBackupsOfADeletedProjectAreCollected(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)
	if _, err := st.SetPersistentPaths(ctx, p.ID, own.ID, []string{"/app/data"}); err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateProjectBackup(ctx, p.ID, own.ID, domain.BackupManual, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ClaimNextProjectBackup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishProjectBackup(ctx, b.ID, domain.BackupSucceeded, 1, domain.BackupManifest{}, ""); err != nil {
		t.Fatal(err)
	}
	if pending, _ := st.BackupsToDelete(ctx); len(pending) != 0 {
		t.Fatalf("a live project's backup is not collected: %+v", pending)
	}
	if _, err := st.DeleteProject(ctx, p.ID, own.ID, p.Slug, false); err != nil {
		t.Fatal(err)
	}
	if pending, _ := st.BackupsToDelete(ctx); len(pending) != 1 || pending[0].ID != b.ID {
		t.Errorf("a deleted project's backups must be collected: %+v", pending)
	}
}
