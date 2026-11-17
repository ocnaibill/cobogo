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
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ocnaibill/cobogo/internal/domain"
)

var (
	ErrNotFound = errors.New("store: registro não encontrado")
	// ErrNotAwaitingInput indica uma resposta a um deploy que não está em
	// NEEDS_INPUT (já respondido, em andamento ou encerrado).
	ErrNotAwaitingInput = errors.New("store: o deploy não está aguardando informações")
)

type Store struct {
	pool   *pgxpool.Pool
	cipher Cipher
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// CreateProject registra um repositório para deploy (RF-01) em nome do dono.
func (s *Store) CreateProject(ctx context.Context, ownerID uuid.UUID, slug, repositoryURL, rootDirectory string) (domain.Project, error) {
	p := domain.Project{
		ID:            uuid.New(),
		OwnerID:       &ownerID,
		Slug:          slug,
		RepositoryURL: repositoryURL,
		RootDirectory: rootDirectory,
		CreatedAt:     time.Now().UTC(),
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO projects (id, owner_id, slug, repository_url, root_directory, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		p.ID, p.OwnerID, p.Slug, p.RepositoryURL, p.RootDirectory, p.CreatedAt,
	)
	if err != nil {
		return domain.Project{}, fmt.Errorf("store: criar projeto: %w", err)
	}
	return p, nil
}

// GetProject lê um projeto sem filtrar por dono. É para o worker, que age em
// nome de um deploy já autorizado; a API usa GetProjectFor.
func (s *Store) GetProject(ctx context.Context, id uuid.UUID) (domain.Project, error) {
	var p domain.Project
	var limitsRaw string
	err := s.pool.QueryRow(ctx, `
		SELECT id, owner_id, slug, repository_url, root_directory, limits, created_at
		FROM projects WHERE id = $1`, id,
	).Scan(&p.ID, &p.OwnerID, &p.Slug, &p.RepositoryURL, &p.RootDirectory, &limitsRaw, &p.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Project{}, ErrNotFound
	}
	if err != nil {
		return domain.Project{}, fmt.Errorf("store: buscar projeto: %w", err)
	}
	if p.Limits, err = scanLimits(limitsRaw); err != nil {
		return domain.Project{}, err
	}
	return p, nil
}

// GetProjectFor lê um projeto do dono. Um projeto de outro dono é tratado como
// inexistente: a API não revela que ele existe (DEC-20, DEC-27).
func (s *Store) GetProjectFor(ctx context.Context, id, ownerID uuid.UUID) (domain.Project, error) {
	p, err := s.GetProject(ctx, id)
	if err != nil {
		return domain.Project{}, err
	}
	if p.OwnerID == nil || *p.OwnerID != ownerID {
		return domain.Project{}, ErrNotFound
	}
	return p, nil
}

// GetDeploymentFor lê um deploy cujo projeto pertence ao dono.
func (s *Store) GetDeploymentFor(ctx context.Context, id, ownerID uuid.UUID) (domain.Deployment, error) {
	dep, err := s.GetDeployment(ctx, id)
	if err != nil {
		return domain.Deployment{}, err
	}
	if _, err := s.GetProjectFor(ctx, dep.ProjectID, ownerID); err != nil {
		return domain.Deployment{}, err
	}
	return dep, nil
}

// CreateDeploymentWithJob cria o deploy e o job na mesma transação (§12.3),
// evitando perder trabalho entre banco e fila. RN-07: idempotency_key é
// única por projeto.
func (s *Store) CreateDeploymentWithJob(ctx context.Context, projectID uuid.UUID, idempotencyKey string, overrides map[string]string) (domain.Deployment, domain.Job, error) {
	now := time.Now().UTC()
	if overrides == nil {
		overrides = map[string]string{}
	}
	overridesJSON, err := json.Marshal(overrides)
	if err != nil {
		return domain.Deployment{}, domain.Job{}, fmt.Errorf("store: serializar overrides: %w", err)
	}
	dep := domain.Deployment{
		ID:             uuid.New(),
		ProjectID:      projectID,
		State:          domain.DeploymentQueued,
		IdempotencyKey: idempotencyKey,
		Overrides:      overrides,
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
		INSERT INTO deployments (id, project_id, commit_sha, state, idempotency_key, overrides, created_at, updated_at)
		VALUES ($1, $2, '', $3, $4, $5, $6, $7)`,
		dep.ID, dep.ProjectID, dep.State, dep.IdempotencyKey, string(overridesJSON), dep.CreatedAt, dep.UpdatedAt,
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

// ClaimNextJob reivindica um job via SKIP LOCKED (§12.3): um PENDING ou um
// LEASED cujo lease venceu (o worker anterior caiu; DEC-26). Cada
// reivindicação incrementa a geração de posse e o número de tentativas. Um job
// que já atingiu maxAttempts é encerrado e o deploy vai a FAILED, em vez de
// derrubar o worker de novo. Devolve (nil, nil, nil) quando não há trabalho.
func (s *Store) ClaimNextJob(ctx context.Context, lease time.Duration, maxAttempts int) (*domain.Job, *domain.Deployment, error) {
	for {
		job, dep, again, err := s.claimOnce(ctx, lease, maxAttempts)
		if err != nil || !again {
			return job, dep, err
		}
	}
}

// claimOnce devolve again=true quando só encerrou um job esgotado e há que
// procurar o próximo.
func (s *Store) claimOnce(ctx context.Context, lease time.Duration, maxAttempts int) (job *domain.Job, dep *domain.Deployment, again bool, err error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, nil, false, fmt.Errorf("store: abrir transação de claim: %w", err)
	}
	defer tx.Rollback(ctx)

	var jobID, deploymentID uuid.UUID
	var attempts int
	err = tx.QueryRow(ctx, `
		SELECT id, deployment_id, attempt_count FROM jobs
		WHERE (state = $1 AND (lease_until IS NULL OR lease_until < now()))
		   OR (state = $2 AND lease_until < now())
		ORDER BY created_at
		FOR UPDATE SKIP LOCKED
		LIMIT 1`, domain.JobPending, domain.JobLeased,
	).Scan(&jobID, &deploymentID, &attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, fmt.Errorf("store: selecionar próximo job: %w", err)
	}

	if attempts >= maxAttempts {
		if err := exhaustJob(ctx, tx, jobID, deploymentID, attempts); err != nil {
			return nil, nil, false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, nil, false, fmt.Errorf("store: confirmar encerramento do job: %w", err)
		}
		return nil, nil, true, nil
	}

	var claimed domain.Job
	err = tx.QueryRow(ctx, `
		UPDATE jobs SET state = $1, lease_until = now() + make_interval(secs => $2),
			ownership_generation = ownership_generation + 1, attempt_count = attempt_count + 1, updated_at = now()
		WHERE id = $3
		RETURNING id, deployment_id, state, lease_until, ownership_generation, attempt_count, created_at, updated_at`,
		domain.JobLeased, lease.Seconds(), jobID,
	).Scan(&claimed.ID, &claimed.DeploymentID, &claimed.State, &claimed.LeaseUntil, &claimed.OwnershipGeneration, &claimed.AttemptCount, &claimed.CreatedAt, &claimed.UpdatedAt)
	if err != nil {
		return nil, nil, false, fmt.Errorf("store: marcar job como leased: %w", err)
	}

	d, err := s.getDeploymentTx(ctx, tx, claimed.DeploymentID)
	if err != nil {
		return nil, nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, false, fmt.Errorf("store: confirmar claim: %w", err)
	}
	return &claimed, &d, false, nil
}

// exhaustJob encerra um job que esgotou as tentativas e falha o deploy, se ele
// ainda não terminou.
func exhaustJob(ctx context.Context, tx pgx.Tx, jobID, deploymentID uuid.UUID, attempts int) error {
	if _, err := tx.Exec(ctx, `UPDATE jobs SET state = $1, lease_until = NULL, updated_at = now() WHERE id = $2`, domain.JobDone, jobID); err != nil {
		return fmt.Errorf("store: encerrar job esgotado: %w", err)
	}
	reason := fmt.Sprintf("tentativas esgotadas: o worker foi interrompido durante %d tentativas", attempts)
	tag, err := tx.Exec(ctx, `
		UPDATE deployments SET state = $1, failure_reason = $2, updated_at = now()
		WHERE id = $3 AND state NOT IN ($4, $5, $6, $7, $8)`,
		domain.DeploymentFailed, reason, deploymentID,
		domain.DeploymentSucceeded, domain.DeploymentFailed, domain.DeploymentUnsupported, domain.DeploymentCancelled, domain.DeploymentNeedsInput)
	if err != nil {
		return fmt.Errorf("store: falhar deploy esgotado: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return appendEventTx(ctx, tx, deploymentID, "pipeline", "failed", map[string]any{"error": reason, "attempts": attempts})
	}
	return nil
}

const deploymentColumns = `id, project_id, analysis_id, commit_sha, state, idempotency_key, overrides, failure_reason, created_at, updated_at`

func scanDeployment(row pgx.Row) (domain.Deployment, error) {
	var dep domain.Deployment
	var overridesRaw string
	err := row.Scan(&dep.ID, &dep.ProjectID, &dep.AnalysisID, &dep.CommitSHA, &dep.State, &dep.IdempotencyKey, &overridesRaw, &dep.FailureReason, &dep.CreatedAt, &dep.UpdatedAt)
	if err != nil {
		return domain.Deployment{}, err
	}
	if err := json.Unmarshal([]byte(overridesRaw), &dep.Overrides); err != nil {
		return domain.Deployment{}, fmt.Errorf("decodificar overrides: %w", err)
	}
	if dep.Overrides == nil {
		dep.Overrides = map[string]string{}
	}
	return dep, nil
}

func (s *Store) getDeploymentTx(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Deployment, error) {
	dep, err := scanDeployment(tx.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id = $1`, id))
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: buscar deployment: %w", err)
	}
	return dep, nil
}

func (s *Store) GetDeployment(ctx context.Context, id uuid.UUID) (domain.Deployment, error) {
	dep, err := scanDeployment(s.pool.QueryRow(ctx, `SELECT `+deploymentColumns+` FROM deployments WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Deployment{}, ErrNotFound
	}
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: buscar deployment: %w", err)
	}
	return dep, nil
}

// AnswerDeployment registra a resposta do usuário a uma pendência (RF-08):
// mescla os overrides, devolve o deploy à fila e reabre o job, tudo numa
// transação. O commit já fixado é mantido, então a nova análise roda sobre o
// mesmo SHA (RN-04). Só vale para um deploy em NEEDS_INPUT; respostas
// concorrentes ao mesmo deploy não se duplicam.
func (s *Store) AnswerDeployment(ctx context.Context, id uuid.UUID, answers map[string]string) (domain.Deployment, error) {
	answersJSON, err := json.Marshal(answers)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: serializar respostas: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: abrir transação de resposta: %w", err)
	}
	defer tx.Rollback(ctx)

	dep, err := scanDeployment(tx.QueryRow(ctx, `
		UPDATE deployments
		SET overrides = overrides || $1::jsonb, state = $2, failure_reason = NULL, updated_at = now()
		WHERE id = $3 AND state = $4
		RETURNING `+deploymentColumns,
		string(answersJSON), domain.DeploymentQueued, id, domain.DeploymentNeedsInput))
	if errors.Is(err, pgx.ErrNoRows) {
		var exists bool
		if qerr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM deployments WHERE id = $1)`, id).Scan(&exists); qerr != nil {
			return domain.Deployment{}, fmt.Errorf("store: verificar deployment: %w", qerr)
		}
		if !exists {
			return domain.Deployment{}, ErrNotFound
		}
		return domain.Deployment{}, ErrNotAwaitingInput
	}
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: registrar resposta: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		UPDATE jobs SET state = $1, lease_until = NULL, attempt_count = 0, updated_at = now()
		WHERE deployment_id = $2 AND state = $3`,
		domain.JobPending, id, domain.JobDone)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: reabrir job: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return domain.Deployment{}, fmt.Errorf("store: o job do deploy %s não está concluído; não foi possível reenfileirar", id)
	}

	// Só os nomes dos campos vão para o evento (RN-11): um comando informado
	// pode embutir um segredo.
	fields := make([]string, 0, len(answers))
	for field := range answers {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	if err := appendEventTx(ctx, tx, id, "needs_input", "answered", map[string]any{"fields": fields}); err != nil {
		return domain.Deployment{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Deployment{}, fmt.Errorf("store: confirmar resposta: %w", err)
	}
	return dep, nil
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

func appendEventTx(ctx context.Context, tx pgx.Tx, deploymentID uuid.UUID, stage, eventType string, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("store: serializar payload do evento: %w", err)
	}

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
	return nil
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
