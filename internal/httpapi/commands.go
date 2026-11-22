package httpapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/auth"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// Comandos pontuais (DEC-45). A API só registra o pedido, confere a senha digitada
// de novo e lê o desfecho; quem executa, num contêiner descartável, é o worker.

const (
	codeReauthRequired = "reauth_required"
	codeCommandActive  = "command_active"
	codeNoRelease      = "no_release"
	codeUnavailable    = "commands_unavailable"
	commandListLimit   = 30
)

// scriptName: só nomes que podem entrar num `npm run <nome>` sem aspas.
var scriptName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_.-]{0,63}$`)

// Scripts que não são tarefas pontuais: o que o Cobogó já roda (start, build), o
// que é de desenvolvimento e o que é verificação de código.
var excludedScripts = regexp.MustCompile(`^(start|build|dev|develop|serve|watch|test|lint|format|prettier|eslint|typecheck|commit|prepare|prepublish|prepublishOnly|install|postinstall|preinstall|pre.+|post.+)([:_.-].*)?$`)

type scriptOption struct {
	// Name é o nome em package.json; Command, o que o botão roda; Runs, o que o
	// script executa (para a pessoa ver antes de clicar).
	Name    string `json:"name"`
	Command string `json:"command"`
	Runs    string `json:"runs"`
}

// scriptsFrom lista os scripts de package.json que servem de comando pontual.
func scriptsFrom(res domain.AnalysisResult) []scriptOption {
	manager := res.Fields[domain.FieldPackageManager].Value
	runner := "npm run "
	if manager == domain.ManagerBun || res.Fields[domain.FieldProfile].Value == domain.ProfileBun {
		runner = "bun run "
	}
	out := []scriptOption{}
	seen := map[string]bool{}
	for _, ev := range res.Evidences {
		if ev.Kind != "npm.script" || seen[ev.Name] || !scriptName.MatchString(ev.Name) || excludedScripts.MatchString(ev.Name) {
			continue
		}
		seen[ev.Name] = true
		runs := ev.Value
		if len(runs) > 200 {
			runs = runs[:200] + "…"
		}
		out = append(out, scriptOption{Name: ev.Name, Command: runner + ev.Name, Runs: runs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

// sessionOf devolve a sessão atual e o usuário.
func (s *Server) sessionOf(r *http.Request) (domain.Session, domain.User, bool) {
	c, err := r.Cookie(s.cookieName())
	if err != nil || c.Value == "" || len(c.Value) > 128 {
		return domain.Session{}, domain.User{}, false
	}
	sess, user, err := s.store.GetSession(r.Context(), auth.HashToken(c.Value), SessionIdle)
	if err != nil {
		return domain.Session{}, domain.User{}, false
	}
	return sess, user, true
}

func reauthActive(sess domain.Session) bool {
	return sess.ReauthUntil != nil && sess.ReauthUntil.After(time.Now())
}

type reauthRequest struct {
	Password string `json:"password"`
}

type reauthResponse struct {
	ReauthUntil time.Time `json:"reauth_until"`
}

// handleReauth confere a senha de novo, com o mesmo limite de tentativas do login,
// e abre a janela de reautenticação da sessão (DEC-45 item 4).
func (s *Server) handleReauth(w http.ResponseWriter, r *http.Request, user domain.User) {
	var req reauthRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	sess, _, ok := s.sessionOf(r)
	if !ok {
		writeCode(w, http.StatusUnauthorized, "unauthenticated", "sessão ausente ou expirada; entre novamente")
		return
	}
	ip := s.clientIP(r)
	if ok, wait := s.limiter.Allow(ip, user.Login); !ok {
		s.tooManyAttempts(w, wait)
		return
	}
	hctx, cancel := context.WithTimeout(r.Context(), hashWait)
	defer cancel()
	stored, err := s.store.GetPasswordHash(r.Context(), user.ID)
	if err != nil {
		s.log.Error("buscar hash", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	valid := false
	if len(req.Password) <= maxPasswordBytes {
		if valid, err = s.hasher.Verify(hctx, stored, req.Password); err != nil {
			writeCode(w, http.StatusServiceUnavailable, "unavailable", "servidor ocupado; tente novamente")
			return
		}
	}
	if !valid {
		s.limiter.Fail(ip, user.Login)
		s.audit(r, &user, "auth.reauth", "", "failed")
		writeCode(w, http.StatusUnauthorized, "invalid_credentials", "senha incorreta")
		return
	}
	until, err := s.store.MarkReauth(r.Context(), sess.ID, domain.CommandReauthWindow)
	if err != nil {
		s.log.Error("registrar reautenticação", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.limiter.Succeed(ip, user.Login)
	s.audit(r, &user, "auth.reauth", "", "ok")
	writeJSON(w, http.StatusOK, reauthResponse{ReauthUntil: until})
}

type commandsView struct {
	Commands []domain.Command `json:"commands"`
	// Scripts são os botões: os scripts do package.json que servem de comando pontual.
	Scripts []scriptOption `json:"scripts"`
	// Available diz se o projeto aceita comandos agora; Reason explica quando não.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// ReauthUntil é até quando a senha digitada de novo vale nesta sessão (nulo se não vale).
	ReauthUntil *time.Time `json:"reauth_until"`
	// DefaultTimeout e MaxTimeout, em segundos.
	DefaultTimeout int `json:"default_timeout"`
	MaxTimeout     int `json:"max_timeout"`
}

// commandAvailability diz se o projeto aceita comando e, se não, por quê.
func (s *Server) commandAvailability(ctx context.Context, p domain.Project, res *domain.AnalysisResult) (bool, string, error) {
	if _, err := s.store.ActiveRelease(ctx, p.ID); err != nil {
		if errors.Is(err, store.ErrNoActiveRelease) {
			return false, "O projeto não tem release no ar. Faça um deploy primeiro: o comando roda na imagem da release ativa.", nil
		}
		return false, "", err
	}
	if res != nil && res.Fields[domain.FieldProfile].Value == domain.ProfileStatic {
		return false, "Um site estático não tem ambiente para comandos: a imagem é só o servidor web.", nil
	}
	return true, "", nil
}

func (s *Server) handleListCommands(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	view := commandsView{Scripts: []scriptOption{}, DefaultTimeout: domain.CommandDefaultTimeout, MaxTimeout: domain.CommandMaxTimeout}
	var result *domain.AnalysisResult
	switch a, err := s.store.LatestAnalysis(r.Context(), p.ID); {
	case err == nil:
		result = &a.Result
		view.Scripts = scriptsFrom(a.Result)
	case !errors.Is(err, store.ErrNotFound):
		s.log.Error("buscar análise", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	var err error
	if view.Available, view.Reason, err = s.commandAvailability(r.Context(), p, result); err != nil {
		s.log.Error("conferir se aceita comando", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if view.Commands, err = s.store.ListCommandsFor(r.Context(), p.ID, user.ID, commandListLimit); err != nil {
		s.log.Error("listar comandos", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if sess, _, ok := s.sessionOf(r); ok && reauthActive(sess) {
		view.ReauthUntil = sess.ReauthUntil
	}
	writeJSON(w, http.StatusOK, view)
}

type createCommandRequest struct {
	// Script é o nome de um script de package.json (um dos botões); Command é o
	// texto livre. Exatamente um dos dois.
	Script  string `json:"script"`
	Command string `json:"command"`
	// PauseApp para a aplicação durante o comando (DEC-45 item 7).
	PauseApp       bool `json:"pause_app"`
	TimeoutSeconds int  `json:"timeout_seconds"`
}

func (s *Server) handleCreateCommand(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return
	}
	var req createCommandRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	// A senha digitada de novo vem antes de qualquer outra conferência: uma sessão
	// sem a janela não aprende nada sobre o projeto por este caminho.
	sess, _, ok := s.sessionOf(r)
	if !ok {
		writeCode(w, http.StatusUnauthorized, "unauthenticated", "sessão ausente ou expirada; entre novamente")
		return
	}
	if !reauthActive(sess) {
		s.audit(r, &user, "project.command", p.ID.String(), "refused")
		writeCode(w, http.StatusForbidden, codeReauthRequired, "digite a sua senha de novo para rodar comandos")
		return
	}

	command, source := strings.TrimSpace(req.Command), domain.CommandCustom
	if (req.Script == "") == (command == "") {
		writeError(w, http.StatusBadRequest, "informe um script ou um comando, e só um dos dois")
		return
	}
	var result *domain.AnalysisResult
	if a, err := s.store.LatestAnalysis(r.Context(), p.ID); err == nil {
		result = &a.Result
	} else if !errors.Is(err, store.ErrNotFound) {
		s.log.Error("buscar análise", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if req.Script != "" {
		source = domain.CommandScript
		command = ""
		if result != nil {
			for _, sc := range scriptsFrom(*result) {
				if sc.Name == req.Script {
					command = sc.Command
				}
			}
		}
		if command == "" {
			writeError(w, http.StatusBadRequest, "esse script não está entre os que o projeto oferece")
			return
		}
	}
	if len(command) > domain.CommandMaxLen || !utf8.ValidString(command) || strings.ContainsRune(command, 0) {
		writeError(w, http.StatusBadRequest, "o comando precisa ser um texto de até 2000 caracteres, sem bytes nulos")
		return
	}
	timeout := req.TimeoutSeconds
	if timeout == 0 {
		timeout = domain.CommandDefaultTimeout
	}
	if timeout < domain.CommandMinTimeout || timeout > domain.CommandMaxTimeout {
		writeError(w, http.StatusBadRequest, "o prazo vai de 5 a 600 segundos")
		return
	}
	if okAvail, reason, err := s.commandAvailability(r.Context(), p, result); err != nil {
		s.log.Error("conferir se aceita comando", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	} else if !okAvail {
		writeCode(w, http.StatusConflict, codeUnavailable, reason)
		return
	}

	cmd, err := s.store.CreateCommand(r.Context(), p.ID, user.ID, command, source, req.PauseApp, timeout)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "projeto não encontrado")
	case errors.Is(err, store.ErrNoActiveRelease):
		writeCode(w, http.StatusConflict, codeNoRelease, "o projeto não tem release no ar")
	case errors.Is(err, store.ErrCommandActive):
		writeCode(w, http.StatusConflict, codeCommandActive, "já há um comando em andamento neste projeto; espere ele terminar ou cancele")
	case err != nil:
		s.log.Error("criar comando", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		// O texto do comando pode ter um valor colado: a API não sabe os valores
		// das variáveis (só o worker), então a auditoria guarda a referência. O texto,
		// já sem os valores, fica no registro do comando, que o worker conclui.
		s.auditDetail(r, &user, "project.command", p.ID.String(), "ok", map[string]any{
			"names": []string{cmd.ID.String(), source},
		})
		writeJSON(w, http.StatusAccepted, cmd)
	}
}

func (s *Server) commandRef(w http.ResponseWriter, r *http.Request, user domain.User) (domain.Project, uuid.UUID, bool) {
	p, ok := s.projectOf(w, r, user)
	if !ok {
		return domain.Project{}, uuid.Nil, false
	}
	id, err := uuid.Parse(r.PathValue("cid"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "id de comando inválido")
		return domain.Project{}, uuid.Nil, false
	}
	return p, id, true
}

func (s *Server) handleGetCommand(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, id, ok := s.commandRef(w, r, user)
	if !ok {
		return
	}
	cmd, err := s.store.GetCommandFor(r.Context(), p.ID, id, user.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "comando não encontrado")
	case err != nil:
		s.log.Error("buscar comando", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		writeJSON(w, http.StatusOK, cmd)
	}
}

func (s *Server) handleCancelCommand(w http.ResponseWriter, r *http.Request, user domain.User) {
	p, id, ok := s.commandRef(w, r, user)
	if !ok {
		return
	}
	cmd, err := s.store.RequestCancelCommand(r.Context(), p.ID, id, user.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "comando não encontrado")
	case err != nil:
		s.log.Error("cancelar comando", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
	default:
		s.audit(r, &user, "project.command.cancel", p.ID.String(), "ok")
		writeJSON(w, http.StatusAccepted, cmd)
	}
}
