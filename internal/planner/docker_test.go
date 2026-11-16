package planner_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/planner"
)

// Estes testes constroem e executam imagens de verdade: precisam de Docker, de
// npm no host (para gerar o lockfile) e de acesso ao registry do npm. Só rodam
// com COBOGO_TEST_DOCKER=1.
func requireDocker(t *testing.T) *dockerrt.Runtime {
	t.Helper()
	if os.Getenv("COBOGO_TEST_DOCKER") != "1" {
		t.Skip("defina COBOGO_TEST_DOCKER=1 para rodar testes que constroem imagens")
	}
	rt, err := dockerrt.New()
	if err != nil {
		t.Fatalf("conectar ao docker: %v", err)
	}
	return rt
}

func writeProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lock := exec.Command("npm", "install", "--package-lock-only", "--ignore-scripts", "--no-audit", "--no-fund")
	lock.Dir = dir
	if out, err := lock.CombinedOutput(); err != nil {
		t.Fatalf("gerar package-lock.json: %v\n%s", err, out)
	}
	return dir
}

// deploy analisa, planeja, constrói, executa e sonda o projeto; devolve o
// contêiner (já com limpeza registrada).
func deploy(t *testing.T, rt *dockerrt.Runtime, dir string, overrides map[string]string) (containerID string, hostPort int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	res, err := analyzer.Analyze(os.DirFS(dir), analyzer.Input{Overrides: overrides})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planner.FromAnalysis(res)
	if err != nil {
		t.Fatalf("plan: %v (outcome %s, pending %+v)", err, res.Outcome, res.Pending)
	}

	tag := "cobogo-test/planner:" + strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-"))
	var buildLog []string
	if _, err := rt.Build(ctx, dir, plan.Dockerfile, tag, plan.Runtime, func(l string) { buildLog = append(buildLog, l) }); err != nil {
		t.Fatalf("build: %v\nDockerfile:\n%s\nlog:\n%s", err, plan.Dockerfile, strings.Join(buildLog, "\n"))
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", tag).Run() })

	run, err := rt.Run(ctx, tag, "cobogo-test-"+strings.ToLower(strings.ReplaceAll(t.Name(), "/", "-")), plan.Port, map[string]string{"PORT": "3000"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Remove(context.Background(), run.ContainerID) })

	if err := dockerrt.ProbeReady(ctx, run.HostPort, "/", 200, time.Minute); err != nil {
		logs, _ := exec.Command("docker", "logs", run.ContainerID).CombinedOutput()
		t.Fatalf("probe: %v\ncontainer log:\n%s", err, logs)
	}
	return run.ContainerID, run.HostPort
}

func containerEnv(t *testing.T, id, name string) string {
	t.Helper()
	out, err := exec.Command("docker", "exec", id, "printenv", name).Output()
	if err != nil {
		t.Fatalf("printenv %s: %v", name, err)
	}
	return strings.TrimSpace(string(out))
}

// Ferramentas instaladas pelo npm (mkdirp no build, http-server em runtime)
// só estão em node_modules/.bin: o comando cru dos scripts precisa achá-las.
func TestProductionBuildAndStartFindToolsInNodeModulesBin(t *testing.T) {
	rt := requireDocker(t)
	dir := writeProject(t, map[string]string{
		"package.json": `{
  "name": "app", "version": "1.0.0",
  "scripts": {
    "build": "mkdirp dist && echo '<h1>built</h1>' > dist/index.html",
    "start": "http-server dist -p 3000 -a 0.0.0.0"
  },
  "dependencies": {"http-server": "14.1.1"},
  "devDependencies": {"mkdirp": "3.0.1"}
}`,
	})
	id, _ := deploy(t, rt, dir, map[string]string{"port": "3000"})

	if got := containerEnv(t, id, "NODE_ENV"); got != "production" {
		t.Errorf("NODE_ENV = %q, want production", got)
	}
	// O build usou uma devDependency; ela não deve sobrar na imagem final.
	if out, err := exec.Command("docker", "exec", id, "sh", "-c", "test -e node_modules/.bin/mkdirp").CombinedOutput(); err == nil {
		t.Errorf("devDependency mkdirp survived the prune: %s", out)
	}
}

func TestDevelopmentModeKeepsDevDependencies(t *testing.T) {
	rt := requireDocker(t)
	dir := writeProject(t, map[string]string{
		"package.json": `{
  "name": "app", "version": "1.0.0",
  "scripts": {"start": "http-server . -p 3000 -a 0.0.0.0"},
  "devDependencies": {"http-server": "14.1.1"}
}`,
	})
	id, _ := deploy(t, rt, dir, map[string]string{"port": "3000", "execution_mode": "development"})

	if got := containerEnv(t, id, "NODE_ENV"); got != "development" {
		t.Errorf("NODE_ENV = %q, want development", got)
	}
}
