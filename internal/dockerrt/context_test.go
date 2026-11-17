package dockerrt

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
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

func contextEntries(t *testing.T, root string) (entries map[string]*tar.Header, warnings []string) {
	t.Helper()
	r, err := buildContext(root, "FROM scratch\n", func(w string) { warnings = append(warnings, w) })
	if err != nil {
		t.Fatal(err)
	}
	entries = map[string]*tar.Header{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return entries, warnings
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeSymlink && h.Size != 0 {
			t.Errorf("link %s carries %d bytes of content", h.Name, h.Size)
		}
		entries[h.Name] = h
	}
}

// Um link nunca é lido (DEC-29 item 8): nem o conteúdo do alvo entra no
// contexto, nem o build falha com "write too long".
func TestBuildContextNeverFollowsSymlinks(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	write(t, outside, "secret.txt")
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	write(t, root, "package.json")

	entries, warnings := contextEntries(t, root)
	if _, has := entries["link"]; has {
		t.Error("a link to outside the repository must not be in the context at all")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], `"link"`) || !strings.Contains(warnings[0], "fora do repositório") {
		t.Errorf("warnings = %q, want one naming the link", warnings)
	}
	if _, has := entries["package.json"]; !has {
		t.Error("the rest of the repository must still be archived")
	}
}

func TestBuildContextKeepsLinksThatStayInside(t *testing.T) {
	root := t.TempDir()
	write(t, root, "docs/guia.md")
	write(t, root, "src/index.js")
	for link, target := range map[string]string{
		"LEIA-ME.md":   "docs/guia.md",    // mesmo nível, descendo
		"src/guia.md":  "../docs/guia.md", // subindo e descendo, sem sair
		"src/atual.js": "./index.js",      // com ./
		"src/ausente":  "nao-existe",      // link pendente é inofensivo
	} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}

	entries, warnings := contextEntries(t, root)
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
	for link, target := range map[string]string{
		"LEIA-ME.md": "docs/guia.md", "src/guia.md": "../docs/guia.md",
		"src/atual.js": "./index.js", "src/ausente": "nao-existe",
	} {
		h := entries[link]
		if h == nil || h.Typeflag != tar.TypeSymlink || h.Linkname != target {
			t.Errorf("%s = %+v, want a symlink to %s", link, h, target)
		}
	}
}

func TestBuildContextOmitsEveryLinkThatEscapes(t *testing.T) {
	root := t.TempDir()
	write(t, root, "src/index.js")
	escaping := map[string]string{
		"abs":          "/etc/passwd",
		"up":           "..",
		"up-file":      "../outro",
		"src/up2":      "../../outro", // dois níveis a partir de src/ saem da raiz
		"deep":         "src/../../outro",
		"src/sneaky":   "../src/../../x",
		"empty-target": "",
	}
	delete(escaping, "empty-target") // o SO não cria link com alvo vazio
	for link, target := range escaping {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}

	entries, warnings := contextEntries(t, root)
	for link := range escaping {
		if _, has := entries[link]; has {
			t.Errorf("%s escapes the repository and must not be archived", link)
		}
	}
	if len(warnings) != len(escaping) {
		t.Errorf("%d warnings for %d escaping links: %q", len(warnings), len(escaping), warnings)
	}
}

// Abrir uma FIFO travaria o worker (DEC-29 item 10).
func TestBuildContextSkipsSpecialFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json")
	if err := syscall.Mkfifo(filepath.Join(root, "fila"), 0o644); err != nil {
		t.Skipf("sem suporte a FIFO aqui: %v", err)
	}

	done := make(chan map[string]*tar.Header, 1)
	go func() {
		entries, _ := contextEntries(t, root)
		done <- entries
	}()
	select {
	case entries := <-done:
		if _, has := entries["fila"]; has {
			t.Error("a FIFO must not be archived")
		}
		if _, has := entries["package.json"]; !has {
			t.Error("regular files must still be archived")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("building the context hung on a FIFO")
	}
}

func TestLinkStaysInside(t *testing.T) {
	for _, c := range []struct {
		link, target string
		want         bool
	}{
		{"a", "b", true},
		{"a", "./b", true},
		{"d/a", "../b", true},
		{"d/a", "../../b", false},
		{"a", "../b", false},
		{"a", "..", false},
		{"d/a", "..", true},
		{"a", "/etc/passwd", false},
		{"a", "", false},
		{"a", "d/../../b", false},
		{"a", "d/../b", true},
	} {
		if got := linkStaysInside(c.link, c.target); got != c.want {
			t.Errorf("linkStaysInside(%q, %q) = %v, want %v", c.link, c.target, got, c.want)
		}
	}
}
