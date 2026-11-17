package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// ListSettings devolve as configurações gravadas, por chave.
func (s *Store) ListSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("store: listar configurações: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("store: ler configuração: %w", err)
		}
		out[k] = v
	}
	return out, rows.Err()
}

// PutSetting grava (ou troca) o valor de uma chave. A validação é de quem
// chama; aqui o valor é só texto.
func (s *Store) PutSetting(ctx context.Context, key, value string, by uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO settings (key, value, updated_at, updated_by) VALUES ($1, $2, now(), $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by`,
		key, value, by)
	if err != nil {
		return fmt.Errorf("store: gravar configuração: %w", err)
	}
	return nil
}

// DeleteSetting remove uma chave, devolvendo-a ao ambiente ou ao padrão.
// Informa se havia algo a remover.
func (s *Store) DeleteSetting(ctx context.Context, key string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM settings WHERE key = $1`, key)
	if err != nil {
		return false, fmt.Errorf("store: remover configuração: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// SetProjectLimits substitui as sobreposições de recursos do projeto do dono.
// Um valor zero limpa tudo (cada campo volta a herdar).
func (s *Store) SetProjectLimits(ctx context.Context, projectID, ownerID uuid.UUID, limits domain.Limits) (domain.Project, error) {
	raw, err := json.Marshal(limits)
	if err != nil {
		return domain.Project{}, fmt.Errorf("store: serializar limites: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET limits = $1 WHERE id = $2 AND owner_id = $3`, string(raw), projectID, ownerID)
	if err != nil {
		return domain.Project{}, fmt.Errorf("store: gravar limites do projeto: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Project{}, ErrNotFound
	}
	return s.GetProjectFor(ctx, projectID, ownerID)
}

// scanLimits decodifica a coluna JSONB de limites.
func scanLimits(raw string) (domain.Limits, error) {
	var l domain.Limits
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return domain.Limits{}, fmt.Errorf("store: decodificar limites: %w", err)
	}
	return l, nil
}
