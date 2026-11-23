package planner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocnaibill/cobogo/internal/domain"
)

func TestPythonServerCommand(t *testing.T) {
	for _, tc := range []struct{ target, iface, want string }{
		{"app:app", "wsgi", `exec gunicorn --bind "0.0.0.0:$PORT" --workers "${WEB_CONCURRENCY:-2}" --access-logfile - --forwarded-allow-ips '*' app:app`},
		{"myapp:create_app()", "wsgi", `--forwarded-allow-ips '*' 'myapp:create_app()'`},
		{"api.main:app", "asgi", `exec uvicorn api.main:app --host 0.0.0.0 --port "$PORT" --proxy-headers --forwarded-allow-ips '*'`},
		{"api:make_app()", "asgi", `exec uvicorn --factory api:make_app --host`},
	} {
		if got := PythonServerCommand(tc.target, tc.iface); !strings.Contains(got, tc.want) {
			t.Errorf("PythonServerCommand(%s, %s) = %s\nwant it to contain %s", tc.target, tc.iface, got, tc.want)
		}
	}
}

func pythonResult(fields map[string]string, ev ...domain.Evidence) domain.AnalysisResult {
	res := domain.AnalysisResult{Outcome: domain.AnalysisReady, Fields: map[string]domain.Field{}, Evidences: ev}
	base := map[string]string{
		domain.FieldProfile: domain.ProfilePython, domain.FieldPackageManager: "pip", domain.FieldPythonVersion: "3.13",
		domain.FieldPort: "8080", domain.FieldExecutionMode: "production", domain.FieldAppTarget: "app:app",
		domain.FieldPythonInterface: "wsgi", domain.FieldPythonFramework: "flask",
	}
	for k, v := range fields {
		base[k] = v
	}
	for k, v := range base {
		if v != "" {
			res.Fields[k] = domain.Field{Value: v, Origin: domain.OriginInference}
		}
	}
	return res
}

func TestPythonDockerfile(t *testing.T) {
	plan, err := FromAnalysis(pythonResult(nil, domain.Evidence{Kind: "py.dependency", Name: "psycopg2"}))
	if err != nil {
		t.Fatal(err)
	}
	df := plan.Dockerfile
	for _, want := range []string{
		"FROM python:3.13 AS build\n", "RUN python -m venv /opt/venv\n", "RUN pip install -r requirements.txt\n",
		`RUN python -c "import gunicorn" 2>/dev/null || pip install gunicorn==` + domain.GunicornVersion,
		"\nFROM python:3.13-slim\n", "apt-get install -y --no-install-recommends libpq5",
		"COPY --from=build /opt/venv /opt/venv\n", "USER app\n", "EXPOSE 8080\n", `CMD ["sh", "-c", "exec gunicorn`,
	} {
		if !strings.Contains(df, want) {
			t.Errorf("Dockerfile sem %q:\n%s", want, df)
		}
	}
	if plan.Profile != domain.ProfilePython || plan.PythonVersion != "3.13" || plan.AppTarget != "app:app" || plan.Interface != "wsgi" {
		t.Errorf("plan = %+v", plan)
	}

	// uv, src e ASGI.
	plan, err = FromAnalysis(pythonResult(map[string]string{
		domain.FieldPackageManager: "uv", domain.FieldPythonVersion: "3.12", domain.FieldAppTarget: "api.main:app",
		domain.FieldPythonInterface: "asgi", domain.FieldPythonFramework: "fastapi",
	}, domain.Evidence{Kind: "py.src_layout"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pip install uv==" + domain.UVVersion, "uv sync --locked --no-dev --no-install-project", "uv pip install uvicorn==", "PYTHONPATH=/app/src", "FROM python:3.12-slim"} {
		if !strings.Contains(plan.Dockerfile, want) {
			t.Errorf("Dockerfile sem %q:\n%s", want, plan.Dockerfile)
		}
	}
	if strings.Contains(plan.Dockerfile, "apt-get") {
		t.Errorf("apt-get sem pacote de sistema:\n%s", plan.Dockerfile)
	}

	// Django: settings no ambiente e collectstatic com variáveis descartáveis só no RUN.
	plan, err = FromAnalysis(pythonResult(map[string]string{
		domain.FieldPythonFramework: "django", domain.FieldAppTarget: "mysite.wsgi:application",
		domain.FieldDjangoSettings: "mysite.settings", domain.FieldBuildCommand: "python manage.py collectstatic --noinput",
	},
		domain.Evidence{Kind: "django.setting", Name: "SECRET_KEY_ENV", Value: "DJANGO_SECRET_KEY", Source: "mysite/settings.py"},
		domain.Evidence{Kind: "env.reference", Name: "DATABASE_URL", Value: "required", Source: "mysite/settings.py"},
		domain.Evidence{Kind: "env.reference", Name: "OTHER", Value: "required", Source: "app/views.py"},
	))
	if err != nil {
		t.Fatal(err)
	}
	df = plan.Dockerfile
	if !strings.Contains(df, "RUN DATABASE_URL=cobogo-build-only-not-a-secret DJANGO_SECRET_KEY=cobogo-build-only-not-a-secret python manage.py collectstatic --noinput\n") {
		t.Errorf("collectstatic sem as variáveis do build:\n%s", df)
	}
	if strings.Contains(df, "OTHER=") {
		t.Errorf("variável fora do settings recebeu valor de build:\n%s", df)
	}
	if runtime := df[strings.Index(df, "\nFROM python:3.13-slim"):]; strings.Contains(runtime, "cobogo-build-only") || !strings.Contains(runtime, "DJANGO_SETTINGS_MODULE=mysite.settings") {
		t.Errorf("estágio de execução errado:\n%s", runtime)
	}

	// Procfile: o comando declarado, sem servidor da plataforma.
	plan, err = FromAnalysis(pythonResult(map[string]string{domain.FieldStartCommand: "gunicorn app:app --bind 0.0.0.0:$PORT"}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plan.Dockerfile, "import gunicorn") || plan.StartCommand != "gunicorn app:app --bind 0.0.0.0:$PORT" {
		t.Errorf("Procfile: %s\n%s", plan.StartCommand, plan.Dockerfile)
	}

	// gunicorn antes da 22: o setuptools com pkg_resources entra no ambiente.
	plan, err = FromAnalysis(pythonResult(map[string]string{domain.FieldStartCommand: "gunicorn app:app"}, domain.Evidence{Kind: "py.dependency", Name: "gunicorn", Value: "==20.1.0"}))
	if err != nil || !strings.Contains(plan.Dockerfile, `RUN pip install "setuptools<81"`) {
		t.Errorf("gunicorn 20 sem setuptools: %v\n%s", err, plan.Dockerfile)
	}
	if !domain.OldGunicorn("==21.2.0") || !domain.OldGunicorn("gunicorn==20.1.0") || domain.OldGunicorn("gunicorn==23.0.0") || domain.OldGunicorn("gunicorn>=20") {
		t.Error("OldGunicorn")
	}

	if _, err := FromAnalysis(pythonResult(map[string]string{domain.FieldPythonVersion: "3.9"})); err == nil {
		t.Error("3.9 aceita")
	}
}

func TestDjangoPlatformHostsDockerfile(t *testing.T) {
	res := pythonResult(map[string]string{
		domain.FieldPythonFramework: "django", domain.FieldAppTarget: "backend.wsgi:application",
		domain.FieldDjangoSettings: "backend.settings", domain.FieldDjangoPlatformHosts: "true",
		domain.FieldBuildCommand: "python manage.py collectstatic --noinput",
	})
	plan, err := FromAnalysis(res)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.DjangoPlatformHosts {
		t.Error("o plano não registrou o ajuste")
	}
	df := plan.Dockerfile
	runtime := df[strings.Index(df, "\nFROM python:3.13-slim"):]
	build := df[:strings.Index(df, "\nFROM python:3.13-slim")]
	if !strings.Contains(runtime, "base64 -d > /app/cobogo_platform_settings.py") || !strings.Contains(runtime, "ENV COBOGO_DJANGO_SETTINGS=backend.settings DJANGO_SETTINGS_MODULE=cobogo_platform_settings") {
		t.Errorf("a imagem de execução não usa o módulo de ajuste:\n%s", runtime)
	}
	if strings.Contains(build, "cobogo_platform_settings") {
		t.Errorf("o build (collectstatic) deve usar o settings do projeto:\n%s", build)
	}

	// Sem a resposta, ou fora do Django, nada é gerado.
	for _, fields := range []map[string]string{
		{domain.FieldPythonFramework: "django", domain.FieldDjangoSettings: "backend.settings", domain.FieldDjangoPlatformHosts: "false"},
		{domain.FieldPythonFramework: "django", domain.FieldDjangoSettings: "backend.settings"},
		{domain.FieldPythonFramework: "flask", domain.FieldDjangoPlatformHosts: "true"},
	} {
		plan, err := FromAnalysis(pythonResult(fields))
		if err != nil {
			t.Fatal(err)
		}
		if plan.DjangoPlatformHosts || strings.Contains(plan.Dockerfile, "cobogo_platform_settings") {
			t.Errorf("ajuste gerado sem pedido (%v):\n%s", fields, plan.Dockerfile)
		}
	}
}

// O módulo gerado, executado de verdade: acrescenta e nunca remove.
func TestDjangoPlatformSettingsModuleBehaviour(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 não instalado")
	}
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("projeto_settings.py", "ALLOWED_HOSTS = ['exemplo.edu.br', 'localhost']\nCSRF_TRUSTED_ORIGINS = ['https://exemplo.edu.br']\nDEBUG = False\nlower = 'ignorado'\n")
	write("coringa_settings.py", "ALLOWED_HOSTS = ['*']\n")
	write("cobogo_platform_settings.py", djangoPlatformSettings)
	run := func(original string, env ...string) string {
		cmd := exec.Command(py, "-c", "import cobogo_platform_settings as s; print(s.ALLOWED_HOSTS, getattr(s,'CSRF_TRUSTED_ORIGINS',None), getattr(s,'DEBUG',None), hasattr(s,'lower'))")
		cmd.Dir = dir
		cmd.Env = append([]string{"COBOGO_DJANGO_SETTINGS=" + original, "PATH=" + os.Getenv("PATH")}, env...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("python: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	got := run("projeto_settings", "COBOGO_PUBLIC_HOST=fec.cobogo.dev", "COBOGO_PUBLIC_URL=https://fec.cobogo.dev")
	if got != "['exemplo.edu.br', 'localhost', 'fec.cobogo.dev'] ['https://exemplo.edu.br', 'https://fec.cobogo.dev'] False False" {
		t.Errorf("com as variáveis: %s", got)
	}
	// Sem as variáveis da plataforma (um comando, por exemplo): o settings fica como está.
	if got := run("projeto_settings"); got != "['exemplo.edu.br', 'localhost'] ['https://exemplo.edu.br'] False False" {
		t.Errorf("sem variáveis: %s", got)
	}
	// Um curinga não é tocado.
	if got := run("coringa_settings", "COBOGO_PUBLIC_HOST=fec.cobogo.dev"); !strings.HasPrefix(got, "['*'] ") {
		t.Errorf("curinga: %s", got)
	}
}
