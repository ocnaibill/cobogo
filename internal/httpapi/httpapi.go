// Package httpapi é a API Go do painel: autenticação do administrador,
// projetos, deploys, pendências e variáveis (RF-01, RF-03, RF-05, RF-07,
// RF-08, RF-09, RF-10). Toda rota, exceto as listadas em publicRoutes, exige
// uma sessão válida; as de alteração exigem também origem permitida
// (DEC-27).
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/auth"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/validate"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Duração das sessões (DEC-27 item 6).
const (
	SessionIdle     = 2 * time.Hour
	SessionAbsolute = 12 * time.Hour
)

// Options reúne o que a API precisa para funcionar.
type Options struct {
	Store *store.Store
	Log   *slog.Logger
	// PublicURL é a origem do painel (esquema e host), para conferir o Origin.
	PublicURL string
	// CookieInsecure usa um cookie sem Secure nem prefixo __Host-: só para
	// desenvolvimento por HTTP.
	CookieInsecure bool
	// TrustedProxies são as redes de que cabeçalhos de proxy são aceitos.
	TrustedProxies []netip.Prefix
	// GitHosts são os hosts de repositório permitidos (padrão mais extras).
	GitHosts []string
	// SetupToken autoriza a criação do administrador; nil quando já existe um.
	SetupToken *auth.SetupToken
	Hasher     *auth.Hasher
	Limiter    *auth.LoginLimiter
}

type Server struct {
	store          *store.Store
	log            *slog.Logger
	publicOrigin   string
	cookieInsecure bool
	trusted        []netip.Prefix
	gitHosts       []string
	setup          *auth.SetupToken
	hasher         *auth.Hasher
	limiter        *auth.LoginLimiter
	lastProxyWarn  atomic.Int64
}

// NewRouter monta a API. Falha se faltar algo de que a segurança depende.
func NewRouter(o Options) (http.Handler, error) {
	if o.Store == nil || o.Log == nil || o.Hasher == nil || o.Limiter == nil {
		return nil, errors.New("httpapi: store, log, hasher e limiter são obrigatórios")
	}
	u, err := url.Parse(o.PublicURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, errors.New("httpapi: PublicURL inválida")
	}
	hosts := o.GitHosts
	if len(hosts) == 0 {
		hosts = validate.GitHosts(nil)
	}
	s := &Server{
		store: o.Store, log: o.Log,
		publicOrigin:   strings.ToLower(u.Scheme + "://" + u.Host),
		cookieInsecure: o.CookieInsecure,
		trusted:        o.TrustedProxies,
		gitHosts:       hosts,
		setup:          o.SetupToken,
		hasher:         o.Hasher,
		limiter:        o.Limiter,
	}

	mux := http.NewServeMux()
	// Rotas públicas: as únicas que não exigem sessão (DEC-27 item 8).
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /api/v1/setup/status", s.handleSetupStatus)
	mux.HandleFunc("POST /api/v1/setup", s.handleSetup)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)

	// Rotas autenticadas.
	mux.HandleFunc("POST /api/v1/auth/logout", s.authed(s.handleLogout))
	mux.HandleFunc("GET /api/v1/auth/me", s.authed(s.handleMe))
	mux.HandleFunc("POST /api/v1/auth/password", s.authed(s.handleChangePassword))
	mux.HandleFunc("POST /api/v1/projects", s.authed(s.handleCreateProject))
	mux.HandleFunc("GET /api/v1/projects/{id}", s.authed(s.handleGetProject))
	mux.HandleFunc("POST /api/v1/projects/{id}/deployments", s.authed(s.handleCreateDeployment))
	mux.HandleFunc("GET /api/v1/deployments/{id}", s.authed(s.handleGetDeployment))
	mux.HandleFunc("POST /api/v1/deployments/{id}/answers", s.authed(s.handleAnswerDeployment))
	mux.HandleFunc("GET /api/v1/projects/{id}/env", s.authed(s.handleListEnv))
	mux.HandleFunc("PUT /api/v1/projects/{id}/env/{name}", s.authed(s.handlePutEnv))
	mux.HandleFunc("DELETE /api/v1/projects/{id}/env/{name}", s.authed(s.handleDeleteEnv))
	mux.HandleFunc("POST /api/v1/projects/{id}/env/{name}/generate", s.authed(s.handleGenerateEnv))

	// O que não existe responde no mesmo formato de erro, em JSON.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "rota não encontrada")
	})
	return s.wrap(mux), nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// clientIP devolve o IP do cliente, atrás de proxy confiável quando
// configurado, e avisa (no máximo uma vez por hora) se chegar cabeçalho de
// proxy de uma origem não confiável: sinal de que falta configurar
// COBOGO_TRUSTED_PROXIES (DEC-27 item 11).
func (s *Server) clientIP(r *http.Request) string {
	ip, suspicious := auth.ClientIP(r, s.trusted)
	if suspicious {
		now := time.Now().Unix()
		if last := s.lastProxyWarn.Load(); now-last >= 3600 && s.lastProxyWarn.CompareAndSwap(last, now) {
			s.log.Warn("recebi cabeçalho de proxy (CF-Connecting-IP ou X-Forwarded-For) de uma origem não confiável; se há um proxy na frente, configure COBOGO_TRUSTED_PROXIES",
				"origem", r.RemoteAddr)
		}
	}
	if !ip.IsValid() {
		return ""
	}
	return ip.String()
}

// audit registra uma ação do administrador. Só nomes e identificadores entram
// em target, nunca valores.
func (s *Server) audit(r *http.Request, actor *domain.User, action, target, outcome string) {
	ev := domain.AuditEvent{Action: action, Target: target, IP: s.clientIP(r), Outcome: outcome}
	if actor != nil {
		ev.ActorID = &actor.ID
	}
	if err := s.store.AppendAudit(context.WithoutCancel(r.Context()), ev); err != nil {
		s.log.Error("registrar auditoria", "acao", action, "erro", err)
	}
}

type createProjectRequest struct {
	Slug          string `json:"slug"`
	RepositoryURL string `json:"repository_url"`
	RootDirectory string `json:"root_directory"`
}

// handleCreateProject implementa RF-01: aceitar só URL pública de um host
// permitido e um diretório que não escape do repositório.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request, user domain.User) {
	var req createProjectRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	if !slugPattern.MatchString(req.Slug) {
		writeError(w, http.StatusBadRequest, "slug inválido: use letras minúsculas, números e hífen")
		return
	}
	repositoryURL, err := validate.RepositoryURL(req.RepositoryURL, s.gitHosts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	root, err := validate.RootDirectory(req.RootDirectory)
	if err != nil {
		writeError(w, http.StatusBadRequest, "root_directory inválido: "+err.Error())
		return
	}

	project, err := s.store.CreateProject(r.Context(), user.ID, req.Slug, repositoryURL, root)
	if err != nil {
		s.log.Error("criar projeto", "erro", err)
		writeError(w, http.StatusConflict, "não foi possível criar o projeto; o slug já pode existir")
		return
	}
	s.audit(r, &user, "project.create", project.Slug, "ok")
	writeJSON(w, http.StatusCreated, project)
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request, user domain.User) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	project, err := s.store.GetProjectFor(r.Context(), id, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return
	}
	if err != nil {
		s.log.Error("buscar projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, project)
}

type createDeploymentRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
	// Overrides são valores explícitos por campo do plano (RN-03, DEC-24);
	// em geral só existem depois de uma pendência, mas podem ser antecipados.
	Overrides map[string]string `json:"overrides,omitempty"`
}

// handleCreateDeployment implementa RF-05: cria a operação e retorna o ID
// sem esperar o build.
func (s *Server) handleCreateDeployment(w http.ResponseWriter, r *http.Request, user domain.User) {
	projectID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id de projeto inválido")
		return
	}
	var req createDeploymentRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 128 {
		writeError(w, http.StatusBadRequest, "idempotency_key é obrigatória e deve ter até 128 caracteres (RN-07)")
		return
	}
	if err := analyzerCheck(req.Overrides); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	project, err := s.store.GetProjectFor(r.Context(), projectID, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return
	}
	if err != nil {
		s.log.Error("buscar projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	dep, _, err := s.store.CreateDeploymentWithJob(r.Context(), project.ID, req.IdempotencyKey, req.Overrides)
	if err != nil {
		s.log.Error("criar deployment", "erro", err)
		writeError(w, http.StatusConflict, "não foi possível criar o deploy; a idempotency_key já pode existir")
		return
	}
	s.audit(r, &user, "deployment.create", project.Slug+"/"+dep.ID.String(), "ok")
	writeJSON(w, http.StatusAccepted, dep)
}

type answerRequest struct {
	Overrides map[string]string `json:"overrides"`
}

// handleAnswerDeployment implementa RF-08: responde às pendências de um
// deploy em NEEDS_INPUT e o reenfileira para o mesmo SHA, sem criar outro
// deploy.
func (s *Server) handleAnswerDeployment(w http.ResponseWriter, r *http.Request, user domain.User) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	var req answerRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	if len(req.Overrides) == 0 {
		writeError(w, http.StatusBadRequest, "informe ao menos um override")
		return
	}
	if err := analyzerCheck(req.Overrides); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.store.GetDeploymentFor(r.Context(), id, user.ID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deploy não encontrado")
		return
	} else if err != nil {
		s.log.Error("buscar deployment", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	dep, err := s.store.AnswerDeployment(r.Context(), id, req.Overrides)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "deploy não encontrado")
	case errors.Is(err, store.ErrNotAwaitingInput):
		writeError(w, http.StatusConflict, "o deploy não está aguardando informações")
	case err != nil:
		s.log.Error("responder deployment", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "deployment.answer", id.String(), "ok")
		writeJSON(w, http.StatusAccepted, dep)
	}
}

type deploymentView struct {
	Deployment any `json:"deployment"`
	Events     any `json:"events"`
	Release    any `json:"release,omitempty"`
}

// handleGetDeployment implementa RF-03/RF-07: expõe plano, evidências e
// eventos sem exigir acesso ao terminal do worker.
func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request, user domain.User) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	dep, err := s.store.GetDeploymentFor(r.Context(), id, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deploy não encontrado")
		return
	}
	if err != nil {
		s.log.Error("buscar deployment", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	events, err := s.store.ListEvents(r.Context(), id)
	if err != nil {
		s.log.Error("listar eventos", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	view := deploymentView{Deployment: dep, Events: events}
	if release, err := s.store.GetReleaseByDeployment(r.Context(), id); err == nil {
		view.Release = release
	}
	writeJSON(w, http.StatusOK, view)
}
