package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ocnaibill/cobogo/internal/domain"
)

var (
	// ErrDeploymentStarted indica um cancelamento de deploy que um worker já
	// começou a executar (DEC-28 item 7).
	ErrDeploymentStarted = errors.New("store: o deploy já começou a executar")
	// ErrDeploymentFinished indica um cancelamento de deploy já encerrado.
	ErrDeploymentFinished = errors.New("store: o deploy já terminou")
	// ErrBadCursor indica um cursor de paginação malformado.
	ErrBadCursor = errors.New("store: cursor de paginação inválido")
)

// Cursor marca a posição numa lista ordenada por criação, do mais novo ao
// mais antigo. O desempate pelo ID evita pular itens criados no mesmo instante.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// String é a forma que viaja na URL: instante (UTC) e ID.
func (c Cursor) String() string {
	return c.At.UTC().Format(time.RFC3339Nano) + "_" + c.ID.String()
}

// ParseCursor lê o que Cursor.String produziu.
func ParseCursor(s string) (Cursor, error) {
	at, id, ok := strings.Cut(s, "_")
	if !ok {
		return Cursor{}, ErrBadCursor
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return Cursor{}, ErrBadCursor
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return Cursor{}, ErrBadCursor
	}
	return Cursor{At: t, ID: u}, nil
}

// cursorArgs devolve os parâmetros SQL do cursor; ambos nulos sem cursor.
func cursorArgs(c *Cursor) (*time.Time, *uuid.UUID) {
	if c == nil {
		return nil, nil
	}
	return &c.At, &c.ID
}

// ListProjectsFor lista os projetos do dono, do mais novo ao mais antigo, cada
// um com o último deploy. Devolve o cursor da próxima página (nil no fim).
func (s *Store) ListProjectsFor(ctx context.Context, ownerID uuid.UUID, limit int, after *Cursor) ([]domain.ProjectSummary, *Cursor, error) {
	at, id := cursorArgs(after)
	rows, err := s.pool.Query(ctx, `
		SELECT p.id, p.owner_id, p.slug, p.repository_url, p.root_directory, p.limits, p.created_at,
		       d.id, d.state, d.created_at,
		       COALESCE((SELECT CASE WHEN r.stopped_at IS NULL THEN 'running' ELSE 'stopped' END
		            FROM releases r JOIN deployments dd ON dd.id = r.deployment_id
		            WHERE dd.project_id = p.id AND r.operational_state = 'active'
		            ORDER BY r.created_at DESC LIMIT 1), 'none')
		FROM projects p
		LEFT JOIN LATERAL (
			SELECT id, state, created_at FROM deployments
			WHERE project_id = p.id ORDER BY created_at DESC, id DESC LIMIT 1
		) d ON true
		WHERE p.owner_id = $1 AND p.deleted_at IS NULL
		  AND ($2::timestamptz IS NULL OR (p.created_at, p.id) < ($2::timestamptz, $3::uuid))
		ORDER BY p.created_at DESC, p.id DESC
		LIMIT $4`, ownerID, at, id, limit+1)
	if err != nil {
		return nil, nil, fmt.Errorf("store: listar projetos: %w", err)
	}
	defer rows.Close()

	items := make([]domain.ProjectSummary, 0, limit)
	for rows.Next() {
		var it domain.ProjectSummary
		var depID *uuid.UUID
		var depState *domain.DeploymentState
		var depCreated *time.Time
		var limitsRaw string
		if err := rows.Scan(&it.ID, &it.OwnerID, &it.Slug, &it.RepositoryURL, &it.RootDirectory, &limitsRaw, &it.CreatedAt,
			&depID, &depState, &depCreated, &it.AppState); err != nil {
			return nil, nil, fmt.Errorf("store: ler projeto: %w", err)
		}
		var lerr error
		if it.Limits, lerr = scanLimits(limitsRaw); lerr != nil {
			return nil, nil, lerr
		}
		if depID != nil {
			it.LastDeployment = &domain.DeploymentBrief{ID: *depID, State: *depState, CreatedAt: *depCreated}
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: listar projetos: %w", err)
	}
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		return items, &Cursor{At: last.CreatedAt, ID: last.ID}, nil
	}
	return items, nil, nil
}

// ListDeployments lista os deploys de um projeto, do mais novo ao mais antigo.
// O dono do projeto já deve ter sido conferido por quem chama.
func (s *Store) ListDeployments(ctx context.Context, projectID uuid.UUID, limit int, after *Cursor) ([]domain.Deployment, *Cursor, error) {
	at, id := cursorArgs(after)
	rows, err := s.pool.Query(ctx, `
		SELECT `+deploymentColumns+` FROM deployments
		WHERE project_id = $1
		  AND ($2::timestamptz IS NULL OR (created_at, id) < ($2::timestamptz, $3::uuid))
		ORDER BY created_at DESC, id DESC
		LIMIT $4`, projectID, at, id, limit+1)
	if err != nil {
		return nil, nil, fmt.Errorf("store: listar deploys: %w", err)
	}
	defer rows.Close()

	items := make([]domain.Deployment, 0, limit)
	for rows.Next() {
		dep, err := scanDeployment(rows)
		if err != nil {
			return nil, nil, fmt.Errorf("store: ler deploy: %w", err)
		}
		items = append(items, dep)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("store: listar deploys: %w", err)
	}
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		return items, &Cursor{At: last.CreatedAt, ID: last.ID}, nil
	}
	return items, nil, nil
}

// EventPage é uma fatia dos eventos de um deploy (DEC-28 itens 2 e 3).
type EventPage struct {
	Events  []domain.DeploymentEvent
	HasMore bool
	State   domain.DeploymentState
	// Settled: o job está encerrado, então nenhum evento novo aparece sem uma
	// ação do usuário. Vale para o instante da leitura do estado, que precede a
	// leitura dos eventos: Settled com HasMore falso significa que a página já
	// contém tudo.
	Settled bool
}

// ListEventsAfter devolve até limit eventos com sequência maior que after.
func (s *Store) ListEventsAfter(ctx context.Context, deploymentID uuid.UUID, after, limit int) (EventPage, error) {
	var page EventPage
	// O estado e o job são lidos antes dos eventos de propósito: o worker grava
	// o último evento antes de encerrar o job, então job encerrado lido aqui
	// implica eventos já confirmados quando a consulta seguinte rodar.
	var jobState *domain.JobState
	err := s.pool.QueryRow(ctx, `
		SELECT d.state, j.state FROM deployments d LEFT JOIN jobs j ON j.deployment_id = d.id
		WHERE d.id = $1`, deploymentID).Scan(&page.State, &jobState)
	if errors.Is(err, pgx.ErrNoRows) {
		return EventPage{}, ErrNotFound
	}
	if err != nil {
		return EventPage{}, fmt.Errorf("store: ler estado do deploy: %w", err)
	}
	page.Settled = jobState != nil && *jobState == domain.JobDone

	rows, err := s.pool.Query(ctx, `
		SELECT id, deployment_id, sequence, stage, event_type, occurred_at, payload
		FROM deployment_events WHERE deployment_id = $1 AND sequence > $2
		ORDER BY sequence LIMIT $3`, deploymentID, after, limit+1)
	if err != nil {
		return EventPage{}, fmt.Errorf("store: listar eventos: %w", err)
	}
	defer rows.Close()

	page.Events = make([]domain.DeploymentEvent, 0, limit)
	for rows.Next() {
		var e domain.DeploymentEvent
		var payloadRaw string
		if err := rows.Scan(&e.ID, &e.DeploymentID, &e.Sequence, &e.Stage, &e.EventType, &e.OccurredAt, &payloadRaw); err != nil {
			return EventPage{}, fmt.Errorf("store: ler evento: %w", err)
		}
		if err := json.Unmarshal([]byte(payloadRaw), &e.Payload); err != nil {
			return EventPage{}, fmt.Errorf("store: decodificar payload do evento: %w", err)
		}
		page.Events = append(page.Events, e)
	}
	if err := rows.Err(); err != nil {
		return EventPage{}, fmt.Errorf("store: listar eventos: %w", err)
	}
	if len(page.Events) > limit {
		page.Events = page.Events[:limit]
		page.HasMore = true
	}
	return page, nil
}

// LastEventSequence devolve a sequência do último evento (0 sem eventos).
func (s *Store) LastEventSequence(ctx context.Context, deploymentID uuid.UUID) (int, error) {
	var seq int
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(sequence), 0) FROM deployment_events WHERE deployment_id = $1`, deploymentID).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("store: ler última sequência: %w", err)
	}
	return seq, nil
}

// CancelDeployment cancela um deploy que ainda não executou: QUEUED sem worker
// ou NEEDS_INPUT (DEC-28 item 7). A trava da linha do job serializa o
// cancelamento contra a reivindicação por um worker; a ordem de travas (job,
// depois deploy) é a mesma das escritas do worker.
func (s *Store) CancelDeployment(ctx context.Context, id uuid.UUID) (domain.Deployment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: abrir transação de cancelamento: %w", err)
	}
	defer tx.Rollback(ctx)

	var jobState domain.JobState
	err = tx.QueryRow(ctx, `SELECT state FROM jobs WHERE deployment_id = $1 FOR UPDATE`, id).Scan(&jobState)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Deployment{}, ErrNotFound
	}
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: travar job: %w", err)
	}

	var state domain.DeploymentState
	if err := tx.QueryRow(ctx, `SELECT state FROM deployments WHERE id = $1 FOR UPDATE`, id).Scan(&state); err != nil {
		return domain.Deployment{}, fmt.Errorf("store: travar deploy: %w", err)
	}
	switch {
	case state.IsTerminal():
		return domain.Deployment{}, ErrDeploymentFinished
	case state != domain.DeploymentQueued && state != domain.DeploymentNeedsInput:
		return domain.Deployment{}, ErrDeploymentStarted
	case jobState == domain.JobLeased:
		return domain.Deployment{}, ErrDeploymentStarted
	}

	dep, err := scanDeployment(tx.QueryRow(ctx, `
		UPDATE deployments SET state = $1, updated_at = now() WHERE id = $2
		RETURNING `+deploymentColumns, domain.DeploymentCancelled, id))
	if err != nil {
		return domain.Deployment{}, fmt.Errorf("store: cancelar deploy: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE jobs SET state = $1, lease_until = NULL, updated_at = now() WHERE deployment_id = $2`, domain.JobDone, id); err != nil {
		return domain.Deployment{}, fmt.Errorf("store: encerrar job cancelado: %w", err)
	}
	if err := appendEventTx(ctx, tx, id, "pipeline", "cancelled", map[string]any{"from": string(state)}); err != nil {
		return domain.Deployment{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Deployment{}, fmt.Errorf("store: confirmar cancelamento: %w", err)
	}
	return dep, nil
}
