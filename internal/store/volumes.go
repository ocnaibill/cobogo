package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ocnaibill/cobogo/internal/domain"
	"github.com/ocnaibill/cobogo/internal/validate"
)

// Dados persistentes (DEC-41): caminhos declarados pelo usuário e os volumes que
// o worker cria para eles.

// ErrInvalidPaths envolve a mensagem de validação de caminhos persistentes.
var ErrInvalidPaths = errors.New("store: caminhos persistentes inválidos")

// SetPersistentPaths substitui a lista de caminhos persistentes do projeto do
// dono. Devolve a lista normalizada.
func (s *Store) SetPersistentPaths(ctx context.Context, projectID, ownerID uuid.UUID, paths []string) ([]string, error) {
	clean, err := validate.PersistentPaths(paths)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPaths, err)
	}
	if clean == nil {
		clean = []string{}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET persistent_paths = $1 WHERE id = $2 AND owner_id = $3 AND deleted_at IS NULL`, clean, projectID, ownerID)
	if err != nil {
		return nil, fmt.Errorf("store: gravar caminhos persistentes: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return clean, nil
}

// ProjectVolume é um volume de dados de um projeto, como o painel o lista.
type ProjectVolume struct {
	Name        string     `json:"name"`
	ProjectID   uuid.UUID  `json:"project_id"`
	ProjectSlug string     `json:"project_slug"`
	Path        string     `json:"path"`
	SizeBytes   *int64     `json:"size_bytes"`
	MeasuredAt  *time.Time `json:"measured_at"`
	CreatedAt   time.Time  `json:"created_at"`
	// Orphan: o volume já não tem projeto vivo ou o caminho dele deixou de ser
	// persistente. Só um volume sem projeto pode ser apagado.
	Orphan         bool `json:"orphan"`
	ProjectDeleted bool `json:"project_deleted"`
}

// orphanExpr: sem projeto vivo; ou, num volume de dados, o caminho deixou de ser
// persistente; ou, no volume de um serviço (DEC-42), o serviço gerenciado já não
// existe.
const orphanExpr = `(p.deleted_at IS NOT NULL
	OR (v.service = '' AND NOT (v.path = ANY(p.persistent_paths)))
	OR (v.service <> '' AND NOT EXISTS (SELECT 1 FROM project_services s WHERE s.project_id = v.project_id AND s.engine = v.service AND s.mode = 'managed' AND s.deleted_at IS NULL AND s.status <> 'deleting')))`

const volumeColumns = `v.name, v.project_id, p.slug, v.path, v.size_bytes, v.measured_at, v.created_at, ` + orphanExpr + `, p.deleted_at IS NOT NULL`

func scanVolume(row pgx.Row) (ProjectVolume, error) {
	var v ProjectVolume
	err := row.Scan(&v.Name, &v.ProjectID, &v.ProjectSlug, &v.Path, &v.SizeBytes, &v.MeasuredAt, &v.CreatedAt, &v.Orphan, &v.ProjectDeleted)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProjectVolume{}, ErrNotFound
	}
	if err != nil {
		return ProjectVolume{}, fmt.Errorf("store: ler volume: %w", err)
	}
	return v, nil
}

func (s *Store) queryVolumes(ctx context.Context, where string, args ...any) ([]ProjectVolume, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+volumeColumns+`
		FROM project_volumes v JOIN projects p ON p.id = v.project_id
		WHERE v.deleted_at IS NULL AND `+where+`
		ORDER BY v.created_at, v.name`, args...)
	if err != nil {
		return nil, fmt.Errorf("store: listar volumes: %w", err)
	}
	defer rows.Close()
	out := []ProjectVolume{}
	for rows.Next() {
		v, err := scanVolume(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListVolumesFor lista os volumes de um projeto do dono (os que ainda existem).
func (s *Store) ListVolumesFor(ctx context.Context, projectID, ownerID uuid.UUID) ([]ProjectVolume, error) {
	return s.queryVolumes(ctx, `v.project_id = $1 AND p.owner_id = $2`, projectID, ownerID)
}

// ListOrphanVolumesFor lista os volumes sem projeto do dono: de projeto
// excluído, ou de um caminho que deixou de ser persistente.
func (s *Store) ListOrphanVolumesFor(ctx context.Context, ownerID uuid.UUID) ([]ProjectVolume, error) {
	return s.queryVolumes(ctx, `p.owner_id = $1 AND `+orphanExpr, ownerID)
}

// UpsertProjectVolume registra (ou reabre) o volume de um caminho do projeto.
// É do worker, que o cria no Docker.
func (s *Store) UpsertProjectVolume(ctx context.Context, name string, projectID uuid.UUID, path string) error {
	return s.UpsertServiceVolume(ctx, name, projectID, path, "")
}

// UpsertServiceVolume é UpsertProjectVolume para o volume de um serviço (DEC-42):
// `service` é a engine, e o volume deixa de ser "sem projeto" por não estar nos
// caminhos persistentes.
func (s *Store) UpsertServiceVolume(ctx context.Context, name string, projectID uuid.UUID, path, service string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO project_volumes (name, project_id, path, service) VALUES ($1, $2, $3, $4)
		ON CONFLICT (name) DO UPDATE SET deleted_at = NULL`, name, projectID, path, service)
	if err != nil {
		return fmt.Errorf("store: registrar volume: %w", err)
	}
	return nil
}

// UpdateVolumeSizes grava os tamanhos medidos pelo worker.
func (s *Store) UpdateVolumeSizes(ctx context.Context, sizes map[string]int64) error {
	for name, size := range sizes {
		if _, err := s.pool.Exec(ctx, `UPDATE project_volumes SET size_bytes = $2, measured_at = now() WHERE name = $1 AND deleted_at IS NULL`, name, size); err != nil {
			return fmt.Errorf("store: gravar tamanho do volume: %w", err)
		}
	}
	return nil
}

// ProjectVolumeNames devolve os nomes dos volumes vivos de um projeto (o worker
// os apaga na exclusão com dados).
func (s *Store) ProjectVolumeNames(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT name FROM project_volumes WHERE project_id = $1 AND deleted_at IS NULL ORDER BY name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: listar nomes de volume: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("store: ler nome de volume: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// MarkVolumeDeleted marca o volume como apagado; o registro fica como história.
func (s *Store) MarkVolumeDeleted(ctx context.Context, name string) error {
	_, err := s.pool.Exec(ctx, `UPDATE project_volumes SET deleted_at = now() WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("store: marcar volume apagado: %w", err)
	}
	return nil
}

// ErrVolumeNotOrphan: só um volume sem projeto pode ser apagado; os de um
// caminho em uso são apagados tirando o caminho da lista antes.
var ErrVolumeNotOrphan = errors.New("store: o volume ainda está em uso por um caminho persistente do projeto")

// RequestDeleteVolume enfileira a exclusão de um volume sem projeto do dono. A
// confirmação é o nome do volume.
func (s *Store) RequestDeleteVolume(ctx context.Context, ownerID uuid.UUID, name, confirmName string) (domain.Operation, error) {
	v, err := scanVolume(s.pool.QueryRow(ctx, `
		SELECT `+volumeColumns+` FROM project_volumes v JOIN projects p ON p.id = v.project_id
		WHERE v.name = $1 AND p.owner_id = $2 AND v.deleted_at IS NULL`, name, ownerID))
	if err != nil {
		return domain.Operation{}, err
	}
	if confirmName != v.Name {
		return domain.Operation{}, ErrConfirmationMismatch
	}
	if !v.Orphan {
		return domain.Operation{}, ErrVolumeNotOrphan
	}
	return s.EnqueueOperation(ctx, &v.ProjectID, domain.OpDeleteVolume, map[string]any{"name": v.Name, "path": v.Path}, time.Now().UTC(), &ownerID)
}

// HasPersistentPaths diz se o projeto declarou ao menos um caminho (o pipeline
// troca de release de outro jeito, DEC-41 item 4).
func HasPersistentPaths(p domain.Project) bool { return len(p.PersistentPaths) > 0 }

// GetVolume lê um volume vivo pelo nome, sem filtrar por dono: é para o worker,
// que age em nome de uma operação já autorizada.
func (s *Store) GetVolume(ctx context.Context, name string) (ProjectVolume, error) {
	return scanVolume(s.pool.QueryRow(ctx, `
		SELECT `+volumeColumns+` FROM project_volumes v JOIN projects p ON p.id = v.project_id
		WHERE v.name = $1 AND v.deleted_at IS NULL`, name))
}
