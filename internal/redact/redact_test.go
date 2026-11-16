package redact

import (
	"strings"
	"testing"
)

func TestValuesRemovesEveryOccurrence(t *testing.T) {
	log := "conectando com senha-super-secreta ao host\nfalhou: senha-super-secreta inválida\ntoken=abcd1234efgh"
	got := Values(log, []string{"senha-super-secreta", "abcd1234efgh"})
	for _, secret := range []string{"senha-super-secreta", "abcd1234efgh"} {
		if strings.Contains(got, secret) {
			t.Errorf("secret %q survived: %q", secret, got)
		}
	}
	if strings.Count(got, Placeholder) != 3 {
		t.Errorf("want 3 placeholders, got %q", got)
	}
	if !strings.Contains(got, "conectando com") || !strings.Contains(got, "ao host") {
		t.Errorf("the surrounding text must stay: %q", got)
	}
}

func TestValuesHandlesOverlapsLongestFirst(t *testing.T) {
	got := Values("a chave é abcdefgh-segredo-longo ok", []string{"abcdefgh", "abcdefgh-segredo-longo"})
	if strings.Contains(got, "segredo") || strings.Contains(got, "longo") {
		t.Errorf("the shorter value left the tail of the longer one behind: %q", got)
	}
}

// Valores muito curtos não são redigidos: "1", "on" e "true" estragariam o log
// sem proteger nada. É um limite conhecido (DEC-26).
func TestValuesIgnoresShortValuesAndEmpty(t *testing.T) {
	log := "PORT=3000 debug=1 verbose=on ok=true"
	got := Values(log, []string{"1", "on", "", "abc"})
	if got != log {
		t.Errorf("short values must not be redacted: %q", got)
	}
	if Values(log, nil) != log {
		t.Error("no secrets means no change")
	}
	if got := Values(log, []string{"true"}); !strings.Contains(got, Placeholder) {
		t.Errorf("a 4-byte value is the shortest that is redacted: %q", got)
	}
}

func TestValuesDoesNotBreakOnRegexLikeSecrets(t *testing.T) {
	got := Values("senha: a.b*c(d)[e]+f?g$h^i\\j|k ok", []string{"a.b*c(d)[e]+f?g$h^i\\j|k"})
	if strings.Contains(got, "a.b*c") {
		t.Errorf("special characters must be matched literally: %q", got)
	}
}

func TestClean(t *testing.T) {
	in := "\x1b[31merro\x1b[0m: falha\r\nlinha com NUL\x00 e sino\x07\ttab\n\xff\xfebytes inválidos"
	got := Clean(in)
	for _, bad := range []string{"\x1b", "\x00", "\x07", "\r"} {
		if strings.Contains(got, bad) {
			t.Errorf("Clean left %q in %q", bad, got)
		}
	}
	if !strings.Contains(got, "erro: falha\n") || !strings.Contains(got, "\ttab\n") {
		t.Errorf("Clean must keep text, newlines and tabs: %q", got)
	}
	if !strings.Contains(strings.ToValidUTF8(got, ""), "bytes inválidos") || strings.ToValidUTF8(got, "") != got {
		t.Errorf("the result must be valid UTF-8: %q", got)
	}
}

func TestTail(t *testing.T) {
	short := "linha 1\nlinha 2\n"
	if Tail(short, 100) != short {
		t.Error("text within the limit stays as is")
	}
	long := strings.Repeat("linha de log com bastante texto\n", 50)
	got := Tail(long, 200)
	if len(got) > 200+len("[...início do log omitido...]\n") {
		t.Errorf("tail has %d bytes", len(got))
	}
	if !strings.HasPrefix(got, "[...início do log omitido...]\n") {
		t.Errorf("the cut must be announced: %q", got[:40])
	}
	if !strings.Contains(strings.TrimPrefix(got, "[...início do log omitido...]\n"), "linha de log com bastante texto\n") ||
		strings.HasPrefix(strings.TrimPrefix(got, "[...início do log omitido...]\n"), "texto") {
		t.Errorf("the tail must start at a whole line: %q", got)
	}
	// Um corte no meio de um caractere multibyte não pode gerar UTF-8 inválido.
	multi := strings.Repeat("ação\n", 100)
	if g := Tail(multi, 53); strings.ToValidUTF8(g, "") != g {
		t.Errorf("tail produced invalid UTF-8: %q", g)
	}
}
