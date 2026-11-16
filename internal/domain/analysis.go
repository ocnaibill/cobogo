package domain

// Contrato de saída do motor de heurísticas (DEC-23).

// AnalysisOutcome é o resultado de uma análise (§11.1 do mestre).
type AnalysisOutcome string

const (
	AnalysisReady       AnalysisOutcome = "READY"
	AnalysisNeedsInput  AnalysisOutcome = "NEEDS_INPUT"
	AnalysisUnsupported AnalysisOutcome = "UNSUPPORTED"
)

// Campos do plano que o analisador resolve para o perfil P-NODE.
const (
	FieldProfile        = "profile"
	FieldPackageManager = "package_manager"
	FieldStartCommand   = "start_command"
	FieldBuildCommand   = "build_command"
	FieldExecutionMode  = "execution_mode"
	FieldNodeVersion    = "node_version"
	FieldHealthPath     = "health_path"
	FieldPort           = "port"
)

// Modos de execução (DEC-25). production é o padrão; development só por
// escolha explícita do usuário.
const (
	ModeProduction  = "production"
	ModeDevelopment = "development"
)

// SupportedNodeVersions são as versões major do Node que o catálogo aceita
// (todas com suporte do projeto Node.js; DEC-25). DefaultNodeVersion vale
// quando o projeto não declara versão ou declara uma que nenhuma admite.
var SupportedNodeVersions = []string{"22", "24", "26"}

const DefaultNodeVersion = "26"

// Scope diz onde uma evidência se aplica (§4.1 do motor).
type Scope string

const (
	ScopeRuntime Scope = "runtime"
	ScopeBuild   Scope = "build"
	ScopeTest    Scope = "test"
	ScopeUnknown Scope = "unknown"
)

// Origin diz de onde veio o valor de um campo, em ordem decrescente de
// precedência (RN-03).
type Origin string

const (
	OriginOverride    Origin = "override"
	OriginDeclaration Origin = "declaration"
	OriginInference   Origin = "inference"
	OriginDefault     Origin = "default"
)

// Evidence é um fato observado no repositório por um extrator. O ID deriva
// do conteúdo, então a mesma entrada gera os mesmos IDs.
type Evidence struct {
	ID        string `json:"id"`
	Extractor string `json:"extractor"`
	Kind      string `json:"kind"`
	Name      string `json:"name,omitempty"`
	Value     string `json:"value,omitempty"`
	Source    string `json:"source"`
	Line      int    `json:"line,omitempty"`
	Scope     Scope  `json:"scope"`
}

// Gap é algo que o analisador não conseguiu ler ou interpretar.
type Gap struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Coverage descreve o que a análise leu e o que ficou de fora; ausência de
// detecção só vale como ausência quando não há lacunas relevantes.
type Coverage struct {
	FilesRead int   `json:"files_read"`
	Gaps      []Gap `json:"gaps"`
}

// Finding é uma pendência, conflito ou motivo de não suporte, sempre ligado
// às evidências que o sustentam.
type Finding struct {
	Rule        string   `json:"rule,omitempty"`
	Field       string   `json:"field,omitempty"`
	Message     string   `json:"message"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
	// Options são valores que o usuário pode escolher para responder a
	// pendência; são sugestões, nunca aplicadas sem escolha explícita.
	Options []Option `json:"options,omitempty"`
	// Omitted lista o que a regra deixou de oferecer como opção, para a
	// interface poder mostrar tudo: nada é escondido em silêncio.
	Omitted []string `json:"omitted,omitempty"`
}

// OptionKind classifica uma opção de execução. É uma inferência a partir do
// comando (e, só como reforço, do nome); a interface a apresenta como tal.
type OptionKind string

const (
	OptionProduction  OptionKind = "production"
	OptionDevelopment OptionKind = "development"
)

// Option é um valor candidato para um campo pendente.
type Option struct {
	Value string     `json:"value"`
	Kind  OptionKind `json:"kind,omitempty"`
	// Note explica o motivo da classificação.
	Note string `json:"note,omitempty"`
}

// Proposal é o valor que uma regra sugere para um campo.
type Proposal struct {
	Field       string   `json:"field"`
	Value       string   `json:"value"`
	Origin      Origin   `json:"origin"`
	Rule        string   `json:"rule"`
	EvidenceIDs []string `json:"evidence_ids,omitempty"`
}

// Field é o valor resolvido de um campo do plano, com sua origem. Overrode
// lista as propostas que um override do usuário cobriu.
type Field struct {
	Value       string     `json:"value"`
	Origin      Origin     `json:"origin"`
	Rules       []string   `json:"rules,omitempty"`
	EvidenceIDs []string   `json:"evidence_ids,omitempty"`
	Overrode    []Proposal `json:"overrode,omitempty"`
}

// EnvReference é um uso de variável de ambiente num arquivo.
type EnvReference struct {
	Path  string `json:"path"`
	Scope Scope  `json:"scope"`
}

// VarClass diz quem fornece o valor de uma variável de ambiente (DEC-24).
type VarClass string

const (
	// ClassPlatform: fornecida pela plataforma (ex.: PORT); o usuário não a preenche.
	ClassPlatform VarClass = "platform"
	// ClassGenerable: segredo que o próprio app inventa; o Cobogó pode gerar
	// um valor, mas só sob ação explícita do usuário.
	ClassGenerable VarClass = "generable"
	// ClassUser: o usuário informa.
	ClassUser VarClass = "user"
)

// ExampleState descreve como a variável aparece nos arquivos de exemplo
// (.env.example e similares).
type ExampleState string

const (
	ExampleAbsent      ExampleState = "absent"      // não está no exemplo
	ExampleEmpty       ExampleState = "empty"       // declarada sem valor
	ExamplePlaceholder ExampleState = "placeholder" // valor de preenchimento (changeme, <token>)
	ExampleDefault     ExampleState = "default"     // valor real não secreto
	ExampleCommented   ExampleState = "commented"   // aparece só comentada
	ExampleExposed     ExampleState = "exposed"     // parece uma credencial real
)

// Códigos de aviso de uma variável. A interface escolhe o texto; Message traz
// uma redação padrão.
const (
	HintLikelyRequired = "likely_required"
	HintLikelyOptional = "likely_optional"
	HintCommented      = "commented"
	HintPrefilled      = "prefilled"
	HintUndeclared     = "undeclared"
	HintReferencedOnly = "referenced_only"
)

// ConfigHint é um aviso não bloqueante sobre uma variável.
type ConfigHint struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ConfigVariable é uma entrada do inventário de HEU-09 (DEC-24). Os avisos
// nunca bloqueiam o deploy. DefaultValue só existe para o estado `default`
// (valor não secreto de um arquivo de exemplo) e serve para pré-preencher um
// campo que o usuário precisa confirmar; nenhum outro valor do repositório
// sai da análise.
type ConfigVariable struct {
	Name              string         `json:"name"`
	Class             VarClass       `json:"class"`
	Example           ExampleState   `json:"example"`
	DefaultValue      string         `json:"default_value,omitempty"`
	Hint              *ConfigHint    `json:"hint,omitempty"`
	DeclaredInExample bool           `json:"declared_in_example"`
	References        []EnvReference `json:"references,omitempty"`
	EvidenceIDs       []string       `json:"evidence_ids,omitempty"`
}

// AppliedRule registra qual regra, em qual versão e estado, participou da
// análise, e se foi aplicada, pulada por pré-condição ou aposentada.
type AppliedRule struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	State   string `json:"state"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
}

// AnalysisResult é o que o analisador produz para um SHA e um conjunto de
// overrides. Não contém o plano nem o Dockerfile: isso é do planner.
type AnalysisResult struct {
	Outcome         AnalysisOutcome  `json:"outcome"`
	CatalogVersion  string           `json:"catalog_version"`
	Rules           []AppliedRule    `json:"rules"`
	Fields          map[string]Field `json:"fields"`
	Pending         []Finding        `json:"pending"`
	Conflicts       []Finding        `json:"conflicts"`
	Unsupported     []Finding        `json:"unsupported"`
	ConfigVariables []ConfigVariable `json:"config_variables"`
	// Warnings são avisos de segurança e de configuração que nunca bloqueiam
	// a análise (ex.: credencial exposta no repositório).
	Warnings  []Finding  `json:"warnings"`
	Evidences []Evidence `json:"evidences"`
	Coverage  Coverage   `json:"coverage"`
}
