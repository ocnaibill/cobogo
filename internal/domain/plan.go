package domain

// Plan é o conjunto de decisões executáveis de um deploy, conforme §11.1.
// Não carrega valores secretos.
type Plan struct {
	Profile        string `json:"profile"`
	Runtime        string `json:"runtime"`
	PackageManager string `json:"package_manager"`
	InstallCommand string `json:"install_command"`
	BuildCommand   string `json:"build_command,omitempty"`
	StartCommand   string `json:"start_command"`
	ExecutionMode  string `json:"execution_mode"`
	Port           int    `json:"port"`
	PortOrigin     string `json:"port_origin"`
	Dockerfile     string `json:"dockerfile"`
}
