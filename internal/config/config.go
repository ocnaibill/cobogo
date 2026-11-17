// Package config carrega a configuração de processo a partir de variáveis
// de ambiente. Segredos de runtime de aplicações publicadas (RF-10) não
// passam por aqui — isto é só a configuração do próprio Cobogó.
package config

import (
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/secrets"
)

type Config struct {
	DatabaseURL string
	APIAddr     string
	// SecretKey é a chave AES-256 que cifra os valores das variáveis dos
	// projetos (DEC-24). Fica fora do banco, no ambiente do processo.
	SecretKey []byte

	// PublicURL é a origem pública do painel (esquema e host), usada para
	// conferir o Origin das requisições de alteração (DEC-27 item 7).
	PublicURL string
	// TrustedProxies são as redes de que a API aceita cabeçalhos de proxy
	// (CF-Connecting-IP, X-Forwarded-For). Vazio: nenhum (DEC-27 item 11).
	TrustedProxies []netip.Prefix
	// ExtraGitHosts acrescenta hosts à lista de repositórios permitidos.
	ExtraGitHosts []string
	// CookieInsecure troca o cookie de sessão por um sem Secure nem prefixo
	// __Host-, só para desenvolvimento por HTTP.
	CookieInsecure bool

	// Limits são os tetos de recursos dos contêineres de aplicação e dos builds
	// (DEC-29); valem para o worker. Zero usa o padrão.
	Limits Limits
}

// Limits são os limites de recursos configuráveis por instalação.
type Limits struct {
	AppMemoryMB   int
	AppCPUs       float64
	AppPids       int
	BuildMemoryMB int
	BuildCPUs     float64
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		APIAddr:        envOr("API_ADDR", ":8080"),
		PublicURL:      strings.TrimRight(strings.TrimSpace(os.Getenv("COBOGO_PUBLIC_URL")), "/"),
		ExtraGitHosts:  splitList(os.Getenv("COBOGO_EXTRA_GIT_HOSTS")),
		CookieInsecure: os.Getenv("COBOGO_COOKIE_INSECURE") == "1",
	}
	if c.DatabaseURL == "" {
		return Config{}, fmt.Errorf("config: variável DATABASE_URL é obrigatória")
	}
	rawKey := os.Getenv("COBOGO_SECRET_KEY")
	if rawKey == "" {
		return Config{}, fmt.Errorf("config: variável COBOGO_SECRET_KEY é obrigatória (32 bytes em base64; gere com `openssl rand -base64 32`)")
	}
	key, err := secrets.ParseKey(rawKey)
	if err != nil {
		return Config{}, fmt.Errorf("config: COBOGO_SECRET_KEY inválida: %w", err)
	}
	c.SecretKey = key

	for _, item := range splitList(os.Getenv("COBOGO_TRUSTED_PROXIES")) {
		prefix, err := parsePrefix(item)
		if err != nil {
			return Config{}, fmt.Errorf("config: COBOGO_TRUSTED_PROXIES: %w", err)
		}
		c.TrustedProxies = append(c.TrustedProxies, prefix)
	}
	limits, err := loadLimits()
	if err != nil {
		return Config{}, err
	}
	c.Limits = limits
	return c, nil
}

// loadLimits lê os limites de recursos. Ausente vale zero (padrão da DEC-29);
// presente, tem de estar numa faixa que não derrube o que ele limita.
func loadLimits() (Limits, error) {
	var l Limits
	var err error
	if l.AppMemoryMB, err = intEnv("COBOGO_APP_MEMORY_MB", 64, 1<<20); err != nil {
		return Limits{}, err
	}
	if l.AppPids, err = intEnv("COBOGO_APP_PIDS", 16, 1<<20); err != nil {
		return Limits{}, err
	}
	if l.BuildMemoryMB, err = intEnv("COBOGO_BUILD_MEMORY_MB", 256, 1<<20); err != nil {
		return Limits{}, err
	}
	if l.AppCPUs, err = floatEnv("COBOGO_APP_CPUS", 0.1, 256); err != nil {
		return Limits{}, err
	}
	if l.BuildCPUs, err = floatEnv("COBOGO_BUILD_CPUS", 0.1, 256); err != nil {
		return Limits{}, err
	}
	return l, nil
}

func intEnv(name string, min, max int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("config: %s inválida: use um inteiro de %d a %d", name, min, max)
	}
	return n, nil
}

func floatEnv(name string, min, max float64) (float64, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(n) || n < min || n > max {
		return 0, fmt.Errorf("config: %s inválida: use um número de %g a %g", name, min, max)
	}
	return n, nil
}

// ValidateForAPI confere o que só a API exige: a URL pública do painel.
func (c Config) ValidateForAPI() error {
	if c.PublicURL == "" {
		return fmt.Errorf("config: variável COBOGO_PUBLIC_URL é obrigatória (por exemplo https://cobogo.exemplo.com, ou http://localhost:8080 em desenvolvimento)")
	}
	u, err := url.Parse(c.PublicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Path != "" || u.User != nil {
		return fmt.Errorf("config: COBOGO_PUBLIC_URL deve ser só esquema e host (https://host), recebi %q", c.PublicURL)
	}
	if u.Scheme == "http" && !c.CookieInsecure {
		return fmt.Errorf("config: COBOGO_PUBLIC_URL usa http, mas o cookie de sessão exige HTTPS; use https ou, só em desenvolvimento, COBOGO_COOKIE_INSECURE=1")
	}
	return nil
}

// parsePrefix aceita "10.0.0.0/8" ou um endereço único ("172.18.0.2").
func parsePrefix(item string) (netip.Prefix, error) {
	if strings.Contains(item, "/") {
		p, err := netip.ParsePrefix(item)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q não é um CIDR válido", item)
		}
		return p.Masked(), nil
	}
	addr, err := netip.ParseAddr(item)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q não é um endereço nem um CIDR válido", item)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
