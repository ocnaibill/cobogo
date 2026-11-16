package analyzer

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ocnaibill/cobogo/internal/domain"
)

type wantField struct {
	value    string
	origin   domain.Origin
	overrode int
}

type want struct {
	outcome         domain.AnalysisOutcome
	fields          map[string]wantField
	pending         []string
	conflicts       []string
	unsupported     []string
	skipped         []string
	configVariables []string
}

type fixture struct {
	name      string
	files     map[string]string
	overrides map[string]string
	want      want
}

const lockJSON = `{"lockfileVersion":3}`

func nodeRepo(pkg string, extra map[string]string) map[string]string {
	files := map[string]string{"package.json": pkg, "package-lock.json": lockJSON}
	maps.Copy(files, extra)
	return files
}

func toFS(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, content := range files {
		m[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return m
}

func nodeFields(start string, extra map[string]wantField) map[string]wantField {
	fields := map[string]wantField{
		domain.FieldProfile:        {value: "P-NODE", origin: domain.OriginInference},
		domain.FieldPackageManager: {value: "npm", origin: domain.OriginInference},
		domain.FieldExecutionMode:  {value: "production", origin: domain.OriginDefault},
	}
	if start != "" {
		fields[domain.FieldStartCommand] = wantField{value: start, origin: domain.OriginDeclaration}
	}
	maps.Copy(fields, extra)
	return fields
}

func fixtures() []fixture {
	const startIndex = `{"scripts":{"start":"node index.js"}}`
	unsupportedByHEU01 := want{
		outcome:     domain.AnalysisUnsupported,
		unsupported: []string{"HEU-01"},
		skipped:     []string{"HEU-02", "HEU-05", "HEU-09"},
	}
	portRepo := nodeRepo(startIndex, map[string]string{
		"index.js": "const port = process.env.PORT || 3000;",
		"test.js":  "process.env.PORT = '0';",
	})

	return []fixture{
		{
			name:  "node app with PORT contract needs the port",
			files: portRepo,
			want: want{
				outcome:         domain.AnalysisNeedsInput,
				fields:          nodeFields("node index.js", nil),
				pending:         []string{"port"},
				configVariables: []string{"PORT"},
			},
		},
		{
			name:      "port override resolves the pending field",
			files:     portRepo,
			overrides: map[string]string{"port": "3000"},
			want: want{
				outcome: domain.AnalysisReady,
				fields: nodeFields("node index.js", map[string]wantField{
					domain.FieldPort: {value: "3000", origin: domain.OriginOverride},
				}),
				configVariables: []string{"PORT"},
			},
		},
		{
			name:      "build script is declared as build command",
			files:     nodeRepo(`{"scripts":{"start":"node server.js","build":"tsc"}}`, nil),
			overrides: map[string]string{"port": "3000"},
			want: want{
				outcome: domain.AnalysisReady,
				fields: nodeFields("node server.js", map[string]wantField{
					domain.FieldBuildCommand: {value: "tsc", origin: domain.OriginDeclaration},
					domain.FieldPort:         {value: "3000", origin: domain.OriginOverride},
				}),
			},
		},
		{
			name: ".env.example and destructuring feed the inventory",
			files: nodeRepo(startIndex, map[string]string{
				".env.example":  "# comment\nDATABASE_URL=\nexport JWT_SECRET=changeme\n",
				"src/server.js": "const { PORT, LOG_LEVEL = 'info' } = process.env;\nconst db = process.env.DATABASE_URL;",
			}),
			overrides: map[string]string{"port": "3000"},
			want: want{
				outcome: domain.AnalysisReady,
				fields: nodeFields("node index.js", map[string]wantField{
					domain.FieldPort: {value: "3000", origin: domain.OriginOverride},
				}),
				configVariables: []string{"DATABASE_URL", "JWT_SECRET", "LOG_LEVEL", "PORT"},
			},
		},
		{
			name: "PORT referenced only in tests is not a runtime contract",
			files: nodeRepo(startIndex, map[string]string{
				"src/index.js":        "console.log('hi')",
				"test/server.test.js": "process.env.PORT = '0'",
			}),
			want: want{
				outcome:         domain.AnalysisNeedsInput,
				fields:          nodeFields("node index.js", nil),
				pending:         []string{"port"},
				configVariables: []string{"PORT"},
			},
		},
		{
			name:  "missing start script is pending",
			files: nodeRepo(`{"name":"app"}`, map[string]string{"index.js": "x"}),
			want: want{
				outcome: domain.AnalysisNeedsInput,
				fields:  nodeFields("", nil),
				pending: []string{"port", "start_command"},
			},
		},
		{
			name:  "development server as start script is pending",
			files: nodeRepo(`{"scripts":{"start":"nodemon index.js"}}`, nil),
			want: want{
				outcome: domain.AnalysisNeedsInput,
				fields:  nodeFields("", nil),
				pending: []string{"port", "start_command"},
			},
		},
		{
			name:      "execution mode defaults to production and only an override changes it",
			files:     nodeRepo(`{"scripts":{"dev":"nodemon index.js"}}`, nil),
			overrides: map[string]string{"start_command": "npm run dev", "execution_mode": "development", "port": "3000"},
			want: want{
				outcome: domain.AnalysisReady,
				fields: nodeFields("", map[string]wantField{
					domain.FieldExecutionMode: {value: "development", origin: domain.OriginOverride, overrode: 1},
					domain.FieldStartCommand:  {value: "npm run dev", origin: domain.OriginOverride},
					domain.FieldPort:          {value: "3000", origin: domain.OriginOverride},
				}),
			},
		},
		{
			name:      "an unknown execution mode is ignored and stays pending",
			files:     nodeRepo(startIndex, nil),
			overrides: map[string]string{"execution_mode": "staging", "port": "3000"},
			want: want{
				outcome: domain.AnalysisNeedsInput,
				pending: []string{"execution_mode"},
				fields: nodeFields("node index.js", map[string]wantField{
					domain.FieldPort: {value: "3000", origin: domain.OriginOverride},
				}),
			},
		},
		{
			name:      "start command override replaces a development server",
			files:     nodeRepo(`{"scripts":{"start":"nodemon index.js"}}`, nil),
			overrides: map[string]string{"start_command": "node index.js", "port": "3000"},
			want: want{
				outcome: domain.AnalysisReady,
				fields: map[string]wantField{
					domain.FieldProfile:        {value: "P-NODE", origin: domain.OriginInference},
					domain.FieldPackageManager: {value: "npm", origin: domain.OriginInference},
					domain.FieldExecutionMode:  {value: "production", origin: domain.OriginDefault},
					domain.FieldStartCommand:   {value: "node index.js", origin: domain.OriginOverride},
					domain.FieldPort:           {value: "3000", origin: domain.OriginOverride},
				},
			},
		},
		{
			name:      "override differing from a declaration wins and records what it covered",
			files:     nodeRepo(startIndex, nil),
			overrides: map[string]string{"start_command": "node server.js", "port": "3000"},
			want: want{
				outcome: domain.AnalysisReady,
				fields: map[string]wantField{
					domain.FieldProfile:        {value: "P-NODE", origin: domain.OriginInference},
					domain.FieldPackageManager: {value: "npm", origin: domain.OriginInference},
					domain.FieldExecutionMode:  {value: "production", origin: domain.OriginDefault},
					domain.FieldStartCommand:   {value: "node server.js", origin: domain.OriginOverride, overrode: 1},
					domain.FieldPort:           {value: "3000", origin: domain.OriginOverride},
				},
			},
		},
		{
			name:      "invalid port override is ignored and stays pending",
			files:     nodeRepo(startIndex, nil),
			overrides: map[string]string{"port": "abc"},
			want: want{
				outcome: domain.AnalysisNeedsInput,
				fields:  nodeFields("node index.js", nil),
				pending: []string{"port"},
			},
		},
		{
			name:  "empty workspaces array is not a monorepo",
			files: nodeRepo(`{"workspaces":[],"scripts":{"start":"node index.js"}}`, map[string]string{"index.js": "x"}),
			want: want{
				outcome: domain.AnalysisNeedsInput,
				fields:  nodeFields("node index.js", nil),
				pending: []string{"port"},
			},
		},
		{
			name:  "python app is unsupported",
			files: map[string]string{"requirements.txt": "flask", "app.py": "print('x')"},
			want:  unsupportedByHEU01,
		},
		{
			name:  "go app is unsupported",
			files: map[string]string{"go.mod": "module x", "main.go": "package main"},
			want:  unsupportedByHEU01,
		},
		{
			name:  "yarn lockfile only is unsupported",
			files: map[string]string{"package.json": startIndex, "yarn.lock": ""},
			want:  unsupportedByHEU01,
		},
		{
			name:  "npm plus pnpm lockfiles are unsupported",
			files: nodeRepo(startIndex, map[string]string{"pnpm-lock.yaml": ""}),
			want:  unsupportedByHEU01,
		},
		{
			name:  "workspaces are unsupported",
			files: nodeRepo(`{"workspaces":["packages/*"],"scripts":{"start":"node index.js"}}`, nil),
			want:  unsupportedByHEU01,
		},
		{
			name:  "missing lockfile is unsupported",
			files: map[string]string{"package.json": startIndex},
			want:  unsupportedByHEU01,
		},
		{
			name:  "invalid package.json is unsupported",
			files: map[string]string{"package.json": "{not json", "package-lock.json": lockJSON},
			want:  unsupportedByHEU01,
		},
	}
}

func TestAnalyzeFixtures(t *testing.T) {
	for _, fx := range fixtures() {
		t.Run(fx.name, func(t *testing.T) {
			got, err := Analyze(toFS(fx.files), Input{Overrides: fx.overrides})
			if err != nil {
				t.Fatalf("Analyze() error = %v", err)
			}
			assertResult(t, got, fx.want)
		})
	}
}

func assertResult(t *testing.T, got domain.AnalysisResult, w want) {
	t.Helper()
	if got.Outcome != w.outcome {
		t.Errorf("outcome = %s, want %s", got.Outcome, w.outcome)
	}
	if gotNames, wantNames := sortedKeys(got.Fields), sortedKeys(w.fields); !slices.Equal(gotNames, wantNames) {
		t.Errorf("resolved fields = %v, want %v", gotNames, wantNames)
	}
	for name, wf := range w.fields {
		gf, ok := got.Fields[name]
		if !ok {
			continue
		}
		if gf.Value != wf.value || gf.Origin != wf.origin || len(gf.Overrode) != wf.overrode {
			t.Errorf("field %s = {%q %s overrode=%d}, want {%q %s overrode=%d}",
				name, gf.Value, gf.Origin, len(gf.Overrode), wf.value, wf.origin, wf.overrode)
		}
	}
	assertStrings(t, "pending fields", findingFields(got.Pending), w.pending)
	assertStrings(t, "conflicts", findingFields(got.Conflicts), w.conflicts)
	assertStrings(t, "unsupported rules", findingRules(got.Unsupported), w.unsupported)
	assertStrings(t, "skipped rules", rulesWithStatus(got.Rules, "skipped"), w.skipped)

	var names []string
	for _, v := range got.ConfigVariables {
		names = append(names, v.Name)
	}
	assertStrings(t, "config variables", names, w.configVariables)
}

func assertStrings(t *testing.T, label string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s = %v, want %v", label, got, want)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := slices.Collect(maps.Keys(m))
	slices.Sort(keys)
	return keys
}

func uniqueSorted(ss []string) []string {
	out := slices.Clone(ss)
	slices.Sort(out)
	return slices.Compact(out)
}

func findingFields(fs []domain.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Field)
	}
	return uniqueSorted(out)
}

func findingRules(fs []domain.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return uniqueSorted(out)
}

func rulesWithStatus(rules []domain.AppliedRule, status string) []string {
	var out []string
	for _, r := range rules {
		if r.Status == status {
			out = append(out, r.ID)
		}
	}
	return out
}

func TestAnalyzeIsDeterministic(t *testing.T) {
	files := nodeRepo(`{"scripts":{"start":"node index.js"}}`, map[string]string{
		".env.example": "A=1\nB=2\n",
		"index.js":     "process.env.PORT; process.env.A;",
		"lib/x.js":     "const { B } = process.env;",
	})
	first, err := Analyze(toFS(files), Input{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := Analyze(toFS(files), Input{})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Errorf("same input produced different analyses:\n%s\n%s", a, b)
	}
	seen := map[string]bool{}
	for _, e := range first.Evidences {
		if e.ID == "" || seen[e.ID] {
			t.Errorf("evidence ID %q is empty or duplicated", e.ID)
		}
		seen[e.ID] = true
	}
}

func TestPortPendingMessages(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "runtime contract names the file",
			files: nodeRepo(`{"scripts":{"start":"node index.js"}}`, map[string]string{"src/index.js": "process.env.PORT"}),
			want:  "src/index.js",
		},
		{
			name: "test-only reference says so",
			files: nodeRepo(`{"scripts":{"start":"node index.js"}}`, map[string]string{
				"test/a.test.js": "process.env.PORT",
			}),
			want: "teste",
		},
		{
			name: "no contract and unread files point to coverage",
			files: nodeRepo(`{"scripts":{"start":"node index.js"}}`, map[string]string{
				"big.js": strings.Repeat(" ", maxFileSize+1),
			}),
			want: "cobertura",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Analyze(toFS(tt.files), Input{})
			if err != nil {
				t.Fatal(err)
			}
			var msg string
			for _, p := range got.Pending {
				if p.Field == domain.FieldPort {
					msg = p.Message
				}
			}
			if !strings.Contains(msg, tt.want) {
				t.Errorf("port pending message = %q, want it to contain %q", msg, tt.want)
			}
		})
	}
}

func TestPortPendingCitesOnlyRuntimeEvidence(t *testing.T) {
	files := nodeRepo(`{"scripts":{"start":"node index.js"}}`, map[string]string{
		"index.js": "process.env.PORT",
		"test.js":  "process.env.PORT",
	})
	got, err := Analyze(toFS(files), Input{})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]domain.Evidence{}
	for _, e := range got.Evidences {
		byID[e.ID] = e
	}
	for _, p := range got.Pending {
		if p.Field != domain.FieldPort {
			continue
		}
		if len(p.EvidenceIDs) != 1 || byID[p.EvidenceIDs[0]].Source != "index.js" {
			t.Errorf("port pending cites %v, want only the index.js evidence", p.EvidenceIDs)
		}
	}
}

func TestParseEnvLines(t *testing.T) {
	data := `# comment in prose = not a variable
DATABASE_URL=postgres://user:pw@host/db
export JWT_SECRET="abc"
  PORT = 3000
lower_case=1
1BAD=2
not an assignment
DATABASE_URL=again
API_URL=http://x # inline note
QUOTED='a # b'
# OPTIONAL_FLAG=on
#ANOTHER=
# lower_commented=1
`
	var got []string
	for _, l := range parseEnvLines([]byte(data)) {
		got = append(got, fmt.Sprintf("%d:%s=%s:%t", l.Line, l.Name, l.Value, l.Commented))
	}
	assertStrings(t, "lines", got, []string{
		"2:DATABASE_URL=postgres://user:pw@host/db:false",
		"3:JWT_SECRET=abc:false",
		"4:PORT=3000:false",
		"5:lower_case=1:false",
		"8:DATABASE_URL=again:false",
		"9:API_URL=http://x:false",
		"10:QUOTED=a # b:false",
		"11:OPTIONAL_FLAG=on:true",
		"12:ANOTHER=:true",
	})
}

func TestClassifyScope(t *testing.T) {
	tests := map[string]domain.Scope{
		"index.js":                    domain.ScopeRuntime,
		"src/server.ts":               domain.ScopeRuntime,
		"contest.js":                  domain.ScopeRuntime,
		"latest/index.js":             domain.ScopeRuntime,
		"test.js":                     domain.ScopeTest,
		"tests/a.js":                  domain.ScopeTest,
		"src/__tests__/a.js":          domain.ScopeTest,
		"src/app.test.js":             domain.ScopeTest,
		"src/app.spec.ts":             domain.ScopeTest,
		"Test/Helpers.js":             domain.ScopeTest,
		"cypress/e2e/home.cy.js":      domain.ScopeTest,
		"webpack.config.js":           domain.ScopeBuild,
		"packages/ui/vite.config.mjs": domain.ScopeBuild,
	}
	for path, want := range tests {
		if got := classifyScope(path); got != want {
			t.Errorf("classifyScope(%q) = %s, want %s", path, got, want)
		}
	}
}

func TestDevServerTool(t *testing.T) {
	tests := map[string]string{
		"node index.js":                       "",
		"npm run build && node dist/index.js": "",
		"next start":                          "",
		"vite build":                          "",
		"vite preview":                        "",
		"nodemon index.js":                    "nodemon",
		"NODE_ENV=development nodemon app.js": "nodemon",
		"npx ts-node-dev src/index.ts":        "ts-node-dev",
		"next dev":                            "next dev",
		"vite":                                "vite",
		"vite --port 3000":                    "vite",
		"node --watch index.js":               "--watch",
		"tsx watch src/index.ts":              "tsx watch",
		"react-scripts start":                 "react-scripts start",
		"webpack serve":                       "webpack serve",
		"node build.js && nodemon server.js":  "nodemon",
	}
	for cmd, want := range tests {
		if got := devServerTool(cmd); got != want {
			t.Errorf("devServerTool(%q) = %q, want %q", cmd, got, want)
		}
	}
}

func TestExtractSource(t *testing.T) {
	files := fstest.MapFS{
		"src/index.js":            {Data: []byte("const a = process.env.PORT;\nconst { HOST: host, DB_URL = 'x', ...rest } = process.env;\n")},
		"src/multi.js":            {Data: []byte("const {\n  A,\n  B,\n} = process.env;")},
		"src/dyn.js":              {Data: []byte("const k = 'X'; process.env[k]")},
		"big.js":                  {Data: []byte(strings.Repeat(" ", maxFileSize+1))},
		"link.js":                 {Data: []byte("target"), Mode: fs.ModeSymlink},
		"node_modules/x/index.js": {Data: []byte("process.env.IGNORED")},
		"view.tsx":                {Data: []byte("process.env.TSX_VAR")},
		"test/a.test.js":          {Data: []byte("process.env.IN_TEST")},
		"README.md":               {Data: []byte("process.env.README")},
	}
	x := newExtraction()
	if err := extractSource(files, x); err != nil {
		t.Fatal(err)
	}
	evidence, coverage := x.finish()

	var got []string
	for _, e := range evidence {
		got = append(got, fmt.Sprintf("%s|%s|%d|%s", e.Name, e.Source, e.Line, e.Scope))
	}
	assertStrings(t, "evidence", got, []string{
		"A|src/multi.js|1|runtime",
		"B|src/multi.js|1|runtime",
		"DB_URL|src/index.js|2|runtime",
		"HOST|src/index.js|2|runtime",
		"IN_TEST|test/a.test.js|1|test",
		"PORT|src/index.js|1|runtime",
		"TSX_VAR|view.tsx|1|runtime",
	})

	var gaps []string
	for _, g := range coverage.Gaps {
		gaps = append(gaps, g.Path+"|"+g.Reason)
	}
	assertStrings(t, "gaps", gaps, []string{"big.js|too_large", "link.js|symlink", "src/dyn.js|dynamic_env_access"})
	if coverage.FilesRead != 5 {
		t.Errorf("files read = %d, want 5", coverage.FilesRead)
	}
}

func syntheticCatalog(rules ...Rule) Catalog {
	return Catalog{
		Version: "test",
		Fields: []FieldSpec{
			{Name: "port", Required: true, Overridable: true, Validate: validatePort},
			{Name: "start_command", Required: true, Overridable: true},
			{Name: "profile", Required: true},
		},
		Rules: rules,
	}
}

func proposes(id, field, value string, origin domain.Origin) Rule {
	return Rule{
		Meta: RuleMeta{ID: id, Version: "1", State: RuleProposed, Fields: []string{field}},
		Apply: func(RuleInput) RuleOutput {
			return RuleOutput{Proposals: []domain.Proposal{{Field: field, Value: value, Origin: origin}}}
		},
	}
}

func TestResolverPrecedence(t *testing.T) {
	empty := fstest.MapFS{}
	tests := []struct {
		name      string
		rules     []Rule
		overrides map[string]string
		wantValue string
		wantOrig  domain.Origin
		wantConf  bool
	}{
		{
			name:      "declaration beats inference",
			rules:     []Rule{proposes("A", "port", "4000", domain.OriginInference), proposes("B", "port", "3000", domain.OriginDeclaration)},
			wantValue: "3000", wantOrig: domain.OriginDeclaration,
		},
		{
			name:      "inference beats default",
			rules:     []Rule{proposes("A", "port", "4000", domain.OriginDefault), proposes("B", "port", "3000", domain.OriginInference)},
			wantValue: "3000", wantOrig: domain.OriginInference,
		},
		{
			name:     "different values at the same level conflict",
			rules:    []Rule{proposes("A", "port", "4000", domain.OriginDeclaration), proposes("B", "port", "3000", domain.OriginDeclaration)},
			wantConf: true,
		},
		{
			name:      "same value at the same level merges",
			rules:     []Rule{proposes("A", "port", "3000", domain.OriginDeclaration), proposes("B", "port", "3000", domain.OriginDeclaration)},
			wantValue: "3000", wantOrig: domain.OriginDeclaration,
		},
		{
			name:      "override beats declaration",
			rules:     []Rule{proposes("A", "port", "3000", domain.OriginDeclaration)},
			overrides: map[string]string{"port": "8080"},
			wantValue: "8080", wantOrig: domain.OriginOverride,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := analyzeWith(syntheticCatalog(tt.rules...), empty, Input{Overrides: tt.overrides})
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantConf {
				if len(got.Conflicts) != 1 || got.Fields["port"].Value != "" {
					t.Errorf("want one conflict and no port, got conflicts=%d port=%+v", len(got.Conflicts), got.Fields["port"])
				}
				return
			}
			f := got.Fields["port"]
			if f.Value != tt.wantValue || f.Origin != tt.wantOrig {
				t.Errorf("port = {%q %s}, want {%q %s}", f.Value, f.Origin, tt.wantValue, tt.wantOrig)
			}
		})
	}
}

func TestResolverFillsRequiredFieldsAndOutcome(t *testing.T) {
	got, err := analyzeWith(syntheticCatalog(proposes("A", "profile", "P", domain.OriginInference)), fstest.MapFS{}, Input{})
	if err != nil {
		t.Fatal(err)
	}
	assertStrings(t, "generic pending", findingFields(got.Pending), []string{"port", "start_command"})
	if got.Outcome != domain.AnalysisNeedsInput {
		t.Errorf("outcome = %s, want %s", got.Outcome, domain.AnalysisNeedsInput)
	}
}

func TestEngineRejectsBadInput(t *testing.T) {
	cat := syntheticCatalog(proposes("A", "profile", "P", domain.OriginInference))
	if _, err := analyzeWith(cat, fstest.MapFS{}, Input{Overrides: map[string]string{"unknown": "x"}}); err == nil {
		t.Error("override for an unknown field must be an error")
	}
	if _, err := analyzeWith(cat, fstest.MapFS{}, Input{Overrides: map[string]string{"profile": "x"}}); err == nil {
		t.Error("override for a non-overridable field must be an error")
	}
	trespass := proposes("A", "port", "3000", domain.OriginDeclaration)
	trespass.Meta.Fields = []string{"start_command"}
	if _, err := analyzeWith(syntheticCatalog(trespass), fstest.MapFS{}, Input{}); err == nil {
		t.Error("a rule writing a field it does not own must be an error")
	}
}

func TestRulesGatingAndRetirement(t *testing.T) {
	called := map[string]bool{}
	gate := Rule{
		Meta: RuleMeta{ID: "GATE", Version: "1", State: RuleProposed},
		Apply: func(RuleInput) RuleOutput {
			return RuleOutput{Unsupported: []domain.Finding{{Message: "no"}}}
		},
	}
	dependent := Rule{
		Meta: RuleMeta{ID: "DEP", Version: "1", State: RuleProposed, Requires: []string{"GATE"}, Fields: []string{"port"}},
		Apply: func(RuleInput) RuleOutput {
			called["DEP"] = true
			return RuleOutput{}
		},
	}
	retired := Rule{
		Meta: RuleMeta{ID: "OLD", Version: "1", State: RuleRetired},
		Apply: func(RuleInput) RuleOutput {
			called["OLD"] = true
			return RuleOutput{}
		},
	}
	got, err := analyzeWith(syntheticCatalog(gate, dependent, retired), fstest.MapFS{}, Input{})
	if err != nil {
		t.Fatal(err)
	}
	if called["DEP"] || called["OLD"] {
		t.Errorf("skipped or retired rules must not run: %v", called)
	}
	assertStrings(t, "skipped", rulesWithStatus(got.Rules, "skipped"), []string{"DEP"})
	assertStrings(t, "retired", rulesWithStatus(got.Rules, "retired"), []string{"OLD"})
	if got.Outcome != domain.AnalysisUnsupported {
		t.Errorf("outcome = %s, want %s", got.Outcome, domain.AnalysisUnsupported)
	}
}

func TestCheckOverrides(t *testing.T) {
	valid := []map[string]string{
		nil,
		{},
		{"port": "3000"},
		{"port": " 3000 ", "start_command": "node server.js", "build_command": "npm run build"},
		{"execution_mode": "development"},
		{"execution_mode": "production"},
	}
	for _, in := range valid {
		if err := CheckOverrides(in); err != nil {
			t.Errorf("CheckOverrides(%v) = %v, want nil", in, err)
		}
	}

	invalid := map[string]map[string]string{
		"unknown field":          {"dockerfile": "FROM scratch"},
		"field without override": {"profile": "P-NODE"},
		"port out of range":      {"port": "70000"},
		"port not a number":      {"port": "exemplo"},
		"empty command":          {"start_command": "   "},
		"multi-line command":     {"start_command": "node a.js\nrm -rf /"},
		"trailing backslash would swallow the next Dockerfile line": {"build_command": "npm run build \\"},
		"nul in command":         {"start_command": "node a.js\x00"},
		"unknown execution mode": {"execution_mode": "staging"},
	}
	for name, in := range invalid {
		if err := CheckOverrides(in); err == nil {
			t.Errorf("%s: CheckOverrides(%v) = nil, want an error", name, in)
		}
	}
}

func scriptEvidence(pairs ...string) []domain.Evidence {
	var out []domain.Evidence
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, domain.Evidence{Kind: kindScript, Name: pairs[i], Value: pairs[i+1]})
	}
	return out
}

func optionValues(options []domain.Option) []string {
	var out []string
	for _, o := range options {
		out = append(out, o.Value)
	}
	return out
}

func TestStartOptions(t *testing.T) {
	tests := []struct {
		name        string
		scripts     []domain.Evidence
		wantOptions []string
		wantOmitted []string
	}{
		{"production scripts, sorted", scriptEvidence("serve", "node server.js", "prod", "node dist/index.js"),
			[]string{"npm run prod", "npm run serve"}, nil},
		{"production is listed before development", scriptEvidence("dev", "nodemon a.js", "serve", "node s.js", "a", "node a.js"),
			[]string{"npm run a", "npm run serve", "npm run dev"}, nil},
		{"test, lint, format, typecheck, clean and build are omitted by name, and reported",
			scriptEvidence("test", "jest", "test:unit", "jest unit", "lint", "eslint .", "format", "prettier .", "typecheck", "tsc", "clean", "rm -rf dist", "build", "tsc", "build:css", "sass a b", "serve", "node s.js"),
			[]string{"npm run serve"}, []string{"build", "build:css", "clean", "format", "lint", "test", "test:unit", "typecheck"}},
		{"a name that only shares the prefix is offered", scriptEvidence("devices", "node devices.js", "testimonials", "node t.js", "builder", "node b.js"),
			[]string{"npm run builder", "npm run devices", "npm run testimonials"}, nil},
		{"hooks of another script are omitted, a lone pre-name is offered",
			scriptEvidence("prestart", "node migrate.js", "poststart", "x", "start", "node a.js", "preserve", "node p.js", "serve", "node s.js", "prepare-db", "node p.js"),
			[]string{"npm run prepare-db", "npm run serve", "npm run start"}, []string{"poststart", "preserve", "prestart"}},
		{"names that are not shell-safe are omitted, not offered", scriptEvidence("a b", "node x.js", "x;rm", "node y.js", "$(id)", "node z.js", "ok:1", "node w.js"),
			[]string{"npm run ok:1"}, []string{"$(id)", "a b", "x;rm"}},
		{"no scripts", nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options, omitted := startOptions(tt.scripts)
			assertStrings(t, "options", optionValues(options), tt.wantOptions)
			assertStrings(t, "omitted", omitted, tt.wantOmitted)
		})
	}
}

func TestStartOptionKinds(t *testing.T) {
	options, _ := startOptions(scriptEvidence(
		"serve", "node server.js",
		"dev", "nodemon index.js", // ferramenta reconhecida no comando
		"watch", "node --watch index.js", // idem, por --watch
		"devish", "node index.js", // sem nome nem ferramenta: produção
		"develop", "node index.js", // o nome sozinho só sugere
		"start", "vite",
	))
	byValue := map[string]domain.Option{}
	for _, o := range options {
		byValue[o.Value] = o
	}

	for value, kind := range map[string]domain.OptionKind{
		"npm run serve":   domain.OptionProduction,
		"npm run dev":     domain.OptionDevelopment,
		"npm run watch":   domain.OptionDevelopment,
		"npm run devish":  domain.OptionProduction,
		"npm run develop": domain.OptionProduction,
		"npm run start":   domain.OptionDevelopment,
	} {
		if byValue[value].Kind != kind {
			t.Errorf("%s: kind %q, want %q", value, byValue[value].Kind, kind)
		}
	}
	if note := byValue["npm run dev"].Note; !strings.Contains(note, "nodemon") {
		t.Errorf("a classification from the command must name the tool, got %q", note)
	}
	if note := byValue["npm run start"].Note; !strings.Contains(note, "scripts.start") || !strings.Contains(note, "vite") {
		t.Errorf("start must say it caused the pending and why it is development, got %q", note)
	}
	if note := byValue["npm run serve"].Note; note != "" {
		t.Errorf("a production option carries no warning, got %q", note)
	}

	// Só o nome "dev", sem ferramenta reconhecida: desenvolvimento, mas o
	// motivo diz que é só o nome.
	options, _ = startOptions(scriptEvidence("dev", "node index.js"))
	if len(options) != 1 || options[0].Kind != domain.OptionDevelopment || !strings.Contains(options[0].Note, "nome") {
		t.Errorf("name-only classification must be flagged as such, got %+v", options)
	}
}

func TestStartPendingOffersScriptOptions(t *testing.T) {
	cases := map[string]struct {
		pkg  string
		want []string
	}{
		"missing start":    {`{"scripts":{"serve":"node server.js","dev":"nodemon a.js","test":"jest"}}`, []string{"npm run serve", "npm run dev"}},
		"dev-server start": {`{"scripts":{"start":"nodemon index.js","serve":"node server.js","test":"jest"}}`, []string{"npm run serve", "npm run start"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res, err := Analyze(toFS(nodeRepo(tc.pkg, nil)), Input{})
			if err != nil {
				t.Fatal(err)
			}
			var got *domain.Finding
			for i, f := range res.Pending {
				if f.Field == domain.FieldStartCommand {
					got = &res.Pending[i]
				}
			}
			if got == nil {
				t.Fatalf("no pending start_command: %+v", res.Pending)
			}
			assertStrings(t, "options", optionValues(got.Options), tc.want)
			assertStrings(t, "omitted", got.Omitted, []string{"test"})
		})
	}

	// Escolher uma opção é só informar o valor: passa pelo mesmo override.
	res, err := Analyze(toFS(nodeRepo(`{"scripts":{"serve":"node server.js"}}`, nil)),
		Input{Overrides: map[string]string{"start_command": "npm run serve", "port": "3000"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != domain.AnalysisReady || res.Fields[domain.FieldStartCommand].Value != "npm run serve" {
		t.Errorf("outcome %s, start_command %+v", res.Outcome, res.Fields[domain.FieldStartCommand])
	}
}

func TestStartPendingWithoutScriptsHasNoOptions(t *testing.T) {
	res, err := Analyze(toFS(nodeRepo(`{"name":"x"}`, nil)), Input{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Pending {
		if f.Field == domain.FieldStartCommand && (len(f.Options) != 0 || len(f.Omitted) != 0) {
			t.Errorf("unexpected options %v / omitted %v", f.Options, f.Omitted)
		}
	}
}

const startIndexPkg = `{"scripts":{"start":"node index.js"}}`

// Credenciais de teste são montadas em tempo de execução para não virarem
// segredo reconhecível no código-fonte do próprio repositório.
var (
	canaryAWS    = "AKIA" + "IOSFODNN7EXAMPLE"
	canaryGitHub = "ghp" + "_" + strings.Repeat("a1", 18)
	canarySlack  = "xoxb" + "-123456789012-abcdefghijkl"
	canaryStripe = "sk" + "_live_" + strings.Repeat("a1", 12)
	canaryGoogle = "AIza" + strings.Repeat("Ab1", 11) + "Zz"
	canaryPEM    = "-----BEGIN " + "RSA PRIVATE KEY-----"
	canaryJWT    = "eyJhbGciOiJIUzI1NiJ9" + "." + "eyJzdWIiOiIxMjM0NTY3ODkwIn0" + "." + "abc_DEF-123"
	canaryRandom = "q8Zr3Kp9Xv2Lm7Tn4Wd6Yb1Hc5Jf0Gs8"
)

func analyzeEnv(t *testing.T, extra map[string]string) domain.AnalysisResult {
	t.Helper()
	res, err := Analyze(toFS(nodeRepo(startIndexPkg, extra)), Input{Overrides: map[string]string{"port": "3000"}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func configVar(t *testing.T, res domain.AnalysisResult, name string) domain.ConfigVariable {
	t.Helper()
	for _, v := range res.ConfigVariables {
		if v.Name == name {
			return v
		}
	}
	t.Fatalf("variable %s not in the inventory: %+v", name, res.ConfigVariables)
	return domain.ConfigVariable{}
}

func TestCredentialKind(t *testing.T) {
	positives := map[string]string{
		"aws_access_key":  canaryAWS,
		"github_token":    canaryGitHub,
		"slack_token":     canarySlack,
		"stripe_live_key": canaryStripe,
		"google_api_key":  canaryGoogle,
		"private_key":     canaryPEM,
		"jwt":             canaryJWT,
	}
	for kind, value := range positives {
		if got := credentialKind("ANY_NAME", value); got != kind {
			t.Errorf("credentialKind(%s) = %q, want %q", kind, got, kind)
		}
	}
	// Entropia só vale para nome de aparência secreta.
	if got := credentialKind("SESSION_SECRET", canaryRandom); got != kindHighEntropy {
		t.Errorf("random value in a secret-like name = %q, want %q", got, kindHighEntropy)
	}
	for name, c := range map[string][2]string{
		"random value in an ordinary name":   {"APP_NAME", canaryRandom},
		"placeholder in a secret-like name":  {"API_TOKEN", "your-token-goes-here-please"},
		"short value in a secret-like name":  {"DB_PASSWORD", "hunter2"},
		"url in a secret-like name":          {"WEBHOOK_SECRET_URL", "https://hooks.example.com/services/abc/def"},
		"empty value":                        {"API_TOKEN", ""},
		"repeated characters (low entropy)":  {"API_TOKEN", strings.Repeat("a", 40)},
		"stripe test key is not a live leak": {"STRIPE_KEY", "sk" + "_test_" + strings.Repeat("a1", 12)},
	} {
		if got := credentialKind(c[0], c[1]); got != "" {
			t.Errorf("%s: credentialKind = %q, want none", name, got)
		}
	}
}

func TestClassifyVariable(t *testing.T) {
	for name, want := range map[string]domain.VarClass{
		"PORT": domain.ClassPlatform, "port": domain.ClassPlatform,
		"JWT_SECRET": domain.ClassGenerable, "SESSION_SECRET": domain.ClassGenerable, "SECRET_KEY_BASE": domain.ClassGenerable,
		"NEXTAUTH_SECRET": domain.ClassGenerable, "ENCRYPTION_KEY": domain.ClassGenerable,
		// Credenciais de terceiros nunca são geráveis.
		"STRIPE_SECRET_KEY": domain.ClassUser, "GITHUB_TOKEN": domain.ClassUser, "SMTP_PASSWORD": domain.ClassUser,
		"DB_PASSWORD": domain.ClassUser, "AWS_SECRET_ACCESS_KEY": domain.ClassUser, "API_KEY": domain.ClassUser,
		"DATABASE_URL": domain.ClassUser, "LOG_LEVEL": domain.ClassUser,
	} {
		if got := classifyVariable(name); got != want {
			t.Errorf("classifyVariable(%s) = %s, want %s", name, got, want)
		}
	}
}

func TestExampleStatesAndHints(t *testing.T) {
	res := analyzeEnv(t, map[string]string{
		".env.example": strings.Join([]string{
			"EMPTY_VAR=",
			"PLACEHOLDER_VAR=changeme",
			"ANGLE_VAR=<your token>",
			"NODE_ENV=production",
			"LOG_LEVEL='info'",
			"# COMMENTED_VAR=abc",
			"JWT_SECRET=dev-only-secret",
			"STRIPE_KEY=" + canaryStripe,
			"PORT=3000",
		}, "\n"),
		"src/server.js": "process.env.USED_NOT_DECLARED; process.env.PORT;",
	})

	type expect struct {
		class   domain.VarClass
		state   domain.ExampleState
		hint    string
		prefill string
	}
	for name, want := range map[string]expect{
		"EMPTY_VAR":         {domain.ClassUser, domain.ExampleEmpty, domain.HintLikelyOptional, ""},
		"PLACEHOLDER_VAR":   {domain.ClassUser, domain.ExamplePlaceholder, domain.HintLikelyRequired, ""},
		"ANGLE_VAR":         {domain.ClassUser, domain.ExamplePlaceholder, domain.HintLikelyRequired, ""},
		"NODE_ENV":          {domain.ClassUser, domain.ExampleDefault, domain.HintPrefilled, "production"},
		"LOG_LEVEL":         {domain.ClassUser, domain.ExampleDefault, domain.HintPrefilled, "info"},
		"COMMENTED_VAR":     {domain.ClassUser, domain.ExampleCommented, domain.HintCommented, ""},
		"JWT_SECRET":        {domain.ClassGenerable, domain.ExamplePlaceholder, domain.HintLikelyRequired, ""},
		"STRIPE_KEY":        {domain.ClassUser, domain.ExampleExposed, domain.HintLikelyRequired, ""},
		"PORT":              {domain.ClassPlatform, domain.ExampleDefault, "", "3000"},
		"USED_NOT_DECLARED": {domain.ClassUser, domain.ExampleAbsent, domain.HintUndeclared, ""},
	} {
		v := configVar(t, res, name)
		gotHint := ""
		if v.Hint != nil {
			gotHint = v.Hint.Code
		}
		if v.Class != want.class || v.Example != want.state || gotHint != want.hint || v.DefaultValue != want.prefill {
			t.Errorf("%s = {%s %s hint=%q default=%q}, want {%s %s hint=%q default=%q}",
				name, v.Class, v.Example, gotHint, v.DefaultValue, want.class, want.state, want.hint, want.prefill)
		}
	}
	if res.Outcome != domain.AnalysisReady {
		t.Errorf("hints and warnings must never block, outcome = %s", res.Outcome)
	}
}

func TestSecretLikeNamesAreNeverPrefilled(t *testing.T) {
	res := analyzeEnv(t, map[string]string{".env.example": "SMTP_PASSWORD=smtp-pass\nAPI_TOKEN=abc123\n"})
	for _, name := range []string{"SMTP_PASSWORD", "API_TOKEN"} {
		if v := configVar(t, res, name); v.DefaultValue != "" || v.Example != domain.ExamplePlaceholder {
			t.Errorf("%s = %+v, want a placeholder without a default value", name, v)
		}
	}
}

func TestHintsWithoutExampleFile(t *testing.T) {
	files := map[string]string{
		"src/server.js":  "process.env.RUNTIME_VAR;",
		"test/a.test.js": "process.env.TEST_ONLY_VAR;",
	}
	res := analyzeEnv(t, files)
	if h := configVar(t, res, "RUNTIME_VAR").Hint; h == nil || h.Code != domain.HintReferencedOnly {
		t.Errorf("runtime reference with no example file: hint = %+v", h)
	}
	if h := configVar(t, res, "TEST_ONLY_VAR").Hint; h != nil {
		t.Errorf("a reference only in tests deserves no hint, got %+v", h)
	}
}

func TestActiveDeclarationBeatsCommentedOne(t *testing.T) {
	res := analyzeEnv(t, map[string]string{".env.example": "# FEATURE=on\nFEATURE=\n", ".env.sample": "# OTHER=1\n"})
	if v := configVar(t, res, "FEATURE"); v.Example != domain.ExampleEmpty || !v.DeclaredInExample {
		t.Errorf("FEATURE = %+v, want the active (empty) declaration", v)
	}
	if v := configVar(t, res, "OTHER"); v.Example != domain.ExampleCommented || v.DeclaredInExample {
		t.Errorf("OTHER = %+v, want commented and not declared", v)
	}
}

func warningMessages(res domain.AnalysisResult) string {
	var parts []string
	for _, w := range res.Warnings {
		parts = append(parts, w.Message)
	}
	return strings.Join(parts, "\n")
}

func TestExposureWarnings(t *testing.T) {
	t.Run("a committed environment file warns even without credentials", func(t *testing.T) {
		res := analyzeEnv(t, map[string]string{".env": "NORMAL=1\n"})
		if got := warningMessages(res); !strings.Contains(got, ".env está versionado") || !strings.Contains(got, "Cobogó não usa") {
			t.Errorf("warnings = %q", got)
		}
	})
	t.Run("every real environment file name is covered", func(t *testing.T) {
		res := analyzeEnv(t, map[string]string{".env.local": "A=1", ".env.production": "A=1"})
		if len(res.Warnings) != 2 {
			t.Errorf("warnings = %q", warningMessages(res))
		}
	})
	t.Run("a credential in an example file warns with its location and kind", func(t *testing.T) {
		res := analyzeEnv(t, map[string]string{".env.example": "A=1\nGITHUB_TOKEN=" + canaryGitHub + "\n"})
		got := warningMessages(res)
		for _, want := range []string{"GITHUB_TOKEN", ".env.example:2", "token do GitHub", "rotacione"} {
			if !strings.Contains(got, want) {
				t.Errorf("warnings %q missing %q", got, want)
			}
		}
		if len(res.Warnings) != 1 {
			t.Errorf("want exactly one warning, got %q", got)
		}
	})
	t.Run("a commented credential is still an exposure", func(t *testing.T) {
		res := analyzeEnv(t, map[string]string{".env.example": "# AWS_ACCESS_KEY_ID=" + canaryAWS + "\n"})
		if got := warningMessages(res); !strings.Contains(got, "chave de acesso da AWS") {
			t.Errorf("warnings = %q", got)
		}
	})
	t.Run("a credential in a real file adds a warning on top of the file warning", func(t *testing.T) {
		res := analyzeEnv(t, map[string]string{".env": "SESSION_SECRET=" + canaryRandom + "\n"})
		if len(res.Warnings) != 2 {
			t.Errorf("warnings = %q", warningMessages(res))
		}
	})
	t.Run("clean example files produce no warning", func(t *testing.T) {
		res := analyzeEnv(t, map[string]string{".env.example": "A=\nSECRET=changeme\nURL=https://example.com\n"})
		if len(res.Warnings) != 0 {
			t.Errorf("warnings = %q", warningMessages(res))
		}
	})
	t.Run("warnings never block the analysis", func(t *testing.T) {
		res := analyzeEnv(t, map[string]string{".env": "X=1\n", ".env.example": "K=" + canaryAWS})
		if res.Outcome != domain.AnalysisReady {
			t.Errorf("outcome = %s", res.Outcome)
		}
	})
}

// O valor lido para classificar nunca sai da análise (DEC-24 item 8): nem em
// evidência, nem em aviso, nem na serialização completa do resultado.
func TestSecretValuesNeverLeaveTheAnalysis(t *testing.T) {
	canaries := []string{canaryAWS, canaryGitHub, canarySlack, canaryStripe, canaryGoogle, canaryJWT, canaryRandom, "real-db-password-value", "plain-real-value"}
	res := analyzeEnv(t, map[string]string{
		".env":            "DB_PASSWORD=real-db-password-value\nNORMAL=plain-real-value\nAWS=" + canaryAWS + "\nSESSION_SECRET=" + canaryRandom + "\n",
		".env.production": "TOKEN=" + canaryGitHub + "\n",
		".env.example":    "SLACK_TOKEN=" + canarySlack + "\nSTRIPE_KEY=" + canaryStripe + "\nGOOGLE_API_KEY=" + canaryGoogle + "\nJWT=" + canaryJWT + "\n# OLD_KEY=" + canaryAWS + "\n",
		"src/uses-env.js": "process.env.DB_PASSWORD; process.env.NORMAL;",
	})
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range canaries {
		if strings.Contains(string(raw), c) {
			t.Errorf("a value read from an environment file leaked into the analysis result: %q", c[:6]+"…")
		}
	}
	if len(res.Warnings) == 0 {
		t.Fatal("the fixture must raise warnings, otherwise the check proves nothing")
	}
}

func TestOnlyDefaultValuesFromExampleFilesAreKept(t *testing.T) {
	res := analyzeEnv(t, map[string]string{
		".env":         "NODE_ENV=from-the-real-file\n",
		".env.example": "NODE_ENV=production\n",
	})
	if v := configVar(t, res, "NODE_ENV"); v.DefaultValue != "production" {
		t.Errorf("default must come from the example file, got %q", v.DefaultValue)
	}
	for _, e := range res.Evidences {
		if e.Kind == kindEnvDefault && e.Source != ".env.example" {
			t.Errorf("a default value was taken from %s", e.Source)
		}
		if strings.Contains(e.Value, "from-the-real-file") {
			t.Errorf("a value from the real environment file leaked: %+v", e)
		}
	}
}

func TestEnvFilesAreNotFollowedThroughSymlinks(t *testing.T) {
	fsys := toFS(nodeRepo(startIndexPkg, nil))
	fsys[".env"] = &fstest.MapFile{Data: []byte("A=1"), Mode: fs.ModeSymlink}
	fsys[".env.example"] = &fstest.MapFile{Data: []byte("B=1"), Mode: fs.ModeSymlink}
	res, err := Analyze(fsys, Input{Overrides: map[string]string{"port": "3000"}})
	if err != nil {
		t.Fatal(err)
	}
	var gaps []string
	for _, g := range res.Coverage.Gaps {
		gaps = append(gaps, g.Path+":"+g.Reason)
	}
	assertStrings(t, "gaps", gaps, []string{".env:symlink", ".env.example:symlink"})
	if len(res.Warnings) != 0 || len(res.ConfigVariables) != 0 {
		t.Errorf("a symlink must not be read: warnings=%v vars=%v", res.Warnings, res.ConfigVariables)
	}
}
