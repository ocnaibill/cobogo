package domain

// AnalysisOutcome é o resultado possível de uma análise, conforme §11.1 do
// documento mestre: pronto, precisa de informação ou não suportado.
type AnalysisOutcome string

const (
	AnalysisReady       AnalysisOutcome = "READY"
	AnalysisNeedsInput  AnalysisOutcome = "NEEDS_INPUT"
	AnalysisUnsupported AnalysisOutcome = "UNSUPPORTED"
)

// Evidence é um fato observado no repositório que sustenta uma regra (HEU-xx).
type Evidence struct {
	Rule        string `json:"rule"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

// PendingField é um campo do plano que ainda não tem origem válida.
type PendingField struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

// Plan é o conjunto de decisões executáveis de um deploy, conforme §11.1.
// Não carrega valores secretos.
type Plan struct {
	Profile        string `json:"profile"`
	Runtime        string `json:"runtime"`
	PackageManager string `json:"package_manager"`
	InstallCommand string `json:"install_command"`
	BuildCommand   string `json:"build_command,omitempty"`
	StartCommand   string `json:"start_command"`
	Port           int    `json:"port"`
	PortOrigin     string `json:"port_origin"`
	Dockerfile     string `json:"dockerfile"`
}

// AnalysisResult é o que o analisador produz para um SHA: evidências,
// pendências, conflitos e o plano candidato quando pronto.
type AnalysisResult struct {
	Outcome      AnalysisOutcome `json:"outcome"`
	Evidences    []Evidence      `json:"evidences"`
	Pending      []PendingField  `json:"pending"`
	Conflicts    []string        `json:"conflicts,omitempty"`
	Plan         *Plan           `json:"plan,omitempty"`
	RulesVersion string          `json:"rules_version"`
}
