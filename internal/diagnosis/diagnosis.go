// Package diagnosis explica, quando possível, por que uma aplicação não subiu
// (DEC-24 item 11, DEC-26 item 12): cruza o log do contêiner com o inventário
// de variáveis da análise e com as variáveis que o projeto já tem.
//
// Distingue sintoma de diagnóstico (issue #12): só afirma uma causa quando o
// próprio log a nomeia.
package diagnosis

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Confidence diz o quanto o diagnóstico se sustenta.
type Confidence string

const (
	// Confirmed: o log cita a variável numa linha que reclama de falta.
	Confirmed Confidence = "confirmed"
	// Possible: há variáveis sem valor que a aplicação provavelmente usa, mas
	// nada no log as aponta; ou o log as cita sem reclamar delas.
	Possible Confidence = "possible"
	// Unknown: nada indica variável ausente.
	Unknown Confidence = "unknown"
)

type Diagnosis struct {
	Confidence Confidence `json:"confidence"`
	// Variables são as variáveis relacionadas, em ordem estável.
	Variables []string `json:"variables,omitempty"`
	// Field é o campo do plano suspeito, quando a correção é um override num
	// novo deploy (por enquanto só "port"; DEC-35).
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
	// Suggestion é uma correção que o painel oferece como "novo deploy com ..."
	// (DEC-54 C): overrides conhecidos do motor, que só valem com a confirmação da pessoa.
	Suggestion *Suggestion `json:"suggestion,omitempty"`
}

// Suggestion é um conjunto de overrides sugerido por um diagnóstico.
type Suggestion struct {
	// Overrides é campo do plano → valor, o mesmo corpo de um novo deploy.
	Overrides map[string]string `json:"overrides"`
	// Label diz o que o botão faz ("Python 3.11").
	Label string `json:"label"`
}

// PlatformPortIgnored explica a sonda que não obteve resposta de um app que lê
// PORT: a plataforma entregou a porta, mas nada respondeu nela (DEC-35 item 5).
// source é onde o código lê PORT ("index.js:12"), quando se sabe.
func PlatformPortIgnored(port int, source string) Diagnosis {
	where := "O código lê `PORT`"
	if source != "" {
		where = fmt.Sprintf("O código lê `PORT` em `%s`", source)
	}
	return Diagnosis{
		Confidence: Possible,
		Field:      domain.FieldPort,
		Message: fmt.Sprintf("Possível causa: a porta. %s, então a plataforma definiu %d, mas nada respondeu nela. "+
			"Se a aplicação escuta em outra porta (ou só em 127.0.0.1), informe a porta certa num novo deploy.", where, port),
	}
}

// complaint reconhece, em minúsculas, uma linha que reclama de falta de
// valor. Lista fechada; mensagens de cada runtime variam, e o corpus mede o
// quanto ela acerta.
var complaint = regexp.MustCompile(`is not defined|is undefined|\bundefined\b|is not set|\bnot set\b|is required|\brequired\b|must be set|must be defined|\bmissing\b|cannot be empty|should be set|não definid|obrigatóri`)

// Diagnose cruza o log (já redigido) com as variáveis sem valor. unset são as
// variáveis do inventário da análise que o projeto não tem e que não são da
// plataforma. exited indica que o contêiner terminou.
func Diagnose(logTail string, unset []domain.ConfigVariable, exited bool) Diagnosis {
	lines := strings.Split(logTail, "\n")

	var confirmed, mentioned []string
	for _, v := range unset {
		word := regexp.MustCompile(`\b` + regexp.QuoteMeta(v.Name) + `\b`)
		var named, complained bool
		for _, line := range lines {
			if !word.MatchString(line) {
				continue
			}
			named = true
			if complaint.MatchString(strings.ToLower(line)) {
				complained = true
				break
			}
		}
		switch {
		case complained:
			confirmed = append(confirmed, v.Name)
		case named:
			mentioned = append(mentioned, v.Name)
		}
	}
	slices.Sort(confirmed)
	slices.Sort(mentioned)

	if len(confirmed) > 0 {
		return Diagnosis{
			Confidence: Confirmed, Variables: confirmed,
			Message: fmt.Sprintf("Provável causa: %s não %s preenchida e a aplicação reclamou %s no log. Preencha e faça um novo deploy.",
				joinNames(confirmed), plural(confirmed, "foi", "foram"), plural(confirmed, "dela", "delas")),
		}
	}

	candidates := slices.Clone(mentioned)
	if exited {
		for _, v := range unset {
			if likelyNeeded(v) && !slices.Contains(candidates, v.Name) {
				candidates = append(candidates, v.Name)
			}
		}
	}
	slices.Sort(candidates)
	if len(candidates) > 0 {
		return Diagnosis{
			Confidence: Possible, Variables: candidates,
			Message: fmt.Sprintf("Possível causa: variáveis sem valor que o código usa (%s). Nada no log confirma qual delas falta; se a aplicação precisa de alguma, preencha-a e faça um novo deploy.",
				strings.Join(candidates, ", ")),
		}
	}
	return Diagnosis{Confidence: Unknown, Message: ""}
}

// likelyNeeded diz se o aviso da análise já indicava que a variável
// provavelmente é necessária.
func likelyNeeded(v domain.ConfigVariable) bool {
	if v.Hint == nil {
		return false
	}
	switch v.Hint.Code {
	case domain.HintLikelyRequired, domain.HintUndeclared, domain.HintReferencedOnly:
		return true
	}
	return false
}

func joinNames(names []string) string { return "`" + strings.Join(names, "`, `") + "`" }

func plural(names []string, one, many string) string {
	if len(names) == 1 {
		return one
	}
	return many
}

// Unset devolve as variáveis do inventário sem valor no projeto: as que não são
// da plataforma e cujo nome não está em have.
func Unset(inventory []domain.ConfigVariable, have map[string]string) []domain.ConfigVariable {
	var out []domain.ConfigVariable
	for _, v := range inventory {
		if v.Class == domain.ClassPlatform {
			continue
		}
		if _, ok := have[v.Name]; ok {
			continue
		}
		out = append(out, v)
	}
	return out
}

// ProbeCause explica uma resposta da sonda que passou (abaixo de 500) mas indica
// um problema conhecido, ou devolve "" quando não há o que dizer (DEC-49 item 5).
// Um Django com DEBUG desligado não escreve a causa de um host não permitido no
// log: a explicação precisa vir daqui.
func ProbeCause(status int, framework string) string {
	if status == 400 && framework == domain.FrameworkDjango {
		return "O Django responde 400 (Bad Request) quando o endereço pelo qual é acessado não está em ALLOWED_HOSTS, e com DEBUG desligado não registra isso no log. Veja os avisos da análise sobre ALLOWED_HOSTS e CSRF_TRUSTED_ORIGINS: a lista precisa incluir o endereço deste projeto, lida de uma variável de ambiente."
	}
	return ""
}
