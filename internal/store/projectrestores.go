package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Restauração dos dados de um projeto (DEC-47, parte C). A API cria, numa transação
// só, o backup de segurança e o pedido; o worker restaura quando o backup de
// segurança terminou bem.

var (
	// ErrRestoreActive: o projeto já tem uma restauração na fila ou rodando.
	ErrRestoreActive = errors.New("store: o projeto já tem uma restauração em andamento")
	// ErrBackupNotUsable: o backup não está concluído, foi marcado para apagar ou não existe mais.
	ErrBackupNotUsable = errors.New("store: o backup não pode ser restaurado")
)

const restoreColumns = `id, project_id, backup_id, safety_backup_id, requested_by, state, step, error, result, created_at, started_at, finished_at`

func scanRestore(row pgx.Row) (domain.ProjectRestore, error) {
	var r domain.ProjectRestore
	var result []byte
	err := row.Scan(&r.ID, &r.ProjectID, &r.BackupID, &r.SafetyBackupID, &r.RequestedBy, &r.State, &r.Step, &r.Error, &result, &r.CreatedAt, &r.StartedAt, &r.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectRestore{}, ErrNotFound
	}
	if err != nil {
		return domain.ProjectRestore{}, fmt.Errorf("store: ler restauração: %w", err)
	}
	if err := json.Unmarshal(result, &r.Result); err != nil {
		return domain.ProjectRestore{}, fmt.Errorf("store: ler o resultado da restauração: %w", err)
	}
	if r.Result.Volumes == nil {
		r.Result.Volumes = []string{}
	}
	if r.Result.Skipped == nil {
		r.Result.Skipped = []string{}
	}
	if r.Result.Warnings == nil {
		r.Result.Warnings = []string{}
	}
	return r, nil
}

// CreateRestore pede a restauração de um backup do projeto do dono. Na mesma
// transação cria o backup de segurança (`before_restore`, com a aplicação pausada),
// que o worker faz primeiro; a restauração só começa se ele der certo.
func (s *Store) CreateRestore(ctx context.Context, projectID, ownerID, backupID uuid.UUID) (domain.ProjectRestore, domain.ProjectBackup, error) {
	p, err := s.GetProjectFor(ctx, projectID, ownerID)
	if err != nil {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, err
	}
	b, err := s.GetProjectBackupFor(ctx, projectID, backupID, ownerID)
	if err != nil {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, err
	}
	if b.State != domain.BackupSucceeded || b.DeleteRequested {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, ErrBackupNotUsable
	}
	managed := false
	if svc, err := s.GetService(ctx, projectID, domain.ServicePostgres); err == nil {
		managed = svc.Mode == domain.ServiceManaged && svc.Status != domain.ServiceDeleting
	} else if !errors.Is(err, ErrNotFound) {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, err
	}
	if len(p.PersistentPaths) == 0 && !managed {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, ErrNothingToBackUp
	}

	if active, err := s.RestoreActive(ctx, projectID); err != nil {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, err
	} else if active {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, ErrRestoreActive
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, fmt.Errorf("store: abrir transação da restauração: %w", err)
	}
	defer tx.Rollback(ctx)
	conflict := func(err error) error {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			if pgErr.ConstraintName == "project_restores_one_active" {
				return ErrRestoreActive
			}
			return ErrBackupActive
		}
		return err
	}
	safety, err := scanProjectBackup(tx.QueryRow(ctx, `
		INSERT INTO project_backups (id, project_id, requested_by, reason, pause_app)
		VALUES ($1, $2, $3, 'before_restore', true) RETURNING `+projectBackupColumns, uuid.New(), projectID, ownerID))
	if err != nil {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, conflict(err)
	}
	r, err := scanRestore(tx.QueryRow(ctx, `
		INSERT INTO project_restores (id, project_id, backup_id, safety_backup_id, requested_by)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+restoreColumns, uuid.New(), projectID, backupID, safety.ID, ownerID))
	if err != nil {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, conflict(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.ProjectRestore{}, domain.ProjectBackup{}, fmt.Errorf("store: confirmar a restauração: %w", err)
	}
	return r, safety, nil
}

// ListRestoresFor lista as restaurações mais recentes de um projeto do dono.
func (s *Store) ListRestoresFor(ctx context.Context, projectID, ownerID uuid.UUID, limit int) ([]domain.ProjectRestore, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+restoreColumns+` FROM project_restores WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listar restaurações: %w", err)
	}
	defer rows.Close()
	out := []domain.ProjectRestore{}
	for rows.Next() {
		r, err := scanRestore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ClaimNextReadyRestore reivindica a restauração mais antiga cujo backup de segurança
// já terminou bem. É do worker.
func (s *Store) ClaimNextReadyRestore(ctx context.Context) (*domain.ProjectRestore, error) {
	r, err := scanRestore(s.pool.QueryRow(ctx, `
		UPDATE project_restores SET state = 'running', started_at = now(), step = 'iniciando'
		WHERE id = (
			SELECT r.id FROM project_restores r JOIN project_backups sb ON sb.id = r.safety_backup_id
			WHERE r.state = 'queued' AND sb.state = 'succeeded' ORDER BY r.created_at LIMIT 1 FOR UPDATE OF r SKIP LOCKED)
		RETURNING `+restoreColumns))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// FailBlockedRestores encerra como falhas as restaurações cujo backup de segurança falhou:
// sem ele a restauração não começa, e nada foi tocado. Devolve quantas.
func (s *Store) FailBlockedRestores(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE project_restores r SET state = 'failed', finished_at = now(),
		       error = 'o backup de segurança falhou (' || sb.error || '); a restauração não começou e nada foi alterado'
		FROM project_backups sb
		WHERE sb.id = r.safety_backup_id AND r.state = 'queued' AND sb.state = 'failed'`)
	if err != nil {
		return 0, fmt.Errorf("store: encerrar restaurações bloqueadas: %w", err)
	}
	return tag.RowsAffected(), nil
}

// SetRestoreStep registra em que passo a restauração está.
func (s *Store) SetRestoreStep(ctx context.Context, id uuid.UUID, step string) error {
	_, err := s.pool.Exec(ctx, `UPDATE project_restores SET step = $2 WHERE id = $1 AND state = 'running'`, id, step)
	if err != nil {
		return fmt.Errorf("store: registrar o passo da restauração: %w", err)
	}
	return nil
}

// FinishRestore grava o desfecho de uma restauração que estava rodando.
func (s *Store) FinishRestore(ctx context.Context, id uuid.UUID, state string, result domain.RestoreResult, errMsg string) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("store: serializar o resultado da restauração: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE project_restores SET state = $2, result = $3, error = $4, step = '', finished_at = now()
		WHERE id = $1 AND state = 'running'`, id, state, string(raw), errMsg)
	if err != nil {
		return fmt.Errorf("store: concluir restauração: %w", err)
	}
	return nil
}

// InterruptRunningRestores marca como falhas as restaurações que ficaram "rodando" de um
// worker que caiu, apontando o backup de segurança, que tem o estado de antes.
func (s *Store) InterruptRunningRestores(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE project_restores SET state = 'failed', step = '', finished_at = now(),
		       error = 'o worker reiniciou durante a restauração; o estado de antes está no backup de segurança ' || safety_backup_id::text || ' e a aplicação pode ter ficado parada'
		WHERE state = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("store: interromper restaurações: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ProtectedBackupIDs são os backups de que uma restauração em andamento depende (o
// que se restaura e o de segurança): a retenção e o apagar não os tocam.
func (s *Store) ProtectedBackupIDs(ctx context.Context, projectID uuid.UUID) (map[uuid.UUID]bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT backup_id, safety_backup_id FROM project_restores WHERE project_id = $1 AND state IN ('queued', 'running')`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: listar backups protegidos: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var a, b uuid.UUID
		if err := rows.Scan(&a, &b); err != nil {
			return nil, err
		}
		out[a], out[b] = true, true
	}
	return out, rows.Err()
}

// RestoreActive diz se o projeto tem uma restauração na fila ou rodando.
func (s *Store) RestoreActive(ctx context.Context, projectID uuid.UUID) (bool, error) {
	var active bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM project_restores WHERE project_id = $1 AND state IN ('queued', 'running'))`, projectID).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("store: conferir restauração ativa: %w", err)
	}
	return active, nil
}
