package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Ciclo de vida (DEC-38): a API só pede; quem mexe em contêiner é o worker.

const (
	codeDeploymentInProgress = "deployment_in_progress"
	codeConfirmationMismatch = "confirmation_mismatch"
	codeNoActiveRelease      = "no_active_release"
)

type deleteProjectRequest struct {
	ConfirmSlug string `json:"confirm_slug"`
}

// handleDeleteProject exclui o projeto de forma lógica: some das listas e das
// rotas na hora, as variáveis cifradas são apagadas e a limpeza do Docker vira
// uma operação. O histórico fica.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	var req deleteProjectRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	if req.ConfirmSlug == "" {
		writeCode(w, http.StatusBadRequest, codeConfirmationMismatch, "informe confirm_slug com o nome do projeto")
		return
	}
	op, err := s.store.DeleteProject(r.Context(), p.ID, user.ID, req.ConfirmSlug)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
	case errors.Is(err, store.ErrConfirmationMismatch):
		s.audit(r, &user, "project.delete", p.Slug, "refused")
		writeCode(w, http.StatusBadRequest, codeConfirmationMismatch, "o nome de confirmação não é o do projeto")
	case errors.Is(err, store.ErrDeploymentInProgress):
		s.audit(r, &user, "project.delete", p.Slug, "refused")
		writeCode(w, http.StatusConflict, codeDeploymentInProgress, "há um deploy em andamento; espere ele terminar para excluir o projeto")
	case err != nil:
		s.log.Error("excluir projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "project.delete", p.Slug, "ok")
		writeJSON(w, http.StatusAccepted, op)
	}
}

// handleApplyLimits pede ao worker que aplique os limites efetivos de hoje ao
// contêiner da release ativa (#33).
func (s *Server) handleApplyLimits(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	op, err := s.store.RequestApplyLimits(r.Context(), p.ID, user.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
	case errors.Is(err, store.ErrNoActiveRelease):
		writeCode(w, http.StatusConflict, codeNoActiveRelease, "o projeto não tem release no ar; os limites valem no próximo deploy")
	case err != nil:
		s.log.Error("pedir aplicação de limites", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "project.limits.apply", p.ID.String(), "ok")
		writeJSON(w, http.StatusAccepted, op)
	}
}

func (s *Server) handleGetOperation(w http.ResponseWriter, r *http.Request, user domain.User) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id inválido")
		return
	}
	op, err := s.store.GetOperationFor(r.Context(), id, user.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "operação não encontrada")
		return
	}
	if err != nil {
		s.log.Error("buscar operação", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, op)
}

// handleListOperations lista as operações recentes de um projeto, inclusive de
// um projeto excluído: a limpeza dele ainda pode estar em andamento.
func (s *Server) handleListOperations(w http.ResponseWriter, r *http.Request, user domain.User) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id de projeto inválido")
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "limit deve ser de 1 a 100")
			return
		}
		limit = n
	}
	ops, err := s.store.ListOperationsFor(r.Context(), id, user.ID, limit)
	if err != nil {
		s.log.Error("listar operações", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"operations": ops})
}
