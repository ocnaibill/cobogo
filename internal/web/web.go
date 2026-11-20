// Package web serve o painel React (web/), embutido no binário da API
// (DEC-34): mesmo domínio, sem CORS, com a sessão e a proteção de origem da
// API valendo como estão. O build (`npm run build` em web/) escreve em dist/.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var files embed.FS

// assetsCache vale só para os arquivos com hash no nome (o Vite os gera em
// assets/): mudam de nome a cada build, então podem ficar em cache para sempre.
const assetsCache = "public, max-age=31536000, immutable"

// Handler devolve o servidor do painel. Arquivos que existem no build são
// servidos como estão; qualquer outro caminho pedido por um navegador (Accept
// com text/html) recebe o index.html, para as rotas do lado do cliente
// funcionarem num recarregamento. O que não é navegador recebe 404, para um
// cliente da API nunca confundir HTML com resposta.
func Handler(notFound http.Handler) http.Handler {
	dist, err := fs.Sub(files, "dist")
	if err != nil {
		panic(err) // dist faz parte do binário; não existir é erro de build
	}
	return handlerFor(dist, notFound)
}

func handlerFor(dist fs.FS, notFound http.Handler) http.Handler {
	index, indexErr := fs.ReadFile(dist, "index.html")
	fileServer := http.FileServer(http.FS(dist))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			notFound.ServeHTTP(w, r)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name != "" && !strings.HasPrefix(path.Base(name), ".") {
			if st, err := fs.Stat(dist, name); err == nil && !st.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					w.Header().Set("Cache-Control", assetsCache)
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		if !strings.Contains(r.Header.Get("Accept"), "text/html") {
			notFound.ServeHTTP(w, r)
			return
		}
		if indexErr != nil {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("O painel não foi construído: rode `npm run build` em web/ e compile a API de novo.\n"))
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(index)
		}
	})
}

// Built diz se o painel foi embutido neste binário.
func Built() bool {
	_, err := fs.Stat(files, "dist/index.html")
	return err == nil
}
