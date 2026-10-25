// Package store é a camada de persistência sobre PostgreSQL (DEC-03,
// DEC-21). Em M1 a aplicação roda com um único role; a separação de roles
// por componente (API/worker/collector) é um endurecimento a fazer antes
// de expor o Cobogó fora do ambiente de desenvolvimento.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/domain"
)

var ErrNotFound = errors.New("store: registro não encontrado")

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// CreateProject registra um repositório para deploy (RF-01).
func (s *Store) CreateProject(ctx context.Context, slug, repositoryURL, rootDirectory string) (domain.Project, error) {
	p := domain.Project{
		ID:            uuid.New(),
		Slug:          slug,
		RepositoryURL: repositoryURL,
		RootDirectory: rootDirectory,
		CreatedAt:     time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO projects (id, slug, repository_url, root_directory, created_at)
		VALUES ($1, $2, $3, $4, $5)`,
		p.ID, p.Slug, p.RepositoryURL, p.RootDirectory, p.CreatedAt,
	)
	if err != nil {
		return domain.Project{}, fmt.Errorf("store: criar projeto: %w", err)
	}
	return p, nil
}

func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (domain.Project, error) {
	var p domain.Project
	err := s.pool.QueryRow(ctx, `
		SELECT id, slug, repository_url, root_directory, created_at
		FROM projects WHERE id = $1`, id,
	).Scan(&p.ID, &p.Slug, &p.RepositoryURL, &p.RootDirectory, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, ErrNotFound
	}
	if err != nil {
		return domain.Project{}, fmt.Errorf("store: buscar projeto: %w", err)
	}
	return p, nil
}

// CreateDeploymentWithJob cria o deploy e o job na mesma transação (§12.3),
// evitando perder trabalho entre banco e fila. RN-07: idempotency_key é
// única por projeto.
func (s *Store) CreateDeploymentWithJob(ctx context.Context, projectID uuid.UUID, idempotencyKey string, portOverride *int) (domain.Deployment, domain.Job, error) {
	now := time.Now().UTC()
	dep := domain.Deployment{
		ID:             uuid.New(),
		ProjectID:      projectID,
		State:          domain.DeploymentQueued,
		IdempotencyKey: idempotencyKey,
		PortOverride:   portOverride,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	job := domain.Job{
		ID:           uuid.New(),
		DeploymentID: dep.ID,
		State:        domain.JobPending,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Deployment{}, domain.Job{}, fmt.Errorf("store: abrir transação de deploy: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO deployments (id, project_id, commit_sha, state, idempotency_key, port_override, created_at, updated_at)
		VALUES ($1, $2, '', $3, $4, $5, $6, $7)`,
		dep.ID, dep.ProjectID, dep.State, dep.IdempotencyKey, dep.PortOverride, dep.CreatedAt, dep.UpdatedAt,
	)
	if err != nil {
		return domain.Deployment{}, domain.Job{}, fmt.Errorf("store: inserir deployment: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO jobs (id, deployment_id, state, ownership_generation, attempt_count, created_at, updated_at)
		VALUES ($1, $2, $3, 0, 0, $4, $5)`,
		job.ID, job.DeploymentID, job.State, job.CreatedAt, job.UpdatedAt,
	)
	if err != nil {
		return domain.Deployment{}, domain.Job{}, fmt.Errorf("store: inserir job: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Deployment{}, domain.Job{}, fmt.Errorf("store: confirmar criação do deploy: %w", err)
	}
	return dep, job, nil
}

// ClaimNextJob reivindica um job pendente (ou com lease expirado) via
// SKIP LOCKED, conforme §12.3. Retorna (nil, nil, nil) quando não há
// trabalho disponível.
func (s *Store) ClaimNextJob(ctx context.Context, lease time.Duration) (*domain.Job, *domain.Deployment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("store: abrir transação de claim: %w", err)
	}
	defer tx.Rollback(ctx)

	var jobID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM jobs
		WHERE state = $1 AND (lease_until IS NULL OR lease_until < now())
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, domain.JobPending,
	).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("store: selecionar próximo job: %w", err)
	}

	now := time.Now().UTC()
	leaseUntil := now.Add(lease)
	var job domain.Job
	err = tx.QueryRow(ctx, `
		UPDATE jobs SET state = $1, lease_until = $2, ownership_generation = ownership_generation + 1,
			attempt_count = attempt_count + 1, updated_at = $3
		WHERE id = $4
		RETURNING id, deployment_id, state, lease_until, ownership_generation, attempt_count, created_at, updated_at`,
		domain.JobLeased, leaseUntil, now, jobID,
	).Scan(&job.ID, &job.DeploymentID, &job.State, &job.LeaseUntil, &job.OwnershipGeneration, &job.AttemptCount, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return nil, nil, fmt.Errorf("store: marcar job como leased: %w", err)
	}

	dep, err := s.getDeploymentTx(ctx, tx, job.DeploymentID)
	if err != nil {
		return nil, nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, nil, fmt.Errorf("store: confirmar claim: %w", err)
	}
	return &job, &dep, nil
}

func (s *Store) getDeploymentTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Deployment, error) {
	var dep domain.Deployment
	var analysisID *uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id, project_id, analysis_id, commit_sha, state, idempotency_key, port_override, failure_reason, created_at, updated_at
		FROM deployments WHERE id = $1`, id,
	).Scan(&dep.ID, &dep.ProjectID, &analysisID, &dep.CommitSHA, &dep.State, &dep.IdempotencyKey, &dep.PortOverride, &dep.FailureReason, &dep.CreatedAt, &dep.UpdatedAt)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: buscar deployment: %w", err)
	}
	dep.AnalysisID = analysisID
	return dep, nil
}

func (s *Store) GetDeployment(ctx context.Context, id uuid.UUID) (domain.Deployment, error) {
	var dep domain.Deployment
	var analysisID *uuid.UUID
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, analysis_id, commit_sha, state, idempotency_key, port_override, failure_reason, created_at, updated_at
		FROM deployments WHERE id = $1`, id,
	).Scan(&dep.ID, &dep.ProjectID, &analysisID, &dep.CommitSHA, &dep.State, &dep.IdempotencyKey, &dep.PortOverride, &dep.FailureReason, &dep.CreatedAt, &dep.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Deployment{}, ErrNotFound
	}
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: buscar deployment: %w", err)
	}
	dep.AnalysisID = analysisID
	return dep, nil
}

// SetDeploymentCommitSHA fixa o commit obtido (RF-02); ocorre uma vez, após
// FETCHING.
func (s *Store) SetDeploymentCommitSHA(ctx context.Context, id uuid.UUID, sha string) error {
	_, err := s.pool.Exec(ctx, `UPDATE deployments SET commit_sha = $1, updated_at = now() WHERE id = $2`, sha, id)
	if err != nil {
		return fmt.Errorf("store: registrar SHA do deployment: %w", err)
	}
	return nil
}

// UpdateDeploymentState avança a máquina de estados do §15.2.
func (s *Store) UpdateDeploymentState(ctx context.Context, id uuid.UUID, state domain.DeploymentState, failureReason string) error {
	var reason *string
	if failureReason != "" {
		reason = &failureReason
	}
	_, err := s.pool.Exec(ctx, `UPDATE deployments SET state = $1, failure_reason = $2, updated_at = now() WHERE id = $3`, state, reason, id)
	if err != nil {
		return fmt.Errorf("store: atualizar estado do deployment: %w", err)
	}
	return nil
}

// SaveAnalysis persiste o snapshot imutável da análise e o vincula ao
// deployment em curso (RF-02, RF-03).
func (s *Store) SaveAnalysis(ctx context.Context, deploymentID, projectID uuid.UUID, commitSHA string, result domain.AnalysisResult) (uuid.UUID, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: serializar resultado da análise: %w", err)
	}

	analysisID := uuid.New()
	now := time.Now().UTC()

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: abrir transação de análise: %w", err)
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO analyses (id, project_id, commit_sha, rules_version, outcome, result, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		analysisID, projectID, commitSHA, result.RulesVersion, result.Outcome, string(resultJSON), now,
	)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: inserir análise: %w", err)
	}

	_, err = tx.Exec(ctx, `UPDATE deployments SET analysis_id = $1, updated_at = now() WHERE id = $2`, analysisID, deploymentID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: vincular análise ao deployment: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("store: confirmar análise: %w", err)
	}
	return analysisID, nil
}

func (s *Store) GetAnalysis(ctx context.Context, id uuid.UUID) (domain.Analysis, error) {
	var a domain.Analysis
	var resultRaw string
	err := s.pool.QueryRow(ctx, `
		SELECT id, project_id, commit_sha, rules_version, outcome, result, created_at
		FROM analyses WHERE id = $1`, id,
	).Scan(&a.ID, &a.ProjectID, &a.CommitSHA, &a.RulesVersion, &a.Outcome, &resultRaw, &a.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Analysis{}, ErrNotFound
	}
	if err != nil {
		return domain.Analysis{}, fmt.Errorf("store: buscar análise: %w", err)
	}
	if err := json.Unmarshal([]byte(resultRaw), &a.Result); err != nil {
		return domain.Analysis{}, fmt.Errorf("store: decodificar resultado da análise: %w", err)
	}
	return a, nil
}

// AppendEvent grava um evento imutável sem segredos (RF-07, RN-11). O
// número de sequência é calculado dentro da mesma transação; adequado para
// o worker único de M1 (RNF-05).
func (s *Store) AppendEvent(ctx context.Context, deploymentID uuid.UUID, stage, eventType string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("store: serializar payload do evento: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: abrir transação de evento: %w", err)
	}
	defer tx.Rollback(ctx)

	var nextSeq int
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence), 0) + 1 FROM deployment_events WHERE deployment_id = $1`, deploymentID).Scan(&nextSeq); err != nil {
		return fmt.Errorf("store: calcular sequência do evento: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO deployment_events (id, deployment_id, sequence, stage, event_type, occurred_at, payload)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uuid.New(), deploymentID, nextSeq, stage, eventType, time.Now().UTC(), string(payloadJSON),
	)
	if err != nil {
		return fmt.Errorf("store: inserir evento: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *Store) ListEvents(ctx context.Context, deploymentID uuid.UUID) ([]domain.DeploymentEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, deployment_id, sequence, stage, event_type, occurred_at, payload
		FROM deployment_events WHERE deployment_id = $1 ORDER BY sequence`, deploymentID)
	if err != nil {
		return nil, fmt.Errorf("store: listar eventos: %w", err)
	}
	defer rows.Close()

	var events []domain.DeploymentEvent
	for rows.Next() {
		var e domain.DeploymentEvent
		var payloadRaw string
		if err := rows.Scan(&e.ID, &e.DeploymentID, &e.Sequence, &e.Stage, &e.EventType, &e.OccurredAt, &payloadRaw); err != nil {
			return nil, fmt.Errorf("store: ler evento: %w", err)
		}
		if err := json.Unmarshal([]byte(payloadRaw), &e.Payload); err != nil {
			return nil, fmt.Errorf("store: decodificar payload do evento: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// MarkJobDone encerra o job, liberando-o permanentemente.
func (s *Store) MarkJobDone(ctx context.Context, jobID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE jobs SET state = $1, updated_at = now() WHERE id = $2`, domain.JobDone, jobID)
	if err != nil {
		return fmt.Errorf("store: concluir job: %w", err)
	}
	return nil
}

func (s *Store) CreateRelease(ctx context.Context, deploymentID uuid.UUID, imageID, containerID, operationalState, health string, hostPort int) (domain.Release, error) {
	r := domain.Release{
		ID:               uuid.New(),
		DeploymentID:     deploymentID,
		ImageID:          imageID,
		ContainerID:      containerID,
		OperationalState: operationalState,
		Health:           health,
		HostPort:         hostPort,
		CreatedAt:        time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO releases (id, deployment_id, image_id, container_id, operational_state, health, host_port, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		r.ID, r.DeploymentID, r.ImageID, r.ContainerID, r.OperationalState, r.Health, r.HostPort, r.CreatedAt,
	)
	if err != nil {
		return domain.Release{}, fmt.Errorf("store: criar release: %w", err)
	}
	return r, nil
}

func (s *Store) GetReleaseByDeployment(ctx context.Context, deploymentID uuid.UUID) (domain.Release, error) {
	var r domain.Release
	err := s.pool.QueryRow(ctx, `
		SELECT id, deployment_id, image_id, container_id, operational_state, health, host_port, created_at
		FROM releases WHERE deployment_id = $1 ORDER BY created_at DESC LIMIT 1`, deploymentID,
	).Scan(&r.ID, &r.DeploymentID, &r.ImageID, &r.ContainerID, &r.OperationalState, &r.Health, &r.HostPort, &r.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Release{}, ErrNotFound
	}
	if err != nil {
		return domain.Release{}, fmt.Errorf("store: buscar release: %w", err)
	}
	return r, nil
}
