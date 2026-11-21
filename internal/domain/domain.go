package domain

import (
	"time"

	"github.com/google/uuid"
)

// Project é um repositório registrado para deploy (RF-01).
type Project struct {
	ID uuid.UUID `json:"id"`
	// OwnerID é o administrador dono do projeto (DEC-20, DEC-27); toda consulta
	// da API é filtrada por ele.
	OwnerID       *uuid.UUID `json:"-"`
	Slug          string     `json:"slug"`
	RepositoryURL string     `json:"repository_url"`
	RootDirectory string     `json:"root_directory"`
	// Limits são as sobreposições de recursos deste projeto; campo zero herda
	// da configuração global (DEC-30 item 7).
	Limits    Limits    `json:"limits"`
	CreatedAt time.Time `json:"created_at"`
	// DeletedAt marca o projeto excluído (DEC-38): ele some da API e das
	// rotas, e o histórico fica.
	DeletedAt *time.Time `json:"-"`
	// AppState é o estado da release ativa: none, running ou stopped (DEC-40).
	AppState string `json:"app_state"`
	// PersistentPaths são os caminhos do contêiner que guardam dados em volume
	// nomeado (DEC-41). Só o usuário os declara.
	PersistentPaths []string `json:"persistent_paths"`
}

// Limits são os tetos de recursos de contêineres de aplicação e de builds
// (DEC-29, DEC-30). Zero em um campo significa "não definido": vale o nível de
// cima (projeto, banco, ambiente, padrão de fábrica).
type Limits struct {
	AppMemoryMB   int     `json:"app_memory_mb,omitempty"`
	AppCPUs       float64 `json:"app_cpus,omitempty"`
	AppPids       int     `json:"app_pids,omitempty"`
	BuildMemoryMB int     `json:"build_memory_mb,omitempty"`
	BuildCPUs     float64 `json:"build_cpus,omitempty"`
}

// DefaultLimits são os padrões de fábrica, pensados para uma VM de 16 GiB
// (DEC-30 item 4). O limite é teto, não reserva.
func DefaultLimits() Limits {
	return Limits{AppMemoryMB: 1024, AppCPUs: 1, AppPids: 256, BuildMemoryMB: 4096, BuildCPUs: 2}
}

// Over completa os campos não definidos de l com os de base.
func (l Limits) Over(base Limits) Limits {
	if l.AppMemoryMB == 0 {
		l.AppMemoryMB = base.AppMemoryMB
	}
	if l.AppCPUs == 0 {
		l.AppCPUs = base.AppCPUs
	}
	if l.AppPids == 0 {
		l.AppPids = base.AppPids
	}
	if l.BuildMemoryMB == 0 {
		l.BuildMemoryMB = base.BuildMemoryMB
	}
	if l.BuildCPUs == 0 {
		l.BuildCPUs = base.BuildCPUs
	}
	return l
}

// WithDefaults completa o que falta com os padrões de fábrica.
func (l Limits) WithDefaults() Limits { return l.Over(DefaultLimits()) }

// DeploymentBrief resume um deploy para listas (DEC-28 item 5).
type DeploymentBrief struct {
	ID        uuid.UUID       `json:"id"`
	State     DeploymentState `json:"state"`
	CreatedAt time.Time       `json:"created_at"`
}

// ProjectSummary é um projeto de uma lista, com o último deploy, se houver.
type ProjectSummary struct {
	Project
	LastDeployment *DeploymentBrief `json:"last_deployment"`
}

// Analysis é o snapshot imutável da análise de um commit (RF-02, RF-03).
type Analysis struct {
	ID           uuid.UUID       `json:"id"`
	ProjectID    uuid.UUID       `json:"project_id"`
	CommitSHA    string          `json:"commit_sha"`
	RulesVersion string          `json:"rules_version"`
	Outcome      AnalysisOutcome `json:"outcome"`
	Result       AnalysisResult  `json:"result"`
	CreatedAt    time.Time       `json:"created_at"`
}

// DeploymentState segue a máquina de estados do §15.2 do mestre.
type DeploymentState string

const (
	DeploymentQueued       DeploymentState = "QUEUED"
	DeploymentFetching     DeploymentState = "FETCHING"
	DeploymentAnalyzing    DeploymentState = "ANALYZING"
	DeploymentNeedsInput   DeploymentState = "NEEDS_INPUT"
	DeploymentUnsupported  DeploymentState = "UNSUPPORTED"
	DeploymentGenerating   DeploymentState = "GENERATING"
	DeploymentBuilding     DeploymentState = "BUILDING"
	DeploymentProvisioning DeploymentState = "PROVISIONING"
	DeploymentStarting     DeploymentState = "STARTING"
	DeploymentVerifying    DeploymentState = "VERIFYING"
	DeploymentSucceeded    DeploymentState = "SUCCEEDED"
	DeploymentFailed       DeploymentState = "FAILED"
	DeploymentCancelled    DeploymentState = "CANCELLED"
)

// Deployment é uma execução de deploy para um commit imutável (RN-04).
type Deployment struct {
	ID             uuid.UUID       `json:"id"`
	ProjectID      uuid.UUID       `json:"project_id"`
	AnalysisID     *uuid.UUID      `json:"analysis_id,omitempty"`
	CommitSHA      string          `json:"commit_sha"`
	State          DeploymentState `json:"state"`
	IdempotencyKey string          `json:"idempotency_key"`
	// Overrides são valores explícitos do solicitante por campo do plano
	// (RN-03, DEC-24); têm precedência sobre qualquer inferência da análise.
	// Valores de variáveis de ambiente nunca passam por aqui.
	Overrides     map[string]string `json:"overrides"`
	FailureReason *string           `json:"failure_reason,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

// JobState descreve a posse de processamento de um deployment pelo worker.
type JobState string

const (
	JobPending JobState = "PENDING"
	JobLeased  JobState = "LEASED"
	JobDone    JobState = "DONE"
)

// Job é a unidade de trabalho reivindicada pelo worker via SKIP LOCKED (§12.3).
type Job struct {
	ID                  uuid.UUID  `json:"id"`
	DeploymentID        uuid.UUID  `json:"deployment_id"`
	State               JobState   `json:"state"`
	LeaseUntil          *time.Time `json:"lease_until,omitempty"`
	OwnershipGeneration int        `json:"ownership_generation"`
	AttemptCount        int        `json:"attempt_count"`
	CreatedAt           time.Time  `json:"created_at"`
	UpdatedAt           time.Time  `json:"updated_at"`
}

// DeploymentEvent é um registro imutável de progresso, sem segredos (RF-07).
type DeploymentEvent struct {
	ID           uuid.UUID      `json:"id"`
	DeploymentID uuid.UUID      `json:"deployment_id"`
	Sequence     int            `json:"sequence"`
	Stage        string         `json:"stage"`
	EventType    string         `json:"event_type"`
	OccurredAt   time.Time      `json:"occurred_at"`
	Payload      map[string]any `json:"payload"`
}

// Release é o resultado operacional de um deployment bem-sucedido.
type Release struct {
	ID               uuid.UUID `json:"id"`
	DeploymentID     uuid.UUID `json:"deployment_id"`
	ImageID          string    `json:"image_id"`
	ContainerID      string    `json:"container_id"`
	OperationalState string    `json:"operational_state"`
	Health           string    `json:"health"`
	HostPort         int       `json:"host_port"`
	// URL é o endereço público com que a release foi roteada (DEC-31); vazio
	// quando não havia domínio configurado.
	URL string `json:"url,omitempty"`
	// RouteHost e RouteTarget são o que o Traefik lê (DEC-31); não saem pela API.
	RouteHost   string `json:"-"`
	RouteTarget string `json:"-"`
	// StoppedAt é quando a aplicação foi parada de propósito (DEC-40).
	StoppedAt *time.Time `json:"stopped_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// EnvSource diz de onde veio o valor de uma variável.
type EnvSource string

const (
	EnvSourceUser      EnvSource = "user"
	EnvSourceGenerated EnvSource = "generated"
	// EnvSourceImported: veio de um .env colado e revisado (DEC-39).
	EnvSourceImported EnvSource = "imported"
	// EnvSourceLinked: o valor é a conexão de um serviço do Cobogó (DEC-42) e não se
	// edita como as demais; desvincular é uma ação própria.
	EnvSourceLinked EnvSource = "linked"
)

// EnvVariable são os metadados de uma variável de ambiente do projeto. O
// valor nunca faz parte deste tipo: a API não o devolve depois de salvo
// (RF-10) e só o worker o decifra para entregá-lo ao contêiner.
type EnvVariable struct {
	Name       string    `json:"name"`
	Version    int       `json:"version"`
	Source     EnvSource `json:"source"`
	KeyVersion int       `json:"key_version"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// User é a conta do administrador. O hash da senha nunca sai do store.
type User struct {
	ID        uuid.UUID `json:"id"`
	Login     string    `json:"login"`
	CreatedAt time.Time `json:"created_at"`
}

// Session é uma sessão autenticada. O token em si nunca é guardado.
type Session struct {
	ID         uuid.UUID `json:"id"`
	UserID     uuid.UUID `json:"-"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// AuditEvent registra uma ação com ator, alvo e resultado. Nunca carrega
// senha, token nem valor de variável.
type AuditEvent struct {
	ActorID *uuid.UUID
	Action  string
	Target  string
	IP      string
	Outcome string
	// Detail guarda fatos sem segredo, como a versão e a origem de uma variável.
	Detail map[string]any
}

// Route é uma linha da tabela de rotas do Traefik: o host público de um
// projeto e o destino interno da sua release ativa (DEC-31).
type Route struct {
	Slug   string
	Host   string
	Target string
	// Stopped: a aplicação foi parada; o host mostra a página de inativo (DEC-40).
	Stopped bool
}

// Operações de ciclo de vida que o worker executa a pedido da API (DEC-38).
type OperationKind string

const (
	OpRetireReleases OperationKind = "retire_releases"
	OpDeleteProject  OperationKind = "delete_project"
	OpApplyLimits    OperationKind = "apply_limits"
	OpSweep          OperationKind = "sweep"
	// DEC-40: parar, iniciar e reiniciar a release ativa.
	OpStopApp    OperationKind = "stop_app"
	OpStartApp   OperationKind = "start_app"
	OpRestartApp OperationKind = "restart_app"
	// DEC-41: apagar o volume de um caminho que já não é persistente, ou de um
	// projeto excluído.
	OpDeleteVolume OperationKind = "delete_volume"
	// DEC-42: apagar o PostgreSQL gerenciado de um projeto, com os dados.
	OpDeleteService OperationKind = "delete_service"
)

// Estados da aplicação de um projeto (DEC-40): sem release ativa, no ar ou
// parada de propósito. "Parada" não é "falhou" (RN-12).
const (
	AppNone    = "none"
	AppRunning = "running"
	AppStopped = "stopped"
)

type OperationState string

const (
	OpPending OperationState = "PENDING"
	OpRunning OperationState = "RUNNING"
	OpDone    OperationState = "DONE"
	OpFailed  OperationState = "FAILED"
)

// Operation é um pedido de ciclo de vida: aposentar releases, excluir um
// projeto, aplicar limites ou varrer recursos órfãos.
type Operation struct {
	ID        uuid.UUID      `json:"id"`
	ProjectID *uuid.UUID     `json:"project_id,omitempty"`
	Kind      OperationKind  `json:"kind"`
	State     OperationState `json:"state"`
	Params    map[string]any `json:"params"`
	Result    map[string]any `json:"result"`
	Error     string         `json:"error,omitempty"`
	Attempts  int            `json:"attempts"`
	NotBefore time.Time      `json:"not_before"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Modos e estados de um serviço auxiliar (DEC-42).
const (
	ServicePostgres = "postgres"

	ServiceManaged  = "managed"  // o Cobogó cria e mantém o banco
	ServiceExternal = "external" // o usuário informa a conexão de um banco que já tem
	ServiceNone     = "none"     // o projeto não precisa de banco

	ServicePending  = "pending"
	ServiceReady    = "ready"
	ServiceFailed   = "failed"
	ServiceDeleting = "deleting"
)

// Service é a intenção de um projeto sobre um banco e, no modo gerenciado, a
// identidade do serviço. A senha nunca está aqui: vive na variável cifrada.
type Service struct {
	ID            uuid.UUID `json:"id"`
	ProjectID     uuid.UUID `json:"project_id"`
	Engine        string    `json:"engine"`
	Mode          string    `json:"mode"`
	Variable      string    `json:"variable"`
	ContainerName string    `json:"container_name,omitempty"`
	VolumeName    string    `json:"volume_name,omitempty"`
	Status        string    `json:"status"`
	StatusDetail  string    `json:"status_detail,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}
