// Package httpapi é a API Go de M1: registrar projeto, disparar deploy e
// consultar plano/eventos (RF-01, RF-03, RF-05, RF-07). Ainda sem
// autenticação (RF-09 entra em M2, antes de qualquer exposição externa).
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/store"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

type Server struct {
	store *store.Store
	log   *slog.Logger
}

func NewRouter(st *store.Store, log *slog.Logger) http.Handler {
	s := &Server{store: st, log: log}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("POST /projects", s.handleCreateProject)
	mux.HandleFunc("GET /projects/{id}", s.handleGetProject)
	mux.HandleFunc("POST /projects/{id}/deployments", s.handleCreateDeployment)
	mux.HandleFunc("GET /deployments/{id}", s.handleGetDeployment)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type createProjectRequest struct {
	Slug          string `json:"slug"`
	RepositoryURL string `json:"repository_url"`
	RootDirectory string `json:"root_directory"`
}

// handleCreateProject implementa RF-01: aceitar URL pública permitida e
// rejeitar protocolos indevidos.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req createProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "corpo inválido: "+err.Error())
		return
	}

	if !slugPattern.MatchString(req.Slug) {
		writeError(w, http.StatusBadRequest, "slug inválido: use letras minúsculas, números e hífen")
		return
	}
	if !isAllowedRepositoryURL(req.RepositoryURL) {
		writeError(w, http.StatusBadRequest, "repository_url deve ser https:// (RNF-04)")
		return
	}

	project, err := s.store.CreateProject(r.Context(), req.Slug, req.RepositoryURL, req.RootDirectory)
	if err != nil {
		s.log.Error("criar projeto", "erro", err)
		writeError(w, http.StatusConflict, "não foi possível criar o projeto; slug já pode existir")
		return
	}
	writeJSON(w, http.StatusCreated, project)
}

func isAllowedRepositoryURL(url string) bool {
	return len(url) > len("https://") && url[:8] == "https://"
}

func (s *Server) handleGetProject(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	project, err := s.store.GetProject(r.Context(), id)
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
	// Port é o override explícito do solicitante (RN-03); usado quando a
	// análise não encontra um contrato de porta inferível (RN-02).
	Port *int `json:"port,omitempty"`
}

// handleCreateDeployment implementa RF-05: cria a operação e retorna o ID
// sem esperar o build.
func (s *Server) handleCreateDeployment(w http.ResponseWriter, r *http.Request) {
	projectID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id de projeto inválido")
		return
	}

	var req createDeploymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "corpo inválido: "+err.Error())
		return
	}
	if req.IdempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "idempotency_key é obrigatória (RN-07)")
		return
	}

	if _, err := s.store.GetProject(r.Context(), projectID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return
	}

	dep, _, err := s.store.CreateDeploymentWithJob(r.Context(), projectID, req.IdempotencyKey, req.Port)
	if err != nil {
		s.log.Error("criar deployment", "erro", err)
		writeError(w, http.StatusConflict, "não foi possível criar o deploy; idempotency_key já pode existir")
		return
	}
	writeJSON(w, http.StatusAccepted, dep)
}

type deploymentView struct {
	Deployment any `json:"deployment"`
	Events     any `json:"events"`
	Release    any `json:"release,omitempty"`
}

// handleGetDeployment implementa RF-03/RF-07: expõe plano, evidências e
// eventos sem exigir acesso ao terminal do worker.
func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}

	dep, err := s.store.GetDeployment(r.Context(), id)
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
