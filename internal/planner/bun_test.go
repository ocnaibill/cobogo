package planner

import (
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func bunAnalysis(fields map[string]domain.Field, lockfile string) domain.AnalysisResult {
	base := map[string]domain.Field{
		domain.FieldProfile:        {Value: domain.ProfileBun},
		domain.FieldPackageManager: {Value: domain.ManagerBun},
		domain.FieldStartCommand:   {Value: "bun run src/server.ts"},
		domain.FieldPort:           {Value: domain.PlatformPort, Origin: domain.OriginPlatform},
	}
	for k, v := range fields {
		base[k] = v
	}
	return domain.AnalysisResult{
		Outcome:   domain.AnalysisReady,
		Fields:    base,
		Evidences: []domain.Evidence{{Kind: "lockfile", Name: domain.ManagerBun, Source: lockfile}},
	}
}

func TestBunPlanWithoutBuild(t *testing.T) {
	plan, err := FromAnalysis(bunAnalysis(nil, "bun.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Profile != domain.ProfileBun || plan.PackageManager != "bun" || plan.BunVersion != "1" || plan.NodeVersion != "" {
		t.Errorf("plan = %+v", plan)
	}
	if plan.BuildImage != "oven/bun:1" || plan.Runtime != "oven/bun:1-slim" {
		t.Errorf("images = %s / %s", plan.BuildImage, plan.Runtime)
	}
	for _, want := range []string{
		"FROM oven/bun:1 AS build",
		"COPY package.json bun.lock ./",
		"RUN bun install --frozen-lockfile --production",
		"FROM oven/bun:1-slim",
		"COPY --from=build --chown=bun:bun /app /app",
		"USER bun",
		"EXPOSE 8080",
		`CMD ["sh", "-c", "exec bun run src/server.ts"]`,
	} {
		if !strings.Contains(plan.Dockerfile, want) {
			t.Errorf("Dockerfile lacks %q:\n%s", want, plan.Dockerfile)
		}
	}
	if strings.Contains(plan.Dockerfile, "rm -rf node_modules") {
		t.Error("without a build there is nothing to reinstall")
	}
}

func TestBunPlanWithBuildPinnedVersionAndBinaryLock(t *testing.T) {
	plan, err := FromAnalysis(bunAnalysis(map[string]domain.Field{
		domain.FieldBuildCommand: {Value: "bun build ./src/index.ts --outdir dist --target bun"},
		domain.FieldStartCommand: {Value: "bun dist/index.js"},
		domain.FieldBunVersion:   {Value: "1.1.38"},
	}, "bun.lockb"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FROM oven/bun:1.1.38 AS build",
		"COPY package.json bun.lockb ./",
		"RUN bun install --frozen-lockfile\n",
		"RUN bun build ./src/index.ts --outdir dist --target bun",
		"RUN rm -rf node_modules && bun install --frozen-lockfile --production",
		"FROM oven/bun:1.1.38-slim",
	} {
		if !strings.Contains(plan.Dockerfile, want) {
			t.Errorf("Dockerfile lacks %q:\n%s", want, plan.Dockerfile)
		}
	}
}

func TestBunPlanDevelopmentSkipsBuildAndProductionInstall(t *testing.T) {
	plan, err := FromAnalysis(bunAnalysis(map[string]domain.Field{
		domain.FieldExecutionMode: {Value: domain.ModeDevelopment},
		domain.FieldBuildCommand:  {Value: "bun build x.ts"},
	}, "bun.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if plan.BuildCommand != "" || plan.InstallCommand != "bun install --frozen-lockfile" || !strings.Contains(plan.Dockerfile, "ENV NODE_ENV=development") {
		t.Errorf("development plan = %+v\n%s", plan, plan.Dockerfile)
	}
}

func TestCatalogImagesIncludeTheDefaultBun(t *testing.T) {
	images := strings.Join(CatalogImages(), " ")
	if !strings.Contains(images, "oven/bun:1 ") && !strings.HasSuffix(images, "oven/bun:1") || !strings.Contains(images, "oven/bun:1-slim") {
		t.Errorf("catalog images = %s", images)
	}
}

func TestGitHookPrepareIsDroppedAndAppDirIsWritable(t *testing.T) {
	withPrepare := func(res domain.AnalysisResult) domain.AnalysisResult {
		res.Evidences = append(res.Evidences, domain.Evidence{Kind: "npm.script", Name: "prepare", Value: "husky"})
		return res
	}
	bunPlan, err := FromAnalysis(withPrepare(bunAnalysis(nil, "bun.lock")))
	if err != nil {
		t.Fatal(err)
	}
	nodeRes := withPrepare(domain.AnalysisResult{Outcome: domain.AnalysisReady, Fields: map[string]domain.Field{
		domain.FieldProfile: {Value: domain.ProfileNode}, domain.FieldStartCommand: {Value: "node a.js"}, domain.FieldPort: {Value: "3000"},
	}})
	nodePlan, err := FromAnalysis(nodeRes)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ dockerfile, drop, chown, install string }{
		{bunPlan.Dockerfile, "RUN bun pm pkg delete scripts.prepare", "RUN chown bun:bun /app", "RUN bun install"},
		{nodePlan.Dockerfile, "RUN npm pkg delete scripts.prepare", "RUN chown node:node /app", "RUN npm ci"},
	} {
		if !strings.Contains(c.dockerfile, c.drop) || !strings.Contains(c.dockerfile, c.chown) {
			t.Errorf("Dockerfile lacks %q or %q:\n%s", c.drop, c.chown, c.dockerfile)
		}
		if strings.Index(c.dockerfile, c.drop) > strings.Index(c.dockerfile, c.install) {
			t.Errorf("prepare must be dropped before installing:\n%s", c.dockerfile)
		}
	}
	plain, _ := FromAnalysis(bunAnalysis(nil, "bun.lock"))
	if strings.Contains(plain.Dockerfile, "pkg delete") {
		t.Error("without a git-hook prepare nothing is dropped")
	}
}
