package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocnaibill/cobogo/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestKeepLeaseRenewsPeriodically(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stop := keepLease(ctx, cancel, 5*time.Millisecond, func(context.Context) error { calls.Add(1); return nil }, quiet)
	waitFor(t, "three renewals", func() bool { return calls.Load() >= 3 })
	stop()
	if ctx.Err() != nil {
		t.Errorf("a healthy heartbeat must not cancel the pipeline: %v", context.Cause(ctx))
	}
}

// Perder a posse cancela o pipeline com essa causa, para ele parar de gravar.
func TestKeepLeaseCancelsThePipelineWhenTheLeaseIsLost(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stop := keepLease(ctx, cancel, 5*time.Millisecond, func(context.Context) error {
		if calls.Add(1) >= 2 {
			return store.ErrLeaseLost
		}
		return nil
	}, quiet)
	defer stop()

	waitFor(t, "the pipeline to be canceled", func() bool { return ctx.Err() != nil })
	if !errors.Is(context.Cause(ctx), store.ErrLeaseLost) {
		t.Errorf("cause = %v, want ErrLeaseLost", context.Cause(ctx))
	}
	settled := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != settled {
		t.Error("the heartbeat must stop renewing after the lease is lost")
	}
}

// Uma falha de rede passageira não pode derrubar um deploy saudável.
func TestKeepLeaseSurvivesTransientErrors(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stop := keepLease(ctx, cancel, 5*time.Millisecond, func(context.Context) error {
		if calls.Add(1) <= 2 {
			return errors.New("conexão com o banco falhou")
		}
		return nil
	}, quiet)
	waitFor(t, "renewals after the errors", func() bool { return calls.Load() >= 5 })
	stop()
	if ctx.Err() != nil {
		t.Errorf("transient errors canceled the pipeline: %v", context.Cause(ctx))
	}
}

func TestKeepLeaseStopWaitsAndStopsRenewing(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	stop := keepLease(ctx, cancel, 5*time.Millisecond, func(context.Context) error { calls.Add(1); return nil }, quiet)
	waitFor(t, "a renewal", func() bool { return calls.Load() >= 1 })
	stop()
	after := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != after {
		t.Error("renewals continued after stop")
	}
}

func TestKeepLeaseStopsWhenThePipelineEnds(t *testing.T) {
	var calls atomic.Int32
	ctx, cancel := context.WithCancelCause(context.Background())
	stop := keepLease(ctx, cancel, 5*time.Millisecond, func(context.Context) error { calls.Add(1); return nil }, quiet)
	cancel(errors.New("fim"))
	stop() // não pode travar
	after := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != after {
		t.Error("renewals continued after the pipeline ended")
	}
}

func TestStageErrors(t *testing.T) {
	if at("BUILDING", nil) != nil {
		t.Error("at(stage, nil) must stay nil: a successful step is not a failure")
	}
	inner := errors.New("falhou")
	err := at("BUILDING", inner)
	var se *stageError
	if !errors.As(err, &se) || se.stage != "BUILDING" || !errors.Is(err, inner) || err.Error() != "falhou" {
		t.Errorf("stage error = %#v", err)
	}
	if !errors.Is(at("STARTING", store.ErrLeaseLost), store.ErrLeaseLost) {
		t.Error("a lost lease wrapped by a stage must still be recognizable")
	}
}
