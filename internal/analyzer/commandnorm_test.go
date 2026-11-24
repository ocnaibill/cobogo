package analyzer

import (
	"slices"
	"testing"
)

// DEC-53 A: o executor e as opções dele não mudam a ferramenta.
func TestCommandTokensStripWrappers(t *testing.T) {
	cases := []struct {
		command string
		want    []string
	}{
		{"vite build", []string{"vite", "build"}},
		{"npx vite build", []string{"vite", "build"}},
		{"npx -y vite build", []string{"vite", "build"}},
		{"npx --yes --package=vite vite build", []string{"vite", "build"}},
		{"npx -p vite vite build", []string{"vite", "build"}},
		{"bunx --bun vite build", []string{"vite", "build"}},
		{"bun x --bun vite build", []string{"vite", "build"}},
		{"npm exec -- vite build", []string{"vite", "build"}},
		{"pnpm exec vite build", []string{"vite", "build"}},
		{"NODE_ENV=production vite build", []string{"vite", "build"}},
		{"cross-env NODE_ENV=production A=1 vite build", []string{"vite", "build"}},
		{"env NODE_ENV=production vite build", []string{"vite", "build"}},
		{"dotenv -e .env.production -- vite build", []string{"vite", "build"}},
		{"env-cmd -f .env.prod vite build", []string{"vite", "build"}},
		{"dotenvx run -f .env -- vite build", []string{"vite", "build"}},
		{"./node_modules/.bin/vite build", []string{"vite", "build"}},
		{"node ./node_modules/vite/bin/vite.js build", []string{"vite", "build"}},
		{"node ./node_modules/@vue/cli-service/bin/x.js build", []string{"@vue/cli-service", "build"}},
		{"node server.js", []string{"node", "server.js"}},
		{"node ./src/server.js --port 3000", []string{"node", "./src/server.js", "--port", "3000"}},
		{"", nil},
	}
	for _, c := range cases {
		got := commandTokens(c.command)
		if !slices.Equal(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
			t.Errorf("commandTokens(%q) = %v, want %v", c.command, got, c.want)
		}
	}
}

func TestStaticBuildThroughWrappers(t *testing.T) {
	for _, command := range []string{
		"bunx --bun vite build",
		"npx -y vite build --outDir out",
		"cross-env NODE_ENV=production vite build --outDir out",
		"tsc -b && bunx --bun vite build --outDir out",
		"./node_modules/.bin/vite build --outDir out",
	} {
		tool, _ := staticBuild(command)
		if tool != "vite" {
			t.Errorf("staticBuild(%q) = %q, want vite", command, tool)
		}
	}
	if tool, flag := staticBuild("cross-env X=1 vite build --outDir out"); tool != "vite" || flag != "out" {
		t.Errorf("flag lost through cross-env: %q %q", tool, flag)
	}
	if tool, _ := staticBuild("bunx --bun some-other-tool build"); tool != "" {
		t.Errorf("an unknown tool must not be taken for a static build: %q", tool)
	}
}

func TestDevServerToolThroughWrappers(t *testing.T) {
	for command, want := range map[string]string{
		"npx -y nodemon index.js":                                          "nodemon",
		"cross-env NODE_ENV=development nodemon":                           "nodemon",
		"dotenv -e .env -- next dev":                                       "next dev",
		"node ./node_modules/webpack-dev-server/bin/webpack-dev-server.js": "webpack-dev-server",
		"./node_modules/.bin/vite":                                         "vite",
		"node server.js":                                                   "",
		"cross-env NODE_ENV=production node server.js":                     "",
	} {
		if got := devServerTool(command); got != want {
			t.Errorf("devServerTool(%q) = %q, want %q", command, got, want)
		}
	}
}

func TestWatchShortFlag(t *testing.T) {
	for command, want := range map[string]string{
		"webpack -c --progress -w --config webpack.config.dev.js": "webpack -w",
		"tsc -w": "tsc -w",
		"webpack --config webpack.config.prod.js": "",
		"node server.js -w":                       "",
	} {
		if got := devServerTool(command); got != want {
			t.Errorf("devServerTool(%q) = %q, want %q", command, got, want)
		}
	}
}
