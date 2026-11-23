package httpapi

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/envfile"
	"github.com/ocnaibill/cobogo/internal/settings"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Variáveis e segredos guiados (DEC-39): importar .env, sugestões, variáveis
// obrigatórias e histórico. Nenhuma resposta daqui devolve um valor colado.

const (
	maxImportText  = 256 << 10
	maxImportBody  = maxImportText + 16<<10
	maxImportItems = 200
	maxRequired    = 200
	historyLimit   = 100
)

// Ações de uma variável na prévia da importação.
const (
	importCreate  = "create"
	importReplace = "replace"
	importBlocked = "blocked"
)

// Códigos de aviso da prévia.
const (
	warnPlatform    = "platform"
	warnEmpty       = "empty"
	warnTooLarge    = "too_large"
	warnNullChar    = "null_char"
	warnDuplicate   = "duplicate"
	warnPlaceholder = "placeholder"
	warnCredential  = "credential"
	warnNotExpanded = "not_expanded"
	warnReplaces    = "replaces_existing"
	warnLinked      = "linked"
)

type importRequest struct {
	Text  string   `json:"text"`
	Mode  string   `json:"mode"`
	Names []string `json:"names"`
}

type importWarning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type importItem struct {
	Name            string          `json:"name"`
	Line            int             `json:"line"`
	Action          string          `json:"action"`
	Class           domain.VarClass `json:"class"`
	DefaultSelected bool            `json:"default_selected"`
	Warnings        []importWarning `json:"warnings"`
}

type importDiagnostic struct {
	Line    int    `json:"line"`
	Name    string `json:"name,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type importPreview struct {
	Variables   []importItem       `json:"variables"`
	Diagnostics []importDiagnostic `json:"diagnostics"`
}

type importApplied struct {
	Name             string `json:"name"`
	Version          int    `json:"version"`
	RedeployRequired bool   `json:"redeploy_required"`
}

type importSkipped struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type importResult struct {
	Applied []importApplied `json:"applied"`
	Skipped []importSkipped `json:"skipped"`
}

// previewImport classifica o que o texto traz. Os valores ficam só em memória:
// o que sai são nomes, linhas, classes e códigos.
func previewImport(text string, existing map[string]bool, linked map[string]bool) (importPreview, map[string]envfile.Entry) {
	entries, diags := envfile.Parse(text)
	out := importPreview{Variables: []importItem{}, Diagnostics: []importDiagnostic{}}
	for _, d := range diags {
		out.Diagnostics = append(out.Diagnostics, importDiagnostic{Line: d.Line, Name: d.Name, Code: d.Code, Message: d.Message})
	}
	byName := make(map[string]envfile.Entry, len(entries))
	for _, e := range entries {
		byName[e.Name] = e
		class := analyzer.ClassifyVariable(e.Name)
		item := importItem{Name: e.Name, Line: e.Line, Class: class, Action: importCreate, Warnings: []importWarning{}}
		warn := func(code, msg string) { item.Warnings = append(item.Warnings, importWarning{code, msg}) }

		risky := false // desmarcada por padrão
		switch {
		case linked[e.Name]:
			item.Action = importBlocked
			warn(warnLinked, e.Name+" é a conexão do PostgreSQL do Cobogó deste projeto e não pode ser importada")
		case class == domain.ClassPlatform:
			item.Action = importBlocked
			warn(warnPlatform, e.Name+" é fornecida pela plataforma e não pode ser definida")
		case e.Value == "":
			item.Action = importBlocked
			warn(warnEmpty, "sem valor: o Cobogó não guarda variável vazia")
		case len(e.Value) > maxEnvValueBytes:
			item.Action = importBlocked
			warn(warnTooLarge, "o valor excede o limite de 32 KiB")
		case strings.ContainsRune(e.Value, 0):
			item.Action = importBlocked
			warn(warnNullChar, "o valor contém o caractere nulo")
		}
		if item.Action != importBlocked {
			if existing[e.Name] {
				item.Action = importReplace
				warn(warnReplaces, "já tem valor; importar cria uma versão nova e pede novo deploy")
				risky = true
			}
			if analyzer.LooksLikePlaceholder(e.Value) {
				warn(warnPlaceholder, "o valor parece um texto de preenchimento")
				risky = true
			}
			if kind := analyzer.CredentialKind(e.Name, e.Value); kind != "" {
				warn(warnCredential, "o valor tem o formato de "+analyzer.CredentialLabel(kind)+"; confirme que é o que você quer usar")
			}
			if e.NotExpanded {
				warn(warnNotExpanded, "contém $ ou crase: o valor fica literal, sem expansão")
			}
		}
		if len(e.Duplicates) > 0 {
			warn(warnDuplicate, "aparece mais de uma vez; vale a última atribuição")
		}
		item.DefaultSelected = item.Action != importBlocked && !risky
		out.Variables = append(out.Variables, item)
	}
	return out, byName
}

// handleImportEnv importa variáveis de um .env colado, em duas etapas: a prévia
// (que não grava nada nem devolve valores) e o aplicar (que grava só os nomes
// escolhidos).
func (s *Server) handleImportEnv(w http.ResponseWriter, r *http.Request, user domain.User) {
	projectID, _, ok := s.envTarget(w, r, user)
	if !ok {
		return
	}
	var req importRequest
	if !readJSON(w, r, &req, maxImportBody) {
		return
	}
	if len(req.Text) > maxImportText {
		writeError(w, http.StatusRequestEntityTooLarge, "o texto excede o limite de 256 KiB")
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		writeError(w, http.StatusBadRequest, "text é obrigatório")
		return
	}
	if req.Mode != "preview" && req.Mode != "apply" {
		writeError(w, http.StatusBadRequest, "mode deve ser preview ou apply")
		return
	}

	vars, err := s.store.ListEnvVariables(r.Context(), projectID)
	if err != nil {
		s.log.Error("listar variáveis", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	existing := make(map[string]bool, len(vars))
	linked := map[string]bool{}
	for _, v := range vars {
		existing[v.Name] = true
		if v.Source == domain.EnvSourceLinked {
			linked[v.Name] = true
		}
	}
	preview, entries := previewImport(req.Text, existing, linked)
	if len(preview.Variables) > maxImportItems {
		writeError(w, http.StatusBadRequest, "o texto tem mais de 200 variáveis; importe em partes")
		return
	}
	if req.Mode == "preview" {
		writeJSON(w, http.StatusOK, preview)
		return
	}

	// Aplicar: só o que a pessoa escolheu e que a prévia não bloqueou.
	if len(req.Names) == 0 {
		writeError(w, http.StatusBadRequest, "names é obrigatório para aplicar: liste as variáveis escolhidas")
		return
	}
	if len(req.Names) > maxImportItems {
		writeError(w, http.StatusBadRequest, "names tem variáveis demais")
		return
	}
	blocked := map[string]bool{}
	for _, it := range preview.Variables {
		if it.Action == importBlocked {
			blocked[it.Name] = true
		}
	}
	names := slices.Clone(req.Names)
	slices.Sort(names)
	names = slices.Compact(names)

	res := importResult{Applied: []importApplied{}, Skipped: []importSkipped{}}
	for _, name := range names {
		entry, present := entries[name]
		switch {
		case !present:
			res.Skipped = append(res.Skipped, importSkipped{Name: name, Reason: "not_in_text"})
			continue
		case blocked[name]:
			res.Skipped = append(res.Skipped, importSkipped{Name: name, Reason: "blocked"})
			continue
		}
		v, err := s.store.PutEnvVariable(r.Context(), projectID, name, entry.Value, domain.EnvSourceImported)
		if err != nil {
			s.log.Error("gravar variável importada", "variavel", name, "erro", err)
			writeError(w, http.StatusInternalServerError, "erro interno depois de gravar "+strconv.Itoa(len(res.Applied))+" variável(is); confira a lista antes de tentar de novo")
			return
		}
		s.auditDetail(r, &user, "env.set", projectID.String()+"/"+name, "ok", map[string]any{"version": v.Version, "source": string(domain.EnvSourceImported)})
		res.Applied = append(res.Applied, importApplied{Name: name, Version: v.Version, RedeployRequired: v.Version > 1})
	}
	applied := make([]string, 0, len(res.Applied))
	for _, a := range res.Applied {
		applied = append(applied, a.Name)
	}
	s.auditDetail(r, &user, "env.import", projectID.String(), "ok", map[string]any{"names": applied})
	writeJSON(w, http.StatusOK, res)
}

// suggestable é a lista fechada de nomes para os quais o Cobogó oferece o
// endereço público do projeto (DEC-39 item 2).
func suggestableNames(slug string) map[string]bool {
	own := strings.ToUpper(strings.ReplaceAll(slug, "-", "_")) + "_URL"
	return map[string]bool{
		own: true, "APP_URL": true, "SITE_URL": true, "BASE_URL": true,
		"PUBLIC_URL": true, "NEXT_PUBLIC_SITE_URL": true,
	}
}

type suggestion struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

// handleEnvSuggestions oferece valores que o Cobogó conhece, sem aplicá-los. Só
// para um nome que a análise encontrou e o projeto ainda não definiu.
func (s *Server) handleEnvSuggestions(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	out := map[string]any{"suggestions": []suggestion{}, "domain_configured": false}
	routing, err := s.settings.Apps(r.Context())
	if err != nil {
		s.log.Error("ler roteamento", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if !routing.Enabled() {
		writeJSON(w, http.StatusOK, out)
		return
	}
	out["domain_configured"] = true

	analysis, err := s.store.LatestAnalysis(r.Context(), p.ID)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, out)
		return
	}
	if err != nil {
		s.log.Error("buscar análise", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	vars, err := s.store.ListEnvVariables(r.Context(), p.ID)
	if err != nil {
		s.log.Error("listar variáveis", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	defined := make(map[string]bool, len(vars))
	for _, v := range vars {
		defined[v.Name] = true
	}
	allowed := suggestableNames(p.Slug)
	value := strings.TrimRight(routing.URL(p.Slug), "/")
	list := []suggestion{}
	for _, cv := range analysis.Result.ConfigVariables {
		name := strings.ToUpper(cv.Name)
		if !allowed[name] || defined[cv.Name] || cv.Class == domain.ClassPlatform {
			continue
		}
		list = append(list, suggestion{Name: cv.Name, Value: value, Reason: "o código usa " + cv.Name + "; este é o endereço público do projeto"})
	}
	list = append(list, s.pairSuggestions(r.Context(), p, user, analysis.Result, routing, defined)...)
	out["suggestions"] = list
	writeJSON(w, http.StatusOK, out)
}

// Nomes do catálogo fechado de sugestões entre projetos (DEC-49 item 3).
var (
	ownHostNames   = map[string]bool{"ALLOWED_HOSTS": true, "DJANGO_ALLOWED_HOSTS": true}
	ownOriginNames = map[string]bool{"CSRF_TRUSTED_ORIGINS": true, "DJANGO_CSRF_TRUSTED_ORIGINS": true}
	// Variáveis que apontam para o frontend: valem se o par é um frontend.
	frontendURLNames = map[string]bool{"FRONTEND_URL": true, "FRONTEND_ORIGIN": true, "CLIENT_URL": true, "WEB_URL": true, "CORS_ALLOWED_ORIGINS": true, "CORS_ORIGINS": true}
	// Variáveis que apontam para a API: valem se o par é um backend.
	backendURLNames = map[string]bool{"VITE_API_URL": true, "REACT_APP_API_URL": true, "NEXT_PUBLIC_API_URL": true, "VUE_APP_API_URL": true, "API_URL": true, "BACKEND_URL": true, "PUBLIC_API_URL": true}
)

// componentRole diz o papel da aplicação de uma pasta no mapa de uma análise ("" se não consta).
func componentRole(res domain.AnalysisResult, dir string) string {
	dir = strings.Trim(path.Clean("/"+dir), "/")
	if dir == "" {
		dir = "."
	}
	for _, c := range res.Components {
		switch c.Role {
		case domain.CompFrontend, domain.CompBackend, domain.CompFullstack, domain.CompApp:
		default:
			continue
		}
		if c.Dir == dir && (dir != "." || !c.Sibling) {
			return c.Role
		}
	}
	return ""
}

// pairSuggestions oferece os valores que dependem do endereço do projeto ou do par dele
// (outro projeto do mesmo repositório). Só para nomes que a análise encontrou e que ainda
// não têm valor; nada é aplicado sozinho.
func (s *Server) pairSuggestions(ctx context.Context, p domain.Project, user domain.User, res domain.AnalysisResult, routing settings.Routing, defined map[string]bool) []suggestion {
	var out []suggestion
	own := strings.TrimRight(routing.URL(p.Slug), "/")
	// ALLOWED_HOSTS compara só o domínio, sem a porta (o Django a separa do Host).
	host := routing.Host(p.Slug)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	hint := map[string]string{}
	for _, e := range res.Evidences {
		if e.Kind == "env.localhost_default" {
			if u, err := url.Parse(e.Value); err == nil && u.Path != "" && u.Path != "/" {
				hint[e.Name] = strings.TrimRight(u.Path, "/")
			}
		}
	}
	peers, err := s.store.ListPeerProjects(ctx, user.ID, p.RepositoryURL, p.ID)
	if err != nil {
		s.log.Error("listar projetos pares", "erro", err)
		peers = nil
	}
	type peerInfo struct {
		slug, role string
	}
	var found []peerInfo
	rootOf := func(d string) string { return strings.Trim(path.Clean("/"+d), "/") }
	for _, peer := range peers {
		role := ""
		// O mapa desta análise é relativo à pasta deste projeto: um par dentro dela
		// (frontend/ de um backend na raiz) aparece nele como irmã. Um par na raiz ou
		// acima só se conhece pela análise dele.
		if rel, ok := strings.CutPrefix(rootOf(peer.RootDirectory), rootOf(p.RootDirectory)); ok && rel != "" && (rootOf(p.RootDirectory) == "" || strings.HasPrefix(rel, "/")) {
			role = componentRole(res, strings.TrimPrefix(rel, "/"))
		}
		if role == "" {
			if a, err := s.store.LatestAnalysis(ctx, peer.ID); err == nil {
				role = componentRole(a.Result, ".")
			}
		}
		found = append(found, peerInfo{peer.Slug, role})
	}
	for _, cv := range res.ConfigVariables {
		name := strings.ToUpper(cv.Name)
		if defined[cv.Name] || cv.Class == domain.ClassPlatform {
			continue
		}
		switch {
		case ownHostNames[name]:
			out = append(out, suggestion{Name: cv.Name, Value: host, Reason: "o código lê " + cv.Name + "; este é o endereço do projeto (sem esquema). Confira o formato: lista separada por vírgula aceita este valor"})
		case ownOriginNames[name]:
			out = append(out, suggestion{Name: cv.Name, Value: own, Reason: "o código lê " + cv.Name + "; esta é a URL pública do projeto. Confira o formato (lista separada por vírgula)"})
		case frontendURLNames[name], backendURLNames[name]:
			want := domain.CompBackend
			if frontendURLNames[name] {
				want = domain.CompFrontend
			}
			for _, pe := range found {
				if pe.role != want && pe.role != domain.CompFullstack {
					continue
				}
				value := strings.TrimRight(routing.URL(pe.slug), "/")
				reason := "o código lê " + cv.Name + "; esta é a URL pública do projeto " + pe.slug + ", que usa o mesmo repositório"
				if sub := hint[cv.Name]; sub != "" && backendURLNames[name] {
					value += sub
					reason += " (com o caminho " + sub + ", que o código usa como padrão)"
				}
				out = append(out, suggestion{Name: cv.Name, Value: value, Reason: reason})
				break
			}
		}
	}
	return out
}

// requiredView é a lista de obrigatórias e o que a análise indica como provável.
type requiredView struct {
	Names  []string `json:"names"`
	Likely []string `json:"likely"`
}

func (s *Server) requiredView(r *http.Request, projectID uuid.UUID) (requiredView, error) {
	names, err := s.store.RequiredEnv(r.Context(), projectID)
	if err != nil {
		return requiredView{}, err
	}
	view := requiredView{Names: names, Likely: []string{}}
	analysis, err := s.store.LatestAnalysis(r.Context(), projectID)
	if errors.Is(err, store.ErrNotFound) {
		return view, nil
	}
	if err != nil {
		return requiredView{}, err
	}
	for _, cv := range analysis.Result.ConfigVariables {
		if cv.Hint != nil && cv.Hint.Code == domain.HintLikelyRequired && cv.Class != domain.ClassPlatform {
			view.Likely = append(view.Likely, cv.Name)
		}
	}
	slices.Sort(view.Likely)
	return view, nil
}

func (s *Server) handleGetRequiredEnv(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	view, err := s.requiredView(r, p.ID)
	if err != nil {
		s.log.Error("ler variáveis obrigatórias", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

type putRequiredRequest struct {
	Names []string `json:"names"`
}

// handlePutRequiredEnv substitui a lista de variáveis que bloqueiam o deploy
// quando faltam. Quem decide é o usuário (DEC-39 item 4).
func (s *Server) handlePutRequiredEnv(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	var req putRequiredRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	if req.Names == nil {
		writeError(w, http.StatusBadRequest, "names é obrigatório (use [] para não exigir nenhuma)")
		return
	}
	if len(req.Names) > maxRequired {
		writeError(w, http.StatusBadRequest, "names tem variáveis demais")
		return
	}
	for _, n := range req.Names {
		switch {
		case !envNamePattern.MatchString(n):
			writeError(w, http.StatusBadRequest, "nome de variável inválido na lista")
			return
		case analyzer.ClassifyVariable(n) == domain.ClassPlatform:
			writeError(w, http.StatusBadRequest, n+" é fornecida pela plataforma e não pode ser exigida")
			return
		}
	}
	names, err := s.store.SetRequiredEnv(r.Context(), p.ID, user.ID, req.Names)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "projeto não encontrado")
		return
	}
	if err != nil {
		s.log.Error("gravar variáveis obrigatórias", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.auditDetail(r, &user, "env.required", p.ID.String(), "ok", map[string]any{"names": names})
	view, err := s.requiredView(r, p.ID)
	if err != nil {
		s.log.Error("ler variáveis obrigatórias", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, view)
}

// handleEnvHistory devolve o que mudou nas variáveis do projeto, lido da
// auditoria: nome, ação, versão, origem, quem e quando. Nunca valores.
func (s *Server) handleEnvHistory(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	changes, err := s.store.ListEnvChanges(r.Context(), p.ID, historyLimit)
	if err != nil {
		s.log.Error("listar histórico de variáveis", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": changes})
}
