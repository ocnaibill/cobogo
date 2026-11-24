package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
	"github.com/ocnaibill/cobogo/internal/validate"
)

// O projeto é o repositório (DEC-52): colar o link cria o projeto e pede a leitura do
// repositório; a proposta mostra as aplicações; confirmar cria e publica todas.

const (
	codeSlugTaken = "slug_taken"
	codeScanBusy  = "scan_busy"
)

func (s *Server) handleListStacks(w http.ResponseWriter, r *http.Request, user domain.User) {
	stacks, err := s.store.ListStacksFor(r.Context(), user.ID)
	if err != nil {
		s.log.Error("listar projetos", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stacks": stacks})
}

type createStackRequest struct {
	RepositoryURL string `json:"repository_url"`
	// Slug é o nome do projeto; vazio usa o nome do repositório.
	Slug string `json:"slug"`
}

func (s *Server) handleCreateStack(w http.ResponseWriter, r *http.Request, user domain.User) {
	var req createStackRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	hosts, err := s.settings.GitHosts(r.Context())
	if err != nil {
		s.log.Error("ler hosts de repositório", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	repositoryURL, err := validate.RepositoryURL(req.RepositoryURL, hosts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	slug := strings.TrimSpace(req.Slug)
	if slug == "" {
		slug = domain.StackSlugFromRepository(repositoryURL)
	}
	if !slugPattern.MatchString(slug) || validate.ReservedSlug(slug) {
		writeError(w, http.StatusBadRequest, "nome inválido: use letras minúsculas, números e hífen, e não um nome reservado")
		return
	}
	st, err := s.store.CreateStack(r.Context(), user.ID, slug, repositoryURL)
	switch {
	case errors.Is(err, store.ErrSlugTaken):
		writeCode(w, http.StatusConflict, codeSlugTaken, "já existe um projeto com o nome "+slug+"; escolha outro")
	case err != nil:
		s.log.Error("criar projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "stack.create", st.Slug, "ok")
		writeJSON(w, http.StatusCreated, st)
	}
}

// stackOf lê o projeto do caminho, pelo id ou pelo slug.
func (s *Server) stackOf(w http.ResponseWriter, r *http.Request, user domain.User) (domain.Stack, bool) {
	ref := r.PathValue("id")
	var st domain.Stack
	var err error
	if id, perr := uuid.Parse(ref); perr == nil {
		st, err = s.store.GetStackFor(r.Context(), id, user.ID)
	} else {
		st, err = s.store.GetStackBySlugFor(r.Context(), ref, user.ID)
	}
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return domain.Stack{}, false
	}
	if err != nil {
		s.log.Error("buscar projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return domain.Stack{}, false
	}
	return st, true
}

func (s *Server) handleGetStack(w http.ResponseWriter, r *http.Request, user domain.User) {
	if st, ok := s.stackOf(w, r, user); ok {
		writeJSON(w, http.StatusOK, st)
	}
}

func (s *Server) handleScanStack(w http.ResponseWriter, r *http.Request, user domain.User) {
	st, ok := s.stackOf(w, r, user)
	if !ok {
		return
	}
	st, err := s.store.RequestStackScan(r.Context(), st.ID, user.ID)
	switch {
	case errors.Is(err, store.ErrScanBusy):
		writeCode(w, http.StatusConflict, codeScanBusy, "o repositório já está sendo lido; espere terminar")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
	case err != nil:
		s.log.Error("pedir a leitura do repositório", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		writeJSON(w, http.StatusAccepted, st)
	}
}

type stackAppRequest struct {
	RootDirectory string `json:"root_directory"`
	Slug          string `json:"slug"`
	Role          string `json:"role"`
	// Ecosystem fixa o ecossistema quando duas aplicações dividem a pasta (DEC-53 F).
	Ecosystem string `json:"ecosystem"`
}

type createStackAppsRequest struct {
	Apps []stackAppRequest `json:"apps"`
	// Deploy publica as aplicações criadas, em ordem (o backend antes do frontend).
	Deploy         bool   `json:"deploy"`
	IdempotencyKey string `json:"idempotency_key"`
}

var appRoles = map[string]bool{domain.CompFrontend: true, domain.CompBackend: true, domain.CompFullstack: true, domain.CompApp: true, "": true}

func (s *Server) handleCreateStackApps(w http.ResponseWriter, r *http.Request, user domain.User) {
	st, ok := s.stackOf(w, r, user)
	if !ok {
		return
	}
	var req createStackAppsRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	if len(req.Apps) == 0 || len(req.Apps) > 8 {
		writeError(w, http.StatusBadRequest, "informe de 1 a 8 aplicações")
		return
	}
	if req.Deploy && (req.IdempotencyKey == "" || len(req.IdempotencyKey) > 90) {
		writeError(w, http.StatusBadRequest, "idempotency_key é obrigatória para publicar e deve ter até 90 caracteres (RN-07)")
		return
	}
	apps := make([]store.NewApp, 0, len(req.Apps))
	seenSlug, seenDir := map[string]bool{}, map[string]bool{}
	for _, a := range req.Apps {
		if !slugPattern.MatchString(a.Slug) || validate.ReservedSlug(a.Slug) {
			writeError(w, http.StatusBadRequest, "endereço inválido: "+a.Slug+" (use letras minúsculas, números e hífen, e não um nome reservado)")
			return
		}
		root, err := validate.RootDirectory(a.RootDirectory)
		if err != nil {
			writeError(w, http.StatusBadRequest, "root_directory inválido: "+err.Error())
			return
		}
		if !appRoles[a.Role] {
			writeError(w, http.StatusBadRequest, "papel inválido: "+a.Role)
			return
		}
		if a.Ecosystem != "" && a.Ecosystem != domain.EcosystemPython && a.Ecosystem != domain.EcosystemJavaScript {
			writeError(w, http.StatusBadRequest, "ecossistema inválido: "+a.Ecosystem)
			return
		}
		// Duas aplicações só dividem a pasta quando cada uma fixa um ecossistema diferente.
		key := store.AppKey(root, a.Ecosystem)
		if seenSlug[a.Slug] || seenDir[key] || (a.Ecosystem == "" && seenDir[store.AppKey(root, domain.EcosystemPython)]) || (a.Ecosystem == "" && seenDir[store.AppKey(root, domain.EcosystemJavaScript)]) {
			writeError(w, http.StatusBadRequest, "duas aplicações com o mesmo endereço ou a mesma pasta")
			return
		}
		seenSlug[a.Slug], seenDir[key] = true, true
		apps = append(apps, store.NewApp{RootDirectory: root, Slug: a.Slug, Role: a.Role, Ecosystem: a.Ecosystem})
	}
	created, err := s.store.CreateApps(r.Context(), st.ID, user.ID, apps)
	switch {
	case errors.Is(err, store.ErrSlugTaken):
		writeCode(w, http.StatusConflict, codeSlugTaken, "o endereço já está em uso por outra aplicação: "+strings.TrimPrefix(err.Error(), store.ErrSlugTaken.Error()+": ")+"; escolha outro")
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return
	case err != nil:
		s.log.Error("criar aplicações", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	for _, p := range created {
		s.audit(r, &user, "project.create", p.Slug, "ok")
	}
	prepared := s.prepareFor(r, user, st.ID)
	deployments := []domain.Deployment{}
	if req.Deploy {
		if deployments, ok = s.deployApps(w, r, user, created, req.IdempotencyKey); !ok {
			return
		}
	}
	st, ok = s.stackOf(w, r, user)
	if !ok {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"stack": st, "deployments": deployments, "prepared": nonNilReports(prepared)})
}

type deployStackRequest struct {
	IdempotencyKey string `json:"idempotency_key"`
}

// handleDeployStack publica todas as aplicações do projeto, o backend primeiro.
func (s *Server) handleDeployStack(w http.ResponseWriter, r *http.Request, user domain.User) {
	st, ok := s.stackOf(w, r, user)
	if !ok {
		return
	}
	var req deployStackRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	if req.IdempotencyKey == "" || len(req.IdempotencyKey) > 90 {
		writeError(w, http.StatusBadRequest, "idempotency_key é obrigatória e deve ter até 90 caracteres (RN-07)")
		return
	}
	if len(st.Apps) == 0 {
		writeCode(w, http.StatusConflict, "no_apps", "o projeto ainda não tem aplicações: confirme a proposta primeiro")
		return
	}
	apps := make([]domain.Project, 0, len(st.Apps))
	for _, a := range st.Apps {
		apps = append(apps, a.Project)
	}
	prepared := s.prepareFor(r, user, st.ID)
	deployments, ok := s.deployApps(w, r, user, apps, req.IdempotencyKey)
	if !ok {
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"deployments": deployments, "prepared": nonNilReports(prepared)})
}

// deployOrder: o que serve dados sobe antes de quem os consome.
var deployOrder = map[string]int{domain.CompBackend: 0, domain.CompFullstack: 1, domain.CompApp: 2, "": 3, domain.CompFrontend: 4}

func (s *Server) deployApps(w http.ResponseWriter, r *http.Request, user domain.User, apps []domain.Project, key string) ([]domain.Deployment, bool) {
	ordered := append([]domain.Project(nil), apps...)
	sort.SliceStable(ordered, func(i, j int) bool { return deployOrder[ordered[i].Role] < deployOrder[ordered[j].Role] })
	out := []domain.Deployment{}
	for _, p := range ordered {
		dep, _, err := s.store.CreateDeploymentWithJob(r.Context(), p.ID, key+":"+p.ID.String(), nil)
		if err != nil {
			s.log.Error("criar deployment", "erro", err)
			writeError(w, http.StatusConflict, "não foi possível criar o deploy de "+p.Slug+"; a idempotency_key já pode existir")
			return nil, false
		}
		s.audit(r, &user, "deployment.create", p.Slug+"/"+dep.ID.String(), "ok")
		out = append(out, dep)
	}
	return out, true
}

type deleteStackRequest struct {
	ConfirmSlug string `json:"confirm_slug"`
	DeleteData  bool   `json:"delete_data"`
}

// handleDeleteStack exclui o projeto: cada aplicação segue a exclusão da DEC-38 (com
// os serviços e, se pedido, os dados), e o projeto some quando não lhe resta nenhuma.
func (s *Server) handleDeleteStack(w http.ResponseWriter, r *http.Request, user domain.User) {
	st, ok := s.stackOf(w, r, user)
	if !ok {
		return
	}
	var req deleteStackRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	if req.ConfirmSlug != st.Slug {
		s.audit(r, &user, "stack.delete", st.Slug, "refused")
		writeCode(w, http.StatusBadRequest, codeConfirmationMismatch, "o nome de confirmação não é o do projeto")
		return
	}
	ops := []domain.Operation{}
	for _, a := range st.Apps {
		op, err := s.store.DeleteProject(r.Context(), a.ID, user.ID, a.Slug, req.DeleteData)
		switch {
		case errors.Is(err, store.ErrDeploymentInProgress):
			s.audit(r, &user, "stack.delete", st.Slug, "refused")
			writeCode(w, http.StatusConflict, codeDeploymentInProgress, "há um deploy em andamento em "+a.Slug+"; espere ele terminar para excluir o projeto")
			return
		case err != nil && !errors.Is(err, store.ErrNotFound):
			s.log.Error("excluir aplicação", "erro", err)
			writeError(w, http.StatusInternalServerError, "erro interno")
			return
		case err == nil:
			s.audit(r, &user, "project.delete", a.Slug, "ok")
			ops = append(ops, op)
		}
	}
	if err := s.store.MarkStackDeletedIfEmpty(r.Context(), st.ID, user.ID); err != nil {
		s.log.Error("excluir projeto", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.audit(r, &user, "stack.delete", st.Slug, "ok")
	writeJSON(w, http.StatusAccepted, map[string]any{"operations": ops})
}

func nonNilReports(r []prepareReport) []prepareReport {
	if r == nil {
		return []prepareReport{}
	}
	return r
}
