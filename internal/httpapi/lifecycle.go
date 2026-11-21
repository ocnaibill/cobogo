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
	codeAppAlreadyStopped    = "app_already_stopped"
	codeAppAlreadyRunning    = "app_already_running"
)

type deleteProjectRequest struct {
	ConfirmSlug string `json:"confirm_slug"`
	// DeleteData apaga também os volumes do projeto (DEC-41 item 5). Sem isso,
	// eles ficam e aparecem como volumes sem projeto.
	DeleteData bool `json:"delete_data"`
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
	op, err := s.store.DeleteProject(r.Context(), p.ID, user.ID, req.ConfirmSlug, req.DeleteData)
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

// appOperationAudit é a ação de auditoria de cada operação de aplicação.
var appOperationAudit = map[domain.OperationKind]string{
	domain.OpStopApp: "project.app.stop", domain.OpStartApp: "project.app.start", domain.OpRestartApp: "project.app.restart",
}

// handleAppOperation pede ao worker que pare, inicie ou reinicie a release
// ativa (DEC-40). A API só enfileira; quem mexe no contêiner é o worker.
func (s *Server) handleAppOperation(kind domain.OperationKind) func(http.ResponseWriter, *http.Request, domain.User) {
	return func(w http.ResponseWriter, r *http.Request, user domain.User) {
		p, ok := s.projectOf(w, r, user)
		if !ok {
			return
		}
		op, err := s.store.RequestAppOperation(r.Context(), p.ID, user.ID, kind)
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeError(w, http.StatusNotFound, "projeto não encontrado")
		case errors.Is(err, store.ErrNoActiveRelease):
			writeCode(w, http.StatusConflict, codeNoActiveRelease, "o projeto não tem release no ar; faça um deploy primeiro")
		case errors.Is(err, store.ErrAppAlreadyStopped):
			writeCode(w, http.StatusConflict, codeAppAlreadyStopped, "a aplicação já está parada")
		case errors.Is(err, store.ErrAppAlreadyRunning):
			writeCode(w, http.StatusConflict, codeAppAlreadyRunning, "a aplicação já está no ar")
		case err != nil:
			s.log.Error("pedir operação da aplicação", "tipo", kind, "erro", err)
			writeError(w, http.StatusInternalServerError, "erro interno")
		default:
			s.audit(r, &user, appOperationAudit[kind], p.ID.String(), "ok")
			writeJSON(w, http.StatusAccepted, op)
		}
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
