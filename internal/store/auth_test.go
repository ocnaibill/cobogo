package store_test

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/auth"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

func admin(t *testing.T, st *store.Store) domain.User {
	t.Helper()
	u, err := st.CreateAdmin(ctx, "admin", "hash-do-admin")
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestCreateAdminOnlyOnce(t *testing.T) {
	st := storetest.New(t)
	if n, _ := st.CountUsers(ctx); n != 0 {
		t.Fatalf("fresh database has %d users", n)
	}
	u := admin(t, st)
	if u.Login != "admin" || u.ID == uuid.Nil {
		t.Errorf("user = %+v", u)
	}
	if _, err := st.CreateAdmin(ctx, "outro", "hash"); !errors.Is(err, store.ErrAdminExists) {
		t.Errorf("a second administrator: %v, want ErrAdminExists", err)
	}
	if n, _ := st.CountUsers(ctx); n != 1 {
		t.Errorf("users = %d", n)
	}
}

func TestConcurrentSetupCreatesExactlyOneAdministrator(t *testing.T) {
	st := storetest.New(t)
	var created atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := st.CreateAdmin(ctx, "admin"+string(rune('a'+i)), "hash"); err == nil {
				created.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Errorf("%d administrators created, want exactly 1", created.Load())
	}
	if n, _ := st.CountUsers(ctx); n != 1 {
		t.Errorf("users = %d", n)
	}
}

func TestFirstAdministratorAdoptsOrphanProjects(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	orphan := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO projects (id, slug, repository_url) VALUES ($1, 'antigo', 'https://github.com/a/b')`, orphan); err != nil {
		t.Fatal(err)
	}
	u := admin(t, st)
	p, err := st.GetProjectFor(ctx, orphan, u.ID)
	if err != nil || p.OwnerID == nil || *p.OwnerID != u.ID {
		t.Errorf("project = %+v, %v: the first administrator must adopt projects without owner", p, err)
	}
}

func TestProjectsAreScopedByOwner(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	a := admin(t, st)
	b := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (id, login, password_hash) VALUES ($1, 'segundo', 'x')`, b); err != nil {
		t.Fatal(err)
	}

	project, err := st.CreateProject(ctx, a.ID, "do-a", "https://github.com/a/b", "")
	if err != nil {
		t.Fatal(err)
	}
	dep, _, err := st.CreateDeploymentWithJob(ctx, project.ID, "k", nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.GetProjectFor(ctx, project.ID, a.ID); err != nil {
		t.Errorf("owner: %v", err)
	}
	if _, err := st.GetProjectFor(ctx, project.ID, b); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner reading the project: %v, want ErrNotFound (not revealed)", err)
	}
	if _, err := st.GetDeploymentFor(ctx, dep.ID, a.ID); err != nil {
		t.Errorf("owner reading the deployment: %v", err)
	}
	if _, err := st.GetDeploymentFor(ctx, dep.ID, b); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("another owner reading the deployment: %v, want ErrNotFound", err)
	}
	if _, err := st.GetProjectFor(ctx, uuid.New(), a.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown project: %v", err)
	}
}

func newSession(t *testing.T, st *store.Store, userID uuid.UUID) (string, []byte) {
	t.Helper()
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSession(ctx, userID, hash, 12*time.Hour, "203.0.113.7", "Mozilla/5.0"); err != nil {
		t.Fatal(err)
	}
	return token, hash
}

func TestSessionLifecycle(t *testing.T) {
	st := storetest.New(t)
	u := admin(t, st)
	_, hash := newSession(t, st, u.ID)

	sess, got, err := st.GetSession(ctx, hash, 2*time.Hour)
	if err != nil || got.Login != "admin" || got.ID != u.ID || sess.UserID != u.ID {
		t.Fatalf("session = %+v, user %+v, %v", sess, got, err)
	}
	if _, _, err := st.GetSession(ctx, auth.HashToken("token-inexistente"), 2*time.Hour); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown token: %v", err)
	}

	if err := st.DeleteSession(ctx, hash); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.GetSession(ctx, hash, 2*time.Hour); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a logged out session must stop working: %v", err)
	}
}

func TestSessionExpiresByIdleAndAbsoluteTime(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	u := admin(t, st)

	_, idle := newSession(t, st, u.ID)
	if _, err := pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() - interval '3 hours' WHERE token_hash = $1`, idle); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.GetSession(ctx, idle, 2*time.Hour); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a session idle for 3h with a 2h limit: %v, want ErrNotFound", err)
	}

	_, abs := newSession(t, st, u.ID)
	if _, err := pool.Exec(ctx, `UPDATE sessions SET expires_at = now() - interval '1 second' WHERE token_hash = $1`, abs); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.GetSession(ctx, abs, 2*time.Hour); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a session past its absolute limit: %v, want ErrNotFound", err)
	}

	// A atividade renova o limite ocioso (no máximo uma vez por minuto), mas
	// nunca o absoluto.
	_, active := newSession(t, st, u.ID)
	if _, err := pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() - interval '90 minutes' WHERE token_hash = $1`, active); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.GetSession(ctx, active, 2*time.Hour); err != nil {
		t.Fatalf("active session: %v", err)
	}
	var age float64
	if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM now() - last_seen_at) FROM sessions WHERE token_hash = $1`, active).Scan(&age); err != nil {
		t.Fatal(err)
	}
	if age > 5 {
		t.Errorf("last_seen_at is %.0fs old: using the session must renew the idle limit", age)
	}
}

func TestSessionTouchIsThrottled(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	u := admin(t, st)
	_, hash := newSession(t, st, u.ID)
	if _, err := pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() - interval '10 seconds' WHERE token_hash = $1`, hash); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.GetSession(ctx, hash, 2*time.Hour); err != nil {
		t.Fatal(err)
	}
	var age float64
	if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM now() - last_seen_at) FROM sessions WHERE token_hash = $1`, hash).Scan(&age); err != nil {
		t.Fatal(err)
	}
	if age < 9 {
		t.Errorf("last_seen_at was rewritten within the same minute (age %.0fs): that would write on every request", age)
	}
}

func TestOnlyTheHashOfTheTokenIsStored(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	u := admin(t, st)
	token, _ := newSession(t, st, u.ID)
	var leaked bool
	if err := pool.QueryRow(ctx, `SELECT bool_or(position(convert_to($1, 'UTF8') in token_hash) > 0 OR user_agent LIKE '%' || $1 || '%' OR ip LIKE '%' || $1 || '%') FROM sessions`, token).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked {
		t.Error("the session token itself must never be stored")
	}
}

func TestPasswordChangeEndsTheOtherSessions(t *testing.T) {
	st := storetest.New(t)
	u := admin(t, st)
	_, keep := newSession(t, st, u.ID)
	_, other := newSession(t, st, u.ID)

	if err := st.SetPasswordHash(ctx, u.ID, "novo-hash"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteOtherSessions(ctx, u.ID, keep); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.GetSession(ctx, keep, time.Hour); err != nil {
		t.Errorf("the current session must survive: %v", err)
	}
	if _, _, err := st.GetSession(ctx, other, time.Hour); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("the other session must end: %v", err)
	}
	if hash, err := st.GetPasswordHash(ctx, u.ID); err != nil || hash != "novo-hash" {
		t.Errorf("hash = %q, %v", hash, err)
	}
}

func TestResetPasswordByLoginEndsEverySession(t *testing.T) {
	st := storetest.New(t)
	u := admin(t, st)
	_, hash := newSession(t, st, u.ID)
	if err := st.ResetPasswordByLogin(ctx, "admin", "hash-redefinido"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.GetSession(ctx, hash, time.Hour); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("a reset must end every session: %v", err)
	}
	if _, h, _ := st.GetUserByLogin(ctx, "admin"); h != "hash-redefinido" {
		t.Errorf("hash = %q", h)
	}
	if err := st.ResetPasswordByLogin(ctx, "ninguem", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown login: %v", err)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	st, pool := storetest.NewWithPool(t)
	u := admin(t, st)
	_, live := newSession(t, st, u.ID)
	_, dead := newSession(t, st, u.ID)
	_, stale := newSession(t, st, u.ID)
	if _, err := pool.Exec(ctx, `UPDATE sessions SET expires_at = now() - interval '1 minute' WHERE token_hash = $1`, dead); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() - interval '5 hours' WHERE token_hash = $1`, stale); err != nil {
		t.Fatal(err)
	}
	n, err := st.DeleteExpiredSessions(ctx, 2*time.Hour)
	if err != nil || n != 2 {
		t.Errorf("deleted %d (%v), want 2", n, err)
	}
	if _, _, err := st.GetSession(ctx, live, time.Hour); err != nil {
		t.Errorf("the live session must remain: %v", err)
	}
}

func TestAuditTrail(t *testing.T) {
	st := storetest.New(t)
	u := admin(t, st)
	if err := st.AppendAudit(ctx, domain.AuditEvent{ActorID: &u.ID, Action: "login", IP: "203.0.113.7", Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendAudit(ctx, domain.AuditEvent{Action: "login", Target: "admin", IP: "198.51.100.1", Outcome: "failed"}); err != nil {
		t.Fatal(err)
	}
	// Texto hostil do cliente não derruba a gravação.
	if err := st.AppendAudit(ctx, domain.AuditEvent{Action: "x\x00y", Target: strings.Repeat("é", 400), IP: "a\xffb", Outcome: "ok"}); err != nil {
		t.Errorf("hostile text: %v", err)
	}
	events, err := st.ListAudit(ctx, 10)
	if err != nil || len(events) != 3 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	for _, e := range events {
		if e.Action == "login" && e.Outcome == "ok" && (e.ActorID == nil || *e.ActorID != u.ID) {
			t.Errorf("actor lost: %+v", e)
		}
		if len(e.Target) > 256 {
			t.Errorf("target not clipped: %d bytes", len(e.Target))
		}
	}
}

func TestSessionForAMissingUser(t *testing.T) {
	st := storetest.New(t)
	_, hash, _ := auth.NewSessionToken()
	if _, err := st.CreateSession(ctx, uuid.New(), hash, time.Hour, "", ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}
