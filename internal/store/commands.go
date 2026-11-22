package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Comandos pontuais (DEC-45): a API cria o pedido e marca o cancelamento; o
// worker executa e grava o desfecho.

// ErrCommandActive: o projeto já tem um comando na fila ou rodando (um por vez).
var ErrCommandActive = errors.New("store: o projeto já tem um comando em andamento")

const commandColumns = `id, project_id, requested_by, command, source, pause_app, state, cancel_requested, timeout_seconds, exit_code, output, output_truncated, error, created_at, started_at, finished_at`

func scanCommand(row pgx.Row) (domain.Command, error) {
	var c domain.Command
	err := row.Scan(&c.ID, &c.ProjectID, &c.RequestedBy, &c.Command, &c.Source, &c.PauseApp, &c.State, &c.CancelRequested,
		&c.TimeoutSeconds, &c.ExitCode, &c.Output, &c.OutputTruncated, &c.Error, &c.CreatedAt, &c.StartedAt, &c.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Command{}, ErrNotFound
	}
	if err != nil {
		return domain.Command{}, fmt.Errorf("store: ler comando: %w", err)
	}
	return c, nil
}

// CreateCommand enfileira um comando pontual do dono. Exige uma release no ar;
// recusa se já há um comando em andamento no projeto.
func (s *Store) CreateCommand(ctx context.Context, projectID, ownerID uuid.UUID, command, source string, pauseApp bool, timeoutSeconds int) (domain.Command, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return domain.Command{}, err
	}
	if _, err := s.ActiveRelease(ctx, projectID); err != nil {
		return domain.Command{}, err
	}
	c, err := scanCommand(s.pool.QueryRow(ctx, `
		INSERT INTO project_commands (id, project_id, requested_by, command, source, pause_app, timeout_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+commandColumns, uuid.New(), projectID, ownerID, command, source, pauseApp, timeoutSeconds))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return domain.Command{}, ErrCommandActive
	}
	return c, err
}

// GetCommandFor lê um comando de um projeto do dono, com a saída.
func (s *Store) GetCommandFor(ctx context.Context, projectID, id, ownerID uuid.UUID) (domain.Command, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return domain.Command{}, err
	}
	return scanCommand(s.pool.QueryRow(ctx, `SELECT `+commandColumns+` FROM project_commands WHERE id = $1 AND project_id = $2`, id, projectID))
}

// ListCommandsFor lista os comandos mais recentes de um projeto do dono, sem a
// saída (que pode ser grande): a saída vem do comando individual.
func (s *Store) ListCommandsFor(ctx context.Context, projectID, ownerID uuid.UUID, limit int) ([]domain.Command, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, project_id, requested_by, command, source, pause_app, state, cancel_requested, timeout_seconds, exit_code, ''::text, output_truncated, error, created_at, started_at, finished_at
		FROM project_commands WHERE project_id = $1 ORDER BY created_at DESC LIMIT $2`, projectID, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listar comandos: %w", err)
	}
	defer rows.Close()
	out := []domain.Command{}
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// RequestCancelCommand marca o cancelamento de um comando em andamento do dono.
// O worker o vê e mata o contêiner (ou nem inicia, se ainda estava na fila).
func (s *Store) RequestCancelCommand(ctx context.Context, projectID, id, ownerID uuid.UUID) (domain.Command, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return domain.Command{}, err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE project_commands SET cancel_requested = true WHERE id = $1 AND project_id = $2 AND state IN ('queued', 'running')`, id, projectID)
	if err != nil {
		return domain.Command{}, fmt.Errorf("store: cancelar comando: %w", err)
	}
	_ = tag // já terminou (ou não existe): devolve como está
	return s.GetCommandFor(ctx, projectID, id, ownerID)
}

// ClaimNextCommand reivindica o comando mais antigo da fila e o marca como
// rodando. Devolve nil se não há nenhum. É do worker.
func (s *Store) ClaimNextCommand(ctx context.Context) (*domain.Command, error) {
	c, err := scanCommand(s.pool.QueryRow(ctx, `
		UPDATE project_commands SET state = 'running', started_at = now()
		WHERE id = (SELECT id FROM project_commands WHERE state = 'queued' ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED)
		RETURNING `+commandColumns))
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// CommandCancelRequested diz se o dono pediu o cancelamento.
func (s *Store) CommandCancelRequested(ctx context.Context, id uuid.UUID) (bool, error) {
	var requested bool
	err := s.pool.QueryRow(ctx, `SELECT cancel_requested FROM project_commands WHERE id = $1`, id).Scan(&requested)
	if err != nil {
		return false, fmt.Errorf("store: ler cancelamento do comando: %w", err)
	}
	return requested, nil
}

// UpdateCommandOutput grava a saída parcial de um comando rodando.
func (s *Store) UpdateCommandOutput(ctx context.Context, id uuid.UUID, output string, truncated bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE project_commands SET output = $2, output_truncated = $3 WHERE id = $1 AND state = 'running'`, id, output, truncated)
	if err != nil {
		return fmt.Errorf("store: gravar a saída do comando: %w", err)
	}
	return nil
}

// FinishCommand grava o desfecho. command é o texto com os valores secretos já
// trocados. Só vale para um comando que ainda não terminou.
func (s *Store) FinishCommand(ctx context.Context, id uuid.UUID, state string, exitCode *int, command, output string, truncated bool, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE project_commands SET state = $2, exit_code = $3, command = $4, output = $5, output_truncated = $6, error = $7, finished_at = now()
		WHERE id = $1 AND state IN ('queued', 'running')`, id, state, exitCode, command, output, truncated, errMsg)
	if err != nil {
		return fmt.Errorf("store: concluir comando: %w", err)
	}
	return nil
}

// InterruptRunningCommands marca como interrompidos os comandos que ficaram
// "rodando" de um worker que caiu. Roda ao subir o worker (há um só).
func (s *Store) InterruptRunningCommands(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE project_commands SET state = 'interrupted', error = 'o worker reiniciou durante o comando', finished_at = now()
		WHERE state = 'running'`)
	if err != nil {
		return 0, fmt.Errorf("store: interromper comandos: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MarkReauth registra que a sessão digitou a senha de novo, por window.
func (s *Store) MarkReauth(ctx context.Context, sessionID uuid.UUID, window time.Duration) (time.Time, error) {
	var until time.Time
	err := s.pool.QueryRow(ctx, `UPDATE sessions SET reauth_until = now() + make_interval(secs => $2) WHERE id = $1 RETURNING reauth_until`, sessionID, window.Seconds()).Scan(&until)
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("store: registrar reautenticação: %w", err)
	}
	return until, nil
}
