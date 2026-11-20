package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

var built = fstest.MapFS{
	"index.html":           {Data: []byte("<!doctype html><div id=root></div>")},
	"favicon.svg":          {Data: []byte("<svg/>")},
	"assets/index-abc.js":  {Data: []byte("console.log(1)")},
	"assets/index-abc.css": {Data: []byte("body{}")},
	".gitkeep":             {Data: nil},
}

var notFound = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte(`{"error":{"code":"not_found"}}`))
})

func get(h http.Handler, method, path, accept string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if accept != "" {
		r.Header.Set("Accept", accept)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

const browser = "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"

func TestServesFilesAndCachesOnlyHashedAssets(t *testing.T) {
	h := handlerFor(built, notFound)
	rec := get(h, "GET", "/assets/index-abc.js", "")
	if rec.Code != 200 || rec.Body.String() != "console.log(1)" || rec.Header().Get("Cache-Control") != assetsCache {
		t.Errorf("asset: %d %q cache=%q", rec.Code, rec.Body, rec.Header().Get("Cache-Control"))
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("asset content type = %q", ct)
	}
	rec = get(h, "GET", "/favicon.svg", "")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") == assetsCache {
		t.Errorf("favicon: %d, must not be cached forever (no hash in its name)", rec.Code)
	}
}

// Rotas do cliente (/projetos/x, /deploys/y) recarregadas no navegador
// recebem o index.html; quem não é navegador recebe 404 em JSON.
func TestClientRoutesGetTheIndexOnlyForBrowsers(t *testing.T) {
	h := handlerFor(built, notFound)
	for _, p := range []string{"/", "/projetos/123", "/deploys/abc", "/entrar?voltar=%2F", "/assets/", "/assets"} {
		rec := get(h, "GET", p, browser)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id=root`) || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			t.Errorf("%s from a browser: %d %q", p, rec.Code, rec.Body)
		}
	}
	for _, p := range []string{"/projetos/123", "/traefik/config", "/qualquer"} {
		if rec := get(h, "GET", p, "application/json"); rec.Code != 404 || !strings.Contains(rec.Body.String(), "not_found") {
			t.Errorf("%s from an API client: %d %q", p, rec.Code, rec.Body)
		}
	}
}

func TestNeverListsDirectoriesNorServesDotfiles(t *testing.T) {
	h := handlerFor(built, notFound)
	if rec := get(h, "GET", "/assets/", ""); rec.Code != 404 || strings.Contains(rec.Body.String(), "index-abc.js") {
		t.Errorf("directory listing: %d %q", rec.Code, rec.Body)
	}
	if rec := get(h, "GET", "/.gitkeep", ""); rec.Code != 404 {
		t.Errorf("dotfile: %d", rec.Code)
	}
	if rec := get(h, "GET", "/../../etc/passwd", browser); rec.Code != 200 || !strings.Contains(rec.Body.String(), "id=root") {
		t.Errorf("a path that tries to leave dist must fall back to the index: %d %q", rec.Code, rec.Body)
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	h := handlerFor(built, notFound)
	for _, m := range []string{"POST", "PUT", "DELETE"} {
		if rec := get(h, m, "/projetos/1", browser); rec.Code != 404 {
			t.Errorf("%s: %d, want the JSON 404", m, rec.Code)
		}
	}
	if rec := get(h, "HEAD", "/projetos/1", browser); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d with %d bytes", rec.Code, rec.Body.Len())
	}
}

func TestUnbuiltPanelSaysSo(t *testing.T) {
	h := handlerFor(fstest.MapFS{".gitkeep": {}}, notFound)
	rec := get(h, "GET", "/", browser)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "npm run build") {
		t.Errorf("unbuilt: %d %q", rec.Code, rec.Body)
	}
}
