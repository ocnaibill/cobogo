package httpapi

import (
	"errors"
	"net/http"
	"sort"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/secrets"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Banco auxiliar (DEC-42, DEC-50). A API registra a escolha do usuário e gera a
// credencial; quem cria e apaga o banco é o worker.

const (
	codeVariableDefined = "variable_defined"
	codeVariableLinked  = "variable_linked"
	codeManagedExists   = "managed_exists"
	codeNoManagedDB     = "no_managed_database"
	databasePasswordLen = 24
)

type databaseView struct {
	// Engine é a engine do banco do projeto: a do serviço, a da necessidade que a análise achou ou PostgreSQL.
	Engine string `json:"engine"`
	// Need é o que a última análise achou sobre o banco relacional (nulo se nada).
	Need *domain.ServiceNeed `json:"need"`
	// Service é a escolha vigente do projeto (nulo se ainda não escolheu).
	Service *domain.Service `json:"service"`
	// Variable é a variável de conexão: a do vínculo, ou a provável.
	Variable string `json:"variable"`
	// Volume são os dados do banco gerenciado, com o tamanho.
	Volume *store.ProjectVolume `json:"volume"`
}

func (s *Server) databaseView(w http.ResponseWriter, r *http.Request, p domain.Project, user domain.User) (databaseView, bool) {
	return s.serviceView(w, r, p, user, "")
}

// serviceView é a visão do banco relacional do projeto (engine vazia) ou do Redis (DEC-50 C).
func (s *Server) serviceView(w http.ResponseWriter, r *http.Request, p domain.Project, user domain.User, only string) (databaseView, bool) {
	view := databaseView{Variable: "DATABASE_URL", Engine: domain.ServicePostgres}
	if only == domain.ServiceRedis {
		view = databaseView{Variable: "REDIS_URL", Engine: domain.ServiceRedis}
	}
	var needs []domain.ServiceNeed
	if analysis, err := s.store.LatestAnalysis(r.Context(), p.ID); err == nil {
		needs = analysis.Result.ServiceNeeds
	} else if !errors.Is(err, store.ErrNotFound) {
		s.log.Error("buscar análise", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return databaseView{}, false
	}
	var svc domain.Service
	var err error
	if only == domain.ServiceRedis {
		svc, err = s.store.GetService(r.Context(), p.ID, domain.ServiceRedis)
	} else {
		svc, err = s.store.GetDatabaseService(r.Context(), p.ID)
		if err == nil {
			view.Engine = svc.Engine
		} else if n := primaryNeed(needs); n != nil {
			view.Engine = n.Engine
		}
	}
	for i := range needs {
		if needs[i].Engine == view.Engine {
			n := needs[i]
			view.Need, view.Variable = &n, n.Variable
			break
		}
	}
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
			if vols[i].Path == domain.EngineDataPath(view.Engine) {
				view.Volume = &vols[i]
			}
		}
	}
	return view, true
}

// primaryNeed é a necessidade de banco relacional que a análise mais sustenta: a primeira
// `required` (na ordem das engines) ou, na falta, a primeira `possible`.
func primaryNeed(needs []domain.ServiceNeed) *domain.ServiceNeed {
	for _, level := range []string{domain.NeedRequired, domain.NeedPossible} {
		for i := range needs {
			if needs[i].Level == level && domain.IsRelationalEngine(needs[i].Engine) {
				n := needs[i]
				return &n
			}
		}
	}
	return nil
}

func (s *Server) handleGetDatabase(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	only := ""
	switch e := r.URL.Query().Get("engine"); e {
	case "", domain.ServiceRedis:
		only = e
	default:
		writeError(w, http.StatusBadRequest, "engine deve ser redis, ou ausente para o banco relacional")
		return
	}
	if v, ok := s.serviceView(w, r, p, user, only); ok {
		writeJSON(w, http.StatusOK, v)
	}
}

type chooseDatabaseRequest struct {
	Choice string `json:"choice"`
	// Engine é a engine do banco (postgres ou mysql); vazio vale a da necessidade que a análise achou, ou PostgreSQL.
	Engine string `json:"engine"`
	// Variable é o nome da variável da URL (forma curta, DEC-42).
	Variable string `json:"variable"`
	// Variables liga cada papel da conexão ao nome da variável (DEC-44).
	Variables map[string]string `json:"variables"`
}

var connectionRoles = map[string]bool{
	domain.RoleURL: true, domain.RoleHost: true, domain.RolePort: true, domain.RoleUser: true,
	domain.RolePassword: true, domain.RoleDatabase: true, domain.RoleCA: true,
}

// connectionVariables valida os nomes por papel e devolve o mapa final. Estilo
// URL: o papel `url` (e opcionalmente `ca`). Estilo partes: ao menos host e
// senha, sem `url`.
func connectionVariables(req chooseDatabaseRequest, engine string) (map[string]string, string) {
	vars := map[string]string{}
	for role, name := range req.Variables {
		if !connectionRoles[role] {
			return nil, "papel de variável desconhecido: " + role
		}
		if name != "" {
			vars[role] = name
		}
	}
	if len(vars) == 0 {
		name := req.Variable
		if name == "" {
			name = "DATABASE_URL"
			if engine == domain.ServiceRedis {
				name = "REDIS_URL"
			}
		}
		vars[domain.RoleURL] = name
	}
	if _, isURL := vars[domain.RoleURL]; isURL {
		for role := range vars {
			if role != domain.RoleURL && role != domain.RoleCA {
				return nil, "a conexão por URL não usa a variável do papel " + role
			}
		}
	} else if vars[domain.RoleHost] == "" || vars[domain.RolePassword] == "" {
		return nil, "a conexão em partes precisa, no mínimo, das variáveis do host e da senha"
	}
	seen := map[string]string{}
	for role, name := range vars {
		if !envNamePattern.MatchString(name) {
			return nil, "nome de variável inválido: use letras, números e _, sem começar por número"
		}
		if analyzer.ClassifyVariable(name) == domain.ClassPlatform {
			return nil, name + " é fornecida pela plataforma e não pode ser a conexão do banco"
		}
		if other, dup := seen[name]; dup {
			return nil, name + " não pode servir a dois papéis (" + other + " e " + role + ")"
		}
		seen[name] = role
	}
	return vars, ""
}

// connectionValues são os valores que o banco gerenciado entrega por papel. O
// papel `ca` não está aqui: o certificado é entregue na execução.
func connectionValues(engine string, vars map[string]string, host, pass string) map[string]string {
	values := map[string]string{}
	conn := domain.ConnectionOf(engine)
	if engine == domain.ServiceRedis {
		// O Redis não tem usuário nem banco nomeado: a senha vai sozinha, e o banco é o 0.
		if _, isURL := vars[domain.RoleURL]; isURL {
			values[domain.RoleURL] = conn.Scheme + "://:" + pass + "@" + host + ":" + conn.Port + "/0"
			return values
		}
		values[domain.RoleHost], values[domain.RolePort], values[domain.RolePassword] = host, conn.Port, pass
		return values
	}
	if _, isURL := vars[domain.RoleURL]; isURL {
		values[domain.RoleURL] = conn.Scheme + "://" + domain.DBUser + ":" + pass + "@" + host + ":" + conn.Port + "/" + domain.DBName
		return values
	}
	values[domain.RoleHost] = host
	values[domain.RolePort] = conn.Port
	values[domain.RoleUser] = domain.DBUser
	values[domain.RolePassword] = pass
	values[domain.RoleDatabase] = domain.DBName
	return values
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
	engine := req.Engine
	if engine == "" {
		engine = domain.ServicePostgres
		if analysis, err := s.store.LatestAnalysis(r.Context(), p.ID); err == nil {
			if n := primaryNeed(analysis.Result.ServiceNeeds); n != nil {
				engine = n.Engine
			}
		}
	}
	if !domain.IsManagedEngine(engine) {
		writeError(w, http.StatusBadRequest, "engine deve ser postgres, mysql ou redis")
		return
	}
	vars, problem := connectionVariables(req, engine)
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}

	var values map[string]string
	if req.Choice == domain.ServiceManaged {
		pass, err := secrets.Generate(databasePasswordLen)
		if err != nil {
			s.log.Error("gerar senha do banco", "erro", err)
			writeError(w, http.StatusInternalServerError, "erro interno")
			return
		}
		values = connectionValues(engine, vars, domain.ServiceContainerNameFor(p.ID.String(), engine), pass)
	}
	svc, err := s.store.ChooseDatabaseFor(r.Context(), engine, p.ID, user.ID, req.Choice, vars, values)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return
	case errors.Is(err, store.ErrVariableDefined):
		s.audit(r, &user, "project.database", p.ID.String(), "refused")
		var def *store.DefinedVariableError
		name := "a variável de conexão"
		if errors.As(err, &def) {
			name = def.Name
		}
		writeCode(w, http.StatusConflict, codeVariableDefined, name+" já tem um valor digitado por você; remova-o (ou escolha usar um banco que já tem) antes de criar o banco do Cobogó, que não sobrescreve o que você definiu")
		return
	case errors.Is(err, store.ErrManagedExists):
		writeCode(w, http.StatusConflict, codeManagedExists, "o projeto já tem um banco do Cobogó; apague-o (com os dados) antes de escolher outra coisa")
		return
	case err != nil:
		s.log.Error("registrar escolha do banco", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.auditDetail(r, &user, "project.database", p.ID.String(), "ok", map[string]any{"names": append([]string{req.Choice}, sortedValues(svc.Variables)...)})

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
	only := ""
	if engine == domain.ServiceRedis {
		only = engine
	}
	view, ok := s.serviceView(w, r, p, user, only)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, chooseDatabaseResponse{databaseView: view, Requeued: requeued})
}

type deleteDatabaseRequest struct {
	ConfirmSlug string `json:"confirm_slug"`
	// Engine escolhe o serviço a apagar; vazio vale o banco relacional (DEC-50).
	Engine string `json:"engine"`
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
	if req.Engine != "" && !domain.IsManagedEngine(req.Engine) {
		writeError(w, http.StatusBadRequest, "engine deve ser postgres, mysql ou redis")
		return
	}
	op, err := s.store.RequestDeleteServiceFor(r.Context(), req.Engine, p.ID, user.ID, req.ConfirmSlug)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
	case errors.Is(err, store.ErrConfirmationMismatch):
		s.audit(r, &user, "project.database.delete", p.ID.String(), "refused")
		writeCode(w, http.StatusBadRequest, codeConfirmationMismatch, "o nome de confirmação não é o do projeto")
	case errors.Is(err, store.ErrNoManagedService):
		writeCode(w, http.StatusConflict, codeNoManagedDB, "o projeto não tem um banco do Cobogó para apagar")
	case err != nil:
		s.log.Error("pedir exclusão do banco", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "project.database.delete", p.ID.String(), "ok")
		writeJSON(w, http.StatusAccepted, op)
	}
}

// sortedValues lista os nomes de variável de um mapa de papéis, em ordem estável.
func sortedValues(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
