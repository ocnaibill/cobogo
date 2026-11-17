package store_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

func TestListProjectsPaginatesNewestFirst(t *testing.T) {
	st := storetest.New(t)
	me := owner(t, st)
	var created []uuid.UUID
	for i := 0; i < 5; i++ {
		p := newProject(t, st)
		created = append(created, p.ID)
	}

	var got []uuid.UUID
	var cursor *store.Cursor
	for page := 0; page < 10; page++ {
		items, next, err := st.ListProjectsFor(ctx, me.ID, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			got = append(got, it.ID)
		}
		if next == nil {
			break
		}
		if len(items) != 2 {
			t.Fatalf("a page with a next cursor must be full, got %d", len(items))
		}
		cursor = next
	}
	if len(got) != 5 {
		t.Fatalf("listed %d projects, want 5", len(got))
	}
	for i, id := range got {
		if want := created[len(created)-1-i]; id != want {
			t.Errorf("position %d: got %s, want %s (newest first)", i, id, want)
		}
	}

	if items, next, err := st.ListProjectsFor(ctx, uuid.New(), 10, nil); err != nil || len(items) != 0 || next != nil {
		t.Errorf("another owner sees %d projects (next=%v, err=%v)", len(items), next, err)
	}
}

func TestListProjectsDoesNotSkipItemsCreatedAtTheSameInstant(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	me := owner(t, st)
	for i := 0; i < 5; i++ {
		newProject(t, st)
	}
	if _, err := pool.Exec(ctx, `UPDATE projects SET created_at = $1`, time.Now().UTC().Truncate(time.Second)); err != nil {
		t.Fatal(err)
	}

	seen := map[uuid.UUID]bool{}
	var cursor *store.Cursor
	for page := 0; page < 10; page++ {
		items, next, err := st.ListProjectsFor(ctx, me.ID, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if seen[it.ID] {
				t.Fatalf("project %s listed twice", it.ID)
			}
			seen[it.ID] = true
		}
		if next == nil {
			break
		}
		cursor = next
	}
	if len(seen) != 5 {
		t.Errorf("listed %d of 5 projects with identical created_at", len(seen))
	}
}

func TestListProjectsShowsTheLastDeployment(t *testing.T) {
	st := storetest.New(t)
	me := owner(t, st)
	withDeploys := newProject(t, st)
	empty := newProject(t, st)

	if _, _, err := st.CreateDeploymentWithJob(ctx, withDeploys.ID, "k1", nil); err != nil {
		t.Fatal(err)
	}
	second, _, err := st.CreateDeploymentWithJob(ctx, withDeploys.ID, "k2", nil)
	if err != nil {
		t.Fatal(err)
	}

	items, _, err := st.ListProjectsFor(ctx, me.ID, 10, nil)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[uuid.UUID]domain.ProjectSummary{}
	for _, it := range items {
		byID[it.ID] = it
	}
	if got := byID[withDeploys.ID].LastDeployment; got == nil || got.ID != second.ID || got.State != domain.DeploymentQueued {
		t.Errorf("last deployment = %+v, want %s QUEUED", got, second.ID)
	}
	if got := byID[empty.ID].LastDeployment; got != nil {
		t.Errorf("a project without deploys must have no last deployment, got %+v", got)
	}
}

func TestListDeploymentsPaginates(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	var ids []uuid.UUID
	for i := 0; i < 5; i++ {
		d, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k-"+uuid.NewString(), nil)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, d.ID)
	}
	other := newProject(t, st)
	if _, _, err := st.CreateDeploymentWithJob(ctx, other.ID, "outro", nil); err != nil {
		t.Fatal(err)
	}

	var got []uuid.UUID
	var cursor *store.Cursor
	for page := 0; page < 10; page++ {
		items, next, err := st.ListDeployments(ctx, p.ID, 2, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			got = append(got, it.ID)
		}
		if next == nil {
			break
		}
		cursor = next
	}
	if len(got) != 5 {
		t.Fatalf("listed %d deployments, want 5 (only the project's own)", len(got))
	}
	for i, id := range got {
		if want := ids[len(ids)-1-i]; id != want {
			t.Errorf("position %d: got %s, want %s", i, id, want)
		}
	}
}

func TestCursorRoundTrip(t *testing.T) {
	c := store.Cursor{At: time.Date(2026, 11, 17, 10, 30, 0, 123456000, time.UTC), ID: uuid.New()}
	back, err := store.ParseCursor(c.String())
	if err != nil || !back.At.Equal(c.At) || back.ID != c.ID {
		t.Fatalf("round trip: %+v, %v", back, err)
	}
	for _, bad := range []string{"", "x", "2026-11-17T00:00:00Z", "2026-11-17T00:00:00Z_nao-uuid", "ontem_" + uuid.NewString()} {
		if _, err := store.ParseCursor(bad); !errors.Is(err, store.ErrBadCursor) {
			t.Errorf("ParseCursor(%q) = %v, want ErrBadCursor", bad, err)
		}
	}
}

func TestListEventsAfterPagesAndSettles(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	dep, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	o, _, _ := claim(t, st)
	for i := 0; i < 5; i++ {
		if err := o.AppendEvent(ctx, dep.ID, "building", "log", map[string]any{"line": i}); err != nil {
			t.Fatal(err)
		}
	}

	page, err := st.ListEventsAfter(ctx, dep.ID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 2 || !page.HasMore || page.Events[0].Sequence != 1 || page.Events[1].Sequence != 2 {
		t.Fatalf("first page = %+v", page)
	}
	if page.Settled {
		t.Error("a deployment whose job is leased is not settled")
	}

	page, err = st.ListEventsAfter(ctx, dep.ID, 4, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].Sequence != 5 || page.HasMore {
		t.Fatalf("last page = %+v", page)
	}

	if page, _ = st.ListEventsAfter(ctx, dep.ID, 5, 10); len(page.Events) != 0 || page.HasMore {
		t.Errorf("nothing after the last event, got %+v", page)
	}

	if err := o.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	if page, _ = st.ListEventsAfter(ctx, dep.ID, 5, 10); !page.Settled {
		t.Error("a finished job must be settled")
	}

	if _, err := st.ListEventsAfter(ctx, uuid.New(), 0, 10); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown deployment: %v, want ErrNotFound", err)
	}
	if seq, err := st.LastEventSequence(ctx, dep.ID); err != nil || seq != 5 {
		t.Errorf("last sequence = %d, %v", seq, err)
	}
}

func TestCancelQueuedDeployment(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	dep, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k", nil)
	if err != nil {
		t.Fatal(err)
	}

	got, err := st.CancelDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.DeploymentCancelled {
		t.Errorf("state = %s, want CANCELLED", got.State)
	}
	if job, _, err := st.ClaimNextJob(ctx, testLease, testAttempts); err != nil || job != nil {
		t.Errorf("a cancelled deployment's job must not be claimable: job=%v err=%v", job, err)
	}
	page, err := st.ListEventsAfter(ctx, dep.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Events) != 1 || page.Events[0].EventType != "cancelled" || !page.Settled {
		t.Errorf("events after cancel = %+v settled=%v", page.Events, page.Settled)
	}
	if _, err := st.CancelDeployment(ctx, dep.ID); !errors.Is(err, store.ErrDeploymentFinished) {
		t.Errorf("second cancel: %v, want ErrDeploymentFinished", err)
	}
	if _, err := st.AnswerDeployment(ctx, dep.ID, map[string]string{"port": "3000"}); !errors.Is(err, store.ErrNotAwaitingInput) {
		t.Errorf("answering a cancelled deployment: %v, want ErrNotAwaitingInput", err)
	}
}

func TestCancelNeedsInputDeployment(t *testing.T) {
	st := storetest.New(t)
	dep := awaitingInput(t, st, nil)

	got, err := st.CancelDeployment(ctx, dep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != domain.DeploymentCancelled {
		t.Errorf("state = %s, want CANCELLED", got.State)
	}
	if _, err := st.AnswerDeployment(ctx, dep.ID, map[string]string{"port": "3000"}); !errors.Is(err, store.ErrNotAwaitingInput) {
		t.Errorf("answering a cancelled deployment: %v, want ErrNotAwaitingInput", err)
	}
}

func TestCancelRefusesWhatAlreadyStarted(t *testing.T) {
	st := storetest.New(t)
	p := newProject(t, st)
	dep, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	o, _, _ := claim(t, st)

	// Reivindicado mas ainda QUEUED: o worker já é o dono.
	if _, err := st.CancelDeployment(ctx, dep.ID); !errors.Is(err, store.ErrDeploymentStarted) {
		t.Errorf("claimed job: %v, want ErrDeploymentStarted", err)
	}
	if err := o.Transition(ctx, dep.ID, domain.DeploymentFetching, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CancelDeployment(ctx, dep.ID); !errors.Is(err, store.ErrDeploymentStarted) {
		t.Errorf("fetching: %v, want ErrDeploymentStarted", err)
	}
	if got, _ := st.GetDeployment(ctx, dep.ID); got.State != domain.DeploymentFetching {
		t.Errorf("a refused cancel must not change the state, got %s", got.State)
	}
	if _, err := st.CancelDeployment(ctx, uuid.New()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown deployment: %v, want ErrNotFound", err)
	}

	for _, to := range []domain.DeploymentState{domain.DeploymentFailed} {
		if err := o.Transition(ctx, dep.ID, to, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.CancelDeployment(ctx, dep.ID); !errors.Is(err, store.ErrDeploymentFinished) {
		t.Errorf("failed: %v, want ErrDeploymentFinished", err)
	}
}

// Cancelar e reivindicar o mesmo job ao mesmo tempo tem um único vencedor:
// ou o deploy é cancelado e ninguém o executa, ou o worker o leva e o
// cancelamento é recusado.
func TestCancelRacesAgainstClaim(t *testing.T) {
	st := storetest.New(t)
	for i := 0; i < 15; i++ {
		p := newProject(t, st)
		dep, _, err := st.CreateDeploymentWithJob(ctx, p.ID, "k", nil)
		if err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		var cancelErr, claimErr error
		var claimed bool
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, cancelErr = st.CancelDeployment(ctx, dep.ID)
		}()
		go func() {
			defer wg.Done()
			var job *domain.Job
			job, _, claimErr = st.ClaimNextJob(ctx, testLease, testAttempts)
			claimed = job != nil
		}()
		wg.Wait()

		if claimErr != nil {
			t.Fatal(claimErr)
		}
		switch {
		case cancelErr == nil && claimed:
			t.Fatalf("round %d: the deployment was cancelled and also claimed", i)
		case cancelErr != nil && !claimed:
			t.Fatalf("round %d: cancel failed (%v) but nobody claimed the job", i, cancelErr)
		case cancelErr != nil && !errors.Is(cancelErr, store.ErrDeploymentStarted):
			t.Fatalf("round %d: unexpected cancel error %v", i, cancelErr)
		}
	}
}
