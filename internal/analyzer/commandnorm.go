package analyzer

import (
	"path"
	"regexp"
	"strings"
)

// Normalização de comandos (DEC-53 A): um script como `bunx --bun vite build`,
// `npx -y vite build`, `cross-env NODE_ENV=production vite build`, `dotenv -e .env --
// next start` ou `./node_modules/.bin/vite build` roda a mesma ferramenta que
// `vite build`. As heurísticas que reconhecem a ferramenta olham para a ferramenta,
// não para o que a embrulha. A lista é curta e por convenção documentada de cada
// executor; o que não é reconhecido fica como está.

var envAssignmentToken = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// runnerValueFlags são as opções de um executor que levam um valor separado.
var runnerValueFlags = map[string]bool{"-p": true, "--package": true, "-c": true, "--call": true}

// envLoaderValueFlags são as opções dos carregadores de arquivo de ambiente que levam um valor.
var envLoaderValueFlags = map[string]bool{
	"-e": true, "-f": true, "--file": true, "--env-file": true, "-p": true, "-v": true, "--variable": true,
	"-c": true, "--cascade": true, "-r": true, "--rc-file": true,
}

// commandTokens divide um segmento de comando (sem && nem ;) e tira o que só
// embrulha a ferramenta: atribuições de ambiente, executores (npx, bunx, npm exec,
// pnpm exec, bun x), carregadores de .env (dotenv, env-cmd, dotenvx) e cross-env. O
// primeiro token que sobra é a ferramenta; um caminho `node_modules/.bin/x` vira `x`.
func commandTokens(segment string) []string {
	tokens := strings.Fields(segment)
	for len(tokens) > 0 {
		t := tokens[0]
		switch {
		case envAssignmentToken.MatchString(t):
			tokens = tokens[1:]
		case t == "env" || t == "cross-env" || t == "cross-env-shell":
			tokens = tokens[1:]
		case t == "npx" || t == "bunx" || t == "pnpx":
			tokens = skipFlags(tokens[1:], runnerValueFlags)
		case (t == "npm" || t == "pnpm" || t == "yarn") && len(tokens) > 1 && tokens[1] == "exec":
			tokens = skipFlags(tokens[2:], runnerValueFlags)
		case t == "bun" && len(tokens) > 1 && tokens[1] == "x":
			tokens = skipFlags(tokens[2:], runnerValueFlags)
		case t == "dotenv" || t == "env-cmd":
			tokens = skipFlags(tokens[1:], envLoaderValueFlags)
		case t == "dotenvx" && len(tokens) > 1 && tokens[1] == "run":
			tokens = skipFlags(tokens[2:], envLoaderValueFlags)
		default:
			return normalizeToolPath(tokens)
		}
	}
	return tokens
}

// skipFlags descarta as opções no início de tokens, com o valor das que levam um,
// e o `--` que as separa do comando.
func skipFlags(tokens []string, valued map[string]bool) []string {
	for len(tokens) > 0 && strings.HasPrefix(tokens[0], "-") {
		flag := tokens[0]
		tokens = tokens[1:]
		if flag == "--" {
			break
		}
		if valued[flag] && !strings.Contains(flag, "=") && len(tokens) > 0 {
			tokens = tokens[1:]
		}
	}
	return tokens
}

// normalizeToolPath troca `./node_modules/.bin/vite` por `vite` e
// `node ./node_modules/webpack-dev-server/bin/x.js` por `webpack-dev-server`.
func normalizeToolPath(tokens []string) []string {
	if len(tokens) == 0 {
		return tokens
	}
	if i := strings.Index(tokens[0], "node_modules/.bin/"); i >= 0 {
		return append([]string{path.Base(tokens[0])}, tokens[1:]...)
	}
	if tokens[0] == "node" && len(tokens) > 1 {
		if pkg := packageOfNodeModulesPath(tokens[1]); pkg != "" {
			return append([]string{pkg}, tokens[2:]...)
		}
	}
	return tokens
}

// packageOfNodeModulesPath devolve o pacote de um caminho `.../node_modules/<pacote>/...`
// (com escopo, `@x/y`), ou "" se o caminho não passa por node_modules.
func packageOfNodeModulesPath(p string) string {
	const marker = "node_modules/"
	i := strings.LastIndex(p, marker)
	if i < 0 {
		return ""
	}
	rest := strings.Split(p[i+len(marker):], "/")
	switch {
	case len(rest) > 0 && strings.HasPrefix(rest[0], "@") && len(rest) > 1:
		return rest[0] + "/" + rest[1]
	case len(rest) > 0 && rest[0] != ".bin":
		return rest[0]
	}
	return ""
}

// commandSegments separa um comando de shell nos trechos que ele encadeia.
func commandSegments(command string) []string {
	seps := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n", "|", "\n")
	return strings.Split(seps.Replace(command), "\n")
}
