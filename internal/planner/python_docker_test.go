package planner_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocnaibill/cobogo/internal/analyzer"
	"github.com/ocnaibill/cobogo/internal/dockerrt"
	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/planner"
)

// Projetos Python de verdade, construídos e executados (DEC-48). Precisam de
// Docker e de acesso ao PyPI; o de uv precisa do uv no host para gerar o lock.

func writePyProject(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func deployPy(t *testing.T, rt *dockerrt.Runtime, dir string, overrides map[string]string, env map[string]string) (string, int, domain.Plan) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	res, err := analyzer.Analyze(os.DirFS(dir), analyzer.Input{Overrides: overrides})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planner.FromAnalysis(res)
	if err != nil {
		t.Fatalf("plan: %v (outcome %s, pending %+v, unsupported %+v)", err, res.Outcome, res.Pending, res.Unsupported)
	}
	name := strings.ToLower(strings.NewReplacer("/", "-", "_", "-").Replace(t.Name()))
	tag := "cobogo-test/planner:" + name
	var buildLog []string
	if _, err := rt.Build(ctx, dir, plan.Dockerfile, tag, plan.Runtime, func(l string) { buildLog = append(buildLog, l) }); err != nil {
		t.Fatalf("build: %v\nDockerfile:\n%s\nlog:\n%s", err, plan.Dockerfile, strings.Join(buildLog, "\n"))
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", tag).Run() })
	runEnv := map[string]string{"PORT": fmt.Sprint(plan.Port)}
	for k, v := range env {
		runEnv[k] = v
	}
	run, err := rt.Run(ctx, tag, "cobogo-test-"+name, plan.Port, runEnv, "planner-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Remove(context.Background(), run.ContainerID) })
	if _, err := dockerrt.ProbeReady(ctx, run.HostPort, plan.HealthPath, time.Minute, nil); err != nil {
		logs, _ := exec.Command("docker", "logs", run.ContainerID).CombinedOutput()
		t.Fatalf("probe: %v\nDockerfile:\n%s\ncontainer log:\n%s", err, plan.Dockerfile, logs)
	}
	return run.ContainerID, run.HostPort, plan
}

func getPath(t *testing.T, hostPort int, path string, host string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d%s", hostPort, path), nil)
	if host != "" {
		req.Host = host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// Flask sem gunicorn nas dependências: a plataforma o instala, roda como usuário
// sem privilégio e encerra rápido no SIGTERM.
func TestPythonFlaskWithPlatformServer(t *testing.T) {
	rt := requireDocker(t)
	dir := writePyProject(t, map[string]string{
		"requirements.txt": "flask==3.1.2\n",
		"app.py":           "import os\nfrom flask import Flask\n\napp = Flask(__name__)\n\n@app.get('/')\ndef index():\n    return 'flask ok ' + str(os.getuid())\n",
	})
	id, hostPort, plan := deployPy(t, rt, dir, nil, nil)
	if _, body := getPath(t, hostPort, "/", ""); body != "flask ok 10001" {
		t.Errorf("body = %q", body)
	}
	if !strings.Contains(plan.StartCommand, "gunicorn") {
		t.Errorf("start = %s", plan.StartCommand)
	}
	if out, _ := execIn(id, "sh", "-c", "command -v gcc || echo none"); out != "none" {
		t.Errorf("compilador na imagem de execução: %s", out)
	}
	start := time.Now()
	if out, err := exec.Command("docker", "stop", id).CombinedOutput(); err != nil {
		t.Fatalf("stop: %v %s", err, out)
	}
	if d := time.Since(start); d > 8*time.Second {
		t.Errorf("stop levou %s: o gunicorn não recebeu o SIGTERM", d)
	}
}

// FastAPI com uv, layout src e fábrica: uvicorn instalado pela plataforma com --factory.
func TestPythonFastAPIWithUVAndSrcLayout(t *testing.T) {
	rt := requireDocker(t)
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv não instalado no host")
	}
	dir := writePyProject(t, map[string]string{
		"pyproject.toml":      "[project]\nname = \"api\"\nversion = \"0.1.0\"\nrequires-python = \">=3.11\"\ndependencies = [\"fastapi==0.121.0\"]\n",
		"src/api/__init__.py": "",
		"src/api/main.py":     "import sys\nfrom fastapi import FastAPI\n\ndef create_app():\n    app = FastAPI()\n\n    @app.get('/')\n    def root():\n        return {'python': f'{sys.version_info[0]}.{sys.version_info[1]}'}\n\n    return app\n",
	})
	lock := exec.Command("uv", "lock", "--python", "3.13")
	lock.Dir = dir
	if out, err := lock.CombinedOutput(); err != nil {
		t.Fatalf("uv lock: %v\n%s", err, out)
	}
	_, hostPort, plan := deployPy(t, rt, dir, nil, nil)
	if _, body := getPath(t, hostPort, "/", ""); body != `{"python":"3.13"}` {
		t.Errorf("body = %q", body)
	}
	if plan.AppTarget != "api.main:create_app()" || !strings.Contains(plan.StartCommand, "--factory") {
		t.Errorf("plan target %s start %s", plan.AppTarget, plan.StartCommand)
	}
}

// Django de verdade: settings por variável, collectstatic no build com a chave
// descartável, estáticos do admin servidos pelo whitenoise.
func TestPythonDjangoWithStatic(t *testing.T) {
	rt := requireDocker(t)
	settings := `import os
from pathlib import Path
BASE_DIR = Path(__file__).resolve().parent.parent
SECRET_KEY = os.environ["DJANGO_SECRET_KEY"]
DEBUG = False
ALLOWED_HOSTS = os.environ.get("ALLOWED_HOSTS", "").split(",")
INSTALLED_APPS = ["django.contrib.admin", "django.contrib.auth", "django.contrib.contenttypes", "django.contrib.sessions", "django.contrib.messages", "django.contrib.staticfiles"]
MIDDLEWARE = [
    "django.middleware.security.SecurityMiddleware",
    "whitenoise.middleware.WhiteNoiseMiddleware",
    "django.contrib.sessions.middleware.SessionMiddleware",
    "django.middleware.common.CommonMiddleware",
    "django.middleware.csrf.CsrfViewMiddleware",
    "django.contrib.auth.middleware.AuthenticationMiddleware",
    "django.contrib.messages.middleware.MessageMiddleware",
]
ROOT_URLCONF = "mysite.urls"
TEMPLATES = [{"BACKEND": "django.template.backends.django.DjangoTemplates", "APP_DIRS": True, "OPTIONS": {"context_processors": ["django.template.context_processors.request", "django.contrib.auth.context_processors.auth", "django.contrib.messages.context_processors.messages"]}}]
WSGI_APPLICATION = "mysite.wsgi.application"
DATABASES = {"default": {"ENGINE": "django.db.backends.sqlite3", "NAME": BASE_DIR / "data" / "db.sqlite3"}}
STATIC_URL = "static/"
STATIC_ROOT = BASE_DIR / "staticfiles"
STORAGES = {"staticfiles": {"BACKEND": "whitenoise.storage.CompressedManifestStaticFilesStorage"}, "default": {"BACKEND": "django.core.files.storage.FileSystemStorage"}}
`
	dir := writePyProject(t, map[string]string{
		"requirements.txt":   "Django==5.2.8\nwhitenoise==6.11.0\n",
		"manage.py":          "import os, sys\nif __name__ == '__main__':\n    os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'mysite.settings')\n    from django.core.management import execute_from_command_line\n    execute_from_command_line(sys.argv)\n",
		"mysite/__init__.py": "",
		"mysite/settings.py": settings,
		"mysite/urls.py":     "from django.contrib import admin\nfrom django.http import HttpResponse\nfrom django.urls import path\nurlpatterns = [path('admin/', admin.site.urls), path('', lambda r: HttpResponse('django ok'))]\n",
		"mysite/wsgi.py":     "import os\nfrom django.core.wsgi import get_wsgi_application\nos.environ.setdefault('DJANGO_SETTINGS_MODULE', 'mysite.settings')\napplication = get_wsgi_application()\n",
		"data/.keep":         "",
	})
	_, hostPort, plan := deployPy(t, rt, dir, nil, map[string]string{"DJANGO_SECRET_KEY": "test-secret-key-for-the-run", "ALLOWED_HOSTS": "app.example,127.0.0.1"})
	if code, body := getPath(t, hostPort, "/", "app.example"); code != 200 || body != "django ok" {
		t.Errorf("GET / = %d %q", code, body)
	}
	if code, _ := getPath(t, hostPort, "/", "other.example"); code != 400 {
		t.Errorf("host fora do ALLOWED_HOSTS = %d, want 400", code)
	}
	if code, body := getPath(t, hostPort, "/static/admin/css/base.css", "app.example"); code != 200 || !strings.Contains(body, "body") {
		t.Errorf("estático do admin = %d", code)
	}
	if !strings.Contains(plan.Dockerfile, "DJANGO_SECRET_KEY=cobogo-build-only-not-a-secret python manage.py collectstatic") {
		t.Errorf("Dockerfile:\n%s", plan.Dockerfile)
	}
}

// psycopg2 compilado no build com a libpq do sistema; a execução recebe só a libpq5.
func TestPythonSystemPackage(t *testing.T) {
	rt := requireDocker(t)
	dir := writePyProject(t, map[string]string{
		"requirements.txt": "flask==3.1.2\npsycopg2==2.9.11\n",
		"app.py":           "import psycopg2\nfrom flask import Flask\napp = Flask(__name__)\n\n@app.get('/')\ndef index():\n    return 'libpq ' + str(psycopg2.__libpq_version__)\n",
	})
	_, hostPort, plan := deployPy(t, rt, dir, nil, nil)
	if _, body := getPath(t, hostPort, "/", ""); !strings.HasPrefix(body, "libpq ") {
		t.Errorf("body = %q", body)
	}
	if len(plan.SystemPackages) != 1 || plan.SystemPackages[0] != "libpq5" {
		t.Errorf("system packages = %v", plan.SystemPackages)
	}
}
