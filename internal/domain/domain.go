package domain

import (
	"time"

	"github.com/google/uuid"
)

// Project é um repositório registrado para deploy (RF-01).
type Project struct {
	ID            uuid.UUID `json:"id"`
	Slug          string    `json:"slug"`
	RepositoryURL string    `json:"repository_url"`
	RootDirectory string    `json:"root_directory"`
	CreatedAt     time.Time `json:"created_at"`
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
	// PortOverride é o valor explícito informado pelo solicitante (RN-03);
	// tem precedência sobre qualquer inferência da análise.
	PortOverride  *int      `json:"port_override,omitempty"`
	FailureReason *string   `json:"failure_reason,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
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
	CreatedAt        time.Time `json:"created_at"`
}
