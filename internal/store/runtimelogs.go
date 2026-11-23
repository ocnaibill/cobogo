package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Logs de execução (DEC-46): o worker grava, a API lê por cursor.

// AppendRuntimeLogs grava um lote de linhas, na ordem dada. É do worker.
func (s *Store) AppendRuntimeLogs(ctx context.Context, lines []domain.LogLine) error {
	if len(lines) == 0 {
		return nil
	}
	rows := make([][]any, len(lines))
	for i, l := range lines {
		rows[i] = []any{l.ProjectID, l.ContainerID, l.ContainerName, l.DeploymentID, l.Origin, l.Stream, l.At, clip(l.Line, domain.LogLineMax*2)}
	}
	_, err := s.pool.CopyFrom(ctx, pgx.Identifier{"runtime_logs"},
		[]string{"project_id", "container_id", "container_name", "deployment_id", "origin", "stream", "at", "line"}, pgx.CopyFromRows(rows))
	if err != nil {
		return fmt.Errorf("store: gravar logs de execução: %w", err)
	}
	return nil
}

// LastRuntimeLogAt devolve a hora da última linha guardada de um contêiner, ou
// false se não há nenhuma. Serve para retomar a captura de onde parou.
func (s *Store) LastRuntimeLogAt(ctx context.Context, containerID string) (time.Time, bool, error) {
	var at *time.Time
	if err := s.pool.QueryRow(ctx, `SELECT max(at) FROM runtime_logs WHERE container_id = $1`, containerID).Scan(&at); err != nil {
		return time.Time{}, false, fmt.Errorf("store: ler a última linha do contêiner: %w", err)
	}
	if at == nil {
		return time.Time{}, false, nil
	}
	return *at, true, nil
}

// PruneRuntimeLogs apaga o que passou da retenção: linhas mais velhas que maxAge,
// o que excede maxLines por projeto e tudo de projeto excluído. Devolve quantas
// linhas apagou.
func (s *Store) PruneRuntimeLogs(ctx context.Context, maxAge time.Duration, maxLines int) (int64, error) {
	var total int64
	tag, err := s.pool.Exec(ctx, `DELETE FROM runtime_logs WHERE at < now() - make_interval(secs => $1)`, maxAge.Seconds())
	if err != nil {
		return 0, fmt.Errorf("store: apagar logs vencidos: %w", err)
	}
	total += tag.RowsAffected()
	tag, err = s.pool.Exec(ctx, `DELETE FROM runtime_logs WHERE project_id IN (SELECT id FROM projects WHERE deleted_at IS NOT NULL)`)
	if err != nil {
		return total, fmt.Errorf("store: apagar logs de projetos excluídos: %w", err)
	}
	total += tag.RowsAffected()
	tag, err = s.pool.Exec(ctx, `
		DELETE FROM runtime_logs l USING (
			SELECT project_id, (array_agg(id ORDER BY id DESC))[$1::int + 1] AS cutoff
			FROM runtime_logs GROUP BY project_id HAVING count(*) > $1::int
		) c WHERE l.project_id = c.project_id AND l.id <= c.cutoff`, maxLines)
	if err != nil {
		return total, fmt.Errorf("store: apagar o excesso de logs: %w", err)
	}
	return total + tag.RowsAffected(), nil
}

// RuntimeLogPage é uma página de linhas, em ordem crescente de id.
type RuntimeLogPage struct {
	Lines []domain.LogLine
	// HasOlder diz se há linhas guardadas antes da primeira da página (com o filtro).
	HasOlder bool
	// HasMore diz, numa leitura por `after`, que há mais linhas depois da última.
	HasMore bool
}

// ListRuntimeLogsFor lê os logs de um projeto do dono. origin vazio traz todas as
// origens. Sem cursor, as últimas limit linhas; `after` traz as seguintes (as
// primeiras limit depois dele); `before` traz as limit imediatamente anteriores.
func (s *Store) ListRuntimeLogsFor(ctx context.Context, projectID, ownerID uuid.UUID, origin string, after, before int64, limit int) (RuntimeLogPage, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return RuntimeLogPage{}, err
	}
	const cols = `id, container_id, container_name, deployment_id, origin, stream, at, line`
	var (
		rows pgx.Rows
		err  error
	)
	switch {
	case after > 0:
		rows, err = s.pool.Query(ctx, `SELECT `+cols+` FROM runtime_logs WHERE project_id = $1 AND ($2::text = '' OR origin = $2::text OR ($2::text = 'database' AND origin <> 'app')) AND id > $3 ORDER BY id ASC LIMIT $4`, projectID, origin, after, limit+1)
	case before > 0:
		rows, err = s.pool.Query(ctx, `SELECT * FROM (SELECT `+cols+` FROM runtime_logs WHERE project_id = $1 AND ($2::text = '' OR origin = $2::text OR ($2::text = 'database' AND origin <> 'app')) AND id < $3 ORDER BY id DESC LIMIT $4) t ORDER BY id ASC`, projectID, origin, before, limit)
	default:
		rows, err = s.pool.Query(ctx, `SELECT * FROM (SELECT `+cols+` FROM runtime_logs WHERE project_id = $1 AND ($2::text = '' OR origin = $2::text OR ($2::text = 'database' AND origin <> 'app')) ORDER BY id DESC LIMIT $3) t ORDER BY id ASC`, projectID, origin, limit)
	}
	if err != nil {
		return RuntimeLogPage{}, fmt.Errorf("store: ler logs de execução: %w", err)
	}
	defer rows.Close()
	page := RuntimeLogPage{Lines: []domain.LogLine{}}
	for rows.Next() {
		var l domain.LogLine
		if err := rows.Scan(&l.ID, &l.ContainerID, &l.ContainerName, &l.DeploymentID, &l.Origin, &l.Stream, &l.At, &l.Line); err != nil {
			return RuntimeLogPage{}, fmt.Errorf("store: ler uma linha de log: %w", err)
		}
		page.Lines = append(page.Lines, l)
	}
	if err := rows.Err(); err != nil {
		return RuntimeLogPage{}, err
	}
	if after > 0 && len(page.Lines) > limit {
		page.Lines, page.HasMore = page.Lines[:limit], true
	}
	if len(page.Lines) > 0 {
		var older bool
		if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM runtime_logs WHERE project_id = $1 AND ($2::text = '' OR origin = $2::text OR ($2::text = 'database' AND origin <> 'app')) AND id < $3)`, projectID, origin, page.Lines[0].ID).Scan(&older); err != nil {
			return RuntimeLogPage{}, fmt.Errorf("store: conferir logs anteriores: %w", err)
		}
		page.HasOlder = older
	}
	return page, nil
}
