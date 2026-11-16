package planner

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func readyAnalysis(fields map[string]domain.Field) domain.AnalysisResult {
	return domain.AnalysisResult{Outcome: domain.AnalysisReady, Fields: fields}
}

// cmdLine extrai o argumento do `CMD ["sh", "-c", ...]` interpretando-o como
// o JSON que o Docker lê.
func cmdLine(t *testing.T, dockerfile string) []string {
	t.Helper()
	for _, line := range strings.Split(dockerfile, "\n") {
		if rest, ok := strings.CutPrefix(line, "CMD "); ok {
			var argv []string
			if err := json.Unmarshal([]byte(rest), &argv); err != nil {
				t.Fatalf("CMD is not valid JSON: %q: %v", rest, err)
			}
			return argv
		}
	}
	t.Fatalf("no CMD in:\n%s", dockerfile)
	return nil
}

func order(t *testing.T, dockerfile string, parts ...string) {
	t.Helper()
	last := -1
	for _, p := range parts {
		i := strings.Index(dockerfile, p)
		if i < 0 {
			t.Fatalf("Dockerfile missing %q:\n%s", p, dockerfile)
		}
		if i < last {
			t.Errorf("%q must come after the previous step:\n%s", p, dockerfile)
		}
		last = i
	}
}

func TestFromAnalysis(t *testing.T) {
	res := readyAnalysis(map[string]domain.Field{
		domain.FieldProfile:      {Value: "P-NODE", Origin: domain.OriginInference},
		domain.FieldStartCommand: {Value: "node server.js", Origin: domain.OriginDeclaration},
		domain.FieldPort:         {Value: "3000", Origin: domain.OriginOverride},
	})
	plan, err := FromAnalysis(res)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Port != 3000 || plan.PortOrigin != "override" || plan.StartCommand != "node server.js" || plan.ExecutionMode != domain.ModeProduction {
		t.Errorf("unexpected plan: %+v", plan)
	}
	for _, want := range []string{
		"ENV NODE_ENV=production", "ENV PATH=/app/node_modules/.bin:$PATH",
		"RUN npm ci --omit=dev", "EXPOSE 3000", `CMD ["sh", "-c", "node server.js"]`, "USER node",
	} {
		if !strings.Contains(plan.Dockerfile, want) {
			t.Errorf("Dockerfile missing %q:\n%s", want, plan.Dockerfile)
		}
	}
	if strings.Contains(plan.Dockerfile, "npm prune") || strings.Contains(plan.Dockerfile, "--include=dev") {
		t.Errorf("a production image without build must not install dev dependencies or prune:\n%s", plan.Dockerfile)
	}
	if plan.InstallCommand != "npm ci --omit=dev" {
		t.Errorf("install command = %q", plan.InstallCommand)
	}
}

// Com NODE_ENV=production o npm omite devDependencies: o build precisa pedi-las
// de forma explícita, e só então podar.
func TestFromAnalysisWithBuildCommand(t *testing.T) {
	res := readyAnalysis(map[string]domain.Field{
		domain.FieldProfile:      {Value: "P-NODE"},
		domain.FieldStartCommand: {Value: "node dist/index.js"},
		domain.FieldBuildCommand: {Value: "vite build"},
		domain.FieldPort:         {Value: "8080"},
	})
	plan, err := FromAnalysis(res)
	if err != nil {
		t.Fatal(err)
	}
	order(t, plan.Dockerfile,
		"ENV NODE_ENV=production", "ENV PATH=", "RUN npm ci --include=dev", "COPY --chown=node:node . .",
		"RUN vite build", "RUN npm prune --omit=dev", "USER node", "CMD ")
	if plan.InstallCommand != "npm ci --include=dev" || plan.BuildCommand != "vite build" {
		t.Errorf("plan = %+v", plan)
	}
}

func TestDevelopmentModeInstallsEverythingAndSkipsBuild(t *testing.T) {
	res := readyAnalysis(map[string]domain.Field{
		domain.FieldProfile:       {Value: "P-NODE"},
		domain.FieldStartCommand:  {Value: "npm run dev"},
		domain.FieldBuildCommand:  {Value: "npm run build"},
		domain.FieldExecutionMode: {Value: "development", Origin: domain.OriginOverride},
		domain.FieldPort:          {Value: "5173"},
	})
	plan, err := FromAnalysis(res)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ExecutionMode != domain.ModeDevelopment || plan.BuildCommand != "" {
		t.Errorf("plan = %+v", plan)
	}
	for _, want := range []string{"ENV NODE_ENV=development", "RUN npm ci --include=dev", `CMD ["sh", "-c", "npm run dev"]`} {
		if !strings.Contains(plan.Dockerfile, want) {
			t.Errorf("Dockerfile missing %q:\n%s", want, plan.Dockerfile)
		}
	}
	for _, unwanted := range []string{"RUN npm run build", "npm prune", "NODE_ENV=production"} {
		if strings.Contains(plan.Dockerfile, unwanted) {
			t.Errorf("development image must not contain %q:\n%s", unwanted, plan.Dockerfile)
		}
	}
}

func TestExecutionModeDefaultsToProduction(t *testing.T) {
	res := readyAnalysis(map[string]domain.Field{
		domain.FieldProfile:      {Value: "P-NODE"},
		domain.FieldStartCommand: {Value: "node a.js"},
		domain.FieldPort:         {Value: "3000"},
	})
	plan, err := FromAnalysis(res)
	if err != nil || plan.ExecutionMode != domain.ModeProduction {
		t.Errorf("plan = %+v, %v", plan, err)
	}
}

// O CMD precisa ser JSON válido e devolver exatamente o comando, qualquer que
// seja o conteúdo (o %q do Go difere de JSON em alguns caracteres).
func TestCmdIsValidJSONForAnyCommand(t *testing.T) {
	for _, command := range []string{
		`node server.js`,
		`NODE_OPTIONS="--max-old-space-size=256" node "my app.js" --flag='x y'`,
		`node a.js && node b.js || echo "falhou" > /tmp/x`,
		`node -e "console.log('<tag> & \"quoted\" \\ backslash')"`,
		"node a.js\t--tab",
		`node ação-ünïcödé.js 日本語`,
		`echo $HOME ${PORT:-3000} $(date)`,
		"echo \u2028 separator",
	} {
		plan := BuildNodePlan(NodePlanInput{StartCommand: command, Port: 3000})
		argv := cmdLine(t, plan.Dockerfile)
		if len(argv) != 3 || argv[0] != "sh" || argv[1] != "-c" || argv[2] != command {
			t.Errorf("CMD for %q parsed as %q", command, argv)
		}
	}
}

func TestFromAnalysisRejects(t *testing.T) {
	ok := map[string]domain.Field{
		domain.FieldProfile:      {Value: "P-NODE"},
		domain.FieldStartCommand: {Value: "node index.js"},
		domain.FieldPort:         {Value: "3000"},
	}
	tests := map[string]domain.AnalysisResult{
		"analysis not ready": {Outcome: domain.AnalysisNeedsInput, Fields: ok},
		"unknown profile":    readyAnalysis(map[string]domain.Field{domain.FieldProfile: {Value: "P-PY"}, domain.FieldPort: {Value: "3000"}}),
		"invalid port":       readyAnalysis(map[string]domain.Field{domain.FieldProfile: {Value: "P-NODE"}, domain.FieldPort: {Value: "abc"}}),
		"unknown mode": readyAnalysis(map[string]domain.Field{
			domain.FieldProfile: {Value: "P-NODE"}, domain.FieldPort: {Value: "3000"}, domain.FieldExecutionMode: {Value: "staging"},
		}),
	}
	for name, res := range tests {
		if _, err := FromAnalysis(res); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
