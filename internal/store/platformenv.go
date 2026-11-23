package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Variáveis da plataforma (DEC-52 item 3): valores que o Cobogó calcula (o endereço
// do projeto, a URL da API do mesmo projeto) e mantém. O valor vai cifrado em
// env_variables, com a origem `platform`; platform_env guarda o que foi gravado e a
// regra, para recalcular sem decifrar.

// PlatformValue é um valor que a plataforma quer numa variável, com o porquê.
type PlatformValue struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Reason    string `json:"reason"`
	BuildTime bool   `json:"build_time"`
}

// PlatformRecord é o que a plataforma gravou numa variável.
type PlatformRecord struct {
	PlatformValue
	UpdatedAt time.Time `json:"updated_at"`
}

// PlatformEnv devolve o que a plataforma gravou nas variáveis da aplicação.
func (s *Store) PlatformEnv(ctx context.Context, projectID uuid.UUID) (map[string]PlatformRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT name, value, reason, build_time, updated_at FROM platform_env WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: ler as variáveis da plataforma: %w", err)
	}
	defer rows.Close()
	out := map[string]PlatformRecord{}
	for rows.Next() {
		var r PlatformRecord
		if err := rows.Scan(&r.Name, &r.Value, &r.Reason, &r.BuildTime, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("store: ler variável da plataforma: %w", err)
		}
		out[r.Name] = r
	}
	return out, rows.Err()
}

// ApplyPlatformEnv grava os valores da plataforma que faltam ou mudaram e devolve os
// que gravou. Uma variável de outra origem (digitada, importada, gerada, vinculada a um
// serviço) nunca é tocada: editar um valor da plataforma o torna da pessoa. Apagar a
// variável a devolve à plataforma no próximo cálculo.
func (s *Store) ApplyPlatformEnv(ctx context.Context, projectID uuid.UUID, values []PlatformValue) ([]PlatformValue, error) {
	vars, err := s.ListEnvVariables(ctx, projectID)
	if err != nil {
		return nil, err
	}
	source := map[string]domain.EnvSource{}
	for _, v := range vars {
		source[v.Name] = v.Source
	}
	recs, err := s.PlatformEnv(ctx, projectID)
	if err != nil {
		return nil, err
	}
	changed := []PlatformValue{}
	for _, v := range values {
		src, defined := source[v.Name]
		if defined && src != domain.EnvSourcePlatform {
			continue
		}
		if rec, ok := recs[v.Name]; ok && defined && rec.Value == v.Value {
			if rec.Reason != v.Reason {
				if _, err := s.pool.Exec(ctx, `UPDATE platform_env SET reason = $3 WHERE project_id = $1 AND name = $2`, projectID, v.Name, v.Reason); err != nil {
					return nil, fmt.Errorf("store: gravar a regra da variável: %w", err)
				}
			}
			continue
		}
		if _, err := s.PutEnvVariable(ctx, projectID, v.Name, v.Value, domain.EnvSourcePlatform); err != nil {
			return nil, err
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO platform_env (project_id, name, value, reason, build_time, updated_at) VALUES ($1, $2, $3, $4, $5, now())
			ON CONFLICT (project_id, name) DO UPDATE SET value = EXCLUDED.value, reason = EXCLUDED.reason, build_time = EXCLUDED.build_time, updated_at = now()`,
			projectID, v.Name, v.Value, v.Reason, v.BuildTime); err != nil {
			return nil, fmt.Errorf("store: registrar a variável da plataforma: %w", err)
		}
		changed = append(changed, v)
	}
	return changed, nil
}

// PutGeneratedIfMissing gera o valor de um segredo que a aplicação inventa (SECRET_KEY)
// só se a variável ainda não existe. Devolve se gravou.
func (s *Store) PutGeneratedIfMissing(ctx context.Context, projectID uuid.UUID, name string, generate func() (string, error)) (bool, error) {
	if _, err := s.GetEnvVariable(ctx, projectID, name); err == nil {
		return false, nil
	} else if err != ErrNotFound {
		return false, err
	}
	value, err := generate()
	if err != nil {
		return false, err
	}
	if _, err := s.PutEnvVariable(ctx, projectID, name, value, domain.EnvSourceGenerated); err != nil {
		return false, err
	}
	return true, nil
}

// EverHadService diz se a aplicação já teve um serviço da engine (ou, para uma engine
// relacional, qualquer banco relacional), mesmo excluído: quem apagou o serviço do
// Cobogó decidiu por isso, e a plataforma não o recria sozinha (DEC-52 item 6).
func (s *Store) EverHadService(ctx context.Context, projectID uuid.UUID, engine string) (bool, error) {
	engines := []string{engine}
	if domain.IsRelationalEngine(engine) {
		engines = domain.RelationalEngines
	}
	var had bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM project_services WHERE project_id = $1 AND engine = ANY($2))`, projectID, engines).Scan(&had); err != nil {
		return false, fmt.Errorf("store: conferir os serviços da aplicação: %w", err)
	}
	return had, nil
}
