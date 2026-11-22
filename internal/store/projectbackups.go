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

// Backup dos dados de um projeto (DEC-47, parte B): a API cria o pedido e marca o
// pedido de apagar; o worker copia, grava o desfecho e apaga os arquivos.

var (
	// ErrBackupActive: o projeto já tem um backup na fila ou rodando.
	ErrBackupActive = errors.New("store: o projeto já tem um backup em andamento")
	// ErrNothingToBackUp: o projeto não declara volume nem tem banco gerenciado.
	ErrNothingToBackUp = errors.New("store: o projeto não tem dados para copiar (nenhum caminho persistente e nenhum banco gerenciado)")
	// ErrBackupBusy: um backup na fila ou rodando não se apaga.
	ErrBackupBusy = errors.New("store: o backup ainda está em andamento")
)

const projectBackupColumns = `id, project_id, requested_by, reason, pause_app, state, error, size_bytes, manifest, delete_requested, created_at, started_at, finished_at`

func scanProjectBackup(row pgx.Row) (domain.ProjectBackup, error) {
	var b domain.ProjectBackup
	var manifest []byte
	err := row.Scan(&b.ID, &b.ProjectID, &b.RequestedBy, &b.Reason, &b.PauseApp, &b.State, &b.Error, &b.SizeBytes, &manifest, &b.DeleteRequested, &b.CreatedAt, &b.StartedAt, &b.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ProjectBackup{}, ErrNotFound
	}
	if err != nil {
		return domain.ProjectBackup{}, fmt.Errorf("store: ler backup do projeto: %w", err)
	}
	if err := json.Unmarshal(manifest, &b.Manifest); err != nil {
		return domain.ProjectBackup{}, fmt.Errorf("store: ler o manifest do backup: %w", err)
	}
	if b.Manifest.Items == nil {
		b.Manifest.Items = []domain.BackupItem{}
	}
	return b, nil
}

// CreateProjectBackup enfileira um backup dos dados do projeto do dono. Recusa se
// não há o que copiar ou se já há um em andamento.
func (s *Store) CreateProjectBackup(ctx context.Context, projectID, ownerID uuid.UUID, reason string, pauseApp bool) (domain.ProjectBackup, error) {
	p, err := s.GetProjectFor(ctx, projectID, ownerID)
	if err != nil {
		return domain.ProjectBackup{}, err
	}
	managed := false
	if svc, err := s.GetService(ctx, projectID, domain.ServicePostgres); err == nil {
		managed = svc.Mode == domain.ServiceManaged && svc.Status != domain.ServiceDeleting
	} else if !errors.Is(err, ErrNotFound) {
		return domain.ProjectBackup{}, err
	}
	if len(p.PersistentPaths) == 0 && !managed {
		return domain.ProjectBackup{}, ErrNothingToBackUp
	}
	b, err := scanProjectBackup(s.pool.QueryRow(ctx, `
		INSERT INTO project_backups (id, project_id, requested_by, reason, pause_app)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+projectBackupColumns, uuid.New(), projectID, ownerID, reason, pauseApp))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return domain.ProjectBackup{}, ErrBackupActive
	}
	return b, err
}

// ListProjectBackupsFor lista os backups vivos de um projeto do dono, do mais novo ao mais antigo.
func (s *Store) ListProjectBackupsFor(ctx context.Context, projectID, ownerID uuid.UUID) ([]domain.ProjectBackup, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT `+projectBackupColumns+` FROM project_backups WHERE project_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: listar backups do projeto: %w", err)
	}
	defer rows.Close()
	out := []domain.ProjectBackup{}
	for rows.Next() {
		b, err := scanProjectBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetProjectBackupFor lê um backup vivo de um projeto do dono.
func (s *Store) GetProjectBackupFor(ctx context.Context, projectID, id, ownerID uuid.UUID) (domain.ProjectBackup, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return domain.ProjectBackup{}, err
	}
	return scanProjectBackup(s.pool.QueryRow(ctx, `SELECT `+projectBackupColumns+` FROM project_backups WHERE id = $1 AND project_id = $2 AND deleted_at IS NULL`, id, projectID))
}

// RequestDeleteProjectBackup marca o pedido de apagar um backup terminado do dono;
// o worker apaga os arquivos. Um backup em andamento não se apaga.
func (s *Store) RequestDeleteProjectBackup(ctx context.Context, projectID, id, ownerID uuid.UUID) (domain.ProjectBackup, error) {
	b, err := s.GetProjectBackupFor(ctx, projectID, id, ownerID)
	if err != nil {
		return domain.ProjectBackup{}, err
	}
	if b.Active() {
		return domain.ProjectBackup{}, ErrBackupBusy
	}
	if _, err := s.pool.Exec(ctx, `UPDATE project_backups SET delete_requested = true WHERE id = $1`, id); err != nil {
		return domain.ProjectBackup{}, fmt.Errorf("store: pedir para apagar o backup: %w", err)
	}
	return s.GetProjectBackupFor(ctx, projectID, id, ownerID)
}

// ClaimNextProjectBackup reivindica o pedido mais antigo da fila (um por vez na
// máquina: o laço do worker só pega o próximo quando termina). É do worker.
func (s *Store) ClaimNextProjectBackup(ctx context.Context) (*domain.ProjectBackup, error) {
	b, err := scanProjectBackup(s.pool.QueryRow(ctx, `
		UPDATE project_backups SET state = 'running', started_at = now()
		WHERE id = (SELECT id FROM project_backups WHERE state = 'queued' AND deleted_at IS NULL ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+projectBackupColumns))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// FinishProjectBackup grava o desfecho de um backup que estava rodando.
func (s *Store) FinishProjectBackup(ctx context.Context, id uuid.UUID, state string, size int64, manifest domain.BackupManifest, errMsg string) error {
	raw, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("store: serializar o manifest: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE project_backups SET state = $2, size_bytes = $3, manifest = $4, error = $5, finished_at = now()
		WHERE id = $1 AND state = 'running'`, id, state, size, string(raw), errMsg)
	if err != nil {
		return fmt.Errorf("store: concluir backup do projeto: %w", err)
	}
	return nil
}

// InterruptRunningProjectBackups marca como falhos os backups que ficaram
// "rodando" de um worker que caiu. Roda ao subir o worker (há um só).
func (s *Store) InterruptRunningProjectBackups(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE project_backups SET state = 'failed', error = 'o worker reiniciou durante o backup', finished_at = now()
		WHERE state = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("store: interromper backups: %w", err)
	}
	return tag.RowsAffected(), nil
}

// BackupsToDelete lista os backups que o worker deve remover: o apagar pedido pela
// pessoa, o que sobrou da retenção e **todos os de um projeto excluído** (os dados
// de um projeto que se foi não ficam para trás sem dono; o painel avisa na exclusão
// e a pessoa baixa antes).
func (s *Store) BackupsToDelete(ctx context.Context) ([]domain.ProjectBackup, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+projectBackupColumns+` FROM project_backups
		WHERE (delete_requested OR project_id IN (SELECT id FROM projects WHERE deleted_at IS NOT NULL))
		  AND deleted_at IS NULL AND state IN ('succeeded', 'failed') ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("store: listar backups a apagar: %w", err)
	}
	defer rows.Close()
	var out []domain.ProjectBackup
	for rows.Next() {
		b, err := scanProjectBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// MarkProjectBackupDeleted fecha o registro depois de os arquivos saírem.
func (s *Store) MarkProjectBackupDeleted(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE project_backups SET deleted_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: fechar o registro do backup: %w", err)
	}
	return nil
}

// SucceededProjectBackups lista os backups bons de um projeto, do mais antigo ao
// mais novo, só os de retenção (os `before_restore` não entram: a restauração
// depende deles e só a pessoa os apaga... mas contam no limite, ver o worker).
func (s *Store) SucceededProjectBackups(ctx context.Context, projectID uuid.UUID) ([]domain.ProjectBackup, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+projectBackupColumns+` FROM project_backups WHERE project_id = $1 AND state = 'succeeded' AND deleted_at IS NULL AND NOT delete_requested ORDER BY created_at`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: listar backups bons: %w", err)
	}
	defer rows.Close()
	var out []domain.ProjectBackup
	for rows.Next() {
		b, err := scanProjectBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// ProjectBackupByID lê um backup pelo id, sem filtrar por dono. É do worker.
func (s *Store) ProjectBackupByID(ctx context.Context, id uuid.UUID) (domain.ProjectBackup, error) {
	return scanProjectBackup(s.pool.QueryRow(ctx, `SELECT `+projectBackupColumns+` FROM project_backups WHERE id = $1`, id))
}

// MarkProjectBackupForDelete marca o apagar de um backup pela retenção. É do worker
// (que só tem UPDATE na tabela): o apagar de verdade vem na rodada seguinte do laço.
func (s *Store) MarkProjectBackupForDelete(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE project_backups SET delete_requested = true WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: marcar backup para apagar: %w", err)
	}
	return nil
}
