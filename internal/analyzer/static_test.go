package analyzer

import (
	"slices"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Perfil estático (DEC-37).
func staticFields(manager, build, out, outOrigin, spa string, extra map[string]wantField) map[string]wantField {
	fields := map[string]wantField{
		domain.FieldProfile:        {value: domain.ProfileStatic, origin: domain.OriginInference},
		domain.FieldPackageManager: {value: manager, origin: domain.OriginInference},
		domain.FieldExecutionMode:  {value: "production", origin: domain.OriginDefault},
		domain.FieldHealthPath:     {value: "/", origin: domain.OriginDefault},
		domain.FieldPort:           {value: domain.PlatformPort, origin: domain.OriginPlatform},
		domain.FieldOutputDir:      {value: out, origin: domain.Origin(outOrigin)},
	}
	spaOrigin := domain.OriginInference
	if manager == domain.ManagerNone {
		spaOrigin = domain.OriginDefault
	}
	fields[domain.FieldSPAFallback] = wantField{value: spa, origin: spaOrigin}
	switch manager {
	case domain.ManagerNPM:
		fields[domain.FieldNodeVersion] = wantField{value: "26", origin: domain.OriginDefault}
	case domain.ManagerBun:
		fields[domain.FieldBunVersion] = wantField{value: "1", origin: domain.OriginDefault}
	}
	if build != "" {
		fields[domain.FieldBuildCommand] = wantField{value: build, origin: domain.OriginDeclaration}
	}
	for k, v := range extra {
		fields[k] = v
	}
	return fields
}

func TestStaticFixtures(t *testing.T) {
	inf, decl := string(domain.OriginInference), string(domain.OriginDeclaration)
	tests := []fixture{
		{
			name:  "vite without start is a static SPA",
			files: nodeRepo(`{"scripts":{"dev":"vite","build":"tsc -b && vite build","preview":"vite preview"}}`, map[string]string{"index.html": "<html>"}),
			want:  want{outcome: domain.AnalysisReady, fields: staticFields("npm", "tsc -b && vite build", "dist", inf, "true", nil)},
		},
		{
			name: "a literal outDir in vite.config wins",
			files: nodeRepo(`{"scripts":{"build":"vite build"}}`, map[string]string{
				"vite.config.ts": "export default defineConfig({ build: { outDir: 'public/app' } })",
			}),
			want: want{outcome: domain.AnalysisReady, fields: staticFields("npm", "vite build", "public/app", decl, "true", nil)},
		},
		{
			name:  "create react app builds to build/ and its start is a dev server",
			files: nodeRepo(`{"scripts":{"start":"react-scripts start","build":"react-scripts build"}}`, nil),
			want:  want{outcome: domain.AnalysisReady, fields: staticFields("npm", "react-scripts build", "build", inf, "true", nil)},
		},
		{
			name:  "astro static is not an SPA",
			files: nodeRepo(`{"scripts":{"dev":"astro dev","start":"astro dev","build":"astro build"},"devDependencies":{"astro":"5"}}`, nil),
			want:  want{outcome: domain.AnalysisReady, fields: staticFields("npm", "astro build", "dist", inf, "false", nil)},
		},
		{
			name:  "bun build with an html entry is a static site",
			files: bunRepo(`{"scripts":{"build":"bun build ./index.html --outdir=www"}}`, nil),
			want:  want{outcome: domain.AnalysisReady, fields: staticFields("bun", "bun build ./index.html --outdir=www", "www", decl, "true", nil)},
		},
		{
			name:  "plain html without package.json is served as is",
			files: map[string]string{"index.html": "<html>", "style.css": "body{}"},
			want:  want{outcome: domain.AnalysisReady, fields: staticFields("none", "", ".", inf, "false", nil)},
		},
		{
			name:  "a real server in start keeps the server profile",
			files: nodeRepo(`{"scripts":{"start":"node server.js","build":"vite build"}}`, map[string]string{"server.js": "process.env.PORT"}),
			want: want{outcome: domain.AnalysisReady, fields: nodeFields("node server.js", map[string]wantField{
				domain.FieldBuildCommand: {value: "vite build", origin: domain.OriginDeclaration},
				domain.FieldPort:         {value: domain.PlatformPort, origin: domain.OriginPlatform},
			}), configVariables: []string{"PORT"}},
		},
		{
			name:  "next without export renders on the server",
			files: nodeRepo(`{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15"}}`, nil),
			want: want{outcome: domain.AnalysisNeedsInput, fields: nodeFields("next start", map[string]wantField{
				domain.FieldBuildCommand: {value: "next build", origin: domain.OriginDeclaration},
			}), pending: []string{"port"}},
		},
		{
			name: "next with output export is static",
			files: nodeRepo(`{"scripts":{"build":"next build","start":"next start"},"dependencies":{"next":"15"}}`, map[string]string{
				"next.config.mjs": "export default { output: 'export' }",
			}),
			want: want{outcome: domain.AnalysisReady, fields: staticFields("npm", "next build", "out", inf, "false", nil)},
		},
		{
			name:  "astro with an adapter renders on the server",
			files: nodeRepo(`{"scripts":{"build":"astro build"}}`, map[string]string{"astro.config.mjs": "export default defineConfig({ adapter: node() })"}),
			want:  want{outcome: domain.AnalysisNeedsInput, fields: nodeFields("", map[string]wantField{domain.FieldBuildCommand: {value: "astro build", origin: domain.OriginDeclaration}}), pending: []string{"port", "start_command"}},
		},
		{
			name:      "the user can force the static profile and give the output dir",
			files:     nodeRepo(`{"scripts":{"build":"webpack --mode production"}}`, nil),
			overrides: map[string]string{"profile": "P-STATIC", "output_dir": "public"},
			want: want{outcome: domain.AnalysisReady, fields: staticFields("npm", "webpack --mode production", "public", "override", "false", map[string]wantField{
				domain.FieldSPAFallback: {value: "false", origin: domain.OriginDefault},
				domain.FieldProfile:     {value: domain.ProfileStatic, origin: domain.OriginOverride, overrode: 1},
				domain.FieldOutputDir:   {value: "public", origin: domain.OriginOverride},
			})},
		},
	}
	for _, fx := range tests {
		t.Run(fx.name, func(t *testing.T) {
			got, err := Analyze(toFS(fx.files), Input{Overrides: fx.overrides})
			if err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}
			assertResult(t, got, fx.want)
		})
	}
}

func TestForcedStaticWithoutKnownToolAsksTheOutputDir(t *testing.T) {
	got, err := Analyze(toFS(nodeRepo(`{"scripts":{"build":"webpack"}}`, nil)), Input{Overrides: map[string]string{"profile": "P-STATIC"}})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(got.Pending, func(f domain.Finding) bool { return f.Field == domain.FieldOutputDir }) {
		t.Errorf("pending = %v, want output_dir", got.Pending)
	}
}

func TestMismatchedServerProfileOverrideIsAConflict(t *testing.T) {
	got, err := Analyze(toFS(bunRepo(`{"scripts":{"start":"bun x.ts"}}`, nil)), Input{Overrides: map[string]string{"profile": "P-NODE"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Conflicts) == 0 || got.Outcome != domain.AnalysisNeedsInput {
		t.Errorf("outcome %s, conflicts %v: want a conflict", got.Outcome, got.Conflicts)
	}
}

func TestPublicBuildVariables(t *testing.T) {
	got, err := Analyze(toFS(nodeRepo(`{"scripts":{"build":"vite build"}}`, map[string]string{
		"src/main.ts": "fetch(import.meta.env.VITE_API_URL); if (import.meta.env.DEV) {}",
		"src/cra.js":  "process.env.REACT_APP_TITLE",
	})), Input{})
	if err != nil {
		t.Fatal(err)
	}
	classes := map[string]domain.VarClass{}
	for _, v := range got.ConfigVariables {
		classes[v.Name] = v.Class
	}
	if classes["VITE_API_URL"] != domain.ClassPublic || classes["REACT_APP_TITLE"] != domain.ClassPublic {
		t.Errorf("classes = %v, want public build variables", classes)
	}
	if _, ok := classes["DEV"]; ok {
		t.Error("Vite built-ins are not project configuration")
	}
}

func TestSSRNoticeExplainsWhyNotStatic(t *testing.T) {
	got, err := Analyze(toFS(nodeRepo(`{"scripts":{"build":"astro build"}}`, map[string]string{"astro.config.mjs": "adapter: cloudflare()"})), Input{})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(got.Warnings, func(f domain.Finding) bool { return strings.Contains(f.Message, "renderiza no servidor") }) {
		t.Errorf("warnings = %v, want the SSR notice", got.Warnings)
	}
}

func TestStaticBuildParsing(t *testing.T) {
	tests := map[string][2]string{
		"vite build":                       {"vite", ""},
		"vite build --outDir out":          {"vite", "out"},
		"tsc && vite build":                {"vite", ""},
		"vite":                             {"", ""},
		"vue-cli-service build --dest www": {"vue-cli-service", "www"},
		"parcel build src/index.html --dist-dir=pub": {"parcel", "pub"},
		"bun build ./src/server.ts --outdir dist":    {"", ""},
		"next build":                    {"next", ""},
		"vite build --outDir ../escape": {"vite", ""},
	}
	for cmd, want := range tests {
		tool, out := staticBuild(cmd)
		if tool != want[0] || out != want[1] {
			t.Errorf("staticBuild(%q) = %q, %q; want %q, %q", cmd, tool, out, want[0], want[1])
		}
	}
}

func TestSecretLookingPublicVariableIsWarned(t *testing.T) {
	got, err := Analyze(toFS(nodeRepo(`{"scripts":{"build":"vite build"}}`, map[string]string{
		"src/a.ts": "import.meta.env.VITE_SPOTIFY_TOKEN; import.meta.env.VITE_API_URL",
	})), Input{})
	if err != nil {
		t.Fatal(err)
	}
	var warned []string
	for _, w := range got.Warnings {
		if strings.Contains(w.Message, "nome de segredo") {
			warned = append(warned, w.Message)
		}
	}
	if len(warned) != 1 || !strings.Contains(warned[0], "VITE_SPOTIFY_TOKEN") {
		t.Errorf("warnings = %v, want one about VITE_SPOTIFY_TOKEN", warned)
	}
}

func TestDockerArtifactsAreReportedButNeverUsed(t *testing.T) {
	files := bunRepo(`{"scripts":{"start":"bun x.ts"}}`, map[string]string{
		"Dockerfile": "FROM oven/bun", "docker-compose.yml": "services:", "x.ts": "process.env.PORT",
	})
	got, err := Analyze(toFS(files), Input{})
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, w := range got.Warnings {
		if strings.Contains(w.Message, "HEU-08") {
			found = w.Message
		}
	}
	if !strings.Contains(found, "Dockerfile, docker-compose.yml") || !strings.Contains(found, "não o usa") {
		t.Errorf("HEU-08 warning = %q", found)
	}
	if got.Outcome != domain.AnalysisReady || got.Fields[domain.FieldProfile].Value != domain.ProfileBun {
		t.Errorf("a Dockerfile must not change the plan: %s %v", got.Outcome, got.Fields[domain.FieldProfile])
	}
	// Também avisa num repositório que o Cobogó não suporta: é informação.
	unsupported, _ := Analyze(toFS(map[string]string{"go.mod": "module x", "Dockerfile": "FROM golang"}), Input{})
	if !slices.ContainsFunc(unsupported.Warnings, func(f domain.Finding) bool { return strings.Contains(f.Message, "HEU-08") }) {
		t.Errorf("unsupported repository warnings = %v, want HEU-08", unsupported.Warnings)
	}
	if clean, _ := Analyze(toFS(bunRepo(`{"scripts":{"start":"bun x.ts"}}`, map[string]string{"x.ts": "process.env.PORT"})), Input{}); slices.ContainsFunc(clean.Warnings, func(f domain.Finding) bool { return strings.Contains(f.Message, "HEU-08") }) {
		t.Error("no HEU-08 warning without a Dockerfile or compose")
	}
}

func TestNodeSassIsWarnedBeforeTheBuild(t *testing.T) {
	got, err := Analyze(toFS(nodeRepo(`{"scripts":{"build":"vue-cli-service build"},"dependencies":{"node-sass":"^4.12.0"}}`, nil)), Input{})
	if err != nil {
		t.Fatal(err)
	}
	var msg string
	for _, w := range got.Warnings {
		if strings.Contains(w.Message, "node-sass") {
			msg = w.Message
		}
	}
	if !strings.Contains(msg, "descontinuado") || !strings.Contains(msg, "sass") || !strings.Contains(msg, "#32") {
		t.Errorf("node-sass warning = %q", msg)
	}
	if clean, _ := Analyze(toFS(nodeRepo(`{"scripts":{"build":"vite build"}}`, nil)), Input{}); slices.ContainsFunc(clean.Warnings, func(f domain.Finding) bool { return strings.Contains(f.Message, "node-sass") }) {
		t.Error("no node-sass warning without the dependency")
	}
}
