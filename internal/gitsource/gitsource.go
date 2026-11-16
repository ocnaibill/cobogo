// Package gitsource obtém a fonte de um repositório permitido em um
// workspace temporário e fixa o SHA analisado (RF-01, RF-02, RNF-04).
package gitsource

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
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
	return fetch(ctx, repositoryURL, opts)
}

// fetch não valida o esquema da URL; só Fetch, a porta de entrada pública,
// aplica a restrição de RNF-04. Os testes usam repositórios locais.
func fetch(ctx context.Context, repositoryURL string, opts Options) (Workspace, error) {
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

	if shaPattern.MatchString(opts.Ref) {
		// RF-08: responder uma pendência reutiliza o commit já fixado (RN-04),
		// mesmo que a branch tenha avançado. `clone --branch` não aceita SHA.
		err = fetchCommit(cctx, repositoryURL, opts.Ref, dir)
	} else {
		args := []string{"clone", "--depth", "1", "--no-tags", "--single-branch"}
		if opts.Ref != "" {
			args = append(args, "--branch", opts.Ref)
		}
		args = append(args, repositoryURL, dir)
		err = runGit(cctx, "", args...)
	}
	if err != nil {
		os.RemoveAll(dir)
		return Workspace{}, err
	}

	sha, err := resolveHead(cctx, dir)
	if err != nil {
		os.RemoveAll(dir)
		return Workspace{}, err
	}

	return Workspace{Dir: dir, CommitSHA: sha}, nil
}

// shaPattern reconhece um SHA completo; abreviações são ambíguas e tratadas
// como nome de branch ou tag.
var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

func fetchCommit(ctx context.Context, repositoryURL, sha, dir string) error {
	steps := [][]string{
		{"init", "--quiet"},
		{"remote", "add", "origin", repositoryURL},
		{"fetch", "--depth", "1", "--no-tags", "origin", sha},
		{"checkout", "--quiet", "--detach", "FETCH_HEAD"},
	}
	for _, args := range steps {
		if err := runGit(ctx, dir, args...); err != nil {
			return err
		}
	}
	return nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		if unavailable := classifyGitFailure(string(out)); unavailable != nil {
			return unavailable
		}
		return fmt.Errorf("gitsource: git %s falhou: %w: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ErrRepositoryUnavailable indica repositório inexistente ou privado. O
// Cobogó só clona repositórios públicos e nunca pede credenciais (DEC-27).
var ErrRepositoryUnavailable = errors.New("repositório inexistente ou privado: o Cobogó só clona repositórios públicos")

// classifyGitFailure reconhece a saída do git quando o repositório não é
// acessível sem credenciais (a clonagem roda sem terminal, então pedir
// usuário e senha falha).
func classifyGitFailure(output string) error {
	lower := strings.ToLower(output)
	for _, marker := range []string{
		"terminal prompts disabled", "could not read username", "authentication failed",
		"repository not found", "does not appear to be a git repository", "invalid username or password",
	} {
		if strings.Contains(lower, marker) {
			return ErrRepositoryUnavailable
		}
	}
	return nil
}

func resolveHead(ctx context.Context, dir string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gitsource: resolver SHA: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
