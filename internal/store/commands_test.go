package store_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

// Comandos pontuais (DEC-45): exige release, um por vez por projeto, só do dono,
// e o worker conduz o estado.
func TestCommandsQueueOnePerProjectAndFollowTheWorker(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	p := newProject(t, st)

	if _, err := st.CreateCommand(ctx, p.ID, own.ID, "bun run reindex", domain.CommandScript, false, 300); !errors.Is(err, store.ErrNoActiveRelease) {
		t.Fatalf("a command without a release = %v, want ErrNoActiveRelease", err)
	}
	o, dep := verifying(t, st, p)
	if _, err := o.PromoteRelease(ctx, dep.ID, routed("ctr-cmd", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateCommand(ctx, p.ID, uuid.New(), "ls", domain.CommandCustom, false, 300); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner = %v, want ErrNotFound", err)
	}

	c, err := st.CreateCommand(ctx, p.ID, own.ID, "bun run reindex", domain.CommandScript, true, 120)
	if err != nil || c.State != domain.CommandQueued || !c.PauseApp || c.TimeoutSeconds != 120 {
		t.Fatalf("create = %+v, %v", c, err)
	}
	if _, err := st.CreateCommand(ctx, p.ID, own.ID, "ls", domain.CommandCustom, false, 300); !errors.Is(err, store.ErrCommandActive) {
		t.Errorf("a second active command = %v, want ErrCommandActive", err)
	}

	// O worker reivindica, acompanha, e o desfecho libera a vaga.
	claimed, err := st.ClaimNextCommand(ctx)
	if err != nil || claimed == nil || claimed.ID != c.ID || claimed.State != domain.CommandRunning || claimed.StartedAt == nil {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if again, _ := st.ClaimNextCommand(ctx); again != nil {
		t.Errorf("nothing else is queued, got %+v", again)
	}
	if err := st.UpdateCommandOutput(ctx, c.ID, "indexando…\n", false); err != nil {
		t.Fatal(err)
	}
	got, _ := st.GetCommandFor(ctx, p.ID, c.ID, own.ID)
	if got.Output != "indexando…\n" {
		t.Errorf("partial output = %q", got.Output)
	}
	if cancel, _ := st.CommandCancelRequested(ctx, c.ID); cancel {
		t.Error("cancel was not requested")
	}
	if _, err := st.RequestCancelCommand(ctx, p.ID, c.ID, own.ID); err != nil {
		t.Fatal(err)
	}
	if cancel, _ := st.CommandCancelRequested(ctx, c.ID); !cancel {
		t.Error("cancel must be recorded")
	}
	code := 143
	if err := st.FinishCommand(ctx, c.ID, domain.CommandCanceled, &code, "bun run reindex", "indexando…\n", false, ""); err != nil {
		t.Fatal(err)
	}
	done, _ := st.GetCommandFor(ctx, p.ID, c.ID, own.ID)
	if done.State != domain.CommandCanceled || done.ExitCode == nil || *done.ExitCode != 143 || done.FinishedAt == nil {
		t.Errorf("finished = %+v", done)
	}
	// Terminado, não muda mais, e o projeto pode rodar outro.
	if err := st.FinishCommand(ctx, c.ID, domain.CommandSucceeded, nil, "x", "x", false, ""); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.GetCommandFor(ctx, p.ID, c.ID, own.ID); again.State != domain.CommandCanceled {
		t.Errorf("a finished command must not change: %q", again.State)
	}
	next, err := st.CreateCommand(ctx, p.ID, own.ID, "ls", domain.CommandCustom, false, 300)
	if err != nil {
		t.Fatalf("the slot must be free after a result: %v", err)
	}

	// A lista não traz a saída; um worker que caiu deixa o comando interrompido.
	if _, err := st.ClaimNextCommand(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := st.InterruptRunningCommands(ctx); err != nil || n != 1 {
		t.Fatalf("interrupt = %d, %v", n, err)
	}
	list, err := st.ListCommandsFor(ctx, p.ID, own.ID, 10)
	if err != nil || len(list) != 2 || list[0].ID != next.ID || list[0].State != domain.CommandInterrupted || list[1].Output != "" {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

func TestReauthWindowBelongsToTheSession(t *testing.T) {
	st := storetest.New(t)
	own := owner(t, st)
	hash := []byte("token-hash-de-teste-0123456789ab")
	sess, err := st.CreateSession(ctx, own.ID, hash, time.Hour, "127.0.0.1", "teste")
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := st.GetSession(ctx, hash, time.Hour)
	if err != nil || got.ReauthUntil != nil {
		t.Fatalf("a new session has no window: %+v, %v", got, err)
	}
	until, err := st.MarkReauth(ctx, sess.ID, domain.CommandReauthWindow)
	if err != nil || time.Until(until) < 9*time.Minute || time.Until(until) > 11*time.Minute {
		t.Fatalf("window = %v, %v", until, err)
	}
	got, _, _ = st.GetSession(ctx, hash, time.Hour)
	if got.ReauthUntil == nil || !got.ReauthUntil.After(time.Now()) {
		t.Errorf("window not stored: %+v", got)
	}
	if _, err := st.MarkReauth(ctx, uuid.New(), time.Minute); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown session = %v", err)
	}
}
