package analyzer

import (
	"math"
	"regexp"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// platformVars são fornecidas pela plataforma. No D0 só PORT; DATABASE_URL
// entra quando houver vínculo com banco provisionado (issue #22).
var platformVars = map[string]bool{"PORT": true}

// generableVars é a lista **fechada** de segredos que o próprio app inventa
// (DEC-24 item 4). Nunca por padrão genérico (`*_KEY`, `*_TOKEN`,
// `*_PASSWORD`): uma credencial de terceiro gerada produziria um app quebrado
// sem erro claro. Falso negativo vira entrada manual.
var generableVars = map[string]bool{
	"JWT_SECRET": true, "SESSION_SECRET": true, "COOKIE_SECRET": true, "CSRF_SECRET": true,
	"SECRET_KEY": true, "SECRET_KEY_BASE": true, "APP_SECRET": true, "APP_KEY": true,
	"AUTH_SECRET": true, "NEXTAUTH_SECRET": true, "ENCRYPTION_KEY": true,
}

func classifyVariable(name string) domain.VarClass {
	upper := strings.ToUpper(name)
	switch {
	case platformVars[upper]:
		return domain.ClassPlatform
	case generableVars[upper]:
		return domain.ClassGenerable
	}
	return domain.ClassUser
}

var secretNameParts = []string{"SECRET", "PASSWORD", "PASSWD", "TOKEN", "PRIVATE_KEY", "API_KEY", "APIKEY", "ACCESS_KEY", "CREDENTIAL"}

// secretLikeName é uma pista pelo nome, usada só para não pré-preencher e
// para decidir se vale checar entropia do valor.
func secretLikeName(name string) bool {
	upper := strings.ToUpper(name)
	if strings.HasSuffix(upper, "_KEY") || upper == "KEY" {
		return true
	}
	for _, part := range secretNameParts {
		if strings.Contains(upper, part) {
			return true
		}
	}
	return false
}

// placeholderMarkers: lista fechada de textos de preenchimento. Comparação em
// minúsculas, por trecho; `<...>` é tratado à parte.
var placeholderMarkers = []string{
	"changeme", "change-me", "change_me", "change-this", "change_this",
	"replace-me", "replace_me", "replace-this", "your-", "your_", "yourkey",
	"xxx", "todo", "fixme", "placeholder", "example", "exemplo",
	"insert-", "insert_", "-here", "_here", "dummy",
}

var angleBracketed = regexp.MustCompile(`<[^<>]+>`)

func isPlaceholder(value string) bool {
	lower := strings.ToLower(value)
	if angleBracketed.MatchString(value) || strings.Trim(lower, "*") == "" {
		return true
	}
	for _, marker := range placeholderMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// credentialFormats são formatos conhecidos de credencial (DEC-24 item 8).
var credentialFormats = []struct {
	kind    string
	pattern *regexp.Regexp
}{
	{"aws_access_key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github_token", regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}\b|\bgithub_pat_[A-Za-z0-9_]{22,}\b`)},
	{"slack_token", regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`)},
	{"stripe_live_key", regexp.MustCompile(`\b[sr]k_live_[A-Za-z0-9]{16,}`)},
	{"google_api_key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{"private_key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"jwt", regexp.MustCompile(`^eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)},
}

// exposureLabels dão o nome legível de cada tipo, para as mensagens.
var exposureLabels = map[string]string{
	"aws_access_key":      "chave de acesso da AWS",
	"github_token":        "token do GitHub",
	"slack_token":         "token do Slack",
	"stripe_live_key":     "chave live do Stripe",
	"google_api_key":      "chave de API do Google",
	"private_key":         "chave privada",
	"jwt":                 "JWT",
	"high_entropy_secret": "valor aleatório em variável de nome secreto",
}

const kindHighEntropy = "high_entropy_secret"

// credentialKind devolve o tipo de credencial que o valor aparenta ser, ou "".
// Só o tipo sai daqui: o valor é lido em memória e descartado (DEC-24 item 8).
func credentialKind(name, value string) string {
	if value == "" {
		return ""
	}
	for _, f := range credentialFormats {
		if f.pattern.MatchString(value) {
			return f.kind
		}
	}
	if secretLikeName(name) && !isPlaceholder(value) && looksRandom(value) {
		return kindHighEntropy
	}
	return ""
}

// looksRandom: longo, sem espaço, sem aparência de URL ou caminho e com
// entropia de Shannon alta. Heurística; erra para os dois lados.
func looksRandom(value string) bool {
	if len(value) < 20 || strings.ContainsAny(value, " \t") || strings.Contains(value, "://") ||
		strings.HasPrefix(value, "/") || strings.HasPrefix(value, ".") {
		return false
	}
	counts := map[rune]float64{}
	for _, r := range value {
		counts[r]++
	}
	total := float64(len([]rune(value)))
	var entropy float64
	for _, c := range counts {
		p := c / total
		entropy -= p * math.Log2(p)
	}
	return entropy >= 3.5
}

// exampleState classifica o valor de uma linha de arquivo de exemplo.
func exampleState(name, value string, commented bool) domain.ExampleState {
	switch {
	case commented:
		return domain.ExampleCommented
	case value == "":
		return domain.ExampleEmpty
	case credentialKind(name, value) != "":
		return domain.ExampleExposed
	case isPlaceholder(value), secretLikeName(name):
		// Nome secreto com valor qualquer nunca é pré-preenchido.
		return domain.ExamplePlaceholder
	}
	return domain.ExampleDefault
}
