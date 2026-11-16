package dockerrt

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func tarNames(t *testing.T, root string) []string {
	t.Helper()
	r, err := buildContextTar(root, "FROM scratch\n")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, h.Name)
	}
	slices.Sort(names)
	return names
}

func write(t *testing.T, root, name string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Valores do repositório nunca chegam à imagem (DEC-24 item 7): o app não
// pode carregá-los sozinho, por exemplo com dotenv.
func TestBuildContextExcludesEnvFilesAndGit(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{
		"package.json", "src/index.js", ".env.example",
		".env", ".env.local", ".env.production", ".env.sample", "api/.env", "api/.env.staging",
		".git/config", ".git/objects/ab",
	} {
		write(t, root, f)
	}
	got := tarNames(t, root)
	want := []string{".env.example", "Dockerfile.cobogo", "package.json", "src/index.js"}
	if !slices.Equal(got, want) {
		t.Errorf("build context = %v, want %v", got, want)
	}
}

func TestBuildContextKeepsLookalikeNames(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"environment.js", "src/env.js", ".envrc", "docs/.environment"} {
		write(t, root, f)
	}
	got := tarNames(t, root)
	want := []string{".envrc", "Dockerfile.cobogo", "docs/.environment", "environment.js", "src/env.js"}
	if !slices.Equal(got, want) {
		t.Errorf("build context = %v, want %v", got, want)
	}
}

// Um link simbólico no repositório nunca pode levar o conteúdo do alvo para a
// imagem. Hoje o contexto falha ("write too long") em vez de ignorar o link;
// tratar links de forma explícita é trabalho do adaptador Docker reforçado.
func TestBuildContextNeverFollowsSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	write(t, outside, "secret.txt")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	r, err := buildContextTar(root, "FROM scratch\n")
	if err != nil {
		return // falha segura
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err != nil {
			return
		}
		if h.Name == "link" && h.Typeflag == tar.TypeReg {
			t.Error("a symlink was archived as a regular file with the target's content")
		}
	}
}
