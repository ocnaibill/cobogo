package domain

// Contrato de saída do motor de heurísticas (DEC-23).

// AnalysisOutcome é o resultado de uma análise (§11.1 do mestre).
type AnalysisOutcome string

const (
	AnalysisReady       AnalysisOutcome = "READY"
	AnalysisNeedsInput  AnalysisOutcome = "NEEDS_INPUT"
	AnalysisUnsupported AnalysisOutcome = "UNSUPPORTED"
)

// Campos do plano que o analisador resolve para os perfis JavaScript
// (P-NODE e P-BUN).
const (
	FieldProfile        = "profile"
	FieldPackageManager = "package_manager"
	FieldStartCommand   = "start_command"
	FieldBuildCommand   = "build_command"
	FieldExecutionMode  = "execution_mode"
	FieldNodeVersion    = "node_version"
	FieldBunVersion     = "bun_version"
	FieldOutputDir      = "output_dir"
	FieldSPAFallback    = "spa_fallback"
	FieldHealthPath     = "health_path"
	FieldPort           = "port"
	// FieldAllowUnlocked é a escolha de instalar sem lockfile (DEC-53 H): true instala com
	// `npm install` ou `bun install`, e as versões podem mudar entre deploys.
	FieldAllowUnlocked = "allow_unlocked"
	// FieldDatabase é a pendência de destino do banco (HEU-06.6, DEC-42). Não é um
	// campo do plano e não se responde por overrides: a escolha vai em
	// POST /projects/{id}/database.
	FieldDatabase = "database"
	// FieldRedis é a pendência de destino do Redis (HEU-06.6 para Redis, DEC-50): a escolha vai
	// em POST /projects/{id}/database com engine `redis`.
	FieldRedis = "redis"
)

// Perfis e gerenciadores do catálogo JavaScript (DEC-25, DEC-36).
const (
	ProfileNode   = "P-NODE"
	ProfileBun    = "P-BUN"
	ProfileStatic = "P-STATIC"
	ManagerNPM    = "npm"
	ManagerBun    = "bun"
	// ManagerNone: site estático sem build, nada a instalar (DEC-37).
	ManagerNone = "none"
)

// DefaultBunVersion é a tag da imagem oven/bun quando o projeto não fixa uma
// versão exata: a linha mais recente da major 1 (DEC-36).
const DefaultBunVersion = "1"

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

// PlatformPort é a porta que a plataforma entrega em PORT a um app que a lê
// em código de execução (DEC-35). Dentro do contêiner não há disputa de porta.
const PlatformPort = "8080"

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
	// OriginPlatform é um valor que a própria plataforma escolhe porque o
	// repositório declara um contrato para ele (o app lê PORT; DEC-35).
	OriginPlatform  Origin = "platform"
	OriginInference Origin = "inference"
	OriginDefault   Origin = "default"
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
	// ClassPublic: configuração pública de build (VITE_, REACT_APP_...): vai
	// para o JavaScript entregue ao navegador; nunca é segredo (DEC-15, DEC-37).
	ClassPublic VarClass = "public"
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
	// HintLocalhostDefault: o código cai para um endereço local quando a variável
	// falta (DEC-49).
	HintLocalhostDefault = "localhost_default"
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
	// Components é o mapa do repositório: aplicações, serviços e processos (DEC-49).
	Components []Component `json:"components"`
	// Warnings são avisos de segurança e de configuração que nunca bloqueiam
	// a análise (ex.: credencial exposta no repositório).
	Warnings []Finding `json:"warnings"`
	// PersistentSuggestions são caminhos que o código parece usar para guardar
	// dados em disco (DEC-41). Só sugerem: quem declara é o usuário.
	PersistentSuggestions []PersistentSuggestion `json:"persistent_suggestions"`
	// ServiceNeeds são os serviços auxiliares que o código parece exigir
	// (HEU-06, DEC-42). Só informam; criar um serviço exige a escolha do usuário.
	ServiceNeeds []ServiceNeed `json:"service_needs"`
	Evidences    []Evidence    `json:"evidences"`
	Coverage     Coverage      `json:"coverage"`
}

// Tipos e confianças de uma sugestão de caminho persistente (DEC-41).
const (
	SuggestSQLite   = "sqlite"    // arquivo de SQLite
	SuggestWriteDir = "write_dir" // pasta em que o código escreve arquivos
	SuggestStrong   = "strong"
	SuggestWeak     = "weak"
)

// PersistentSuggestion é uma pista de que um caminho guarda dados que
// sobreviveriam a um deploy. Path vazio quer dizer "usa disco, mas não achei
// onde": a pessoa informa. A sugestão nunca é aplicada sozinha.
type PersistentSuggestion struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Confidence string   `json:"confidence"`
	Reason     string   `json:"reason"`
	Evidence   []string `json:"evidence"`
}

// Níveis de necessidade de um serviço (HEU-06.3 e HEU-06.5).
const (
	NeedRequired = "required" // código de runtime usa a engine
	NeedPossible = "possible" // só a dependência: não se presume ativa nem inativa
)

// ServiceNeed é uma pista de que o projeto precisa de um banco. Variable é a
// variável de conexão provável, que a pessoa pode trocar.
type ServiceNeed struct {
	Engine string `json:"engine"`
	Level  string `json:"level"`
	// Variable é a variável principal: a URL, ou a da senha no estilo de partes.
	Variable string `json:"variable"`
	// Style é `url` ou `parts` (DEC-44). Family é o prefixo das partes (POSTGRES,
	// PG, DB, DATABASE); vazio no estilo URL.
	Style  string `json:"style"`
	Family string `json:"family,omitempty"`
	// Variables é papel -> nome da variável que o código lê.
	Variables map[string]string `json:"variables"`
	Reason    string            `json:"reason"`
	Evidence  []string          `json:"evidence"`
	// FixedAt são os lugares (arquivo:linha) onde o código escreve o endereço do serviço em vez de
	// lê-lo de uma variável de ambiente: o serviço do Cobogó só será usado se o código mudar.
	FixedAt []string `json:"fixed_at,omitempty"`
}

// Papéis das peças do repositório (DEC-49 item 1).
const (
	CompFrontend  = "frontend"
	CompBackend   = "backend"
	CompFullstack = "fullstack"
	CompApp       = "app"
	CompWorker    = "worker"
	CompDatabase  = "database"
	CompCache     = "cache"
	CompQueue     = "queue"
)

// Component é uma peça do repositório: uma aplicação (desta pasta ou irmã), um
// serviço de dados ou um processo que o código pede. É inferência com evidência.
type Component struct {
	// Dir é a pasta da peça ("." para a aplicação desta pasta e para os serviços
	// que ela pede).
	Dir   string `json:"dir"`
	Role  string `json:"role"`
	Label string `json:"label"`
	// Ecosystem é javascript ou python, só para aplicações.
	Ecosystem string `json:"ecosystem,omitempty"`
	// Sibling marca outra aplicação do repositório, que pode virar um projeto.
	Sibling bool `json:"sibling"`
	// Supported diz se o Cobogó publica ou provê a peça hoje.
	Supported bool     `json:"supported"`
	Note      string   `json:"note,omitempty"`
	Evidence  []string `json:"evidence"`
}
