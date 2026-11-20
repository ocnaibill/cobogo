package domain

import "testing"

func TestGitHookOnly(t *testing.T) {
	for cmd, want := range map[string]bool{
		"husky":                    true,
		"husky install":            true,
		"is-ci || husky":           true,
		"npx simple-git-hooks":     true,
		"lefthook install || true": true,
		"husky && npm run build":   false,
		"npm run build":            false,
		"":                         false,
	} {
		if got := GitHookOnly(cmd); got != want {
			t.Errorf("GitHookOnly(%q) = %v, want %v", cmd, got, want)
		}
	}
}
