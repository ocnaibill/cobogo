// Package settings define a política operacional que o administrador edita
// pelo painel (DEC-30) e resolve o valor efetivo de cada campo: projeto,
// banco, ambiente e padrão de fábrica, nessa ordem. Fundação e segurança
// (banco, chave, origem pública, proxies) ficam fora: só o ambiente as define.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/validate"
)

// Chaves editáveis.
const (
	AppMemoryMB   = "app_memory_mb"
	AppCPUs       = "app_cpus"
	AppPids       = "app_pids"
	BuildMemoryMB = "build_memory_mb"
	BuildCPUs     = "build_cpus"
	ExtraGitHosts = "extra_git_hosts"
	AppsDomain    = "apps_domain"
	AppsScheme    = "apps_scheme"
	// Política do ciclo de vida (DEC-38).
	RetireGraceSeconds = "retire_grace_seconds"
	ReleaseImagesKept  = "release_images_kept"
)

// Kind é o tipo do valor de uma chave.
type Kind string

const (
	KindInt   Kind = "integer"
	KindFloat Kind = "number"
	KindHosts Kind = "hosts"
	// KindDomain é um nome de host só, ou vazio para desligar.
	KindDomain Kind = "domain"
	// KindChoice é um valor de uma lista fechada.
	KindChoice Kind = "choice"
)

// Def descreve uma chave: tipo, faixa aceita e a variável de ambiente que a
// define como padrão inicial.
type Def struct {
	Key      string
	Kind     Kind
	Min, Max float64
	EnvVar   string
	// Choices são os valores aceitos de uma KindChoice.
	Choices []string
	// Default é o padrão de fábrica dos tipos que não são limites.
	Default string
	// AllowEmpty diz que um valor vazio gravado é uma escolha ("nenhum"), e
	// não a ausência de valor: ele sobrepõe o ambiente.
	AllowEmpty bool
	// Policy marca um número que não é limite de recursos: tem padrão próprio e
	// um projeto não o sobrepõe (DEC-38).
	Policy bool
}

// Defs são as chaves, na ordem em que o painel as mostra.
var Defs = []Def{
	{Key: AppMemoryMB, Kind: KindInt, Min: 64, Max: 262144, EnvVar: "COBOGO_APP_MEMORY_MB"},
	{Key: AppCPUs, Kind: KindFloat, Min: 0.1, Max: 256, EnvVar: "COBOGO_APP_CPUS"},
	{Key: AppPids, Kind: KindInt, Min: 16, Max: 65536, EnvVar: "COBOGO_APP_PIDS"},
	{Key: BuildMemoryMB, Kind: KindInt, Min: 256, Max: 262144, EnvVar: "COBOGO_BUILD_MEMORY_MB"},
	{Key: BuildCPUs, Kind: KindFloat, Min: 0.1, Max: 256, EnvVar: "COBOGO_BUILD_CPUS"},
	{Key: ExtraGitHosts, Kind: KindHosts, EnvVar: "COBOGO_EXTRA_GIT_HOSTS", AllowEmpty: true},
	{Key: AppsDomain, Kind: KindDomain, EnvVar: "COBOGO_APPS_DOMAIN", AllowEmpty: true},
	{Key: AppsScheme, Kind: KindChoice, EnvVar: "COBOGO_APPS_SCHEME", Choices: []string{"https", "http"}, Default: "https"},
	{Key: RetireGraceSeconds, Kind: KindInt, Min: 5, Max: 3600, Default: "30", Policy: true},
	{Key: ReleaseImagesKept, Kind: KindInt, Min: 1, Max: 20, Default: "2", Policy: true},
}

// Lookup acha a definição de uma chave.
func Lookup(key string) (Def, bool) {
	for _, d := range Defs {
		if d.Key == key {
			return d, true
		}
	}
	return Def{}, false
}

// IsLimit diz se a chave é um limite de recursos (os únicos que um projeto
// pode sobrepor).
func (d Def) IsLimit() bool { return (d.Kind == KindInt || d.Kind == KindFloat) && !d.Policy }

const maxHosts = 20

var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ErrInvalid é um valor fora do que a chave aceita.
var ErrInvalid = errors.New("valor inválido")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// Normalize valida o texto de um valor e o devolve em forma canônica: inteiros
// e números sem zeros à toa, hosts em minúsculas, sem repetição, separados por
// vírgula.
func (d Def) Normalize(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	switch d.Kind {
	case KindInt:
		n, err := strconv.Atoi(raw)
		if err != nil || float64(n) < d.Min || float64(n) > d.Max {
			return "", invalid("use um inteiro de %g a %g", d.Min, d.Max)
		}
		return strconv.Itoa(n), nil
	case KindFloat:
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil || n != n || n < d.Min || n > d.Max {
			return "", invalid("use um número de %g a %g", d.Min, d.Max)
		}
		return strconv.FormatFloat(n, 'f', -1, 64), nil
	case KindHosts:
		var hosts []string
		seen := map[string]bool{}
		for _, h := range strings.Split(raw, ",") {
			h = strings.ToLower(strings.TrimSpace(h))
			if h == "" {
				continue
			}
			if len(h) > 253 || !hostPattern.MatchString(h) {
				return "", invalid("%q não é um nome de host (sem esquema, porta nem caminho)", h)
			}
			if _, err := netip.ParseAddr(h); err == nil {
				return "", invalid("%q é um endereço IP; use um nome de host", h)
			}
			if !seen[h] {
				seen[h] = true
				hosts = append(hosts, h)
			}
		}
		if len(hosts) > maxHosts {
			return "", invalid("no máximo %d hosts", maxHosts)
		}
		return strings.Join(hosts, ","), nil
	case KindDomain:
		v := strings.ToLower(strings.TrimSpace(raw))
		host, port, hasPort := strings.Cut(v, ":")
		if hasPort {
			n, err := strconv.Atoi(port)
			if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
				return "", invalid("a porta de %q deve ser um número de 1 a 65535", v)
			}
		}
		if host == "" && !hasPort {
			return "", nil
		}
		if host != "localhost" && (len(host) > 253 || !hostPattern.MatchString(host)) {
			return "", invalid("%q não é um nome de domínio (sem esquema nem caminho; porta opcional)", v)
		}
		if _, err := netip.ParseAddr(host); err == nil {
			return "", invalid("%q é um endereço IP; use um nome de domínio", v)
		}
		return v, nil
	case KindChoice:
		v := strings.ToLower(strings.TrimSpace(raw))
		if !slices.Contains(d.Choices, v) {
			return "", invalid("use um de: %s", strings.Join(d.Choices, ", "))
		}
		return v, nil
	}
	return "", invalid("chave desconhecida")
}

// FromJSON converte o valor de um corpo JSON em texto e o valida. Números para
// limites; lista de textos para hosts. Textos no lugar de números são
// recusados de propósito: o contrato é um só.
func (d Def) FromJSON(raw json.RawMessage) (string, error) {
	switch d.Kind {
	case KindInt, KindFloat:
		var n json.Number
		dec := json.NewDecoder(strings.NewReader(string(raw)))
		dec.UseNumber()
		if err := dec.Decode(&n); err != nil || strings.HasPrefix(strings.TrimSpace(string(raw)), `"`) {
			return "", invalid("envie um número")
		}
		return d.Normalize(n.String())
	case KindHosts:
		var list []string
		if err := json.Unmarshal(raw, &list); err != nil {
			return "", invalid("envie uma lista de nomes de host")
		}
		return d.Normalize(strings.Join(list, ","))
	case KindDomain, KindChoice:
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return "", invalid("envie um texto")
		}
		return d.Normalize(text)
	}
	return "", invalid("chave desconhecida")
}

// Env são os valores de ambiente que servem de padrão inicial (zero = ausente).
type Env struct {
	Limits     domain.Limits
	GitHosts   []string
	AppsDomain string
	AppsScheme string
}

func (e Env) raw(key string) string {
	switch key {
	case AppMemoryMB:
		return nonZeroInt(e.Limits.AppMemoryMB)
	case AppCPUs:
		return nonZeroFloat(e.Limits.AppCPUs)
	case AppPids:
		return nonZeroInt(e.Limits.AppPids)
	case BuildMemoryMB:
		return nonZeroInt(e.Limits.BuildMemoryMB)
	case BuildCPUs:
		return nonZeroFloat(e.Limits.BuildCPUs)
	case ExtraGitHosts:
		return strings.Join(e.GitHosts, ",")
	case AppsDomain:
		return e.AppsDomain
	case AppsScheme:
		return e.AppsScheme
	}
	return ""
}

func nonZeroInt(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func nonZeroFloat(f float64) string {
	if f == 0 {
		return ""
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// ParseEnv lê o valor de uma variável de ambiente para a chave, ou "" se vazia.
func ParseEnv(key, value string) (string, error) {
	d, ok := Lookup(key)
	if !ok {
		return "", invalid("chave desconhecida")
	}
	if strings.TrimSpace(value) == "" {
		return "", nil
	}
	norm, err := d.Normalize(value)
	if err != nil {
		return "", fmt.Errorf("%s: %w", d.EnvVar, err)
	}
	return norm, nil
}

// Reader é o que o resolvedor precisa do banco.
type Reader interface {
	ListSettings(ctx context.Context) (map[string]string, error)
}

// Fontes de um valor efetivo.
const (
	SourceProject  = "project"
	SourceDatabase = "database"
	SourceEnv      = "env"
	SourceDefault  = "default"
)

// Resolver escolhe o valor efetivo de cada chave.
type Resolver struct {
	db  Reader
	env Env
}

// NewResolver monta o resolvedor. db pode ser nil (só ambiente e padrão).
func NewResolver(db Reader, env Env) *Resolver { return &Resolver{db: db, env: env} }

func (r *Resolver) stored(ctx context.Context) (map[string]string, error) {
	if r.db == nil {
		return nil, nil
	}
	m, err := r.db.ListSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: ler configurações: %w", err)
	}
	return m, nil
}

func defaultRaw(key string) string {
	if d, ok := Lookup(key); ok && !d.IsLimit() {
		return d.Default
	}
	return Env{Limits: domain.DefaultLimits()}.raw(key)
}

// pick escolhe o texto e a fonte de uma chave, sem o nível do projeto.
func (r *Resolver) pick(stored map[string]string, key string) (raw, source string) {
	d, _ := Lookup(key)
	if v := stored[key]; v != "" || (d.AllowEmpty && hasKey(stored, key)) {
		return v, SourceDatabase
	}
	if v := r.env.raw(key); v != "" {
		return v, SourceEnv
	}
	return defaultRaw(key), SourceDefault
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

// Item é uma chave com o valor efetivo, para o painel.
type Item struct {
	Key     string   `json:"key"`
	Kind    Kind     `json:"kind"`
	Value   any      `json:"value"`
	Source  string   `json:"source"`
	Default any      `json:"default"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	// Choices são os valores aceitos de uma chave do tipo choice.
	Choices []string `json:"choices,omitempty"`
}

func typed(d Def, raw string) any {
	switch d.Kind {
	case KindInt:
		n, _ := strconv.Atoi(raw)
		return n
	case KindFloat:
		f, _ := strconv.ParseFloat(raw, 64)
		return f
	}
	if d.Kind == KindDomain || d.Kind == KindChoice {
		return raw
	}
	hosts := []string{}
	for _, h := range strings.Split(raw, ",") {
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// List devolve todas as chaves com o valor efetivo global.
func (r *Resolver) List(ctx context.Context) ([]Item, error) {
	stored, err := r.stored(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(Defs))
	for _, d := range Defs {
		raw, source := r.pick(stored, d.Key)
		it := Item{Key: d.Key, Kind: d.Kind, Value: typed(d, raw), Source: source, Default: typed(d, defaultRaw(d.Key))}
		if d.Kind == KindInt || d.Kind == KindFloat {
			min, max := d.Min, d.Max
			it.Min, it.Max = &min, &max
		}
		it.Choices = d.Choices
		items = append(items, it)
	}
	return items, nil
}

// Effective é o valor efetivo de um limite e de onde ele vem.
type Effective struct {
	Value  float64 `json:"value"`
	Source string  `json:"source"`
}

// EffectiveLimits devolve, por campo, o valor que vale para um projeto com as
// sobreposições dadas, e a fonte.
func (r *Resolver) EffectiveLimits(ctx context.Context, project domain.Limits) (map[string]Effective, error) {
	stored, err := r.stored(ctx)
	if err != nil {
		return nil, err
	}
	over := map[string]float64{
		AppMemoryMB: float64(project.AppMemoryMB), AppCPUs: project.AppCPUs, AppPids: float64(project.AppPids),
		BuildMemoryMB: float64(project.BuildMemoryMB), BuildCPUs: project.BuildCPUs,
	}
	out := map[string]Effective{}
	for _, d := range Defs {
		if !d.IsLimit() {
			continue
		}
		if v := over[d.Key]; v != 0 {
			out[d.Key] = Effective{Value: v, Source: SourceProject}
			continue
		}
		raw, source := r.pick(stored, d.Key)
		v, _ := strconv.ParseFloat(raw, 64)
		out[d.Key] = Effective{Value: v, Source: source}
	}
	return out, nil
}

// Limits devolve os limites que valem para um projeto.
func (r *Resolver) Limits(ctx context.Context, project domain.Limits) (domain.Limits, error) {
	eff, err := r.EffectiveLimits(ctx, project)
	if err != nil {
		return domain.Limits{}, err
	}
	return domain.Limits{
		AppMemoryMB: int(eff[AppMemoryMB].Value), AppCPUs: eff[AppCPUs].Value, AppPids: int(eff[AppPids].Value),
		BuildMemoryMB: int(eff[BuildMemoryMB].Value), BuildCPUs: eff[BuildCPUs].Value,
	}, nil
}

// GitHosts devolve os hosts de repositório aceitos: os padrão mais os extras
// efetivos.
func (r *Resolver) GitHosts(ctx context.Context) ([]string, error) {
	stored, err := r.stored(ctx)
	if err != nil {
		return nil, err
	}
	raw, _ := r.pick(stored, ExtraGitHosts)
	return validate.GitHosts(typed(Def{Kind: KindHosts}, raw).([]string)), nil
}

// Routing é a configuração de roteamento das aplicações (DEC-31): o domínio e
// o esquema da URL pública. Domínio vazio desliga o roteamento. O domínio pode
// levar uma porta (`localhost:8088`), que aparece na URL mas não no nome de
// host que o Traefik compara.
type Routing struct {
	Domain string
	Scheme string
}

// Enabled diz se as aplicações recebem um endereço público.
func (r Routing) Enabled() bool { return r.Domain != "" }

// hostAndPort separa o domínio da porta opcional.
func (r Routing) hostAndPort() (host, port string) {
	host, port, _ = strings.Cut(r.Domain, ":")
	return host, port
}

// Host é o nome público de um projeto, sem porta.
func (r Routing) Host(slug string) string {
	host, _ := r.hostAndPort()
	return slug + "." + host
}

// URL é o endereço público de um projeto, com a porta se houver e a barra final.
func (r Routing) URL(slug string) string {
	_, port := r.hostAndPort()
	if port != "" {
		port = ":" + port
	}
	return r.Scheme + "://" + r.Host(slug) + port + "/"
}

// Lifecycle é a política de retirada de releases (DEC-38): quanto esperar
// antes de remover a release anterior e quantas imagens guardar por projeto.
type Lifecycle struct {
	GraceSeconds int
	ImagesKept   int
}

// Lifecycle devolve a política efetiva.
func (r *Resolver) Lifecycle(ctx context.Context) (Lifecycle, error) {
	stored, err := r.stored(ctx)
	if err != nil {
		return Lifecycle{}, err
	}
	grace, _ := r.pick(stored, RetireGraceSeconds)
	kept, _ := r.pick(stored, ReleaseImagesKept)
	g, _ := strconv.Atoi(grace)
	k, _ := strconv.Atoi(kept)
	return Lifecycle{GraceSeconds: g, ImagesKept: k}, nil
}

// Apps devolve o roteamento efetivo.
func (r *Resolver) Apps(ctx context.Context) (Routing, error) {
	stored, err := r.stored(ctx)
	if err != nil {
		return Routing{}, err
	}
	domainRaw, _ := r.pick(stored, AppsDomain)
	schemeRaw, _ := r.pick(stored, AppsScheme)
	return Routing{Domain: domainRaw, Scheme: schemeRaw}, nil
}

// ValidateLimits confere as sobreposições de um projeto: cada campo definido
// tem de estar na faixa da chave correspondente.
func ValidateLimits(l domain.Limits) error {
	check := func(key string, v float64) error {
		if v == 0 {
			return nil
		}
		d, _ := Lookup(key)
		if v < 0 {
			return invalid("%s: use um valor de %g a %g", key, d.Min, d.Max)
		}
		if _, err := d.Normalize(strconv.FormatFloat(v, 'f', -1, 64)); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		return nil
	}
	for key, v := range map[string]float64{
		AppMemoryMB: float64(l.AppMemoryMB), AppCPUs: l.AppCPUs, AppPids: float64(l.AppPids),
		BuildMemoryMB: float64(l.BuildMemoryMB), BuildCPUs: l.BuildCPUs,
	} {
		if err := check(key, v); err != nil {
			return err
		}
	}
	return nil
}
