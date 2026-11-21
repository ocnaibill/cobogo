package planner

import (
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func staticAnalysis(manager string, fields map[string]domain.Field, vars ...domain.ConfigVariable) domain.AnalysisResult {
	base := map[string]domain.Field{
		domain.FieldProfile:        {Value: domain.ProfileStatic},
		domain.FieldPackageManager: {Value: manager},
		domain.FieldPort:           {Value: domain.PlatformPort, Origin: domain.OriginPlatform},
		domain.FieldOutputDir:      {Value: "dist"},
		domain.FieldSPAFallback:    {Value: "true"},
		domain.FieldBuildCommand:   {Value: "vite build"},
	}
	for k, v := range fields {
		base[k] = v
	}
	return domain.AnalysisResult{Outcome: domain.AnalysisReady, Fields: base, ConfigVariables: vars,
		Evidences: []domain.Evidence{{Kind: "lockfile", Name: domain.ManagerBun, Source: "bun.lock"}}}
}

func TestStaticPlanBuildsWithNodeAndServesWithNginx(t *testing.T) {
	plan, err := FromAnalysis(staticAnalysis("npm", nil,
		domain.ConfigVariable{Name: "VITE_API_URL", Class: domain.ClassPublic},
		domain.ConfigVariable{Name: "DATABASE_URL", Class: domain.ClassUser}))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Profile != domain.ProfileStatic || plan.Port != 8080 || plan.Runtime != staticServerImage || plan.BuildImage != "node:26" {
		t.Errorf("plan = %+v", plan)
	}
	if len(plan.BuildArgs) != 1 || plan.BuildArgs[0] != "VITE_API_URL" {
		t.Errorf("build args = %v, want only the public variable", plan.BuildArgs)
	}
	for _, want := range []string{
		"FROM node:26 AS build",
		"RUN npm ci --include=dev",
		"ARG VITE_API_URL",
		"RUN vite build",
		"RUN test -f dist/index.html ||",
		"FROM " + staticServerImage,
		"'    location / { try_files $uri $uri/ /index.html; }'",
		"COPY --from=build /app/dist/ /srv/site/",
		"USER 101",
		"EXPOSE 8080",
	} {
		if !strings.Contains(plan.Dockerfile, want) {
			t.Errorf("Dockerfile lacks %q:\n%s", want, plan.Dockerfile)
		}
	}
	if strings.Contains(plan.Dockerfile, "DATABASE_URL") {
		t.Error("a non-public variable must never reach a static build")
	}
}

func TestStaticPlanWithBunAndNoSPA(t *testing.T) {
	plan, err := FromAnalysis(staticAnalysis("bun", map[string]domain.Field{
		domain.FieldSPAFallback:  {Value: "false"},
		domain.FieldBuildCommand: {Value: "astro build"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"FROM oven/bun:1 AS build", "COPY package.json bun.lock ./", "RUN bun install --frozen-lockfile\n", "try_files $uri $uri/ $uri.html =404;", "error_page 404 /404.html;"} {
		if !strings.Contains(plan.Dockerfile, want) {
			t.Errorf("Dockerfile lacks %q:\n%s", want, plan.Dockerfile)
		}
	}
}

func TestStaticPlanWithoutBuildServesTheRepository(t *testing.T) {
	plan, err := FromAnalysis(staticAnalysis(domain.ManagerNone, map[string]domain.Field{
		domain.FieldOutputDir:    {Value: "."},
		domain.FieldSPAFallback:  {Value: "false"},
		domain.FieldBuildCommand: {Value: ""},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Dockerfile, "AS build") || plan.InstallCommand != "" {
		t.Errorf("no build stage expected:\n%s", plan.Dockerfile)
	}
	// O filtro roda numa etapa à parte, antes do estágio final, para os
	// arquivos de infraestrutura nem existirem na imagem servida.
	filter, final, ok := strings.Cut(plan.Dockerfile, "\nFROM "+staticServerImage+"\nUSER root")
	if !ok || !strings.Contains(filter, "AS site") {
		t.Fatalf("expected a filter stage before the final stage:\n%s", plan.Dockerfile)
	}
	for _, name := range []string{"'.*' ! -name '.well-known'", "'Dockerfile*'", "'docker-compose*'", "'compose.y*ml'", "'package.json'", "'package-lock.json'", "'bun.lock'", "'yarn.lock'", "'pnpm-lock.yaml'", "'node_modules'"} {
		if !strings.Contains(filter, name) {
			t.Errorf("filter does not remove %s:\n%s", name, filter)
		}
	}
	if !strings.Contains(final, "COPY --from=site /site/ /srv/site/") || strings.Contains(final, "COPY . ") {
		t.Errorf("the final stage must copy only the filtered site:\n%s", final)
	}
	// Lista de exclusão, não de permissão: nenhum tipo de dado do site é filtrado.
	for _, kept := range []string{".csv", ".md", ".json'", ".geojson", ".txt", ".xml"} {
		if strings.Contains(filter, "'*"+kept) {
			t.Errorf("the filter must not remove data files by extension (%s)", kept)
		}
	}
}

func TestStaticPlanRefusesMissingPieces(t *testing.T) {
	if _, err := FromAnalysis(staticAnalysis("npm", map[string]domain.Field{domain.FieldOutputDir: {Value: ""}})); err == nil {
		t.Error("a static site without output dir must not plan")
	}
	if _, err := FromAnalysis(staticAnalysis("npm", map[string]domain.Field{domain.FieldBuildCommand: {Value: ""}})); err == nil {
		t.Error("an npm static site without build must not plan")
	}
}

func TestNginxDeniesDotFilesInEveryStaticPlan(t *testing.T) {
	for _, spa := range []bool{true, false} {
		conf := nginxConf(spa)
		if !strings.Contains(conf, `location ~ /\.(?!well-known) { return 404; }`) {
			t.Errorf("spa=%v: dot files must answer 404 except .well-known:\n%s", spa, conf)
		}
		// A regra tem de vir antes do location / para ganhar do fallback.
		if strings.Index(conf, "well-known") > strings.Index(conf, "location / {") {
			t.Errorf("spa=%v: the dot file rule must precede location /", spa)
		}
	}
}
