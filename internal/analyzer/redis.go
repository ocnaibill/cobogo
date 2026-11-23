package analyzer

import (
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// HEU-06 para Redis (DEC-50 C). O Redis é um serviço de cache e de fila: a análise o reconhece
// por evidência (o import do cliente, o backend de cache do Django, o endereço `redis://` escrito
// no código, o cliente nas dependências) e diz **como o código chega até ele**: lendo uma variável de
// ambiente (o Cobogó liga a do Redis gerenciado) ou com o endereço escrito no código (o Redis do
// Cobogó, que vive noutro contêiner, só serve se o código passar a ler uma variável).

// Nomes de URL do Redis, em ordem de preferência.
var redisVariables = []string{"REDIS_URL", "CELERY_BROKER_URL", "BROKER_URL", "CACHE_URL", "REDISCLOUD_URL", "REDIS_TLS_URL"}

var (
	redisPartName    = regexp.MustCompile(`^(REDIS)_?(HOST|HOSTNAME|PORT|PASSWORD|PASS|PWD)$`)
	redisFamilyOrder = []string{"REDIS"}
	// Um endereço escrito entre aspas: redis://… ou rediss://… (o do broker do Celery, o do cache).
	redisURLLiteral = regexp.MustCompile("[\"'`](rediss?://[^\"'`\\s]*)[\"'`]")
)

// redisAddress devolve o endereço de uma URL redis sem credenciais (nunca se guarda ou se mostra
// uma senha lida do código) e o host dela. ok é falso se não é uma URL redis válida.
func redisAddress(raw string) (display, host string, ok bool) {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "redis" && u.Scheme != "rediss") || u.Hostname() == "" {
		return "", "", false
	}
	h := u.Hostname()
	if p := u.Port(); p != "" {
		return u.Scheme + "://" + net.JoinHostPort(h, p), h, true
	}
	return u.Scheme + "://" + h, h, true
}

// pyRedisClients e jsRedisClients: os clientes de Redis por linguagem.
var (
	pyRedisDeps   = []string{"redis", "django-redis", "aioredis", "redis-py-cluster", "rq", "django-rq", "channels-redis"}
	jsRedisDeps   = map[string]bool{"ioredis": true, "redis": true, "bullmq": true, "bull": true, "connect-redis": true, "@redis/client": true}
	jsRedisImport = regexp.MustCompile(`(?:from\s+|require\(|import\(|import\s+)\s*["'](?:ioredis|redis|bullmq|bull|connect-redis|@redis/client)["']`)
)

// buildRedisNeed monta a necessidade de Redis a partir da evidência: forte (uso no código) vale
// `required`; só a dependência, `possible`. fixed são os lugares onde o código escreve o endereço.
func buildRedisNeed(strong []string, dep, depSource string, fixed []string, vars []domain.ConfigVariable) []domain.ServiceNeed {
	need := domain.ServiceNeed{Engine: domain.ServiceRedis, FixedAt: fixed}
	switch {
	case len(strong) > 0:
		need.Level, need.Evidence, need.Reason = domain.NeedRequired, strong, "o código usa Redis"
	case dep != "":
		need.Level, need.Evidence = domain.NeedPossible, []string{depSource}
		need.Reason = "a dependência " + dep + " fala com Redis, mas nenhum arquivo de runtime a usa"
	default:
		return nil
	}
	need.Style, need.Family, need.Variables = connectionStyleFor(domain.ServiceRedis, "", vars)
	need.Variable = need.Variables[domain.PrimaryRole(need.Style)]
	return []domain.ServiceNeed{need}
}

// Um endereço que é o padrão de uma leitura de variável de ambiente (os.environ.get('X', 'redis://…'),
// process.env.X || 'redis://…') não é um endereço fixo: o código lê a variável, e o literal é só o que vale
// sem ela. O que vem antes do literal, na mesma linha, decide.
var envDefaultBefore = regexp.MustCompile(`(?:environ\.get|getenv|config|env(?:\.\w+)?)\(\s*["'][A-Za-z_]\w*["']\s*,\s*(?:default\s*=\s*)?$|(?:process\.env|import\.meta\.env)(?:\.\w+|\[\s*["']\w+["']\s*\])\s*(?:\|\||\?\?)\s*$`)

// isEnvDefault diz se o literal que começa em start é o padrão de uma variável de ambiente.
func isEnvDefault(data []byte, start int) bool {
	lineStart := start
	for lineStart > 0 && data[lineStart-1] != '\n' {
		lineStart--
	}
	return envDefaultBefore.Match(data[lineStart:start])
}

func isLocalHost(h string) bool {
	switch strings.ToLower(h) {
	case "localhost", "127.0.0.1", "::1", "0.0.0.0":
		return true
	}
	return false
}
