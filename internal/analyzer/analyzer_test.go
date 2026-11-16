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
			name:      "start command override replaces a development server",
			files:     nodeRepo(`{"scripts":{"start":"nodemon index.js"}}`, nil),
			overrides: map[string]string{"start_command": "node index.js", "port": "3000"},
			want: want{
				outcome: domain.AnalysisReady,
				fields: map[string]wantField{
					domain.FieldProfile:        {value: "P-NODE", origin: domain.OriginInference},
					domain.FieldPackageManager: {value: "npm", origin: domain.OriginInference},
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

func TestParseEnvNames(t *testing.T) {
	data := `# comment
DATABASE_URL=postgres://user:secret@host/db
export JWT_SECRET="abc"
  PORT = 3000
lower_case=1
1BAD=2
not an assignment
DATABASE_URL=again
`
	got := parseEnvNames([]byte(data))
	assertStrings(t, "names", got, []string{"DATABASE_URL", "JWT_SECRET", "PORT", "lower_case"})
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
