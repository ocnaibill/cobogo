package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/auth"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/httpapi"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/store/storetest"
)

const (
	publicURL    = "https://cobogo.test"
	adminLogin   = "admin"
	adminPass    = "uma-senha-de-teste-bem-longa"
	cookieSecure = "__Host-cobogo_session"
)

// logSink guarda tudo o que a API escreve no log, para os testes verificarem
// que nada sensível foi registrado.
type logSink struct{ b strings.Builder }

func (l *logSink) Write(p []byte) (int, error) { return l.b.Write(p) }

type harness struct {
	t      *testing.T
	st     *store.Store
	pool   *pgxpool.Pool
	router http.Handler
	logs   *logSink
	hasher *auth.Hasher
	cookie *http.Cookie
	remote string
}

type options struct {
	trusted        []netip.Prefix
	cookieInsecure bool
	publicURL      string
	setupToken     *auth.SetupToken
	limiter        *auth.LoginLimiter
	backupDir      string
}

func newHarness(t *testing.T, opts ...func(*options)) *harness {
	t.Helper()
	st, pool := storetest.NewWithPool(t)
	// Custo baixo só nos testes: o argon2id de produção levaria minutos aqui.
	hasher, err := auth.NewHasherWithParams(2, 1024, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	o := options{publicURL: publicURL, limiter: auth.NewLoginLimiter()}
	for _, f := range opts {
		f(&o)
	}
	sink := &logSink{}
	router, err := httpapi.NewRouter(httpapi.Options{
		Store: st, Log: slog.New(slog.NewTextHandler(io.MultiWriter(sink, io.Discard), nil)),
		PublicURL: o.publicURL, CookieInsecure: o.cookieInsecure, TrustedProxies: o.trusted,
		SetupToken: o.setupToken, Hasher: hasher, Limiter: o.limiter, BackupDir: o.backupDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &harness{t: t, st: st, pool: pool, router: router, logs: sink, hasher: hasher, remote: "203.0.113.7:4321"}
}

// newReq monta uma requisição como a do painel: mesma origem, JSON e, se
// houver, o cookie da sessão.
func (h *harness) newReq(method, path, body string) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rd)
	r.RemoteAddr = h.remote
	r.Header.Set("Origin", publicURL)
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if h.cookie != nil {
		r.AddCookie(h.cookie)
	}
	return r
}

func (h *harness) serve(r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.router.ServeHTTP(rec, r)
	return rec
}

func (h *harness) do(method, path, body string) *httptest.ResponseRecorder {
	return h.serve(h.newReq(method, path, body))
}

// admin cria o administrador direto no banco, sem passar pela API.
func (h *harness) admin() domain.User {
	h.t.Helper()
	hash, err := h.hasher.Hash(context.Background(), adminPass)
	if err != nil {
		h.t.Fatal(err)
	}
	u, err := h.st.CreateAdmin(context.Background(), adminLogin, hash)
	if err != nil {
		h.t.Fatal(err)
	}
	return u
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieSecure || c.Name == "cobogo_session" {
			return c
		}
	}
	return nil
}

// login entra pela API e guarda o cookie para as próximas chamadas.
func (h *harness) login() domain.User {
	h.t.Helper()
	u := h.admin()
	h.signIn(adminLogin, adminPass)
	return u
}

func (h *harness) signIn(login, password string) {
	h.t.Helper()
	rec := h.do("POST", "/api/v1/auth/login", `{"login":"`+login+`","password":"`+password+`"}`)
	if rec.Code != http.StatusOK {
		h.t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	c := sessionCookie(rec)
	if c == nil {
		h.t.Fatal("login did not set a session cookie")
	}
	h.cookie = &http.Cookie{Name: c.Name, Value: c.Value}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
	return v
}

type apiError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (h *harness) project() string {
	h.t.Helper()
	rec := h.do("POST", "/api/v1/projects", `{"slug":"app-teste","repository_url":"https://github.com/exemplo/repositorio"}`)
	if rec.Code != http.StatusCreated {
		h.t.Fatalf("create project: %d %s", rec.Code, rec.Body)
	}
	return decode[domain.Project](h.t, rec).ID.String()
}

func (h *harness) deployment(projectID, body string) domain.Deployment {
	h.t.Helper()
	rec := h.do("POST", "/api/v1/projects/"+projectID+"/deployments", body)
	if rec.Code != http.StatusAccepted {
		h.t.Fatalf("create deployment: %d %s", rec.Code, rec.Body)
	}
	return decode[domain.Deployment](h.t, rec)
}
