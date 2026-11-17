package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/planner"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Limites de paginação (DEC-28 itens 2 e 6).
const (
	defaultPageSize = 20
	maxPageSize     = 100

	defaultEventPage = 200
	maxEventPage     = 1000
)

// Códigos de erro estáveis, além dos derivados do status (DEC-28 item 8).
const (
	codeNotAwaitingInput    = "not_awaiting_input"
	codeDeploymentStarted   = "deployment_started"
	codeDeploymentFinished  = "deployment_finished"
	codeAnalysisUnavailable = "analysis_unavailable"
	codePlanUnavailable     = "plan_unavailable"
)

// intParam lê um parâmetro inteiro da query dentro de [min, max]. Ausente vale
// def. Valor malformado ou fora da faixa já responde 400 e devolve ok=false.
func intParam(w http.ResponseWriter, r *http.Request, name string, def, min, max int) (int, bool) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		writeError(w, http.StatusBadRequest, "parâmetro "+name+" inválido: use um inteiro de "+strconv.Itoa(min)+" a "+strconv.Itoa(max))
		return 0, false
	}
	return n, true
}

// pageParams lê limit e before das listas paginadas.
func pageParams(w http.ResponseWriter, r *http.Request) (limit int, after *store.Cursor, ok bool) {
	limit, ok = intParam(w, r, "limit", defaultPageSize, 1, maxPageSize)
	if !ok {
		return 0, nil, false
	}
	if raw := r.URL.Query().Get("before"); raw != "" {
		c, err := store.ParseCursor(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "parâmetro before inválido: use o next_before devolvido pela API")
			return 0, nil, false
		}
		after = &c
	}
	return limit, after, true
}

func nextBefore(c *store.Cursor) string {
	if c == nil {
		return ""
	}
	return c.String()
}

type projectList struct {
	Projects   []domain.ProjectSummary `json:"projects"`
	NextBefore string                  `json:"next_before"`
}

// handleListProjects lista os projetos do administrador (DEC-28 item 5).
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request, user domain.User) {
	limit, after, ok := pageParams(w, r)
	if !ok {
		return
	}
	items, next, err := s.store.ListProjectsFor(r.Context(), user.ID, limit, after)
	if err != nil {
		s.log.Error("listar projetos", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, projectList{Projects: items, NextBefore: nextBefore(next)})
}

type deploymentList struct {
	Deployments []domain.Deployment `json:"deployments"`
	NextBefore  string              `json:"next_before"`
}

// handleListDeployments lista o histórico de deploys de um projeto.
func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request, user domain.User) {
	projectID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id de projeto inválido")
		return
	}
	limit, after, ok := pageParams(w, r)
	if !ok {
		return
	}
	if _, err := s.store.GetProjectFor(r.Context(), projectID, user.ID); errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return
	} else if err != nil {
		s.log.Error("buscar projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	items, next, err := s.store.ListDeployments(r.Context(), projectID, limit, after)
	if err != nil {
		s.log.Error("listar deploys", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, deploymentList{Deployments: items, NextBefore: nextBefore(next)})
}

type eventList struct {
	Events    []domain.DeploymentEvent `json:"events"`
	NextAfter int                      `json:"next_after"`
	HasMore   bool                     `json:"has_more"`
	State     domain.DeploymentState   `json:"state"`
	Settled   bool                     `json:"settled"`
}

// handleListEvents devolve os eventos novos de um deploy (DEC-28 itens 2 e 3).
func (s *Server) handleListEvents(w http.ResponseWriter, r *http.Request, user domain.User) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	after, ok := intParam(w, r, "after", 0, 0, 1<<31-1)
	if !ok {
		return
	}
	limit, ok := intParam(w, r, "limit", defaultEventPage, 1, maxEventPage)
	if !ok {
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
	page, err := s.store.ListEventsAfter(r.Context(), id, after, limit)
	if err != nil {
		s.log.Error("listar eventos", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	next := after
	if n := len(page.Events); n > 0 {
		next = page.Events[n-1].Sequence
	}
	writeJSON(w, http.StatusOK, eventList{
		Events: page.Events, NextAfter: next, HasMore: page.HasMore, State: page.State, Settled: page.Settled,
	})
}

// analysisOf devolve a análise do deploy, já respondendo o erro quando não há.
func (s *Server) analysisOf(w http.ResponseWriter, r *http.Request, user domain.User) (domain.Deployment, domain.Analysis, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return domain.Deployment{}, domain.Analysis{}, false
	}
	dep, err := s.store.GetDeploymentFor(r.Context(), id, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "deploy não encontrado")
		return domain.Deployment{}, domain.Analysis{}, false
	}
	if err != nil {
		s.log.Error("buscar deployment", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return domain.Deployment{}, domain.Analysis{}, false
	}
	if dep.AnalysisID == nil {
		writeCode(w, http.StatusNotFound, codeAnalysisUnavailable, "este deploy ainda não tem análise")
		return domain.Deployment{}, domain.Analysis{}, false
	}
	analysis, err := s.store.GetAnalysis(r.Context(), *dep.AnalysisID)
	if err != nil {
		s.log.Error("buscar análise", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return domain.Deployment{}, domain.Analysis{}, false
	}
	return dep, analysis, true
}

// handleGetAnalysis expõe o snapshot da análise do deploy (RF-03).
func (s *Server) handleGetAnalysis(w http.ResponseWriter, r *http.Request, user domain.User) {
	if _, analysis, ok := s.analysisOf(w, r, user); ok {
		writeJSON(w, http.StatusOK, analysis)
	}
}

// handleGetPlan expõe o plano e o Dockerfile que a análise do deploy gera
// (DEC-28 item 5).
func (s *Server) handleGetPlan(w http.ResponseWriter, r *http.Request, user domain.User) {
	_, analysis, ok := s.analysisOf(w, r, user)
	if !ok {
		return
	}
	if analysis.Result.Outcome != domain.AnalysisReady {
		writeCode(w, http.StatusConflict, codePlanUnavailable, "a análise não está pronta: não há plano para este deploy")
		return
	}
	plan, err := planner.FromAnalysis(analysis.Result)
	if err != nil {
		s.log.Error("gerar plano", "erro", err)
		writeCode(w, http.StatusConflict, codePlanUnavailable, "não foi possível gerar o plano desta análise")
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// handleCancelDeployment implementa o cancelamento de DEC-28 item 7.
func (s *Server) handleCancelDeployment(w http.ResponseWriter, r *http.Request, user domain.User) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
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
	dep, err := s.store.CancelDeployment(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "deploy não encontrado")
	case errors.Is(err, store.ErrDeploymentStarted):
		writeCode(w, http.StatusConflict, codeDeploymentStarted, "o deploy já começou a executar e não pode mais ser cancelado")
	case errors.Is(err, store.ErrDeploymentFinished):
		writeCode(w, http.StatusConflict, codeDeploymentFinished, "o deploy já terminou")
	case err != nil:
		s.log.Error("cancelar deployment", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "deployment.cancel", id.String(), "ok")
		writeJSON(w, http.StatusOK, dep)
	}
}
