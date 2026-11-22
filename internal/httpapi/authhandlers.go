package httpapi

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/auth"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

var loginPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)

const (
	cookieSecure   = "__Host-cobogo_session"
	cookieInsecure = "cobogo_session"
	// hashWait é quanto uma requisição espera por um espaço de verificação de
	// senha antes de desistir (DEC-27 item 9).
	hashWait = 10 * time.Second
	// maxPasswordBytes descarta, antes do argon2id, senhas absurdas: a política
	// aceita até 128 caracteres.
	maxPasswordBytes = 4 * auth.MaxPasswordLen
)

func analyzerCheck(overrides map[string]string) error { return analyzer.CheckOverrides(overrides) }

func withRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func (s *Server) cookieName() string {
	if s.cookieInsecure {
		return cookieInsecure
	}
	return cookieSecure
}

// setSessionCookie emite o cookie de sessão: HttpOnly, SameSite=Strict e, em
// produção, Secure com prefixo __Host- (que obriga Path=/ e proíbe Domain,
// prendendo o cookie ao hostname de gestão, RNF-08).
func (s *Server) setSessionCookie(w http.ResponseWriter, token string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: token, Path: "/",
		MaxAge: int(maxAge.Seconds()), HttpOnly: true, Secure: !s.cookieInsecure, SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: s.cookieName(), Value: "", Path: "/",
		MaxAge: -1, HttpOnly: true, Secure: !s.cookieInsecure, SameSite: http.SameSiteStrictMode,
	})
}

// authed protege uma rota: sem sessão válida, 401 sem tocar em nada.
func (s *Server) authed(h func(w http.ResponseWriter, r *http.Request, user domain.User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, _, ok := s.currentSession(r)
		if !ok {
			s.clearSessionCookie(w)
			writeCode(w, http.StatusUnauthorized, "unauthenticated", "sessão ausente ou expirada; entre novamente")
			return
		}
		h(w, r, user)
	}
}

// currentSession devolve o usuário da sessão do cookie e o hash do token.
func (s *Server) currentSession(r *http.Request) (domain.User, []byte, bool) {
	c, err := r.Cookie(s.cookieName())
	if err != nil || c.Value == "" || len(c.Value) > 128 {
		return domain.User{}, nil, false
	}
	hash := auth.HashToken(c.Value)
	_, user, err := s.store.GetSession(r.Context(), hash, SessionIdle)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("buscar sessão", "erro", err)
		}
		return domain.User{}, nil, false
	}
	return user, hash, true
}

// startSession cria a sessão e emite o cookie. O login sempre cria uma
// sessão nova: nunca reaproveita o token que veio na requisição.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user domain.User) error {
	token, hash, err := auth.NewSessionToken()
	if err != nil {
		return err
	}
	if _, err := s.store.CreateSession(r.Context(), user.ID, hash, SessionAbsolute, s.clientIP(r), r.UserAgent()); err != nil {
		return err
	}
	s.setSessionCookie(w, token, SessionAbsolute)
	return nil
}

func (s *Server) handleSetupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountUsers(r.Context())
	if err != nil {
		s.log.Error("contar usuários", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needs_setup": n == 0})
}

type setupRequest struct {
	Token    string `json:"token"`
	Login    string `json:"login"`
	Password string `json:"password"`
}

// handleSetup cria o administrador uma única vez, com o token de uso único
// impresso no log (DEC-27 item 3), e já o deixa autenticado.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	ip := s.clientIP(r)
	const limiterKey = "\x00setup"
	if ok, wait := s.limiter.Allow(ip, limiterKey); !ok {
		s.tooManyAttempts(w, wait)
		return
	}

	if n, err := s.store.CountUsers(r.Context()); err != nil {
		s.log.Error("contar usuários", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	} else if n > 0 || s.setup == nil {
		writeCode(w, http.StatusConflict, "setup_closed", "o administrador já foi configurado")
		return
	}
	if !s.setup.Check(req.Token) {
		s.limiter.Fail(ip, limiterKey)
		s.audit(r, nil, "setup", "", "failed")
		writeCode(w, http.StatusForbidden, "invalid_setup_token", "token de configuração inválido")
		return
	}

	login := strings.ToLower(strings.TrimSpace(req.Login))
	if !loginPattern.MatchString(login) {
		writeError(w, http.StatusBadRequest, "login inválido: use de 3 a 64 caracteres entre letras minúsculas, números, ponto, hífen e sublinhado")
		return
	}
	if err := auth.CheckPassword(login, req.Password); err != nil {
		writeCode(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}

	hctx, cancel := context.WithTimeout(r.Context(), hashWait)
	defer cancel()
	hash, err := s.hasher.Hash(hctx, req.Password)
	if err != nil {
		s.log.Error("calcular hash da senha", "erro", err)
		writeCode(w, http.StatusServiceUnavailable, "unavailable", "servidor ocupado; tente novamente")
		return
	}
	user, err := s.store.CreateAdmin(r.Context(), login, hash)
	if errors.Is(err, store.ErrAdminExists) {
		writeCode(w, http.StatusConflict, "setup_closed", "o administrador já foi configurado")
		return
	}
	if err != nil {
		s.log.Error("criar administrador", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.setup.Consume()
	if err := s.startSession(w, r, user); err != nil {
		s.log.Error("criar sessão", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.audit(r, &user, "setup", user.Login, "ok")
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

type loginRequest struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

func (s *Server) tooManyAttempts(w http.ResponseWriter, wait time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
	writeCode(w, http.StatusTooManyRequests, "rate_limited", "muitas tentativas; aguarde alguns minutos antes de tentar de novo")
}

// handleLogin autentica o administrador. A resposta é idêntica e leva o mesmo
// tempo para login inexistente e senha errada (DEC-27 item 9).
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !readJSON(w, r, &req, 0) {
		return
	}
	ip := s.clientIP(r)
	login := strings.ToLower(strings.TrimSpace(req.Login))
	if ok, wait := s.limiter.Allow(ip, login); !ok {
		s.audit(r, nil, "login", "", "limited")
		s.tooManyAttempts(w, wait)
		return
	}

	hctx, cancel := context.WithTimeout(r.Context(), hashWait)
	defer cancel()

	user, hash, err := s.store.GetUserByLogin(r.Context(), login)
	found := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("buscar usuário", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}

	valid := false
	switch {
	case len(req.Password) > maxPasswordBytes:
		// Senha absurda: nem chega ao argon2id; conta como falha.
	case found:
		valid, err = s.hasher.Verify(hctx, hash, req.Password)
	default:
		err = s.hasher.VerifyDummy(hctx, req.Password)
	}
	if err != nil {
		s.log.Error("verificar senha", "erro", err)
		writeCode(w, http.StatusServiceUnavailable, "unavailable", "servidor ocupado; tente novamente")
		return
	}

	if !valid {
		s.limiter.Fail(ip, login)
		// O login digitado só vai para a auditoria se ele existe: quem digita a
		// senha no campo do login não pode deixá-la gravada, e um atacante não
		// enche a trilha com textos inventados.
		target := "(desconhecido)"
		if found {
			target = user.Login
		}
		s.audit(r, nil, "login", target, "failed")
		writeCode(w, http.StatusUnauthorized, "invalid_credentials", "login ou senha incorretos")
		return
	}

	s.limiter.Succeed(ip, login)
	if err := s.startSession(w, r, user); err != nil {
		s.log.Error("criar sessão", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	s.audit(r, &user, "login", user.Login, "ok")
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, user domain.User) {
	if c, err := r.Cookie(s.cookieName()); err == nil {
		if err := s.store.DeleteSession(r.Context(), auth.HashToken(c.Value)); err != nil {
			s.log.Error("encerrar sessão", "erro", err)
		}
	}
	s.clearSessionCookie(w)
	s.audit(r, &user, "logout", user.Login, "ok")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user domain.User) {
	out := map[string]any{"user": user}
	// Até quando a senha digitada de novo vale nesta sessão (DEC-45), se vale.
	if sess, _, ok := s.sessionOf(r); ok && reauthActive(sess) {
		out["reauth_until"] = sess.ReauthUntil
	}
	writeJSON(w, http.StatusOK, out)
}

type changePasswordRequest struct {
	Current string `json:"current_password"`
	New     string `json:"new_password"`
}

// handleChangePassword confere a senha atual (com o mesmo limite de tentativas
// do login, para uma sessão roubada não servir de oráculo), troca a senha e
// encerra as outras sessões.
func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request, user domain.User) {
	var req changePasswordRequest
	if !readJSON(w, r, &req, 0) {
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
	if len(req.Current) <= maxPasswordBytes {
		if valid, err = s.hasher.Verify(hctx, stored, req.Current); err != nil {
			writeCode(w, http.StatusServiceUnavailable, "unavailable", "servidor ocupado; tente novamente")
			return
		}
	}
	if !valid {
		s.limiter.Fail(ip, user.Login)
		s.audit(r, &user, "password.change", "", "failed")
		writeCode(w, http.StatusUnauthorized, "invalid_credentials", "senha atual incorreta")
		return
	}
	if err := auth.CheckPassword(user.Login, req.New); err != nil {
		writeCode(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	newHash, err := s.hasher.Hash(hctx, req.New)
	if err != nil {
		writeCode(w, http.StatusServiceUnavailable, "unavailable", "servidor ocupado; tente novamente")
		return
	}
	if err := s.store.SetPasswordHash(r.Context(), user.ID, newHash); err != nil {
		s.log.Error("trocar senha", "erro", err)
		writeError(w, http.StatusInternalServerError, "erro interno")
		return
	}
	if c, err := r.Cookie(s.cookieName()); err == nil {
		if err := s.store.DeleteOtherSessions(r.Context(), user.ID, auth.HashToken(c.Value)); err != nil {
			s.log.Error("encerrar outras sessões", "erro", err)
		}
	}
	s.limiter.Succeed(ip, user.Login)
	s.audit(r, &user, "password.change", "", "ok")
	w.WriteHeader(http.StatusNoContent)
}
