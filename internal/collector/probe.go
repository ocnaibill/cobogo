package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/store"
)

// Sonda de rota (DEC-43 item 10): uma requisição `GET /` pelo próprio Traefik,
// com o `Host` do projeto, medindo o caminho que o visitante percorre. O
// coletor não entra nas redes dos projetos e não precisa de escrita no Docker.
// O resultado é uma verificação sintética, não disponibilidade nem latência de
// usuários.

const (
	probeEvery   = 15 * time.Second
	probeTimeout = 5 * time.Second
	panelRouter  = "cobogo_panel"
)

// ProbeConfig diz de onde vêm as rotas e por onde passa a sonda.
type ProbeConfig struct {
	// RoutesURL é o endpoint interno de rotas (a tabela do Traefik) e Token o
	// seu token de leitura.
	RoutesURL string
	Token     string
	// TraefikURL é o endereço do Traefik na rede (por exemplo http://traefik:80).
	TraefikURL string
}

// Prober faz as sondas.
type Prober struct {
	cfg    ProbeConfig
	client *http.Client
}

// NewProber monta a sonda. Não segue redirecionamentos: um 3xx já é uma resposta.
func NewProber(cfg ProbeConfig) *Prober {
	return &Prober{cfg: cfg, client: &http.Client{
		Timeout:       probeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

type route struct {
	slug    string
	host    string
	stopped bool
}

var hostRule = regexp.MustCompile("^Host\\(`([^`]+)`\\)$")

// Routes lê a tabela de rotas e devolve as das aplicações (a do painel fica de
// fora). Uma aplicação parada aponta para o mesmo destino do painel (DEC-40).
func (p *Prober) Routes(ctx context.Context) ([]route, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.cfg.RoutesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ler as rotas: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ler as rotas: status %d", resp.StatusCode)
	}
	var cfg struct {
		HTTP *struct {
			Routers  map[string]struct{ Rule, Service string } `json:"routers"`
			Services map[string]struct {
				LoadBalancer struct {
					Servers []struct{ URL string } `json:"servers"`
				} `json:"loadBalancer"`
			} `json:"services"`
		} `json:"http"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("ler as rotas: %w", err)
	}
	if cfg.HTTP == nil {
		return nil, nil
	}
	serviceURL := func(name string) string {
		if s, ok := cfg.HTTP.Services[name]; ok && len(s.LoadBalancer.Servers) > 0 {
			return s.LoadBalancer.Servers[0].URL
		}
		return ""
	}
	panelURL := serviceURL(panelRouter)
	var out []route
	for name, r := range cfg.HTTP.Routers {
		if name == panelRouter || !strings.HasPrefix(name, "cobogo-") {
			continue
		}
		m := hostRule.FindStringSubmatch(r.Rule)
		if m == nil {
			continue
		}
		out = append(out, route{slug: strings.TrimPrefix(name, "cobogo-"), host: m[1], stopped: panelURL != "" && serviceURL(r.Service) == panelURL})
	}
	return out, nil
}

// probeOne faz a sonda de uma rota.
func (p *Prober) probeOne(ctx context.Context, r route, projectID uuid.UUID) store.Probe {
	out := store.Probe{ProjectID: projectID, Host: r.host, DurationMS: -1}
	if r.stopped {
		out.Outcome = store.ProbeStopped
		return out
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.cfg.TraefikURL, "/")+"/", nil)
	if err != nil {
		out.Outcome = store.ProbeFailed
		return out
	}
	req.Host = r.host
	req.Header.Set("User-Agent", "cobogo-collector/1")
	start := time.Now()
	resp, err := p.client.Do(req)
	out.DurationMS = int(time.Since(start).Milliseconds())
	if err != nil {
		out.Outcome = store.ProbeFailed
		return out
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	out.Status = resp.StatusCode
	switch {
	case resp.StatusCode < 400:
		out.Outcome = store.ProbeResponded
	case resp.StatusCode < 500:
		out.Outcome = store.ProbeClientError
	default:
		out.Outcome = store.ProbeFailed
	}
	return out
}

// ProbeRound sonda cada rota de aplicação e grava o resultado. Um erro ao ler as
// rotas só pula a rodada.
func (c *Collector) ProbeRound(ctx context.Context) {
	routes, err := c.probe.Routes(ctx)
	if err != nil {
		c.log.Warn("sonda: rotas", "erro", err)
		return
	}
	now := time.Now().UTC()
	var probes []store.Probe
	for _, r := range routes {
		id, ok := c.slugs[r.slug]
		if !ok {
			continue // ainda sem contêiner amostrado deste projeto
		}
		probes = append(probes, c.probe.probeOne(ctx, r, id))
	}
	if err := c.store.InsertProbes(ctx, now, probes); err != nil {
		c.log.Warn("sonda: gravar", "erro", err)
	}
}
