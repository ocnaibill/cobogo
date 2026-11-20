package analyzer

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Perfil P-BUN (DEC-36). O lockfile do Bun é opaco para o analisador: basta
// existir.
func bunRepo(pkg string, extra map[string]string) map[string]string {
	files := map[string]string{"package.json": pkg, "bun.lock": "{}"}
	maps.Copy(files, extra)
	return files
}

func bunFields(start string, extra map[string]wantField) map[string]wantField {
	fields := map[string]wantField{
		domain.FieldProfile:        {value: domain.ProfileBun, origin: domain.OriginInference},
		domain.FieldPackageManager: {value: domain.ManagerBun, origin: domain.OriginInference},
		domain.FieldExecutionMode:  {value: "production", origin: domain.OriginDefault},
		domain.FieldBunVersion:     {value: domain.DefaultBunVersion, origin: domain.OriginDefault},
		domain.FieldHealthPath:     {value: "/", origin: domain.OriginDefault},
	}
	if start != "" {
		fields[domain.FieldStartCommand] = wantField{value: start, origin: domain.OriginDeclaration}
	}
	maps.Copy(fields, extra)
	return fields
}

var platformPort = map[string]wantField{domain.FieldPort: {value: domain.PlatformPort, origin: domain.OriginPlatform}}

func TestBunFixtures(t *testing.T) {
	const startServer = `{"scripts":{"start":"bun run src/server.ts"}}`
	tests := []fixture{
		{
			name:  "bun.lock and a PORT reader publish without questions",
			files: bunRepo(startServer, map[string]string{"src/server.ts": "Bun.serve({ port: Number(process.env.PORT ?? 3000), fetch })"}),
			want:  want{outcome: domain.AnalysisReady, fields: bunFields("bun run src/server.ts", platformPort), configVariables: []string{"PORT"}},
		},
		{
			name: "the old binary bun.lockb is accepted too",
			files: map[string]string{
				"package.json": startServer, "bun.lockb": "\x00binary",
				"src/server.ts": "const port = process.env?.PORT || 8000",
			},
			want: want{outcome: domain.AnalysisReady, fields: bunFields("bun run src/server.ts", platformPort), configVariables: []string{"PORT"}},
		},
		{
			name:  "Bun.serve without a port reads PORT by itself",
			files: bunRepo(startServer, map[string]string{"src/server.ts": "Bun.serve({ fetch(req) { return new Response('ok') } })"}),
			want:  want{outcome: domain.AnalysisReady, fields: bunFields("bun run src/server.ts", platformPort)},
		},
		{
			name:  "a fixed port in Bun.serve is only a hint",
			files: bunRepo(startServer, map[string]string{"src/server.ts": "Bun.serve({ port: 3000, fetch })"}),
			want:  want{outcome: domain.AnalysisNeedsInput, fields: bunFields("bun run src/server.ts", nil), pending: []string{"port"}},
		},
		{
			name: "export default of the start entry is served by Bun on PORT",
			files: bunRepo(`{"scripts":{"start":"NODE_ENV=production bun --smol src/index.ts"}}`, map[string]string{
				"src/index.ts": "import { Hono } from 'hono'\nconst app = new Hono()\nexport default app\n",
			}),
			want: want{outcome: domain.AnalysisReady, fields: bunFields("NODE_ENV=production bun --smol src/index.ts", platformPort)},
		},
		{
			name: "export default outside the entry file is not a server",
			files: bunRepo(startServer, map[string]string{
				"src/server.ts":    "console.log('hi')",
				"src/component.ts": "export default function Button() {}",
			}),
			want: want{outcome: domain.AnalysisNeedsInput, fields: bunFields("bun run src/server.ts", nil), pending: []string{"port"}},
		},
		{
			name:  "Bun.serve in a test file is not a contract",
			files: bunRepo(startServer, map[string]string{"src/server.ts": "x", "test/server.test.ts": "Bun.serve({ fetch })"}),
			want:  want{outcome: domain.AnalysisNeedsInput, fields: bunFields("bun run src/server.ts", nil), pending: []string{"port"}},
		},
		{
			name:  "npm and Bun lockfiles together ask which manager",
			files: map[string]string{"package.json": startServer, "bun.lock": "{}", "package-lock.json": lockJSON},
			want: want{
				outcome: domain.AnalysisNeedsInput,
				fields:  map[string]wantField{domain.FieldExecutionMode: {value: "production", origin: domain.OriginDefault}, domain.FieldStartCommand: {value: "bun run src/server.ts", origin: domain.OriginDeclaration}, domain.FieldHealthPath: {value: "/", origin: domain.OriginDefault}},
				pending: []string{"package_manager", "port"},
			},
		},
		{
			name:      "choosing bun picks the P-BUN profile",
			files:     map[string]string{"package.json": startServer, "bun.lock": "{}", "package-lock.json": lockJSON, "src/server.ts": "process.env.PORT"},
			overrides: map[string]string{"package_manager": "bun"},
			want: want{outcome: domain.AnalysisReady, fields: bunFields("bun run src/server.ts", map[string]wantField{
				domain.FieldPackageManager: {value: "bun", origin: domain.OriginOverride, overrode: 1},
				domain.FieldPort:           {value: domain.PlatformPort, origin: domain.OriginPlatform},
			}), configVariables: []string{"PORT"}},
		},
		{
			name:      "choosing npm picks the P-NODE profile",
			files:     map[string]string{"package.json": startServer, "bun.lock": "{}", "package-lock.json": lockJSON, "src/server.ts": "process.env.PORT"},
			overrides: map[string]string{"package_manager": "npm"},
			want: want{outcome: domain.AnalysisReady, fields: nodeFields("bun run src/server.ts", map[string]wantField{
				domain.FieldPackageManager: {value: "npm", origin: domain.OriginOverride, overrode: 1},
				domain.FieldPort:           {value: domain.PlatformPort, origin: domain.OriginPlatform},
			}), configVariables: []string{"PORT"}},
		},
		{
			name:  "yarn next to bun is unsupported",
			files: map[string]string{"package.json": startServer, "bun.lock": "{}", "yarn.lock": ""},
			want:  want{outcome: domain.AnalysisUnsupported, unsupported: []string{"HEU-01"}, skipped: []string{"HEU-02", "HEU-05", "HEU-09", "HEU-10", "HEU-11"}},
		},
		{
			name:  "packageManager pins an exact Bun version",
			files: map[string]string{"package.json": `{"packageManager":"bun@1.1.38+sha512.abc","scripts":{"start":"bun x.ts"}}`, "bun.lockb": "x", "x.ts": "process.env.PORT"},
			want: want{outcome: domain.AnalysisReady, fields: bunFields("bun x.ts", map[string]wantField{
				domain.FieldBunVersion: {value: "1.1.38", origin: domain.OriginDeclaration},
				domain.FieldPort:       {value: domain.PlatformPort, origin: domain.OriginPlatform},
			}), configVariables: []string{"PORT"}},
		},
		{
			name:  "an engines.bun range keeps the latest 1.x",
			files: bunRepo(`{"engines":{"bun":">=1.0.0"},"scripts":{"start":"bun x.ts"}}`, map[string]string{"x.ts": "process.env.PORT"}),
			want:  want{outcome: domain.AnalysisReady, fields: bunFields("bun x.ts", platformPort), configVariables: []string{"PORT"}},
		},
		{
			name:      "a Bun version override wins",
			files:     bunRepo(`{"scripts":{"start":"bun x.ts"}}`, map[string]string{"x.ts": "process.env.PORT"}),
			overrides: map[string]string{"bun_version": "1.2"},
			want: want{outcome: domain.AnalysisReady, fields: bunFields("bun x.ts", map[string]wantField{
				domain.FieldBunVersion: {value: "1.2", origin: domain.OriginOverride, overrode: 1},
				domain.FieldPort:       {value: domain.PlatformPort, origin: domain.OriginPlatform},
			}), configVariables: []string{"PORT"}},
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

func TestBunManagerQuestionOffersBoth(t *testing.T) {
	got, err := Analyze(toFS(map[string]string{"package.json": `{"scripts":{"start":"bun x.ts"}}`, "bun.lock": "{}", "package-lock.json": lockJSON}), Input{})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got.Pending {
		if p.Field == domain.FieldProfile {
			t.Errorf("profile must not get a generic question while the manager is being asked: %v", p)
		}
		if p.Field == domain.FieldPackageManager {
			if values := optionValues(p.Options); !slices.Equal(values, []string{"npm", "bun"}) {
				t.Errorf("options = %v, want npm and bun", values)
			}
			if len(p.EvidenceIDs) != 2 {
				t.Errorf("the question must cite both lockfiles, got %v", p.EvidenceIDs)
			}
		}
	}
}

func TestBunStartOptionsUseBunAndEntryFields(t *testing.T) {
	got, err := Analyze(toFS(bunRepo(`{"module":"index.ts","main":"dist/index.js","scripts":{"dev":"bun --hot index.ts","serve":"bun index.ts"}}`, nil)), Input{})
	if err != nil {
		t.Fatal(err)
	}
	var options []domain.Option
	for _, p := range got.Pending {
		if p.Field == domain.FieldStartCommand {
			options = p.Options
		}
	}
	byValue := map[string]domain.Option{}
	for _, o := range options {
		byValue[o.Value] = o
	}
	for _, v := range []string{"bun run serve", "bun run dev", "bun run index.ts", "bun run dist/index.js"} {
		if _, ok := byValue[v]; !ok {
			t.Errorf("missing option %q in %v", v, optionValues(options))
		}
	}
	if byValue["bun run dev"].Kind != domain.OptionDevelopment {
		t.Errorf("bun --hot must be a development option: %+v", byValue["bun run dev"])
	}
	if _, ok := byValue["npm run serve"]; ok {
		t.Error("a Bun project must not be offered npm run")
	}
}

func TestBunVersionWarnings(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"old version with the text lockfile", bunRepo(`{"packageManager":"bun@1.1.0","scripts":{"start":"bun x.ts"}}`, nil), "1.2"},
		{"a major other than 1", bunRepo(`{"packageManager":"bun@2.0.0","scripts":{"start":"bun x.ts"}}`, nil), "major 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Analyze(toFS(tt.files), Input{})
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(got.Warnings, func(f domain.Finding) bool { return strings.Contains(f.Message, tt.want) }) {
				t.Errorf("warnings %v lack %q", got.Warnings, tt.want)
			}
		})
	}
}

func TestEnvReferencesCoverOptionalChainingBracketsAndBunEnv(t *testing.T) {
	src := strings.Join([]string{
		"const a = process.env?.OPT_CHAIN",
		`const b = process.env["BRACKET_D"]`,
		"const c = process.env['BRACKET_S']",
		"const d = Bun.env.BUN_DOT",
		"const e = Bun.env?.BUN_OPT",
		"const { DESTR } = Bun.env",
		"const f = process.env?.['OPT_BRACKET']",
	}, "\n")
	refs := envReferences([]byte(src))
	for _, name := range []string{"OPT_CHAIN", "BRACKET_D", "BRACKET_S", "BUN_DOT", "BUN_OPT", "DESTR", "OPT_BRACKET"} {
		if _, ok := refs[name]; !ok {
			t.Errorf("reference %s not found in %v", name, slices.Sorted(maps.Keys(refs)))
		}
	}
	if envDynamic.MatchString(src) {
		t.Error("literal keys must not count as dynamic access")
	}
	if !envDynamic.MatchString("process.env[key]") || !envDynamic.MatchString("Bun.env?.[name]") {
		t.Error("a computed key is dynamic access")
	}
}

func TestBunEntry(t *testing.T) {
	tests := map[string]string{
		"bun run src/server.ts":                 "src/server.ts",
		"bun src/index.ts":                      "src/index.ts",
		"NODE_ENV=production bun --smol app.js": "app.js",
		"bun run start":                         "",
		"node index.js":                         "",
		"bun":                                   "",
	}
	for cmd, want := range tests {
		if got := bunEntry(cmd); got != want {
			t.Errorf("bunEntry(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestOverrideValidationForBun(t *testing.T) {
	for _, tt := range []struct {
		overrides map[string]string
		ok        bool
	}{
		{map[string]string{"bun_version": "1.2.3"}, true},
		{map[string]string{"bun_version": "1"}, true},
		{map[string]string{"bun_version": "2"}, false},
		{map[string]string{"bun_version": "latest"}, false},
		{map[string]string{"package_manager": "bun"}, true},
		{map[string]string{"package_manager": "yarn"}, false},
	} {
		if err := CheckOverrides(tt.overrides); (err == nil) != tt.ok {
			t.Errorf("CheckOverrides(%v) = %v, want ok=%v", tt.overrides, err, tt.ok)
		}
	}
}
