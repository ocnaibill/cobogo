package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Operações de ciclo de vida (DEC-38): a API pede, o worker executa.

const operationColumns = `id, project_id, kind, state, params, result, error, attempts, not_before, created_at, updated_at`

func scanOperation(row pgx.Row) (domain.Operation, error) {
	var op domain.Operation
	var params, result []byte
	err := row.Scan(&op.ID, &op.ProjectID, &op.Kind, &op.State, &params, &result, &op.Error, &op.Attempts, &op.NotBefore, &op.CreatedAt, &op.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, ErrNotFound
	}
	if err != nil {
		return domain.Operation{}, fmt.Errorf("store: ler operação: %w", err)
	}
	if err := json.Unmarshal(params, &op.Params); err != nil {
		return domain.Operation{}, fmt.Errorf("store: ler parâmetros da operação: %w", err)
	}
	if err := json.Unmarshal(result, &op.Result); err != nil {
		return domain.Operation{}, fmt.Errorf("store: ler resultado da operação: %w", err)
	}
	return op, nil
}

func marshalMap(m map[string]any) ([]byte, error) {
	if m == nil {
		m = map[string]any{}
	}
	return json.Marshal(m)
}

// notBeforeSQL: o relógio de quem pede (a API, o worker) e o do banco podem
// diferir por alguns milissegundos, e uma operação "para agora" não pode ficar
// esperando por isso. Um `not_before` até 1 s no futuro vale como imediato; os
// atrasos de verdade (o período de espera da aposentadoria) são de segundos ou
// mais e ficam como pedidos.
const notBeforeSQL = `CASE WHEN $5::timestamptz <= now() + interval '1 second' THEN now() ELSE $5::timestamptz END`

// insertOperationTx cria uma operação PENDING. Para a aposentadoria de
// releases, uma operação pendente do mesmo projeto é reaproveitada: um deploy
// atrás do outro não empilha pedidos iguais.
func insertOperationTx(ctx context.Context, tx pgx.Tx, projectID *uuid.UUID, kind domain.OperationKind, params map[string]any, notBefore time.Time, requestedBy *uuid.UUID) (domain.Operation, error) {
	if kind == domain.OpRetireReleases && projectID != nil {
		op, err := scanOperation(tx.QueryRow(ctx, `
			UPDATE operations SET not_before = $3, updated_at = now()
			WHERE id = (SELECT id FROM operations WHERE project_id = $1 AND kind = $2 AND state = 'PENDING' LIMIT 1)
			RETURNING `+operationColumns, projectID, kind, notBefore))
		if err == nil {
			return op, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return domain.Operation{}, err
		}
	}
	raw, err := marshalMap(params)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("store: serializar parâmetros: %w", err)
	}
	return scanOperation(tx.QueryRow(ctx, `
		INSERT INTO operations (id, project_id, kind, state, params, not_before, requested_by)
		VALUES ($1, $2, $3, 'PENDING', $4, `+notBeforeSQL+`, $6)
		RETURNING `+operationColumns, uuid.New(), projectID, kind, string(raw), notBefore, requestedBy))
}

// EnqueueOperation cria uma operação pendente. projectID é nulo na faxina.
func (s *Store) EnqueueOperation(ctx context.Context, projectID *uuid.UUID, kind domain.OperationKind, params map[string]any, notBefore time.Time, requestedBy *uuid.UUID) (domain.Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("store: abrir transação da operação: %w", err)
	}
	defer tx.Rollback(ctx)
	op, err := insertOperationTx(ctx, tx, projectID, kind, params, notBefore, requestedBy)
	if err != nil {
		return domain.Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, fmt.Errorf("store: confirmar operação: %w", err)
	}
	return op, nil
}

// RecordOperation grava uma operação já concluída (a faxina), com o que ela fez.
func (s *Store) RecordOperation(ctx context.Context, projectID *uuid.UUID, kind domain.OperationKind, result map[string]any) error {
	raw, err := marshalMap(result)
	if err != nil {
		return fmt.Errorf("store: serializar resultado: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO operations (id, project_id, kind, state, result, attempts)
		VALUES ($1, $2, $3, 'DONE', $4, 1)`, uuid.New(), projectID, kind, string(raw))
	if err != nil {
		return fmt.Errorf("store: registrar operação: %w", err)
	}
	return nil
}

// GetOperationFor lê uma operação de um projeto do dono. Operações sem projeto
// (a faxina) não aparecem na API.
func (s *Store) GetOperationFor(ctx context.Context, id, ownerID uuid.UUID) (domain.Operation, error) {
	return scanOperation(s.pool.QueryRow(ctx, `
		SELECT o.id, o.project_id, o.kind, o.state, o.params, o.result, o.error, o.attempts, o.not_before, o.created_at, o.updated_at
		FROM operations o JOIN projects p ON p.id = o.project_id
		WHERE o.id = $1 AND p.owner_id = $2`, id, ownerID))
}

// ListOperationsFor lista as operações mais recentes de um projeto do dono,
// inclusive de um projeto excluído (a limpeza dele ainda pode estar pendente).
func (s *Store) ListOperationsFor(ctx context.Context, projectID, ownerID uuid.UUID, limit int) ([]domain.Operation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT o.id, o.project_id, o.kind, o.state, o.params, o.result, o.error, o.attempts, o.not_before, o.created_at, o.updated_at
		FROM operations o JOIN projects p ON p.id = o.project_id
		WHERE o.project_id = $1 AND p.owner_id = $2
		ORDER BY o.created_at DESC, o.id DESC LIMIT $3`, projectID, ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listar operações: %w", err)
	}
	defer rows.Close()
	ops := []domain.Operation{}
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		ops = append(ops, op)
	}
	return ops, rows.Err()
}

// ClaimNextOperation reivindica a próxima operação vencida: pendente com
// not_before no passado, ou em execução com a posse vencida (o worker caiu).
// Uma operação que esgotou as tentativas fica FAILED.
func (s *Store) ClaimNextOperation(ctx context.Context, lease time.Duration, maxAttempts int) (*domain.Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: abrir transação da reivindicação: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		UPDATE operations SET state = 'FAILED', error = 'esgotou as tentativas', lease_until = NULL, updated_at = now()
		WHERE state = 'RUNNING' AND lease_until < now() AND attempts >= $1`, maxAttempts); err != nil {
		return nil, fmt.Errorf("store: encerrar operações esgotadas: %w", err)
	}
	var id uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT id FROM operations
		WHERE (state = 'PENDING' AND not_before <= now())
		   OR (state = 'RUNNING' AND lease_until < now() AND attempts < $1)
		ORDER BY not_before, created_at LIMIT 1 FOR UPDATE SKIP LOCKED`, maxAttempts).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: buscar operação: %w", err)
	}
	op, err := scanOperation(tx.QueryRow(ctx, `
		UPDATE operations SET state = 'RUNNING', attempts = attempts + 1, lease_until = now() + make_interval(secs => $2), updated_at = now()
		WHERE id = $1 RETURNING `+operationColumns, id, lease.Seconds()))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: confirmar reivindicação: %w", err)
	}
	return &op, nil
}

// FinishOperation conclui uma operação com o seu resultado.
func (s *Store) FinishOperation(ctx context.Context, id uuid.UUID, result map[string]any) error {
	raw, err := marshalMap(result)
	if err != nil {
		return fmt.Errorf("store: serializar resultado: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE operations SET state = 'DONE', result = $2, error = '', lease_until = NULL, updated_at = now() WHERE id = $1`, id, string(raw))
	if err != nil {
		return fmt.Errorf("store: concluir operação: %w", err)
	}
	return nil
}

// FailOperation registra uma falha: com tentativas restantes volta a PENDING
// depois de backoff; sem elas, fica FAILED (RN-09). Um erro permanente
// (retry=false) encerra na hora.
func (s *Store) FailOperation(ctx context.Context, id uuid.UUID, cause string, retry bool, maxAttempts int, backoff time.Duration, result map[string]any) error {
	raw, err := marshalMap(result)
	if err != nil {
		return fmt.Errorf("store: serializar resultado: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE operations SET
			state = CASE WHEN $3 AND attempts < $4 THEN 'PENDING' ELSE 'FAILED' END,
			error = $2, result = $6, lease_until = NULL,
			not_before = CASE WHEN $3 AND attempts < $4 THEN now() + make_interval(secs => $5) ELSE not_before END,
			updated_at = now()
		WHERE id = $1`, id, cause, retry, maxAttempts, backoff.Seconds(), string(raw))
	if err != nil {
		return fmt.Errorf("store: registrar falha da operação: %w", err)
	}
	return nil
}

// DeleteProject exclui um projeto de forma lógica (DEC-38 item 5), numa só
// transação: recusa se há deploy em execução, cancela os que esperam, apaga as
// variáveis cifradas, marca o projeto como excluído (a rota some e a API o
// esconde) e enfileira a limpeza. O histórico fica.
func (s *Store) DeleteProject(ctx context.Context, projectID, ownerID uuid.UUID, confirmSlug string, deleteData bool) (domain.Operation, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("store: abrir transação da exclusão: %w", err)
	}
	defer tx.Rollback(ctx)

	var slug string
	err = tx.QueryRow(ctx, `
		SELECT slug FROM projects WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL FOR UPDATE`, projectID, ownerID).Scan(&slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Operation{}, ErrNotFound
	}
	if err != nil {
		return domain.Operation{}, fmt.Errorf("store: travar projeto: %w", err)
	}
	if confirmSlug != slug {
		return domain.Operation{}, ErrConfirmationMismatch
	}

	rows, err := tx.Query(ctx, `
		SELECT d.id, d.state, j.state
		FROM deployments d JOIN jobs j ON j.deployment_id = d.id
		WHERE d.project_id = $1 AND d.state NOT IN ('SUCCEEDED', 'FAILED', 'UNSUPPORTED', 'CANCELLED')
		FOR UPDATE OF d, j`, projectID)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("store: listar deploys do projeto: %w", err)
	}
	type waiting struct {
		id    uuid.UUID
		state domain.DeploymentState
	}
	var toCancel []waiting
	for rows.Next() {
		var w waiting
		var jobState domain.JobState
		if err := rows.Scan(&w.id, &w.state, &jobState); err != nil {
			rows.Close()
			return domain.Operation{}, fmt.Errorf("store: ler deploy do projeto: %w", err)
		}
		if (w.state != domain.DeploymentQueued && w.state != domain.DeploymentNeedsInput) || jobState == domain.JobLeased {
			rows.Close()
			return domain.Operation{}, ErrDeploymentInProgress
		}
		toCancel = append(toCancel, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return domain.Operation{}, fmt.Errorf("store: ler deploys do projeto: %w", err)
	}
	for _, w := range toCancel {
		if _, err := tx.Exec(ctx, `UPDATE deployments SET state = $1, updated_at = now() WHERE id = $2`, domain.DeploymentCancelled, w.id); err != nil {
			return domain.Operation{}, fmt.Errorf("store: cancelar deploy: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE jobs SET state = $1, lease_until = NULL, updated_at = now() WHERE deployment_id = $2`, domain.JobDone, w.id); err != nil {
			return domain.Operation{}, fmt.Errorf("store: encerrar job: %w", err)
		}
		if err := appendEventTx(ctx, tx, w.id, "pipeline", "cancelled", map[string]any{"from": string(w.state), "reason": "projeto excluído"}); err != nil {
			return domain.Operation{}, err
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM env_variables WHERE project_id = $1`, projectID); err != nil {
		return domain.Operation{}, fmt.Errorf("store: apagar variáveis: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE projects SET deleted_at = now() WHERE id = $1`, projectID); err != nil {
		return domain.Operation{}, fmt.Errorf("store: marcar projeto excluído: %w", err)
	}
	op, err := insertOperationTx(ctx, tx, &projectID, domain.OpDeleteProject, map[string]any{"slug": slug, "delete_data": deleteData}, time.Now().UTC(), &ownerID)
	if err != nil {
		return domain.Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, fmt.Errorf("store: confirmar exclusão: %w", err)
	}
	return op, nil
}

// RequestApplyLimits enfileira a atualização dos limites da release ativa
// (#33). Recusa se o projeto não tem release no ar.
func (s *Store) RequestApplyLimits(ctx context.Context, projectID, ownerID uuid.UUID) (domain.Operation, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return domain.Operation{}, err
	}
	if _, err := s.ActiveRelease(ctx, projectID); err != nil {
		return domain.Operation{}, err
	}
	return s.EnqueueOperation(ctx, &projectID, domain.OpApplyLimits, nil, time.Now().UTC(), &ownerID)
}

// ActiveRelease devolve a release ativa mais recente do projeto, ou
// ErrNoActiveRelease.
func (s *Store) ActiveRelease(ctx context.Context, projectID uuid.UUID) (domain.Release, error) {
	var r domain.Release
	err := s.pool.QueryRow(ctx, `
		SELECT r.id, r.deployment_id, r.image_id, r.container_id, r.operational_state, r.health, r.host_port, r.url, r.route_host, r.route_target, r.created_at, r.stopped_at
		FROM releases r JOIN deployments d ON d.id = r.deployment_id
		WHERE d.project_id = $1 AND r.operational_state = 'active'
		ORDER BY r.created_at DESC LIMIT 1`, projectID,
	).Scan(&r.ID, &r.DeploymentID, &r.ImageID, &r.ContainerID, &r.OperationalState, &r.Health, &r.HostPort, &r.URL, &r.RouteHost, &r.RouteTarget, &r.CreatedAt, &r.StoppedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Release{}, ErrNoActiveRelease
	}
	if err != nil {
		return domain.Release{}, fmt.Errorf("store: buscar release ativa: %w", err)
	}
	return r, nil
}

// ContainerFact é o que o banco sabe sobre o contêiner de um deploy, para a
// aposentadoria e a faxina decidirem sem adivinhar.
type ContainerFact struct {
	DeploymentID    uuid.UUID
	DeploymentState domain.DeploymentState
	ProjectID       uuid.UUID
	ProjectDeleted  bool
	ActiveContainer string
	// ActiveDeployment é o deploy da release ativa: os processos além do web dele (DEC-51) pertencem a ela.
	ActiveDeployment uuid.UUID
	// ActiveSince é desde quando a release ativa do projeto está ativa; nulo
	// sem release ativa. O período de espera conta a partir daí.
	ActiveSince      *time.Time
	ProjectSlug      string
	DeploymentUpdate time.Time
}

// ContainerFacts devolve, por deploy, o estado dele, o do projeto e o contêiner
// da release ativa do projeto. Deploys que não existem ficam de fora.
func (s *Store) ContainerFacts(ctx context.Context, deploymentIDs []uuid.UUID) (map[uuid.UUID]ContainerFact, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT d.id, d.state, d.updated_at, p.id, p.slug, p.deleted_at IS NOT NULL, COALESCE(a.container_id, ''), COALESCE(a.deployment_id, '00000000-0000-0000-0000-000000000000'::uuid), a.created_at
		FROM deployments d JOIN projects p ON p.id = d.project_id
		LEFT JOIN LATERAL (
			SELECT r.container_id, r.deployment_id, r.created_at FROM releases r JOIN deployments dd ON dd.id = r.deployment_id
			WHERE dd.project_id = p.id AND r.operational_state = 'active'
			ORDER BY r.created_at DESC LIMIT 1
		) a ON true
		WHERE d.id = ANY($1)`, deploymentIDs)
	if err != nil {
		return nil, fmt.Errorf("store: ler fatos dos contêineres: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]ContainerFact{}
	for rows.Next() {
		var f ContainerFact
		if err := rows.Scan(&f.DeploymentID, &f.DeploymentState, &f.DeploymentUpdate, &f.ProjectID, &f.ProjectSlug, &f.ProjectDeleted, &f.ActiveContainer, &f.ActiveDeployment, &f.ActiveSince); err != nil {
			return nil, fmt.Errorf("store: ler fato do contêiner: %w", err)
		}
		out[f.DeploymentID] = f
	}
	return out, rows.Err()
}

// MarkReleasesRetired marca como aposentadas as releases substituídas cujos
// contêineres saíram (DEC-38 item 2). Só uma release já substituída pode ser
// aposentada: a ativa nunca, mesmo que alguém chame com o contêiner errado.
func (s *Store) MarkReleasesRetired(ctx context.Context, containerIDs []string) error {
	if len(containerIDs) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE releases SET operational_state = 'retired'
		WHERE container_id = ANY($1) AND operational_state = 'superseded'`, containerIDs)
	if err != nil {
		return fmt.Errorf("store: aposentar releases: %w", err)
	}
	return nil
}

// MarkProjectReleasesRemoved marca todas as releases de um projeto excluído
// como removidas (DEC-38 item 5).
func (s *Store) MarkProjectReleasesRemoved(ctx context.Context, projectID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE releases SET operational_state = 'removed'
		WHERE operational_state <> 'removed'
		  AND deployment_id IN (SELECT id FROM deployments WHERE project_id = $1)`, projectID)
	if err != nil {
		return fmt.Errorf("store: marcar releases removidas: %w", err)
	}
	return nil
}

// AppendEvent grava um evento no histórico de um deploy fora do pipeline, como
// o ciclo de vida faz (DEC-38): a release foi aposentada, os limites mudaram,
// o contêiner saiu na faxina. Não exige posse: o deploy já terminou.
func (s *Store) AppendEvent(ctx context.Context, deploymentID uuid.UUID, stage, eventType string, payload map[string]any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: abrir transação do evento: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := appendEventTx(ctx, tx, deploymentID, stage, eventType, payload); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: confirmar evento: %w", err)
	}
	return nil
}

// RequestAppOperation enfileira parar, iniciar ou reiniciar a release ativa
// (DEC-40). Recusa sem release no ar e quando o pedido não muda nada (parar o
// que já está parado, iniciar o que já está no ar); reiniciar sempre vale.
func (s *Store) RequestAppOperation(ctx context.Context, projectID, ownerID uuid.UUID, kind domain.OperationKind) (domain.Operation, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return domain.Operation{}, err
	}
	active, err := s.ActiveRelease(ctx, projectID)
	if err != nil {
		return domain.Operation{}, err
	}
	switch {
	case kind == domain.OpStopApp && active.StoppedAt != nil:
		return domain.Operation{}, ErrAppAlreadyStopped
	case kind == domain.OpStartApp && active.StoppedAt == nil:
		return domain.Operation{}, ErrAppAlreadyRunning
	}
	return s.EnqueueOperation(ctx, &projectID, kind, nil, time.Now().UTC(), &ownerID)
}

// SetAppStopped marca a release ativa como parada ou em execução (DEC-40). Só
// vale para uma release que ainda é a ativa: se um deploy a substituiu no meio
// do caminho, não há o que marcar.
func (s *Store) SetAppStopped(ctx context.Context, releaseID uuid.UUID, stopped bool) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE releases SET stopped_at = CASE WHEN $2 THEN COALESCE(stopped_at, now()) ELSE NULL END
		WHERE id = $1 AND operational_state = 'active'`, releaseID, stopped)
	if err != nil {
		return fmt.Errorf("store: marcar a aplicação como parada: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNoActiveRelease
	}
	return nil
}

// UpdateReleaseHostPort guarda a porta de host nova de uma release ativa que
// foi reiniciada: a porta publicada é efêmera e pode mudar (DEC-40).
func (s *Store) UpdateReleaseHostPort(ctx context.Context, releaseID uuid.UUID, hostPort int) error {
	_, err := s.pool.Exec(ctx, `UPDATE releases SET host_port = $2 WHERE id = $1 AND operational_state = 'active'`, releaseID, hostPort)
	if err != nil {
		return fmt.Errorf("store: atualizar a porta da release: %w", err)
	}
	return nil
}

// AppStoppedBySlug diz se o projeto de um slug existe e está com a aplicação
// parada. Serve à página de inativo (DEC-40) e só lê o que ela mostra.
func (s *Store) AppStoppedBySlug(ctx context.Context, slug string) (bool, error) {
	var stopped bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM projects p
			JOIN deployments d ON d.project_id = p.id
			JOIN releases r ON r.deployment_id = d.id
			WHERE p.slug = $1 AND p.deleted_at IS NULL AND r.operational_state = 'active' AND r.stopped_at IS NOT NULL)`, slug).Scan(&stopped)
	if err != nil {
		return false, fmt.Errorf("store: ler se a aplicação está parada: %w", err)
	}
	return stopped, nil
}
