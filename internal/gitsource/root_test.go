package gitsource

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestResolveRootAcceptsDirectoriesInsideTheWorkspace(t *testing.T) {
	ws := t.TempDir()
	mkdir(t, filepath.Join(ws, "apps", "web"))
	mkdir(t, filepath.Join(ws, "shared"))
	// Um link para outro diretório do próprio repositório é válido.
	if err := os.Symlink(filepath.Join(ws, "shared"), filepath.Join(ws, "apps", "link-to-shared")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../shared", filepath.Join(ws, "apps", "relative-link")); err != nil {
		t.Fatal(err)
	}
	base, _ := filepath.EvalSymlinks(ws)

	for root, want := range map[string]string{
		"":                    base,
		"apps/web":            filepath.Join(base, "apps", "web"),
		"apps/link-to-shared": filepath.Join(base, "shared"),
		"apps/relative-link":  filepath.Join(base, "shared"),
	} {
		got, err := ResolveRoot(ws, root)
		if err != nil || got != want {
			t.Errorf("ResolveRoot(%q) = %q, %v; want %q", root, got, err, want)
		}
	}
}

// Um repositório hostil não pode apontar o analisador nem o build para o host.
func TestResolveRootRefusesLinksThatLeaveTheWorkspace(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	mkdir(t, filepath.Join(outside, "secrets"))
	mkdir(t, filepath.Join(ws, "apps"))

	links := map[string]string{
		"apps/absolute":   outside,
		"apps/up":         "../..",
		"apps/deep-up":    "../../../../../../../../etc",
		"escape":          filepath.Join(outside, "secrets"),
		"apps/via-parent": "../../" + filepath.Base(outside),
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(ws, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Um diretório dentro do repositório que contém um link para fora.
	mkdir(t, filepath.Join(ws, "pkg"))
	if err := os.Symlink(outside, filepath.Join(ws, "pkg", "out")); err != nil {
		t.Fatal(err)
	}

	for _, root := range []string{"apps/absolute", "apps/up", "apps/deep-up", "escape", "apps/via-parent", "pkg/out", "pkg/out/secrets"} {
		got, err := ResolveRoot(ws, root)
		var re *RootError
		if !errors.As(err, &re) {
			t.Errorf("ResolveRoot(%q) = %q, %v; want a RootError", root, got, err)
			continue
		}
		if re.Reason != "aponta para fora do repositório" && re.Reason != "não existe neste commit" {
			t.Errorf("ResolveRoot(%q): reason %q", root, re.Reason)
		}
	}
}

func TestResolveRootReportsAMissingDirectoryClearly(t *testing.T) {
	ws := t.TempDir()
	_, err := ResolveRoot(ws, "apps/web")
	var re *RootError
	if !errors.As(err, &re) || re.Reason != "não existe neste commit" {
		t.Fatalf("err = %v", err)
	}
	if err.Error() != `o diretório "apps/web" não existe neste commit` {
		t.Errorf("message = %q", err)
	}
}

func TestResolveRootRefusesAFile(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveRoot(ws, "package.json"); err == nil {
		t.Error("a file is not a project directory")
	}
}

func TestClassifyGitFailure(t *testing.T) {
	for _, out := range []string{
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled",
		"remote: Repository not found.\nfatal: repository 'https://github.com/a/b/' not found",
		"fatal: Authentication failed for 'https://gitlab.com/a/b'",
		"fatal: 'x' does not appear to be a git repository",
	} {
		if !errors.Is(classifyGitFailure(out), ErrRepositoryUnavailable) {
			t.Errorf("%q must be classified as unavailable", out)
		}
	}
	for _, out := range []string{"fatal: unable to access: Could not resolve host: github.com", "fatal: early EOF", ""} {
		if classifyGitFailure(out) != nil {
			t.Errorf("%q is not an access problem", out)
		}
	}
}
