package httpapi_test

import (
	"bufio"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/httpapi"
)

const openAPIFile = "../../docs/api/openapi.yaml"

var (
	pathLine      = regexp.MustCompile(`^  (/\S*):\s*$`)
	methodLine    = regexp.MustCompile(`^    (get|post|put|delete|patch|head|options):\s*$`)
	operationLine = regexp.MustCompile(`^      operationId:\s*(\S+)\s*$`)
)

// openAPIOperations lê "MÉTODO /caminho" e o operationId de cada operação do
// arquivo. Não é um leitor de YAML: depende da disposição do arquivo (caminhos
// com 2 espaços de recuo, métodos com 4), que o próprio teste confere ao exigir
// que toda operação encontrada tenha um operationId.
func openAPIOperations(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open(openAPIFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	ops := map[string]string{}
	inPaths, path, current := false, "", ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") { // chave de primeiro nível
			inPaths = line == "paths:"
			continue
		}
		if !inPaths {
			continue
		}
		if m := pathLine.FindStringSubmatch(line); m != nil {
			path, current = m[1], ""
		} else if m := methodLine.FindStringSubmatch(line); m != nil {
			current = strings.ToUpper(m[1]) + " " + path
			ops[current] = ""
		} else if m := operationLine.FindStringSubmatch(line); m != nil && current != "" {
			ops[current] = m[1]
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	return ops
}

// O contrato e o roteador descrevem as mesmas rotas (DEC-28 item 9).
func TestOpenAPIMatchesRoutes(t *testing.T) {
	documented := openAPIOperations(t)
	if len(documented) == 0 {
		t.Fatal("nenhuma operação lida de " + openAPIFile + ": a leitura depende da disposição do arquivo")
	}

	registered := map[string]bool{}
	for _, pattern := range httpapi.RoutePatterns() {
		method, path, _ := strings.Cut(pattern, " ")
		registered[method+" "+strings.TrimPrefix(path, "/api/v1")] = true
	}

	var missing, extra []string
	for r := range registered {
		if _, ok := documented[r]; !ok {
			missing = append(missing, r)
		}
	}
	for d := range documented {
		if !registered[d] {
			extra = append(extra, d)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("rotas registradas e ausentes de docs/api/openapi.yaml: %v", missing)
	}
	if len(extra) > 0 {
		t.Errorf("rotas de docs/api/openapi.yaml que o roteador não registra: %v", extra)
	}

	seen := map[string]string{}
	for op, id := range documented {
		if id == "" {
			t.Errorf("%s sem operationId", op)
		} else if other, dup := seen[id]; dup {
			t.Errorf("operationId %q repetido em %s e %s", id, op, other)
		}
		seen[id] = op
	}
}
