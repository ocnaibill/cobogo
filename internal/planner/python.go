package planner

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Perfil P-PY (DEC-48 itens 6 a 8). O build roda na imagem Python completa
// (compilador e cabeçalhos para os pacotes sem wheel) e cria um ambiente virtual em
// /opt/venv; a execução, na variante slim da mesma versão e distribuição, recebe o
// ambiente e o código e roda como usuário sem privilégio.
func pythonBuildImage(version string) string   { return "python:" + version }
func pythonRuntimeImage(version string) string { return "python:" + version + "-slim" }

// pythonUser é o usuário sem privilégio da execução (a imagem slim não traz um).
const pythonUser = "app"
const pythonUID = 10001

// systemPackages é o catálogo fechado de pacotes de sistema de execução que uma
// dependência exige (DEC-48 item 7). Nada de apt-get vindo do repositório.
var systemPackages = map[string]string{
	"psycopg2":    "libpq5",      // o driver sem -binary liga na libpq do sistema
	"mysqlclient": "libmariadb3", // a biblioteca cliente do MariaDB/MySQL
	"python-ldap": "libldap2",    // OpenLDAP
	"pyodbc":      "unixodbc",    // ODBC
	"cairocffi":   "libcairo2",   // Cairo
	"weasyprint":  "libpango-1.0-0",
}

// PythonPlanInput são os campos já resolvidos pelo analisador para P-PY.
type PythonPlanInput struct {
	Version        string
	Manager        string // pip ou uv
	AppTarget      string // modulo:atributo ou modulo:fabrica()
	Interface      string // wsgi ou asgi
	Framework      string
	DjangoSettings string
	StartCommand   string // vazio: o servidor de produção do perfil
	BuildCommand   string
	Mode           string
	HealthPath     string
	Port           int
	PortOrigin     string
	// PlatformHosts gera, na imagem, o módulo que acrescenta o endereço do projeto às
	// listas de hosts e de origens do Django (DEC-49 item 6).
	PlatformHosts bool
	// Processes são os processos além do web (worker e agendador, DEC-51).
	Processes []domain.PlanProcess
	// Release é a fase de release (DEC-52 item 4).
	Release string
	// SrcLayout põe /app/src no PYTHONPATH.
	SrcLayout bool
	// SystemPackages são os pacotes de execução do catálogo que as dependências pedem.
	SystemPackages []string
	// LegacySetuptools instala o setuptools com pkg_resources: um gunicorn antes da 22
	// o importa, e o venv do Python 3.12+ não traz o setuptools.
	LegacySetuptools bool
	// BuildEnv são variáveis só do build (nome → valor descartável), para o settings
	// do Django carregar no collectstatic. Nunca entram na imagem final.
	BuildEnv map[string]string
}

func pythonFromAnalysis(res domain.AnalysisResult, port int, portOrigin, mode string) (domain.Plan, error) {
	f := func(name string) string { return res.Fields[name].Value }
	version := f(domain.FieldPythonVersion)
	if version == "" {
		version = domain.DefaultPythonVersion
	}
	if !slices.Contains(domain.SupportedPythonVersions, version) {
		return domain.Plan{}, fmt.Errorf("planner: versão de Python %q não suportada", version)
	}
	manager := f(domain.FieldPackageManager)
	if manager != domain.ManagerPip && manager != domain.ManagerUV {
		return domain.Plan{}, fmt.Errorf("planner: gerenciador %q não suportado em P-PY", manager)
	}
	target := f(domain.FieldAppTarget)
	if target == "" && f(domain.FieldStartCommand) == "" {
		return domain.Plan{}, fmt.Errorf("planner: P-PY sem alvo nem comando de início")
	}
	platformHosts := f(domain.FieldDjangoPlatformHosts) == "true" && f(domain.FieldPythonFramework) == domain.FrameworkDjango && f(domain.FieldDjangoSettings) != ""
	var processes []domain.PlanProcess
	for _, p := range []struct{ kind, field string }{{domain.ProcessWorker, domain.FieldProcessWorker}, {domain.ProcessScheduler, domain.FieldProcessScheduler}} {
		if cmd := f(p.field); cmd != "" && cmd != domain.ProcessNone {
			processes = append(processes, domain.PlanProcess{Name: p.kind, Kind: p.kind, Command: cmd})
		}
	}
	release := f(domain.FieldReleaseCommand)
	if release == domain.ProcessNone {
		release = ""
	}
	in := PythonPlanInput{
		Release:       release,
		Processes:     processes,
		PlatformHosts: platformHosts,
		Version:       version, Manager: manager, AppTarget: target,
		Interface: f(domain.FieldPythonInterface), Framework: f(domain.FieldPythonFramework),
		DjangoSettings: f(domain.FieldDjangoSettings), StartCommand: f(domain.FieldStartCommand),
		BuildCommand: f(domain.FieldBuildCommand), Mode: mode, HealthPath: f(domain.FieldHealthPath),
		Port: port, PortOrigin: portOrigin,
	}
	deps := map[string]bool{}
	required := map[string]bool{}
	settingsFiles := map[string]bool{}
	for _, e := range res.Evidences {
		switch e.Kind {
		case "py.dependency":
			deps[e.Name] = true
			if e.Name == "gunicorn" && domain.OldGunicorn(e.Value) {
				in.LegacySetuptools = true
			}
		case "py.src_layout":
			in.SrcLayout = true
		case "django.setting", "django.app":
			settingsFiles[e.Source] = true
		}
	}
	for _, e := range res.Evidences {
		// Variável lida sem padrão no settings: o build recebe um valor descartável,
		// senão o settings não carrega no collectstatic.
		if e.Kind == "env.reference" && e.Value == "required" && settingsFiles[e.Source] {
			required[e.Name] = true
		}
		if e.Kind == "django.setting" && e.Name == "SECRET_KEY_ENV" {
			required[e.Value] = true
		}
	}
	for dep, pkg := range systemPackages {
		if deps[dep] {
			in.SystemPackages = append(in.SystemPackages, pkg)
		}
	}
	slices.Sort(in.SystemPackages)
	if in.Framework == domain.FrameworkDjango && in.BuildCommand != "" {
		in.BuildEnv = map[string]string{}
		for name := range required {
			if validEnvName.MatchString(name) && name != "PATH" && !strings.HasPrefix(name, "PYTHON") {
				in.BuildEnv[name] = "cobogo-build-only-not-a-secret"
			}
		}
	}
	return BuildPythonPlan(in), nil
}

var validEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// PythonServerCommand é o comando do servidor de produção que a plataforma monta
// para o alvo (DEC-48 item 6): gunicorn para WSGI, uvicorn para ASGI, sempre na
// porta da plataforma e com exec, para o servidor receber o SIGTERM.
func PythonServerCommand(target, iface string) string {
	if iface == domain.InterfaceASGI {
		args := []string{"exec uvicorn"}
		if strings.HasSuffix(target, "()") {
			target = strings.TrimSuffix(target, "()")
			args = append(args, "--factory")
		}
		args = append(args, shellQuote(target), "--host 0.0.0.0 --port \"$PORT\"", "--proxy-headers --forwarded-allow-ips '*'")
		return strings.Join(args, " ")
	}
	return "exec gunicorn --bind \"0.0.0.0:$PORT\" --workers \"${WEB_CONCURRENCY:-2}\" --access-logfile - --forwarded-allow-ips '*' " + shellQuote(target)
}

// pythonServer é o pacote do servidor de produção da interface e a versão fixada
// que a plataforma instala quando o projeto não o traz.
func pythonServer(iface string) (string, string) {
	if iface == domain.InterfaceASGI {
		return "uvicorn", domain.UvicornVersion
	}
	return "gunicorn", domain.GunicornVersion
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_.:/=-]+$`)

func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// BuildPythonPlan monta o plano e o Dockerfile do perfil P-PY.
func BuildPythonPlan(in PythonPlanInput) domain.Plan {
	if in.Mode == "" {
		in.Mode = domain.ModeProduction
	}
	if in.Version == "" {
		in.Version = domain.DefaultPythonVersion
	}
	if in.HealthPath == "" {
		in.HealthPath = "/"
	}
	if in.Interface == "" {
		in.Interface = domain.InterfaceWSGI
	}
	start := in.StartCommand
	if start == "" {
		start = PythonServerCommand(in.AppTarget, in.Interface)
	}
	install := "pip install -r requirements.txt"
	if in.Manager == domain.ManagerUV {
		install = "uv sync --locked --no-dev --no-install-project"
	}
	return domain.Plan{
		Profile:        domain.ProfilePython,
		Runtime:        pythonRuntimeImage(in.Version),
		BuildImage:     pythonBuildImage(in.Version),
		PythonVersion:  in.Version,
		PackageManager: in.Manager,
		InstallCommand: install,
		BuildCommand:   in.BuildCommand,
		StartCommand:   start,
		ExecutionMode:  in.Mode,
		Port:           in.Port,
		PortOrigin:     in.PortOrigin,
		HealthPath:     in.HealthPath,
		AppTarget:      in.AppTarget,
		Interface:      in.Interface,
		SystemPackages: in.SystemPackages,
		Dockerfile:     renderPythonDockerfile(in, install, start),

		Processes:           in.Processes,
		DjangoPlatformHosts: in.PlatformHosts && in.DjangoSettings != "",
		ReleaseCommand:      in.Release,
	}
}

func renderPythonDockerfile(in PythonPlanInput, install, start string) string {
	var env []string
	env = append(env, "PYTHONUNBUFFERED=1", "PYTHONDONTWRITEBYTECODE=1", "VIRTUAL_ENV=/opt/venv", "PATH=/opt/venv/bin:$PATH")
	if in.SrcLayout {
		env = append(env, "PYTHONPATH=/app/src")
	}
	if in.Framework == domain.FrameworkDjango && in.DjangoSettings != "" {
		env = append(env, "DJANGO_SETTINGS_MODULE="+in.DjangoSettings)
	}
	// Um uvicorn do Procfile sem --host ouviria só em 127.0.0.1: estas variáveis
	// valem como as opções dele, e as opções na linha de comando vencem.
	env = append(env, "UVICORN_HOST=0.0.0.0", fmt.Sprintf("UVICORN_PORT=%d", in.Port))
	common := "WORKDIR /app\nENV " + strings.Join(env, " \\\n    ") + "\n"

	var b strings.Builder
	fmt.Fprintf(&b, "FROM %s AS build\n", pythonBuildImage(in.Version))
	b.WriteString("ENV PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_NO_CACHE_DIR=1 PIP_ROOT_USER_ACTION=ignore\n")
	b.WriteString("RUN python -m venv /opt/venv\n")
	b.WriteString(common)
	// O código inteiro antes da instalação: um requirements pode incluir outros
	// arquivos (-r requirements/base.txt) em qualquer pasta.
	b.WriteString("COPY . .\n")
	server, version := pythonServer(in.Interface)
	if in.Manager == domain.ManagerUV {
		fmt.Fprintf(&b, "RUN pip install uv==%s\n", domain.UVVersion)
		b.WriteString("ENV UV_PROJECT_ENVIRONMENT=/opt/venv UV_PYTHON_DOWNLOADS=never UV_PYTHON=/opt/venv/bin/python UV_LINK_MODE=copy UV_NO_CACHE=1\n")
		fmt.Fprintf(&b, "RUN %s\n", install)
		// O servidor de produção, se o projeto não o trouxe (DEC-48 item 6).
		if in.StartCommand == "" {
			fmt.Fprintf(&b, "RUN python -c \"import %s\" 2>/dev/null || uv pip install %s==%s\n", server, server, version)
		}
	} else {
		fmt.Fprintf(&b, "RUN %s\n", install)
		if in.StartCommand == "" {
			fmt.Fprintf(&b, "RUN python -c \"import %s\" 2>/dev/null || pip install %s==%s\n", server, server, version)
		}
	}
	if in.LegacySetuptools {
		if in.Manager == domain.ManagerUV {
			fmt.Fprintf(&b, "RUN uv pip install \"setuptools%s\"\n", domain.LegacySetuptoolsSpec)
		} else {
			fmt.Fprintf(&b, "RUN pip install \"setuptools%s\"\n", domain.LegacySetuptoolsSpec)
		}
	}
	if in.BuildCommand != "" {
		names := make([]string, 0, len(in.BuildEnv))
		for name := range in.BuildEnv {
			names = append(names, name)
		}
		slices.Sort(names)
		var assigns []string
		for _, name := range names {
			assigns = append(assigns, name+"="+shellQuote(in.BuildEnv[name]))
		}
		prefix := ""
		if len(assigns) > 0 {
			// Variáveis descartáveis, só deste RUN: não ficam na imagem.
			prefix = strings.Join(assigns, " ") + " "
		}
		fmt.Fprintf(&b, "RUN %s%s\n", prefix, in.BuildCommand)
	}

	fmt.Fprintf(&b, "\nFROM %s\n", pythonRuntimeImage(in.Version))
	if len(in.SystemPackages) > 0 {
		fmt.Fprintf(&b, "RUN apt-get update && apt-get install -y --no-install-recommends %s && rm -rf /var/lib/apt/lists/*\n", strings.Join(in.SystemPackages, " "))
	}
	b.WriteString(common)
	fmt.Fprintf(&b, "RUN useradd --uid %d --no-create-home --home-dir /app --shell /usr/sbin/nologin %s && chown %s:%s /app\n", pythonUID, pythonUser, pythonUser, pythonUser)
	b.WriteString("COPY --from=build /opt/venv /opt/venv\n")
	fmt.Fprintf(&b, "COPY --from=build --chown=%s:%s /app /app\n", pythonUser, pythonUser)
	if in.PlatformHosts && in.DjangoSettings != "" {
		// O módulo de ajuste só existe na imagem de execução: o build (collectstatic) usa o
		// settings do projeto como está. O DJANGO_SETTINGS_MODULE passa a apontar para ele.
		encoded := base64.StdEncoding.EncodeToString([]byte(djangoPlatformSettings))
		fmt.Fprintf(&b, "RUN echo %s | base64 -d > /app/%s.py && chown %s:%s /app/%s.py\n", encoded, platformSettingsModule, pythonUser, pythonUser, platformSettingsModule)
		fmt.Fprintf(&b, "ENV COBOGO_DJANGO_SETTINGS=%s DJANGO_SETTINGS_MODULE=%s\n", in.DjangoSettings, platformSettingsModule)
	}
	fmt.Fprintf(&b, "USER %s\n", pythonUser)
	fmt.Fprintf(&b, "EXPOSE %d\n", in.Port)
	cmd := start
	if in.StartCommand != "" {
		cmd = ShellStart(start)
	}
	fmt.Fprintf(&b, "CMD [\"sh\", \"-c\", %s]\n", jsonString(cmd))
	return b.String()
}

// platformSettingsModule é o módulo gerado na imagem (DEC-49 item 6).
const platformSettingsModule = "cobogo_platform_settings"

// djangoPlatformSettings importa o settings do projeto e ACRESCENTA o endereço público
// do projeto às listas de hosts e de origens de CSRF. Nada que o projeto definiu é
// removido; sem as variáveis da plataforma (um comando, por exemplo), não muda nada.
const djangoPlatformSettings = `# Gerado pelo Cobogó (DEC-49): importa o settings do projeto e acrescenta o endereço
# público do projeto a ALLOWED_HOSTS e CSRF_TRUSTED_ORIGINS. Nada é removido.
import importlib
import os

_original = importlib.import_module(os.environ["COBOGO_DJANGO_SETTINGS"])
globals().update({_k: _v for _k, _v in vars(_original).items() if _k.isupper()})

_host = os.environ.get("COBOGO_PUBLIC_HOST", "")
_url = os.environ.get("COBOGO_PUBLIC_URL", "")
if _host:
    _hosts = list(globals().get("ALLOWED_HOSTS", []))
    if "*" not in _hosts and _host not in _hosts:
        _hosts.append(_host)
    ALLOWED_HOSTS = _hosts
if _url:
    _origins = list(globals().get("CSRF_TRUSTED_ORIGINS", []))
    if _url not in _origins:
        _origins.append(_url)
    CSRF_TRUSTED_ORIGINS = _origins
`
