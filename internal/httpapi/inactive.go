package httpapi

import (
	"html/template"
	"net"
	"net/http"
	"strings"
)

// Página de projeto inativo (DEC-40). Quando uma aplicação está parada, o
// Traefik leva o endereço dela até a API, e esta responde com uma página do
// Cobogó em vez do 404 do proxy. Só o nome do projeto, que já é o endereço,
// aparece nela.

var inactiveTemplate = template.Must(template.New("inactive").Parse(`<!doctype html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.Name}} não está ativo · Cobogó</title>
<style>
:root{color-scheme:light dark;--bg:#f8f9ff;--surface:#fff;--border:#e1e8f2;--text:#0b1c30;--muted:#3f4850;--brand:#006194}
@media (prefers-color-scheme:dark){:root{--bg:#0b1c30;--surface:#13283f;--border:#27415c;--text:#eaf1fb;--muted:#b4c3d6;--brand:#7cc4f2}}
*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:var(--bg);color:var(--text);font:16px/1.55 system-ui,-apple-system,"Segoe UI",Roboto,sans-serif}
main{max-width:30rem;background:var(--surface);border:1px solid var(--border);border-radius:14px;padding:32px}
.tag{display:inline-block;font:600 12px/1 ui-monospace,Menlo,Consolas,monospace;letter-spacing:.04em;text-transform:uppercase;color:var(--brand)}
h1{margin:10px 0 8px;font-size:1.5rem;line-height:1.25}
p{margin:0 0 8px;color:var(--muted)}
.name{font:600 .95rem ui-monospace,Menlo,Consolas,monospace;color:var(--text);word-break:break-all}
</style>
</head>
<body>
<main>
<span class="tag">Cobogó</span>
<h1>Este projeto não está ativo no momento</h1>
<p class="name">{{.Name}}</p>
<p>A aplicação foi parada por quem a administra. Volte mais tarde.</p>
</main>
</body>
</html>
`))

// inactiveGate responde a página de inativo para o endereço de um projeto com a
// aplicação parada; qualquer outro pedido segue para a API. O painel nunca é
// afetado: ele não tem o formato `<slug>.<domínio das aplicações>`.
func (s *Server) inactiveGate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.ToLower(host)
		// O host do painel e os acessos diretos (IP, localhost) nem consultam o banco.
		if host == "" || !strings.Contains(host, ".") || strings.EqualFold(r.Host, strings.TrimPrefix(strings.TrimPrefix(s.publicOrigin, "https://"), "http://")) {
			next.ServeHTTP(w, r)
			return
		}
		routing, err := s.settings.Apps(r.Context())
		if err != nil || !routing.Enabled() {
			next.ServeHTTP(w, r)
			return
		}
		slug, _, _ := strings.Cut(host, ".")
		if !slugPattern.MatchString(slug) || routing.Host(slug) != host {
			next.ServeHTTP(w, r)
			return
		}
		stopped, err := s.store.AppStoppedBySlug(r.Context(), slug)
		if err != nil {
			s.log.Error("ler se a aplicação está parada", "erro", err)
			next.ServeHTTP(w, r)
			return
		}
		if !stopped {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		h.Set("Retry-After", "60")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Robots-Tag", "noindex")
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		w.WriteHeader(http.StatusServiceUnavailable)
		if r.Method == http.MethodHead {
			return
		}
		_ = inactiveTemplate.Execute(w, struct{ Name string }{slug})
	})
}
