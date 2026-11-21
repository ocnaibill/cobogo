package validate

import (
	"slices"
	"strings"
	"testing"
)

func TestRootDirectoryNormalizesTheCommonSpellings(t *testing.T) {
	for in, want := range map[string]string{
		"":              "",
		".":             "",
		"./":            "",
		"/":             "",
		"  ":            "",
		"apps/web":      "apps/web",
		"./apps/web":    "apps/web",
		"apps/web/":     "apps/web",
		"/apps/web":     "apps/web",
		"//apps//web//": "apps/web",
		"apps/./web":    "apps/web",
		" apps/web ":    "apps/web",
		"packages/api":  "packages/api",
		"a b/c d":       "a b/c d",
		".github/app":   ".github/app",
		"..hidden/x":    "..hidden/x",
		"a/..b":         "a/..b",
		".gitlab/x":     ".gitlab/x",
	} {
		got, err := RootDirectory(in)
		if err != nil || got != want {
			t.Errorf("RootDirectory(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestRootDirectoryRefusesWhatCouldEscapeOrBreak(t *testing.T) {
	for _, in := range []string{
		"..", "../", "../x", "a/../b", "a/..", "a/../../b", "/../etc", "./../x", "../../..",
		`a\b`, `..\x`, "a\x00b", "a\nb", "a\tb", "a\x7fb",
		".git", ".git/config", "/.git/hooks", "./.git",
		strings.Repeat("a", 256), strings.Repeat("a/", 130),
	} {
		if got, err := RootDirectory(in); err == nil {
			t.Errorf("RootDirectory(%q) = %q, want an error", in, got)
		}
	}
}

func TestGitHostsDefaultsAndExtras(t *testing.T) {
	got := GitHosts([]string{"Git.Example.com", "github.com", "", " gitea.internal "})
	for _, want := range []string{"github.com", "gitlab.com", "codeberg.org", "bitbucket.org", "git.sr.ht", "git.example.com", "gitea.internal"} {
		if !slices.Contains(got, want) {
			t.Errorf("hosts %v missing %q", got, want)
		}
	}
	if len(got) != 7 {
		t.Errorf("hosts = %v: expected no duplicates or blanks", got)
	}
	if def := GitHosts(nil); len(def) != 5 {
		t.Errorf("default hosts = %v", def)
	}
}

func TestRepositoryURLAcceptsPublicHosts(t *testing.T) {
	hosts := GitHosts(nil)
	for in, want := range map[string]string{
		"https://github.com/heroku/node-js-getting-started":     "https://github.com/heroku/node-js-getting-started",
		"https://GitHub.com/heroku/node-js-getting-started":     "https://github.com/heroku/node-js-getting-started",
		"https://github.com/heroku/node-js-getting-started/":    "https://github.com/heroku/node-js-getting-started",
		"https://github.com/heroku/node-js-getting-started.git": "https://github.com/heroku/node-js-getting-started.git",
		"https://gitlab.com/group/subgroup/project":             "https://gitlab.com/group/subgroup/project",
		"https://codeberg.org/user/repo":                        "https://codeberg.org/user/repo",
		"https://bitbucket.org/team/repo":                       "https://bitbucket.org/team/repo",
		"https://git.sr.ht/~user/repo":                          "https://git.sr.ht/~user/repo",
		"https://github.com:443/a/b":                            "https://github.com/a/b",
		"  https://github.com/a/b  ":                            "https://github.com/a/b",
	} {
		got, err := RepositoryURL(in, hosts)
		if err != nil || got != want {
			t.Errorf("RepositoryURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestRepositoryURLRefusesWhatCouldReachTheInternalNetwork(t *testing.T) {
	hosts := GitHosts(nil)
	for name, in := range map[string]string{
		"empty":              "",
		"not https":          "http://github.com/a/b",
		"ssh":                "ssh://git@github.com/a/b",
		"scp style":          "git@github.com:a/b.git",
		"file":               "file:///etc/passwd",
		"git protocol":       "git://github.com/a/b",
		"credentials":        "https://user:pass@github.com/a/b",
		"user only":          "https://token@github.com/a/b",
		"other port":         "https://github.com:8443/a/b",
		"query":              "https://github.com/a/b?x=1",
		"empty query":        "https://github.com/a/b?",
		"fragment":           "https://github.com/a/b#x",
		"unknown host":       "https://example.com/a/b",
		"internal host":      "https://git.internal/a/b",
		"localhost":          "https://localhost/a/b",
		"loopback":           "https://127.0.0.1/a/b",
		"ipv6 loopback":      "https://[::1]/a/b",
		"private ip":         "https://192.168.1.10/a/b",
		"metadata ip":        "https://169.254.169.254/a/b",
		"lookalike suffix":   "https://github.com.evil.example/a/b",
		"lookalike prefix":   "https://evilgithub.com/a/b",
		"subdomain":          "https://api.github.com/a/b",
		"userinfo trick":     "https://github.com@evil.example/a/b",
		"no path":            "https://github.com",
		"only owner":         "https://github.com/heroku",
		"traversal":          "https://github.com/a/../b",
		"dot segment":        "https://github.com/a/./b",
		"empty segment":      "https://github.com/a//b",
		"backslash":          `https://github.com/a\b`,
		"too long":           "https://github.com/a/" + strings.Repeat("b", 2100),
		"control char":       "https://github.com/a/b\x00",
		"encoded traversal":  "https://github.com/a/%2e%2e/b",
		"whitespace in path": "https://github.com/a/b c",
	} {
		if got, err := RepositoryURL(in, hosts); err == nil {
			t.Errorf("%s: RepositoryURL(%q) = %q, want an error", name, in, got)
		}
	}
}

func TestRepositoryURLHonorsExtraHosts(t *testing.T) {
	url := "https://git.example.com/team/app"
	if _, err := RepositoryURL(url, GitHosts(nil)); err == nil {
		t.Error("a host outside the defaults must be refused")
	}
	if got, err := RepositoryURL(url, GitHosts([]string{"git.example.com"})); err != nil || got != url {
		t.Errorf("extra host = %q, %v", got, err)
	}
}

func TestPersistentPaths(t *testing.T) {
	got, err := PersistentPaths([]string{"/app/data/", " /app/content", "/app/data", "/srv/uploads"})
	if err != nil || strings.Join(got, ",") != "/app/content,/app/data,/srv/uploads" {
		t.Fatalf("got %v, %v; want sorted, trimmed and deduplicated", got, err)
	}
	if got, err := PersistentPaths(nil); err != nil || len(got) != 0 {
		t.Errorf("empty list = %v, %v", got, err)
	}
	for name, in := range map[string][]string{
		"relative":           {"app/data"},
		"root":               {"/"},
		"the whole app":      {"/app"},
		"empty":              {""},
		"parent traversal":   {"/app/../etc"},
		"repeated slash":     {"/app//data"},
		"dot segment":        {"/app/./data"},
		"space":              {"/app/my data"},
		"shell character":    {"/app/$(x)"},
		"system path":        {"/etc/app"},
		"system root of dir": {"/usr"},
		"proc":               {"/proc/1"},
		"nested":             {"/app", "/app/data"},
		"too many":           {"/a", "/b", "/c", "/d", "/e", "/f", "/g", "/h", "/i"},
		"too long":           {"/" + strings.Repeat("a", 300)},
	} {
		if got, err := PersistentPaths(in); err == nil {
			t.Errorf("%s: %v accepted as %v", name, in, got)
		}
	}
	// Um nome parecido com um prefixo do sistema não é do sistema.
	if _, err := PersistentPaths([]string{"/etcetera/data", "/usrdata"}); err != nil {
		t.Errorf("lookalike names must be fine: %v", err)
	}
}
