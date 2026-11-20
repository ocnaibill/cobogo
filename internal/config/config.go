// Package config carrega a configuração de processo a partir de variáveis
// de ambiente. Segredos de runtime de aplicações publicadas (RF-10) não
// passam por aqui — isto é só a configuração do próprio Cobogó.
package config

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/secrets"
	"github.com/ocnaibill/cobogo/internal/settings"
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
	// ExtraGitHosts acrescenta hosts à lista de repositórios permitidos; vale
	// como padrão inicial, o banco o sobrepõe (DEC-30).
	ExtraGitHosts []string
	// RouterAddr e RouterToken configuram o endpoint de rotas do Traefik, em um
	// listener interno próprio (DEC-31 item 8). Sem token o listener não sobe.
	RouterAddr  string
	RouterToken string
	// AppsDomain e AppsScheme são o padrão inicial do roteamento das
	// aplicações (DEC-31); o banco os sobrepõe.
	AppsDomain, AppsScheme string
	// CookieInsecure troca o cookie de sessão por um sem Secure nem prefixo
	// __Host-, só para desenvolvimento por HTTP.
	CookieInsecure bool

	// Limits são os tetos de recursos dos contêineres de aplicação e dos builds
	// (DEC-29); valem para o worker. Zero usa o padrão.
	Limits domain.Limits
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		APIAddr:        envOr("API_ADDR", ":8080"),
		RouterAddr:     envOr("COBOGO_ROUTER_ADDR", ":8081"),
		RouterToken:    strings.TrimSpace(os.Getenv("COBOGO_ROUTER_TOKEN")),
		PublicURL:      strings.TrimRight(strings.TrimSpace(os.Getenv("COBOGO_PUBLIC_URL")), "/"),
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
	if c.RouterToken != "" && len(c.RouterToken) < 24 {
		return Config{}, fmt.Errorf("config: COBOGO_ROUTER_TOKEN precisa ter ao menos 24 caracteres (gere com `openssl rand -hex 32`)")
	}
	limits, hosts, err := loadEnvDefaults()
	if err != nil {
		return Config{}, err
	}
	c.Limits, c.ExtraGitHosts = limits, hosts
	for key, dst := range map[string]*string{settings.AppsDomain: &c.AppsDomain, settings.AppsScheme: &c.AppsScheme} {
		def, _ := settings.Lookup(key)
		v, err := settings.ParseEnv(key, os.Getenv(def.EnvVar))
		if err != nil {
			return Config{}, fmt.Errorf("config: %w", err)
		}
		*dst = v
	}
	return c, nil
}

// loadEnvDefaults lê a política operacional do ambiente (limites e hosts
// extras). Ausente vale zero ou vazio; presente, tem de estar na faixa da
// chave (as mesmas definições do painel, DEC-30). Estes valores são só o
// padrão inicial: o banco os sobrepõe.
func loadEnvDefaults() (domain.Limits, []string, error) {
	get := func(key string) (string, error) {
		d, _ := settings.Lookup(key)
		return settings.ParseEnv(key, os.Getenv(d.EnvVar))
	}
	var l domain.Limits
	var firstErr error
	num := func(key string) float64 {
		raw, err := get(key)
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("config: %w", err)
		}
		v, _ := strconv.ParseFloat(raw, 64)
		return v
	}
	l.AppMemoryMB = int(num(settings.AppMemoryMB))
	l.AppCPUs = num(settings.AppCPUs)
	l.AppPids = int(num(settings.AppPids))
	l.BuildMemoryMB = int(num(settings.BuildMemoryMB))
	l.BuildCPUs = num(settings.BuildCPUs)
	hostsRaw, err := get(settings.ExtraGitHosts)
	if err != nil && firstErr == nil {
		firstErr = fmt.Errorf("config: %w", err)
	}
	return l, splitList(hostsRaw), firstErr
}

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
