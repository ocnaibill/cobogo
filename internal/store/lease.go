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

var (
	// ErrLeaseLost indica que o worker já não é o dono do job: outro o
	// reivindicou depois de o lease vencer. Quem recebe este erro não pode
	// gravar mais nada sobre o deploy (§12.3, DEC-26).
	ErrLeaseLost = errors.New("store: o lease do job foi perdido")
	// ErrInvalidTransition indica uma mudança de estado fora do §15.2.
	ErrInvalidTransition = errors.New("store: transição de estado inválida")
)

// Lease identifica a posse de um job por um worker: o job e a geração em que
// foi reivindicado.
type Lease struct {
	JobID      uuid.UUID
	Generation int
}

// LeaseOf devolve a posse que a reivindicação de job concedeu.
func LeaseOf(job *domain.Job) Lease {
	return Lease{JobID: job.ID, Generation: job.OwnershipGeneration}
}

// Owned reúne as escritas que só o dono de um job pode fazer. Cada uma
// confere, na mesma transação, que a geração do job ainda é a do lease, e
// trava a linha do job enquanto grava: uma reivindicação concorrente (SKIP
// LOCKED) a pula até a escrita terminar. Escritas de um worker que perdeu a
// posse falham com ErrLeaseLost e não alteram nada.
type Owned struct {
	s     *Store
	lease Lease
}

// Owned devolve as escritas protegidas por lease.
func (s *Store) Owned(lease Lease) *Owned { return &Owned{s: s, lease: lease} }

func (o *Owned) Lease() Lease { return o.lease }

func (o *Owned) withLease(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := o.s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: abrir transação protegida por lease: %w", err)
	}
	defer tx.Rollback(ctx)

	var one int
	err = tx.QueryRow(ctx, `
		SELECT 1 FROM jobs WHERE id = $1 AND ownership_generation = $2 AND state = $3 FOR SHARE`,
		o.lease.JobID, o.lease.Generation, domain.JobLeased).Scan(&one)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("store: conferir lease: %w", err)
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: confirmar escrita protegida: %w", err)
	}
	return nil
}

// Check confere que a posse continua válida, sem gravar nada.
func (o *Owned) Check(ctx context.Context) error {
	return o.withLease(ctx, func(pgx.Tx) error { return nil })
}

// Renew estende o lease (heartbeat). O relógio é o do banco, então a
// diferença entre máquinas não afeta o vencimento.
func (o *Owned) Renew(ctx context.Context, lease time.Duration) error {
	tag, err := o.s.pool.Exec(ctx, `
		UPDATE jobs SET lease_until = now() + make_interval(secs => $1), updated_at = now()
		WHERE id = $2 AND ownership_generation = $3 AND state = $4`,
		lease.Seconds(), o.lease.JobID, o.lease.Generation, domain.JobLeased)
	if err != nil {
		return fmt.Errorf("store: renovar lease: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrLeaseLost
	}
	return nil
}

// Finish encerra o job, liberando-o permanentemente.
func (o *Owned) Finish(ctx context.Context) error {
	return o.withLease(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE jobs SET state = $1, lease_until = NULL, updated_at = now() WHERE id = $2`, domain.JobDone, o.lease.JobID)
		if err != nil {
			return fmt.Errorf("store: concluir job: %w", err)
		}
		return nil
	})
}

// Release devolve o job à fila sem contar a tentativa (desligamento limpo do
// worker); a próxima reivindicação retoma do início.
func (o *Owned) Release(ctx context.Context) error {
	return o.withLease(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE jobs SET state = $1, lease_until = NULL, attempt_count = GREATEST(attempt_count - 1, 0), updated_at = now()
			WHERE id = $2`, domain.JobPending, o.lease.JobID)
		if err != nil {
			return fmt.Errorf("store: devolver job à fila: %w", err)
		}
		return nil
	})
}

// Transition avança a máquina de estados do §15.2. O estado atual é lido sob
// trava; uma mudança fora da tabela de domain.CanTransition falha com
// ErrInvalidTransition e não altera nada. failureReason só vale para FAILED.
func (o *Owned) Transition(ctx context.Context, deploymentID uuid.UUID, to domain.DeploymentState, failureReason string) error {
	return o.withLease(ctx, func(tx pgx.Tx) error {
		var from domain.DeploymentState
		if err := tx.QueryRow(ctx, `SELECT state FROM deployments WHERE id = $1 FOR UPDATE`, deploymentID).Scan(&from); err != nil {
			return fmt.Errorf("store: ler estado do deployment: %w", err)
		}
		if !domain.CanTransition(from, to) {
			return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
		}
		var reason *string
		if to == domain.DeploymentFailed && failureReason != "" {
			reason = &failureReason
		}
		if _, err := tx.Exec(ctx, `UPDATE deployments SET state = $1, failure_reason = $2, updated_at = now() WHERE id = $3`, to, reason, deploymentID); err != nil {
			return fmt.Errorf("store: atualizar estado do deployment: %w", err)
		}
		return nil
	})
}

// SetCommitSHA fixa o commit obtido (RF-02, RN-04). Uma retomada reafirma o
// mesmo SHA; um SHA diferente do já fixado é recusado.
func (o *Owned) SetCommitSHA(ctx context.Context, deploymentID uuid.UUID, sha string) error {
	return o.withLease(ctx, func(tx pgx.Tx) error {
		var current string
		if err := tx.QueryRow(ctx, `SELECT commit_sha FROM deployments WHERE id = $1 FOR UPDATE`, deploymentID).Scan(&current); err != nil {
			return fmt.Errorf("store: ler SHA do deployment: %w", err)
		}
		if current != "" && current != sha {
			return fmt.Errorf("store: o deploy já está fixado no commit %s e não pode passar a %s (RN-04)", current, sha)
		}
		if _, err := tx.Exec(ctx, `UPDATE deployments SET commit_sha = $1, updated_at = now() WHERE id = $2`, sha, deploymentID); err != nil {
			return fmt.Errorf("store: registrar SHA do deployment: %w", err)
		}
		return nil
	})
}

// SaveAnalysis persiste o snapshot imutável da análise e o vincula ao
// deployment em curso (RF-02, RF-03).
func (o *Owned) SaveAnalysis(ctx context.Context, deploymentID, projectID uuid.UUID, commitSHA string, result domain.AnalysisResult) (uuid.UUID, error) {
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return uuid.Nil, fmt.Errorf("store: serializar resultado da análise: %w", err)
	}
	analysisID := uuid.New()
	err = o.withLease(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO analyses (id, project_id, commit_sha, rules_version, outcome, result, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			analysisID, projectID, commitSHA, result.CatalogVersion, result.Outcome, string(resultJSON), time.Now().UTC(),
		); err != nil {
			return fmt.Errorf("store: inserir análise: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE deployments SET analysis_id = $1, updated_at = now() WHERE id = $2`, analysisID, deploymentID); err != nil {
			return fmt.Errorf("store: vincular análise ao deployment: %w", err)
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return analysisID, nil
}

// AppendEvent grava um evento imutável sem segredos (RF-07, RN-11). O número
// de sequência é calculado dentro da mesma transação.
func (o *Owned) AppendEvent(ctx context.Context, deploymentID uuid.UUID, stage, eventType string, payload map[string]any) error {
	return o.withLease(ctx, func(tx pgx.Tx) error {
		return appendEventTx(ctx, tx, deploymentID, stage, eventType, payload)
	})
}

// CreateRelease registra a release de um deploy bem-sucedido.
func (o *Owned) CreateRelease(ctx context.Context, deploymentID uuid.UUID, imageID, containerID, operationalState, health string, hostPort int) (domain.Release, error) {
	r := domain.Release{
		ID: uuid.New(), DeploymentID: deploymentID, ImageID: imageID, ContainerID: containerID,
		OperationalState: operationalState, Health: health, HostPort: hostPort, CreatedAt: time.Now().UTC(),
	}
	err := o.withLease(ctx, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO releases (id, deployment_id, image_id, container_id, operational_state, health, host_port, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			r.ID, r.DeploymentID, r.ImageID, r.ContainerID, r.OperationalState, r.Health, r.HostPort, r.CreatedAt)
		if err != nil {
			return fmt.Errorf("store: criar release: %w", err)
		}
		return nil
	})
	if err != nil {
		return domain.Release{}, err
	}
	return r, nil
}
