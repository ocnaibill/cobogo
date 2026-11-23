package domain

import (
	"regexp"
	"strconv"
)

// Python (DEC-48). Um perfil, P-PY; a interface (WSGI ou ASGI) é um campo, não um
// perfil: a imagem é a mesma e só o servidor muda.

const (
	// FieldEcosystem decide qual catálogo analisa a pasta quando há marcadores de
	// mais de um ecossistema (DEC-48 item 1): a resposta à pergunta F-6.
	FieldEcosystem = "ecosystem"

	FieldPythonVersion   = "python_version"
	FieldPythonInterface = "python_interface"
	FieldAppTarget       = "app_target"
	FieldPythonFramework = "python_framework"
	FieldDjangoSettings  = "django_settings"
	// FieldDjangoPlatformHosts: a plataforma acrescenta o endereço do projeto a
	// ALLOWED_HOSTS e CSRF_TRUSTED_ORIGINS, num módulo gerado na imagem (DEC-49 item 6).
	FieldDjangoPlatformHosts = "django_platform_hosts"
	// FieldProcessWorker e FieldProcessScheduler são o comando do processo worker e do agendador
	// do projeto (DEC-51); "none" os desliga.
	FieldProcessWorker    = "process_worker"
	FieldProcessScheduler = "process_scheduler"
)

// Ecossistemas.
const (
	EcosystemJavaScript = "javascript"
	EcosystemPython     = "python"
)

const (
	ProfilePython = "P-PY"

	ManagerPip = "pip"
	ManagerUV  = "uv"

	InterfaceWSGI = "wsgi"
	InterfaceASGI = "asgi"
)

// Frameworks reconhecidos (informativo; o Django tem contrato próprio).
const (
	FrameworkDjango    = "django"
	FrameworkFlask     = "flask"
	FrameworkFastAPI   = "fastapi"
	FrameworkStarlette = "starlette"
	FrameworkQuart     = "quart"
	FrameworkLitestar  = "litestar"
	FrameworkFalcon    = "falcon"
	FrameworkOther     = "outro"
)

// SupportedPythonVersions são as versões que o catálogo aceita (decisão do autor,
// DEC-48). A 3.10 saiu de suporte em outubro de 2026: vale, com aviso.
var SupportedPythonVersions = []string{"3.10", "3.11", "3.12", "3.13", "3.14", "3.15"}

const DefaultPythonVersion = "3.13"

// UnsupportedUpstreamPython são as versões do catálogo que o projeto Python já não
// corrige (fim de suporte): a escolha vale, com aviso.
var UnsupportedUpstreamPython = map[string]string{"3.10": "outubro de 2026"}

// Servidores que a plataforma instala quando o projeto não os declara (DEC-48,
// decisão 2), em versões fixadas (estáveis há algumas semanas em 22/11/2026). O uv é
// instalado pelo pip no estágio de build, só quando o projeto usa uv.
const (
	GunicornVersion = "26.2.0"
	UvicornVersion  = "0.54.0"
	UVVersion       = "0.13.7"
)

// LegacySetuptoolsSpec é a faixa do setuptools que ainda traz o pkg_resources,
// instalada quando o projeto fixa um gunicorn antes da 22 (que o importa).
const LegacySetuptoolsSpec = "<81"

var pinnedVersion = regexp.MustCompile(`===?\s*(\d+)`)

// OldGunicorn diz se a especificação fixa um gunicorn antes da 22.
func OldGunicorn(spec string) bool {
	m := pinnedVersion.FindStringSubmatch(spec)
	if m == nil {
		return false
	}
	major, err := strconv.Atoi(m[1])
	return err == nil && major < 22
}

// ProcessNone desliga um processo proposto pela análise (override de process_worker ou process_scheduler).
const ProcessNone = "none"

// Tipos de processo de um projeto além do web (DEC-51).
const (
	ProcessWorker    = "worker"
	ProcessScheduler = "scheduler"
)
