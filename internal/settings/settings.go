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
)

// Kind é o tipo do valor de uma chave.
type Kind string

const (
	KindInt   Kind = "integer"
	KindFloat Kind = "number"
	KindHosts Kind = "hosts"
)

// Def descreve uma chave: tipo, faixa aceita e a variável de ambiente que a
// define como padrão inicial.
type Def struct {
	Key      string
	Kind     Kind
	Min, Max float64
	EnvVar   string
}

// Defs são as chaves, na ordem em que o painel as mostra.
var Defs = []Def{
	{AppMemoryMB, KindInt, 64, 262144, "COBOGO_APP_MEMORY_MB"},
	{AppCPUs, KindFloat, 0.1, 256, "COBOGO_APP_CPUS"},
	{AppPids, KindInt, 16, 65536, "COBOGO_APP_PIDS"},
	{BuildMemoryMB, KindInt, 256, 262144, "COBOGO_BUILD_MEMORY_MB"},
	{BuildCPUs, KindFloat, 0.1, 256, "COBOGO_BUILD_CPUS"},
	{ExtraGitHosts, KindHosts, 0, 0, "COBOGO_EXTRA_GIT_HOSTS"},
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
func (d Def) IsLimit() bool { return d.Kind == KindInt || d.Kind == KindFloat }

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
	}
	return "", invalid("chave desconhecida")
}

// Env são os valores de ambiente que servem de padrão inicial (zero = ausente).
type Env struct {
	Limits   domain.Limits
	GitHosts []string
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
	l := domain.DefaultLimits()
	return Env{Limits: l}.raw(key)
}

// pick escolhe o texto e a fonte de uma chave, sem o nível do projeto.
func (r *Resolver) pick(stored map[string]string, key string) (raw, source string) {
	if v := stored[key]; v != "" || (key == ExtraGitHosts && hasKey(stored, key)) {
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
		if d.IsLimit() {
			min, max := d.Min, d.Max
			it.Min, it.Max = &min, &max
		}
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
