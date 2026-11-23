package domain

import "strings"

// Plan é o conjunto de decisões executáveis de um deploy, conforme §11.1.
// Não carrega valores secretos.
type Plan struct {
	Profile        string `json:"profile"`
	Runtime        string `json:"runtime"`
	BuildImage     string `json:"build_image"`
	NodeVersion    string `json:"node_version,omitempty"`
	BunVersion     string `json:"bun_version,omitempty"`
	PackageManager string `json:"package_manager"`
	InstallCommand string `json:"install_command"`
	BuildCommand   string `json:"build_command,omitempty"`
	StartCommand   string `json:"start_command"`
	ExecutionMode  string `json:"execution_mode"`
	Port           int    `json:"port"`
	PortOrigin     string `json:"port_origin"`
	HealthPath     string `json:"health_path"`
	Dockerfile     string `json:"dockerfile"`
	// Perfil estático (DEC-37): diretório servido, fallback de SPA e as
	// variáveis públicas que entram no build como argumentos.
	OutputDir   string   `json:"output_dir,omitempty"`
	SPAFallback bool     `json:"spa_fallback,omitempty"`
	BuildArgs   []string `json:"build_args,omitempty"`
	// Perfil Python (DEC-48): a versão, o alvo que o servidor de produção carrega,
	// a interface (wsgi/asgi) e os pacotes de sistema do catálogo.
	PythonVersion  string   `json:"python_version,omitempty"`
	AppTarget      string   `json:"app_target,omitempty"`
	Interface      string   `json:"interface,omitempty"`
	SystemPackages []string `json:"system_packages,omitempty"`
	// Processes são os processos além do web (DEC-51): worker e agendador, da mesma imagem, sem
	// porta nem rota, com as mesmas variáveis, volumes e serviços.
	Processes []PlanProcess `json:"processes,omitempty"`
	// DjangoPlatformHosts: a imagem usa o módulo de ajuste que acrescenta o endereço
	// do projeto a ALLOWED_HOSTS e CSRF_TRUSTED_ORIGINS (DEC-49 item 6).
	DjangoPlatformHosts bool `json:"django_platform_hosts,omitempty"`
	// ReleaseCommand roda uma vez por deploy, num contêiner descartável da imagem nova,
	// antes de a aplicação nova subir (DEC-52 item 4); se falha, o deploy falha.
	ReleaseCommand string `json:"release_command,omitempty"`
}

// gitHookTools são ferramentas que só instalam ganchos do Git; num contêiner
// não há repositório Git e elas costumam ser dependências de desenvolvimento.
var gitHookTools = map[string]bool{"husky": true, "lefthook": true, "simple-git-hooks": true, "is-ci": true}

// GitHookOnly diz se um script (o prepare, tipicamente) só instala ganchos do
// Git: cada trecho, separado por &&, || ou ;, invoca uma dessas ferramentas
// (direto ou por npx/bunx). Um script misto não é tocado (DEC-36).
func GitHookOnly(command string) bool {
	seps := strings.NewReplacer("&&", "\n", "||", "\n", ";", "\n")
	found := false
	for _, segment := range strings.Split(seps.Replace(command), "\n") {
		tokens := strings.Fields(segment)
		for len(tokens) > 0 && (tokens[0] == "npx" || tokens[0] == "bunx" || tokens[0] == "pnpm" || tokens[0] == "exec") {
			tokens = tokens[1:]
		}
		if len(tokens) == 0 {
			continue
		}
		if tokens[0] == "true" || tokens[0] == "exit" {
			continue
		}
		if !gitHookTools[tokens[0]] {
			return false
		}
		found = true
	}
	return found
}

// PlanProcess é um processo do projeto além do web (DEC-51).
type PlanProcess struct {
	// Name e Kind: `worker` ou `scheduler`.
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Command string `json:"command"`
}
