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

// ErrNoCipher indica uso das variáveis de ambiente sem uma cifra configurada.
var ErrNoCipher = errors.New("store: cifra de variáveis não configurada")

// Cipher cifra e decifra o valor de uma variável. Os dados autenticados (aad)
// prendem o texto cifrado ao projeto, ao nome e à versão.
type Cipher interface {
	Seal(plaintext, aad []byte) ([]byte, error)
	Open(sealed, aad []byte) ([]byte, error)
	// OpenVersion abre um valor com a chave da versão em que ele foi gravado.
	OpenVersion(keyVersion int, sealed, aad []byte) ([]byte, error)
	KeyVersion() int
}

// WithCipher devolve um Store que cifra e decifra variáveis com c.
func (s *Store) WithCipher(c Cipher) *Store {
	cp := *s
	cp.cipher = c
	return &cp
}

func envAAD(projectID uuid.UUID, name string, version int) []byte {
	return []byte(fmt.Sprintf("cobogo/env/%s/%s/%d", projectID, name, version))
}

// PutEnvVariable cria ou substitui o valor de uma variável do projeto, na
// versão seguinte. O valor é cifrado aqui, antes de tocar o banco, e nunca é
// devolvido. Duas gravações simultâneas da mesma variável são serializadas.
func (s *Store) PutEnvVariable(ctx context.Context, projectID uuid.UUID, name, value string, source domain.EnvSource) (domain.EnvVariable, error) {
	if s.cipher == nil {
		return domain.EnvVariable{}, ErrNoCipher
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.EnvVariable{}, fmt.Errorf("store: abrir transação de variável: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, projectID.String()+"/"+name); err != nil {
		return domain.EnvVariable{}, fmt.Errorf("store: serializar gravação da variável: %w", err)
	}

	version := 1
	exists := false
	var current int
	switch err := tx.QueryRow(ctx, `SELECT version FROM env_variables WHERE project_id = $1 AND name = $2`, projectID, name).Scan(&current); {
	case err == nil:
		version, exists = current+1, true
	case !errors.Is(err, pgx.ErrNoRows):
		return domain.EnvVariable{}, fmt.Errorf("store: ler versão da variável: %w", err)
	}

	sealed, err := s.cipher.Seal([]byte(value), envAAD(projectID, name, version))
	if err != nil {
		return domain.EnvVariable{}, err
	}

	// Duas instruções em vez de INSERT ... ON CONFLICT: o ON CONFLICT DO UPDATE
	// lê o texto cifrado pela linha EXCLUDED, o que exigiria à API o privilégio
	// de SELECT sobre ele (DEC-33 item 3). A serialização acima já impede a corrida.
	now := time.Now().UTC()
	var v domain.EnvVariable
	if exists {
		err = tx.QueryRow(ctx, `
			UPDATE env_variables
			SET version = $3, source = $4, ciphertext = $5, key_version = $6, updated_at = $7
			WHERE project_id = $1 AND name = $2
			RETURNING name, version, source, key_version, created_at, updated_at`,
			projectID, name, version, source, sealed, s.cipher.KeyVersion(), now,
		).Scan(&v.Name, &v.Version, &v.Source, &v.KeyVersion, &v.CreatedAt, &v.UpdatedAt)
	} else {
		err = tx.QueryRow(ctx, `
			INSERT INTO env_variables (id, project_id, name, version, source, ciphertext, key_version, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
			RETURNING name, version, source, key_version, created_at, updated_at`,
			uuid.New(), projectID, name, version, source, sealed, s.cipher.KeyVersion(), now,
		).Scan(&v.Name, &v.Version, &v.Source, &v.KeyVersion, &v.CreatedAt, &v.UpdatedAt)
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // projeto inexistente
			return domain.EnvVariable{}, ErrNotFound
		}
		return domain.EnvVariable{}, fmt.Errorf("store: gravar variável: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.EnvVariable{}, fmt.Errorf("store: confirmar variável: %w", err)
	}
	return v, nil
}

const envMetaColumns = `name, version, source, key_version, created_at, updated_at`

func scanEnvMeta(row pgx.Row) (domain.EnvVariable, error) {
	var v domain.EnvVariable
	err := row.Scan(&v.Name, &v.Version, &v.Source, &v.KeyVersion, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

// GetEnvVariable devolve os metadados de uma variável, sem o valor.
func (s *Store) GetEnvVariable(ctx context.Context, projectID uuid.UUID, name string) (domain.EnvVariable, error) {
	v, err := scanEnvMeta(s.pool.QueryRow(ctx, `SELECT `+envMetaColumns+` FROM env_variables WHERE project_id = $1 AND name = $2`, projectID, name))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.EnvVariable{}, ErrNotFound
	}
	if err != nil {
		return domain.EnvVariable{}, fmt.Errorf("store: buscar variável: %w", err)
	}
	return v, nil
}

// ListEnvVariables lista os metadados das variáveis do projeto, sem valores.
func (s *Store) ListEnvVariables(ctx context.Context, projectID uuid.UUID) ([]domain.EnvVariable, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+envMetaColumns+` FROM env_variables WHERE project_id = $1 ORDER BY name`, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: listar variáveis: %w", err)
	}
	defer rows.Close()
	vars := []domain.EnvVariable{}
	for rows.Next() {
		v, err := scanEnvMeta(rows)
		if err != nil {
			return nil, fmt.Errorf("store: ler variável: %w", err)
		}
		vars = append(vars, v)
	}
	return vars, rows.Err()
}

// DeleteEnvVariable remove a variável do projeto.
func (s *Store) DeleteEnvVariable(ctx context.Context, projectID uuid.UUID, name string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM env_variables WHERE project_id = $1 AND name = $2`, projectID, name)
	if err != nil {
		return fmt.Errorf("store: remover variável: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevealEnvValues decifra todas as variáveis do projeto. Só o worker deve
// chamá-la, no momento de criar o contêiner (DEC-21, DEC-24 item 10); os
// valores nunca são registrados em evento, log ou resposta da API. Devolve
// também as versões entregues, para registrar o que cada release recebeu.
func (s *Store) RevealEnvValues(ctx context.Context, projectID uuid.UUID) (map[string]string, []domain.EnvVariable, error) {
	if s.cipher == nil {
		return nil, nil, ErrNoCipher
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+envMetaColumns+`, ciphertext FROM env_variables WHERE project_id = $1 ORDER BY name`, projectID)
	if err != nil {
		return nil, nil, fmt.Errorf("store: ler variáveis cifradas: %w", err)
	}
	defer rows.Close()

	values := map[string]string{}
	var meta []domain.EnvVariable
	for rows.Next() {
		var v domain.EnvVariable
		var sealed []byte
		if err := rows.Scan(&v.Name, &v.Version, &v.Source, &v.KeyVersion, &v.CreatedAt, &v.UpdatedAt, &sealed); err != nil {
			return nil, nil, fmt.Errorf("store: ler variável cifrada: %w", err)
		}
		plain, err := s.cipher.OpenVersion(v.KeyVersion, sealed, envAAD(projectID, v.Name, v.Version))
		if err != nil {
			return nil, nil, fmt.Errorf("store: decifrar a variável %s: %w", v.Name, err)
		}
		values[v.Name] = string(plain)
		meta = append(meta, v)
	}
	return values, meta, rows.Err()
}
