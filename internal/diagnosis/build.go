package diagnosis

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Falhas de build de uma época (DEC-54 C). O log do build nomeia a causa; o diagnóstico cita a
// linha e, quando a correção é um override que o motor já conhece, a oferece como "novo deploy
// com ...". Só o log decide: não há tabela aqui, e nada é aplicado sem a confirmação da pessoa.

var (
	opensslFailure = regexp.MustCompile(`ERR_OSSL_EVP_UNSUPPORTED|digital envelope routines::unsupported|error:0308010C`)
	distutilsGone  = regexp.MustCompile(`No module named ['"]?distutils['"]?`)
	wheelFailure   = regexp.MustCompile(`Failed building wheel for ([A-Za-z0-9._-]+)|Could not build wheels? for ([A-Za-z0-9._-]+)|Failed to build ['"]?([A-Za-z0-9._-]+)['"]?`)
	noDistribution = regexp.MustCompile(`No matching distribution found for ([A-Za-z0-9._-]+)`)
)

const (
	profileNode   = "P-NODE"
	profileStatic = "P-STATIC"
	profilePython = "P-PY"
)

// firstLine devolve a primeira linha do log que casa com o padrão, limpa para a mensagem.
func firstLine(log string, re *regexp.Regexp) string {
	for _, line := range strings.Split(log, "\n") {
		if re.MatchString(line) {
			line = strings.TrimSpace(line)
			// O prefixo `#13 12.3` do BuildKit não ajuda a ler.
			line = regexp.MustCompile(`^#\d+\s+[\d.]+\s*`).ReplaceAllString(line, "")
			if len(line) > 160 {
				line = line[:160] + "…"
			}
			return line
		}
	}
	return ""
}

// Build reconhece, no final do log de um build que falhou, uma falha de época: ferramenta ou
// dependência de uma geração mais antiga que o runtime da imagem. profile é o perfil do plano e
// pythonVersion, a versão do Python dele (vazia fora do perfil Python).
func Build(logTail, profile, pythonVersion string) Diagnosis {
	switch {
	case (profile == profileNode || profile == profileStatic) && opensslFailure.MatchString(logTail):
		return Diagnosis{
			Confidence: Confirmed,
			Message: fmt.Sprintf("Causa: o build usa uma ferramenta que depende do OpenSSL antigo (webpack 4, como o react-scripts 4 ou menor) e o Node da imagem é mais novo. O log diz: `%s`. O contorno documentado é rodar só o build com `NODE_OPTIONS=--openssl-legacy-provider`; a aplicação em execução não muda.",
				firstLine(logTail, opensslFailure)),
			Suggestion: &Suggestion{Overrides: map[string]string{domain.FieldBuildLegacyOpenSSL: "true"}, Label: "o contorno do OpenSSL no build"},
		}
	case profile == profilePython && distutilsGone.MatchString(logTail):
		d := Diagnosis{
			Confidence: Confirmed,
			Message: fmt.Sprintf("Causa: o projeto usa uma dependência que ainda importa o `distutils`, que saiu do Python 3.12. O log diz: `%s`.",
				firstLine(logTail, distutilsGone)),
		}
		if pyMinor(pythonVersion) > pyMinor("3.11") {
			d.Message += " Tente o Python 3.11 (ou um mais antigo, se o projeto for de uma época anterior)."
			d.Suggestion = &Suggestion{Overrides: map[string]string{domain.FieldPythonVersion: "3.11"}, Label: "Python 3.11"}
		}
		return d
	case profile == profilePython && wheelFailure.MatchString(logTail):
		d := Diagnosis{
			Confidence: Possible,
			Message: fmt.Sprintf("Possível causa: um pacote fixado não tem versão pronta para o Python %s e não compilou. O log diz: `%s`. Projetos que fixam dependências antigas costumam precisar de um Python mais antigo.",
				pythonVersion, firstLine(logTail, wheelFailure)),
		}
		if older := olderPython(pythonVersion); older != "" {
			d.Message += fmt.Sprintf(" Tente o Python %s.", older)
			d.Suggestion = &Suggestion{Overrides: map[string]string{domain.FieldPythonVersion: older}, Label: "Python " + older}
		}
		return d
	case profile == profilePython && noDistribution.MatchString(logTail):
		// A causa é ambígua (versão removida do índice, índice privado, outro Python): o
		// motor diz o que o log diz e não sugere um Python que talvez não resolva.
		return Diagnosis{
			Confidence: Possible,
			Message: fmt.Sprintf("Possível causa: o pip não encontrou uma versão fixada para o Python %s. O log diz: `%s`. O release pode ter sido removido do índice ou exigir outro Python; confira a linha no `requirements`.",
				pythonVersion, firstLine(logTail, noDistribution)),
		}
	}
	return Diagnosis{Confidence: Unknown}
}

func pyMinor(v string) int {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) < 2 {
		return 0
	}
	a, _ := strconv.Atoi(parts[0])
	b, _ := strconv.Atoi(parts[1])
	return a*100 + b
}

// olderPython devolve a versão conhecida (do catálogo ou legada) logo abaixo da dada, ou "".
func olderPython(v string) string {
	best, bestMinor := "", 0
	for _, list := range [][]string{domain.SupportedPythonVersions, domain.LegacyPythonVersions} {
		for _, x := range list {
			if m := pyMinor(x); m < pyMinor(v) && m > bestMinor {
				best, bestMinor = x, m
			}
		}
	}
	return best
}
