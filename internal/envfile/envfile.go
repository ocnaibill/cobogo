// Package envfile lê o texto de um arquivo .env colado pelo usuário (DEC-39).
//
// A leitura é deliberadamente literal: nada é executado nem expandido. `$VAR`,
// `${VAR}`, `$(cmd)` e crases ficam como texto, com um aviso. O que não é uma
// atribuição simples vira diagnóstico e nunca valor, e nenhum diagnóstico
// repete o conteúdo de uma linha (ela pode ser um segredo).
package envfile

import (
	"regexp"
	"strings"
)

// Entry é uma variável lida: a última atribuição de cada nome vale.
type Entry struct {
	Name  string
	Value string
	// Line é a linha da atribuição que valeu (começa em 1).
	Line int
	// Duplicates são as linhas anteriores do mesmo nome, descartadas.
	Duplicates []int
	// NotExpanded diz que o valor contém `$`, `${`, `$(` ou crase: fica literal.
	NotExpanded bool
}

// Diagnostic é um problema de leitura. Message nunca traz o conteúdo da linha.
type Diagnostic struct {
	Line    int
	Name    string
	Code    string
	Message string
}

// Códigos de diagnóstico.
const (
	CodeNotAssignment = "not_assignment"
	CodeInvalidName   = "invalid_name"
	CodeMultiline     = "multiline_value"
	CodeTrailingText  = "unsupported_syntax"
	CodeEmptyName     = "empty_name"
)

var namePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// ValidName diz se é um nome de variável aceito.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// Parse lê o texto e devolve as variáveis (na ordem da última atribuição de
// cada nome) e os diagnósticos.
func Parse(text string) ([]Entry, []Diagnostic) {
	text = strings.TrimPrefix(text, "\ufeff")
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	byName := map[string]*Entry{}
	var order []string
	var diags []Diagnostic
	skipUntilQuote := byte(0) // dentro de um valor de várias linhas, até a aspa de fechamento

	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		line := lines[i]

		if skipUntilQuote != 0 {
			if closes(line, skipUntilQuote) {
				skipUntilQuote = 0
			}
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "export ")
		trimmed = strings.TrimLeft(trimmed, " \t")

		eq := strings.IndexByte(trimmed, '=')
		if eq < 0 {
			diags = append(diags, Diagnostic{Line: lineNo, Code: CodeNotAssignment, Message: "a linha não é uma atribuição NOME=valor e foi ignorada"})
			continue
		}
		name := strings.TrimSpace(trimmed[:eq])
		if name == "" {
			diags = append(diags, Diagnostic{Line: lineNo, Code: CodeEmptyName, Message: "atribuição sem nome foi ignorada"})
			continue
		}
		if !ValidName(name) {
			diags = append(diags, Diagnostic{Line: lineNo, Code: CodeInvalidName, Message: "nome inválido: use letras, números e _, sem começar por número (até 128 caracteres)"})
			continue
		}

		value, rest, state := readValue(strings.TrimLeft(trimmed[eq+1:], " \t"))
		switch state {
		case stateUnterminated:
			diags = append(diags, Diagnostic{Line: lineNo, Name: name, Code: CodeMultiline, Message: "valor de várias linhas não é suportado; a variável foi ignorada (cole-a pelo campo de valor)"})
			skipUntilQuote = rest[0]
			continue
		case stateTrailing:
			diags = append(diags, Diagnostic{Line: lineNo, Name: name, Code: CodeTrailingText, Message: "há texto depois da aspa de fechamento; a variável foi ignorada"})
			continue
		}

		e, seen := byName[name]
		if !seen {
			e = &Entry{Name: name}
			byName[name] = e
			order = append(order, name)
		} else {
			e.Duplicates = append(e.Duplicates, e.Line)
		}
		e.Value, e.Line, e.NotExpanded = value, lineNo, hasExpansion(value)
	}

	entries := make([]Entry, 0, len(order))
	for _, n := range order {
		entries = append(entries, *byName[n])
	}
	return entries, diags
}

type valueState int

const (
	stateOK valueState = iota
	stateUnterminated
	stateTrailing
)

// readValue lê o valor que começa em s. Em estado não terminado, rest[0] é a
// aspa que ficou aberta.
func readValue(s string) (value string, rest string, state valueState) {
	if s == "" {
		return "", "", stateOK
	}
	switch s[0] {
	case '"':
		var b strings.Builder
		for i := 1; i < len(s); i++ {
			switch c := s[i]; {
			case c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\' || s[i+1] == 'n'):
				if s[i+1] == 'n' {
					b.WriteByte('\n')
				} else {
					b.WriteByte(s[i+1])
				}
				i++
			case c == '"':
				if after := strings.TrimSpace(s[i+1:]); after != "" && !strings.HasPrefix(after, "#") {
					return "", "", stateTrailing
				}
				return b.String(), "", stateOK
			default:
				b.WriteByte(c)
			}
		}
		return "", `"`, stateUnterminated
	case '\'':
		end := strings.IndexByte(s[1:], '\'')
		if end < 0 {
			return "", "'", stateUnterminated
		}
		if after := strings.TrimSpace(s[end+2:]); after != "" && !strings.HasPrefix(after, "#") {
			return "", "", stateTrailing
		}
		return s[1 : end+1], "", stateOK
	}
	// Sem aspas: até um comentário (espaço e #), sem espaços nas pontas.
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, " \t"), "", stateOK
}

// closes diz se a linha contém a aspa de fechamento (não escapada, no caso da dupla).
func closes(line string, quote byte) bool {
	for i := 0; i < len(line); i++ {
		if quote == '"' && line[i] == '\\' {
			i++
			continue
		}
		if line[i] == quote {
			return true
		}
	}
	return false
}

var expansion = regexp.MustCompile("\\$[({A-Za-z_]|`")

func hasExpansion(v string) bool { return expansion.MatchString(v) }
