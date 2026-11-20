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
