package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/store"
)

// MinRouterTokenLen é o tamanho mínimo do token do endpoint de rotas.
const MinRouterTokenLen = 24

// traefikEntrypoint é o entrypoint HTTP do Traefik; o HTTPS é da borda.
const traefikEntrypoint = "web"

var (
	routeHostPattern   = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]{0,251}[a-z0-9])?$`)
	routeTargetPattern = regexp.MustCompile(`^http://([a-z0-9][a-z0-9_.-]{0,127}):([0-9]{1,5})$`)
)

type traefikRouter struct {
	Rule        string   `json:"rule"`
	EntryPoints []string `json:"entryPoints"`
	Service     string   `json:"service"`
}

type traefikServer struct {
	URL string `json:"url"`
}

type traefikService struct {
	LoadBalancer struct {
		Servers []traefikServer `json:"servers"`
	} `json:"loadBalancer"`
}

// traefikHTTP é a seção http da configuração dinâmica.
type traefikHTTP struct {
	Routers  map[string]traefikRouter  `json:"routers"`
	Services map[string]traefikService `json:"services"`
}

// traefikConfig omite a seção http quando não há rota: o Traefik recusa mapas
// vazios ("routers cannot be a standalone element"), e `{}` é o que ele aceita
// como "nenhuma configuração".
type traefikConfig struct {
	HTTP *traefikHTTP `json:"http,omitempty"`
}

// NewRoutingHandler monta o endpoint de rotas do Traefik (DEC-31 itens 7 a 9).
// Ele é servido por um listener interno próprio, nunca pela API pública, e
// exige o token no cabeçalho Authorization. Não usa sessão nem CSRF.
func NewRoutingHandler(st *store.Store, token string, log *slog.Logger) (http.Handler, error) {
	if st == nil || log == nil {
		return nil, errors.New("httpapi: store e log são obrigatórios")
	}
	if len(token) < MinRouterTokenLen {
		return nil, fmt.Errorf("httpapi: o token das rotas precisa ter ao menos %d caracteres", MinRouterTokenLen)
	}
	// Compara os hashes: tempo constante e sem revelar o tamanho do token.
	want := sha256.Sum256([]byte(token))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /traefik/config", func(w http.ResponseWriter, r *http.Request) {
		scheme, got, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		sum := sha256.Sum256([]byte(got))
		if !ok || !strings.EqualFold(scheme, "Bearer") || subtle.ConstantTimeCompare(sum[:], want[:]) != 1 {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, http.StatusUnauthorized, "token inválido")
			return
		}
		routes, err := st.ListRoutes(r.Context())
		if err != nil {
			log.Error("listar rotas", "erro", err)
			writeError(w, http.StatusInternalServerError, "erro interno")
			return
		}
		cfg := buildTraefikConfig(routes, log)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, cfg)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "rota não encontrada")
	})
	return mux, nil
}

// buildTraefikConfig traduz a tabela de rotas na configuração dinâmica. Cada
// valor é conferido contra um padrão fechado: o host entra numa regra textual
// do Traefik, então um valor estranho (que deveria ter sido barrado antes) é
// omitido e registrado, nunca emitido.
func buildTraefikConfig(routes []domain.Route, log *slog.Logger) traefikConfig {
	routers := map[string]traefikRouter{}
	services := map[string]traefikService{}
	for _, rt := range routes {
		if !slugPattern.MatchString(rt.Slug) || !routeHostPattern.MatchString(rt.Host) || !validRouteTarget(rt.Target) {
			log.Warn("rota fora do padrão omitida", "slug", rt.Slug)
			continue
		}
		name := "cobogo-" + rt.Slug
		routers[name] = traefikRouter{
			Rule: "Host(`" + rt.Host + "`)", EntryPoints: []string{traefikEntrypoint}, Service: name,
		}
		var svc traefikService
		svc.LoadBalancer.Servers = []traefikServer{{URL: rt.Target}}
		services[name] = svc
	}
	if len(routers) == 0 {
		return traefikConfig{}
	}
	return traefikConfig{HTTP: &traefikHTTP{Routers: routers, Services: services}}
}

func validRouteTarget(target string) bool {
	m := routeTargetPattern.FindStringSubmatch(target)
	if m == nil {
		return false
	}
	port, err := strconv.Atoi(m[2])
	return err == nil && port >= 1 && port <= 65535
}
