package envfile

import (
	"strings"
	"testing"
)

func values(entries []Entry) map[string]string {
	m := map[string]string{}
	for _, e := range entries {
		m[e.Name] = e.Value
	}
	return m
}

func TestParseReadsPlainQuotedAndExportedAssignments(t *testing.T) {
	entries, diags := Parse(strings.Join([]string{
		"# comentário",
		"",
		"PLAIN=abc",
		"export EXPORTED=com-export",
		"  SPACED = valor com espaços  ",
		`DOUBLE="a \"b\" \\ c\nd"`,
		`SINGLE='literal \n $HOME'`,
		"WITH_COMMENT=valor # comentário",
		`QUOTED_COMMENT="x" # c`,
		"EMPTY=",
		"URL=https://exemplo.com/p?a=1&b=2",
		"HASH_NO_SPACE=abc#def",
	}, "\n"))
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %+v, want none", diags)
	}
	want := map[string]string{
		"PLAIN": "abc", "EXPORTED": "com-export", "SPACED": "valor com espaços",
		"DOUBLE": "a \"b\" \\ c\nd", "SINGLE": `literal \n $HOME`, "WITH_COMMENT": "valor",
		"QUOTED_COMMENT": "x", "EMPTY": "", "URL": "https://exemplo.com/p?a=1&b=2", "HASH_NO_SPACE": "abc#def",
	}
	got := values(entries)
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s = %q (present %v), want %q", k, g, ok, v)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d entries, want %d: %v", len(got), len(want), got)
	}
}

func TestParseNeverExpandsAndFlagsIt(t *testing.T) {
	entries, _ := Parse("A=$HOME\nB=${OUTRA}\nC=$(whoami)\nD=`id`\nE=sem-expansao\nF=preco$5\nG=\"$X\"")
	flagged := map[string]bool{}
	for _, e := range entries {
		flagged[e.Name] = e.NotExpanded
	}
	for _, name := range []string{"A", "B", "C", "D", "G"} {
		if !flagged[name] {
			t.Errorf("%s must be flagged as not expanded", name)
		}
	}
	if flagged["E"] || flagged["F"] {
		t.Errorf("plain values must not be flagged: %v", flagged)
	}
	v := values(entries)
	if v["A"] != "$HOME" || v["B"] != "${OUTRA}" || v["C"] != "$(whoami)" || v["D"] != "`id`" {
		t.Errorf("values must stay literal: %v", v)
	}
}

func TestParseLastAssignmentWinsAndRecordsTheDuplicates(t *testing.T) {
	entries, _ := Parse("A=1\nB=2\nA=3\nA=4")
	if len(entries) != 2 || entries[0].Name != "A" || entries[1].Name != "B" {
		t.Fatalf("entries = %+v, want A then B in first-seen order", entries)
	}
	a := entries[0]
	if a.Value != "4" || a.Line != 4 || len(a.Duplicates) != 2 || a.Duplicates[0] != 1 || a.Duplicates[1] != 3 {
		t.Errorf("A = %+v", a)
	}
}

func TestParseDiagnosticsNeverEchoTheLine(t *testing.T) {
	secret := "senha-que-nao-pode-vazar"
	entries, diags := Parse(strings.Join([]string{
		"isso nao e uma atribuicao " + secret,
		"1INVALIDO=" + secret,
		"COM ESPACO=" + secret,
		"=" + secret,
		`TRAILING="a" ` + secret,
		"OK=1",
	}, "\n"))
	if len(entries) != 1 || entries[0].Name != "OK" {
		t.Errorf("entries = %+v, want only OK", entries)
	}
	codes := map[string]bool{}
	for _, d := range diags {
		codes[d.Code] = true
		if strings.Contains(d.Message, secret) || strings.Contains(d.Name, secret) {
			t.Errorf("a diagnostic leaked line content: %+v", d)
		}
	}
	for _, c := range []string{CodeNotAssignment, CodeInvalidName, CodeEmptyName, CodeTrailingText} {
		if !codes[c] {
			t.Errorf("missing diagnostic %s in %+v", c, diags)
		}
	}
}

func TestParseSkipsMultilineValuesWithoutLeakingTheirLinesAsVariables(t *testing.T) {
	entries, diags := Parse("BEFORE=1\nKEY=\"-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkq=\nabc==\n-----END PRIVATE KEY-----\"\nAFTER=2")
	if got := values(entries); len(got) != 2 || got["BEFORE"] != "1" || got["AFTER"] != "2" {
		t.Errorf("entries = %v, want only BEFORE and AFTER (the key body must not become variables)", got)
	}
	if len(diags) != 1 || diags[0].Code != CodeMultiline || diags[0].Name != "KEY" || diags[0].Line != 2 {
		t.Errorf("diagnostics = %+v, want one multiline for KEY at line 2", diags)
	}
	// Aspa que nunca fecha: o resto do texto é consumido, sem virar variável.
	entries, diags = Parse("A=1\nB=\"nunca fecha\nC=3")
	if got := values(entries); len(got) != 1 || got["A"] != "1" {
		t.Errorf("entries = %v, want only A", got)
	}
	if len(diags) != 1 || diags[0].Code != CodeMultiline {
		t.Errorf("diagnostics = %+v", diags)
	}
}

func TestParseHandlesWindowsLineEndingsAndBOM(t *testing.T) {
	entries, diags := Parse("\ufeffA=1\r\nB=\"2\"\r\n")
	if len(diags) != 0 || values(entries)["A"] != "1" || values(entries)["B"] != "2" {
		t.Errorf("entries = %+v, diags = %+v", entries, diags)
	}
}

func TestValidName(t *testing.T) {
	for name, want := range map[string]bool{"A": true, "_X1": true, "lower_case": true, "1A": false, "A-B": false, "": false, strings.Repeat("A", 128): true, strings.Repeat("A", 129): false} {
		if ValidName(name) != want {
			t.Errorf("ValidName(%q) = %v, want %v", name, !want, want)
		}
	}
}
