package httpapi

import (
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/google/uuid"
)

type ctxKey int

const requestIDKey ctxKey = iota

// statusRecorder guarda o status da resposta para o log de acesso.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// wrap monta a cadeia de proteções de toda requisição, da mais externa para a
// mais interna (DEC-27 itens 7 e 14): recuperação de pânico, ID da
// requisição, cabeçalhos de segurança, log de acesso e proteção de alteração.
func (s *Server) wrap(next http.Handler) http.Handler {
	return s.recoverPanics(s.requestID(s.securityHeaders(s.accessLog(s.sameOrigin(next)))))
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// Só o log vê o pânico; a resposta não vaza detalhe.
				s.log.Error("pânico em manipulador", "metodo", r.Method, "caminho", r.URL.Path, "pânico", rec, "pilha", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "erro interno")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// requestID dá a cada requisição um identificador novo. Um X-Request-Id
// enviado pelo cliente é ignorado: ele não deve poder forjar a correlação
// nos logs.
func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := uuid.NewString()
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(withRequestID(r.Context(), id)))
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// accessLog registra método, caminho (sem consulta), status e duração. Nunca
// registra corpo, cookies nem a consulta.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		ip := s.clientIP(r)
		s.log.Info("requisição", "metodo", r.Method, "caminho", r.URL.Path, "status", rec.status,
			"duracao_ms", time.Since(start).Milliseconds(), "ip", ip, "id", requestIDFrom(r.Context()))
	})
}

var unsafeMethods = map[string]bool{
	http.MethodPost: true, http.MethodPut: true, http.MethodPatch: true, http.MethodDelete: true,
}

// sameOrigin protege as requisições de alteração (DEC-27 item 7), sem token
// CSRF: o Origin precisa ser o do painel; sem Origin, o navegador precisa
// declarar Sec-Fetch-Site: same-origin; e corpos precisam ser JSON. Junto de
// SameSite=Strict isso cobre uma API de mesma origem, sem CORS.
func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !unsafeMethods[r.Method] {
			next.ServeHTTP(w, r)
			return
		}
		if !s.originAllowed(r) {
			writeCode(w, http.StatusForbidden, "csrf", "requisição de origem não permitida")
			return
		}
		if r.ContentLength != 0 && !isJSON(r.Header.Get("Content-Type")) {
			writeError(w, http.StatusUnsupportedMediaType, "use Content-Type: application/json")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) originAllowed(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Scheme == "" || u.Host == "" || u.Path != "" || u.User != nil {
			return false
		}
		return strings.EqualFold(u.Scheme+"://"+u.Host, s.publicOrigin)
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}
