// Package redact prepara texto vindo de aplicações (logs de contêiner) para
// ser guardado e exibido: remove os valores secretos que o Cobogó conhece e
// o que quebraria o armazenamento (RN-11).
//
// A redação só cobre valores que o Cobogó conhece, os das variáveis do
// projeto. Não promete encontrar qualquer segredo que a aplicação imprima.
package redact

import (
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Placeholder substitui cada ocorrência de um valor secreto.
const Placeholder = "[REDIGIDO]"

// MinSecretLen é o menor valor redigido: abaixo disso não há como distinguir
// o segredo de texto comum ("1", "on", "true"), e a redação destruiria o log.
const MinSecretLen = 4

// Values substitui toda ocorrência dos valores dados por Placeholder. Os mais
// longos são trocados primeiro, para um valor que contém outro não deixar
// sobra. Valores curtos demais (MinSecretLen) são ignorados.
func Values(text string, secrets []string) string {
	usable := make([]string, 0, len(secrets))
	seen := map[string]bool{}
	for _, v := range secrets {
		if len(v) >= MinSecretLen && !seen[v] {
			seen[v] = true
			usable = append(usable, v)
		}
	}
	sort.Slice(usable, func(i, j int) bool { return len(usable[i]) > len(usable[j]) })
	for _, v := range usable {
		text = strings.ReplaceAll(text, v, Placeholder)
	}
	return text
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)

// Clean remove sequências ANSI e caracteres de controle (menos nova linha e
// tabulação) e troca bytes que não são UTF-8 válido: o PostgreSQL recusa NUL
// em JSONB e a interface não deve receber lixo de terminal.
func Clean(text string) string {
	text = ansi.ReplaceAllString(text, "")
	text = strings.ToValidUTF8(text, "�")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r == '\r':
			return -1
		case r < ' ' || r == 0x7f:
			return -1
		}
		return r
	}, text)
}

// Tail mantém só o final de text, no máximo maxBytes, começando numa linha
// inteira quando possível. Indica o corte com uma linha de aviso.
func Tail(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	cut := text[len(text)-maxBytes:]
	if i := strings.IndexByte(cut, '\n'); i >= 0 && i+1 < len(cut) {
		cut = cut[i+1:]
	}
	for !utf8.ValidString(cut) && len(cut) > 0 {
		cut = cut[1:]
	}
	return "[...início do log omitido...]\n" + cut
}
