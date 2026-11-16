package gitsource

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// repoWithTwoCommits devolve um repositório local e os SHAs do primeiro e do
// segundo commit; o conteúdo de version.txt muda entre eles.
func repoWithTwoCommits(t *testing.T) (dir, first, second string) {
	t.Helper()
	dir = t.TempDir()
	git(t, dir, "init", "--quiet", "--initial-branch=main")
	commit := func(content string) string {
		if err := os.WriteFile(filepath.Join(dir, "version.txt"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		git(t, dir, "add", ".")
		git(t, dir, "commit", "--quiet", "-m", content)
		return git(t, dir, "rev-parse", "HEAD")
	}
	first = commit("um")
	second = commit("dois")
	return dir, first, second
}

func TestFetchHeadByDefault(t *testing.T) {
	repo, _, second := repoWithTwoCommits(t)
	ws, err := fetch(context.Background(), "file://"+repo, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	if ws.CommitSHA != second {
		t.Errorf("SHA = %s, want HEAD %s", ws.CommitSHA, second)
	}
}

// Responder uma pendência (RF-08) precisa analisar o mesmo commit mesmo que a
// branch já tenha avançado (RN-04).
func TestFetchPinnedSHAIgnoresNewerCommits(t *testing.T) {
	repo, first, _ := repoWithTwoCommits(t)
	ws, err := fetch(context.Background(), "file://"+repo, Options{Ref: first})
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Cleanup()
	if ws.CommitSHA != first {
		t.Errorf("SHA = %s, want %s", ws.CommitSHA, first)
	}
	content, err := os.ReadFile(filepath.Join(ws.Dir, "version.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "um" {
		t.Errorf("version.txt = %q, want the content of the pinned commit", content)
	}
}

func TestFetchUnknownSHAFailsAndCleansUp(t *testing.T) {
	repo, _, _ := repoWithTwoCommits(t)
	before, _ := filepath.Glob(filepath.Join(os.TempDir(), "cobogo-src-*"))
	_, err := fetch(context.Background(), "file://"+repo, Options{Ref: strings.Repeat("a", 40)})
	if err == nil {
		t.Fatal("expected an error for a commit that does not exist")
	}
	after, _ := filepath.Glob(filepath.Join(os.TempDir(), "cobogo-src-*"))
	if len(after) != len(before) {
		t.Errorf("workspace leaked after failure: %d before, %d after", len(before), len(after))
	}
}

func TestFetchRejectsNonHTTPS(t *testing.T) {
	for _, url := range []string{"http://example.com/r.git", "git@github.com:o/r.git", "file:///tmp/r", "/tmp/r"} {
		if _, err := Fetch(context.Background(), url, Options{}); err == nil {
			t.Errorf("Fetch(%q) should be rejected (RNF-04)", url)
		}
	}
}
