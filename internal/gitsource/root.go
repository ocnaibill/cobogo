package gitsource

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RootError descreve por que o diretório do projeto não pôde ser usado.
type RootError struct {
	Dir    string
	Reason string
}

func (e *RootError) Error() string { return fmt.Sprintf("o diretório %q %s", e.Dir, e.Reason) }

// ResolveRoot devolve o caminho real do diretório do projeto dentro do
// workspace clonado, depois de resolver links simbólicos, e garante que ele
// continua dentro do workspace (DEC-27 item 12). Um repositório pode conter
// um link para fora (`apps/web -> /etc`); sem esta checagem o analisador leria
// e o build empacotaria diretórios do host. Links que apontam para outro
// diretório do próprio repositório são válidos.
//
// root deve vir normalizado por validate.RootDirectory ("" é a raiz).
func ResolveRoot(workspaceDir, root string) (string, error) {
	base, err := filepath.EvalSymlinks(workspaceDir)
	if err != nil {
		return "", fmt.Errorf("gitsource: resolver o workspace: %w", err)
	}
	if root == "" {
		return base, nil
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(base, filepath.FromSlash(root)))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", &RootError{Dir: root, Reason: "não existe neste commit"}
		}
		return "", &RootError{Dir: root, Reason: "não pôde ser resolvido: " + err.Error()}
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", &RootError{Dir: root, Reason: "aponta para fora do repositório"}
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", &RootError{Dir: root, Reason: "não é um diretório"}
	}
	return resolved, nil
}
