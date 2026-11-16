package planner

import (
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func readyAnalysis(fields map[string]domain.Field) domain.AnalysisResult {
	return domain.AnalysisResult{Outcome: domain.AnalysisReady, Fields: fields}
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
	if plan.Port != 3000 || plan.PortOrigin != "override" || plan.StartCommand != "node server.js" {
		t.Errorf("unexpected plan: %+v", plan)
	}
	for _, want := range []string{"RUN npm ci --omit=dev", "EXPOSE 3000", `CMD ["sh", "-c", "node server.js"]`, "USER node"} {
		if !strings.Contains(plan.Dockerfile, want) {
			t.Errorf("Dockerfile missing %q:\n%s", want, plan.Dockerfile)
		}
	}
	if strings.Contains(plan.Dockerfile, "npm prune") {
		t.Errorf("Dockerfile without build command must not prune:\n%s", plan.Dockerfile)
	}
}

func TestFromAnalysisWithBuildCommand(t *testing.T) {
	res := readyAnalysis(map[string]domain.Field{
		domain.FieldProfile:      {Value: "P-NODE"},
		domain.FieldStartCommand: {Value: "node dist/index.js"},
		domain.FieldBuildCommand: {Value: "npm run build"},
		domain.FieldPort:         {Value: "8080"},
	})
	plan, err := FromAnalysis(res)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"RUN npm ci\n", "RUN npm run build", "RUN npm prune --omit=dev"} {
		if !strings.Contains(plan.Dockerfile, want) {
			t.Errorf("Dockerfile missing %q:\n%s", want, plan.Dockerfile)
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
	}
	for name, res := range tests {
		if _, err := FromAnalysis(res); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
