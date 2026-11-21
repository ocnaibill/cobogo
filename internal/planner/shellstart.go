package planner

import (
	"regexp"
	"strings"
)

// O comando de início roda dentro de `sh -c` porque pode ser uma linha de shell
// (`npm run migrate && node server.js`). Mas um `sh` como processo 1 não repassa
// o sinal de término ao filho: o Docker espera 10 s e mata a aplicação à força,
// o que atrasa parar, reiniciar e trocar de release (DEC-40, DEC-41). Quando é
// seguro, o último comando passa a rodar com `exec`, que troca o `sh` pela
// aplicação, e ela, como processo 1, recebe o sinal.

// execBlocked são os começos que não aceitam `exec` na frente: palavras
// reservadas e comandos internos do shell.
var execBlocked = map[string]bool{
	"exec": true, "cd": true, "export": true, "set": true, "unset": true, "eval": true, "source": true, ".": true,
	"if": true, "for": true, "while": true, "until": true, "case": true, "time": true, "!": true, "[": true,
	"{": true, "(": true, "trap": true, "wait": true, "exit": true, "return": true, "umask": true, "ulimit": true,
}

// shellMeta são os caracteres que tornam o trecho algo além de "um comando":
// pipe, segundo comando, redirecionamento, substituição, aspas e barra invertida.
// Aspas e barra ficam de fora de propósito para argumentos comuns, mas qualquer
// outro metacaractere deixa o comando como está.
var shellMeta = regexp.MustCompile("[|&;<>()$`\n\\\\]")

// assignmentPrefix é uma atribuição de variável na frente do comando, sem aspas
// nem espaços no valor (`NODE_ENV=production `).
var assignmentPrefix = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=[^\s"']*\s+`)

// ShellStart devolve o texto que vai para `sh -c`: o próprio comando, com `exec`
// no último comando simples quando isso é seguro. Em dúvida, devolve o comando
// sem mudança (o comportamento anterior, só mais lento para parar).
func ShellStart(cmd string) string {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || strings.Count(cmd, `"`)%2 != 0 || strings.Count(cmd, "'")%2 != 0 {
		return cmd
	}
	// `a && b && c`: só o último recebe `exec`, e os anteriores têm de ser simples.
	segments := strings.Split(cmd, "&&")
	for i, seg := range segments {
		seg = strings.TrimSpace(seg)
		if seg == "" || shellMeta.MatchString(seg) {
			return cmd
		}
		segments[i] = seg
	}
	last := segments[len(segments)-1]
	// Atribuições na frente (`NODE_ENV=production node app.js`) ficam antes do exec.
	prefix := ""
	for {
		m := assignmentPrefix.FindString(last)
		if m == "" {
			break
		}
		prefix += m
		last = last[len(m):]
	}
	first, _, _ := strings.Cut(last, " ")
	if last == "" || execBlocked[first] || strings.Contains(first, "=") {
		return cmd
	}
	segments[len(segments)-1] = prefix + "exec " + last
	return strings.Join(segments, " && ")
}
