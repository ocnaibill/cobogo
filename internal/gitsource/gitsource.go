// Package gitsource obtém a fonte de um repositório permitido em um
// workspace temporário e fixa o SHA analisado (RF-01, RF-02, RNF-04).
package gitsource

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Workspace é o diretório temporário de um checkout já resolvido para um SHA.
type Workspace struct {
	Dir       string
	CommitSHA string
}

// Cleanup remove o workspace temporário. Chamar sempre após o uso, mesmo em
// falha, para não acumular clones.
func (w Workspace) Cleanup() error {
	if w.Dir == "" {
		return nil
	}
	return os.RemoveAll(w.Dir)
}

// Options controla os limites de aquisição (RNF-04).
type Options struct {
	// Ref é a branch, tag ou SHA solicitado. Vazio usa o HEAD padrão remoto.
	Ref     string
	Timeout time.Duration
}

const defaultTimeout = 60 * time.Second

// Fetch clona superficialmente o repositório, somente HTTPS, com timeout, e
// resolve o SHA efetivamente obtido. Não executa submódulos nem LFS.
func Fetch(ctx context.Context, repositoryURL string, opts Options) (Workspace, error) {
	if !strings.HasPrefix(repositoryURL, "https://") {
		return Workspace{}, fmt.Errorf("gitsource: apenas URLs https:// são permitidas, recebido %q", repositoryURL)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dir, err := os.MkdirTemp("", "cobogo-src-*")
	if err != nil {
		return Workspace{}, fmt.Errorf("gitsource: criar workspace: %w", err)
	}

	args := []string{"clone", "--depth", "1", "--no-tags", "--single-branch"}
	if opts.Ref != "" {
		args = append(args, "--branch", opts.Ref)
	}
	args = append(args, repositoryURL, dir)

	cmd := exec.CommandContext(cctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(dir)
		return Workspace{}, fmt.Errorf("gitsource: clone falhou: %w: %s", err, strings.TrimSpace(string(out)))
	}

	sha, err := resolveHead(cctx, dir)
	if err != nil {
		os.RemoveAll(dir)
		return Workspace{}, err
	}

	return Workspace{Dir: dir, CommitSHA: sha}, nil
}

func resolveHead(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gitsource: resolver SHA: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
