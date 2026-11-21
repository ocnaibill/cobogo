package httpapi

import (
	"errors"
	"net/http"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/secrets"
	"github.com/ocnaibill/cobogo/internal/store"
)

// PostgreSQL auxiliar (DEC-42). A API registra a escolha do usuário e gera a
// credencial; quem cria e apaga o banco é o worker.

const (
	codeVariableDefined = "variable_defined"
	codeVariableLinked  = "variable_linked"
	codeManagedExists   = "managed_exists"
	codeNoManagedDB     = "no_managed_database"
	databasePasswordLen = 24
)

type databaseView struct {
	// Need é o que a última análise achou sobre PostgreSQL (nulo se nada).
	Need *domain.ServiceNeed `json:"need"`
	// Service é a escolha vigente do projeto (nulo se ainda não escolheu).
	Service *domain.Service `json:"service"`
	// Variable é a variável de conexão: a do vínculo, ou a provável.
	Variable string `json:"variable"`
	// Volume são os dados do banco gerenciado, com o tamanho.
	Volume *store.ProjectVolume `json:"volume"`
}

func (s *Server) databaseView(w http.ResponseWriter, r *http.Request, p domain.Project, user domain.User) (databaseView, bool) {
	view := databaseView{Variable: "DATABASE_URL"}
	if analysis, err := s.store.LatestAnalysis(r.Context(), p.ID); err == nil {
		for i := range analysis.Result.ServiceNeeds {
			if n := analysis.Result.ServiceNeeds[i]; n.Engine == domain.ServicePostgres {
				view.Need, view.Variable = &n, n.Variable
				break
			}
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		s.log.Error("buscar análise", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return databaseView{}, false
	}
	svc, err := s.store.GetService(r.Context(), p.ID, domain.ServicePostgres)
	switch {
	case err == nil:
		view.Service, view.Variable = &svc, svc.Variable
	case !errors.Is(err, store.ErrNotFound):
		s.log.Error("buscar serviço", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return databaseView{}, false
	}
	if view.Service != nil && view.Service.Mode == domain.ServiceManaged {
		vols, err := s.store.ListVolumesFor(r.Context(), p.ID, user.ID)
		if err != nil {
			s.log.Error("listar volumes", "erro", err)
			writeError(w, http.StatusInternalServerError, "erro interno")
			return databaseView{}, false
		}
		for i := range vols {
			if vols[i].Path == domain.PostgresDataPath {
				view.Volume = &vols[i]
			}
		}
	}
	return view, true
}

func (s *Server) handleGetDatabase(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	if v, ok := s.databaseView(w, r, p, user); ok {
		writeJSON(w, http.StatusOK, v)
	}
}

type chooseDatabaseRequest struct {
	Choice   string `json:"choice"`
	Variable string `json:"variable"`
}

type chooseDatabaseResponse struct {
	databaseView
	// Requeued são os deploys que esperavam esta escolha e voltaram à fila.
	Requeued int `json:"requeued"`
}

// handleChooseDatabase registra o destino do banco (HEU-06.6). Nada é criado
// aqui: no modo gerenciado, o worker cria o banco no próximo deploy, e os deploys
// parados esperando esta escolha voltam à fila.
func (s *Server) handleChooseDatabase(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	var req chooseDatabaseRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	switch req.Choice {
	case domain.ServiceManaged, domain.ServiceExternal, domain.ServiceNone:
	default:
		writeError(w, http.StatusBadRequest, "choice deve ser managed, external ou none")
		return
	}
	variable := req.Variable
	if variable == "" {
		variable = "DATABASE_URL"
	}
	if !envNamePattern.MatchString(variable) {
		writeError(w, http.StatusBadRequest, "nome de variável inválido: use letras, números e _, sem começar por número")
		return
	}
	if analyzer.ClassifyVariable(variable) == domain.ClassPlatform {
		writeError(w, http.StatusBadRequest, variable+" é fornecida pela plataforma e não pode ser a conexão do banco")
		return
	}

	url := ""
	if req.Choice == domain.ServiceManaged {
		pass, err := secrets.Generate(databasePasswordLen)
		if err != nil {
			s.log.Error("gerar senha do banco", "erro", err)
			writeError(w, http.StatusInternalServerError, "erro interno")
			return
		}
		url = "postgres://app:" + pass + "@" + domain.ServiceContainerName(p.ID.String()) + ":5432/app"
	}
	svc, err := s.store.ChooseDatabase(r.Context(), p.ID, user.ID, req.Choice, variable, url)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return
	case errors.Is(err, store.ErrVariableDefined):
		s.audit(r, &user, "project.database", p.ID.String(), "refused")
		writeCode(w, http.StatusConflict, codeVariableDefined, variable+" já tem um valor digitado por você; remova-o (ou escolha usar um banco que já tem) antes de criar o banco do Cobogó, que não sobrescreve o que você definiu")
		return
	case errors.Is(err, store.ErrManagedExists):
		writeCode(w, http.StatusConflict, codeManagedExists, "o projeto já tem um PostgreSQL do Cobogó; apague-o (com os dados) antes de escolher outra coisa")
		return
	case err != nil:
		s.log.Error("registrar escolha do banco", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.auditDetail(r, &user, "project.database", p.ID.String(), "ok", map[string]any{"names": []string{req.Choice, svc.Variable}})

	requeued := 0
	if waiting, err := s.store.WaitingForDatabase(r.Context(), p.ID); err != nil {
		s.log.Error("listar deploys à espera do banco", "erro", err)
	} else {
		for _, id := range waiting {
			if _, err := s.store.AnswerDeployment(r.Context(), id, map[string]string{}); err != nil {
				s.log.Warn("reenfileirar deploy depois da escolha do banco", "deployment", id, "erro", err)
				continue
			}
			requeued++
		}
	}
	view, ok := s.databaseView(w, r, p, user)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, chooseDatabaseResponse{databaseView: view, Requeued: requeued})
}

type deleteDatabaseRequest struct {
	ConfirmSlug string `json:"confirm_slug"`
}

// handleDeleteDatabase pede ao worker que apague o PostgreSQL gerenciado e os
// dados. Irreversível; a confirmação é o nome do projeto.
func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	var req deleteDatabaseRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	op, err := s.store.RequestDeleteService(r.Context(), p.ID, user.ID, req.ConfirmSlug)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
	case errors.Is(err, store.ErrConfirmationMismatch):
		s.audit(r, &user, "project.database.delete", p.ID.String(), "refused")
		writeCode(w, http.StatusBadRequest, codeConfirmationMismatch, "o nome de confirmação não é o do projeto")
	case errors.Is(err, store.ErrNoManagedService):
		writeCode(w, http.StatusConflict, codeNoManagedDB, "o projeto não tem um PostgreSQL do Cobogó para apagar")
	case err != nil:
		s.log.Error("pedir exclusão do banco", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "project.database.delete", p.ID.String(), "ok")
		writeJSON(w, http.StatusAccepted, op)
	}
}
