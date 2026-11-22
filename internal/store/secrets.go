package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Variáveis e segredos guiados (DEC-39).

// RequiredEnv devolve os nomes de variável que o usuário exigiu no projeto.
func (s *Store) RequiredEnv(ctx context.Context, projectID uuid.UUID) ([]string, error) {
	var names []string
	err := s.pool.QueryRow(ctx, `SELECT required_env FROM projects WHERE id = $1`, projectID).Scan(&names)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: ler variáveis obrigatórias: %w", err)
	}
	if names == nil {
		names = []string{}
	}
	return names, nil
}

// SetRequiredEnv substitui a lista de variáveis obrigatórias do projeto do dono.
// A lista é gravada ordenada e sem repetição.
func (s *Store) SetRequiredEnv(ctx context.Context, projectID, ownerID uuid.UUID, names []string) ([]string, error) {
	clean := slices.Clone(names)
	slices.Sort(clean)
	clean = slices.Compact(clean)
	if clean == nil {
		clean = []string{}
	}
	tag, err := s.pool.Exec(ctx, `UPDATE projects SET required_env = $1 WHERE id = $2 AND owner_id = $3 AND deleted_at IS NULL`, clean, projectID, ownerID)
	if err != nil {
		return nil, fmt.Errorf("store: gravar variáveis obrigatórias: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return nil, ErrNotFound
	}
	return clean, nil
}

// EnvChange é uma mudança de variável, lida da auditoria (DEC-39 item 5). Nunca
// tem valor.
type EnvChange struct {
	Name    string    `json:"name,omitempty"`
	Action  string    `json:"action"`
	Version int       `json:"version,omitempty"`
	Source  string    `json:"source,omitempty"`
	Actor   string    `json:"actor,omitempty"`
	At      time.Time `json:"at"`
	// Names é a lista de uma marcação de obrigatórias (env.required).
	Names []string `json:"names,omitempty"`
}

// ListEnvChanges devolve as mudanças de variáveis do projeto, da mais nova para
// a mais antiga. O alvo da auditoria é "<projeto>/<NOME>" ou "<projeto>".
func (s *Store) ListEnvChanges(ctx context.Context, projectID uuid.UUID, limit int) ([]EnvChange, error) {
	id := projectID.String()
	rows, err := s.pool.Query(ctx, `
		SELECT a.at, a.action, a.target, a.detail, COALESCE(u.login, '')
		FROM audit_events a LEFT JOIN users u ON u.id = a.actor_id
		WHERE a.action LIKE 'env.%' AND a.outcome = 'ok' AND (a.target = $1 OR a.target LIKE $1 || '/%')
		ORDER BY a.at DESC, a.id DESC LIMIT $2`, id, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listar mudanças de variáveis: %w", err)
	}
	defer rows.Close()
	out := []EnvChange{}
	for rows.Next() {
		var c EnvChange
		var target string
		var detail []byte
		if err := rows.Scan(&c.At, &c.Action, &target, &detail, &c.Actor); err != nil {
			return nil, fmt.Errorf("store: ler mudança de variável: %w", err)
		}
		if len(target) > len(id)+1 {
			c.Name = target[len(id)+1:]
		}
		var d struct {
			Version int      `json:"version"`
			Source  string   `json:"source"`
			Names   []string `json:"names"`
		}
		if err := json.Unmarshal(detail, &d); err == nil {
			c.Version, c.Source, c.Names = d.Version, d.Source, d.Names
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// KeyVersionCounts conta as variáveis por versão de chave. Precisa do role
// dono ou do worker: a API não lê essa coluna de forma útil para rotação.
func (s *Store) KeyVersionCounts(ctx context.Context) (map[int]int, error) {
	rows, err := s.pool.Query(ctx, `SELECT key_version, count(*) FROM env_variables GROUP BY key_version`)
	if err != nil {
		return nil, fmt.Errorf("store: contar versões de chave: %w", err)
	}
	defer rows.Close()
	counts := map[int]int{}
	for rows.Next() {
		var v, n int
		if err := rows.Scan(&v, &n); err != nil {
			return nil, fmt.Errorf("store: ler contagem: %w", err)
		}
		counts[v] = n
	}
	return counts, rows.Err()
}

// RotationReport diz o que a rotação fez.
type RotationReport struct {
	// Rotated conta as variáveis recifradas, por versão de chave de origem.
	Rotated map[int]int
	// Current é a versão da chave atual.
	Current int
}

// RotateSecrets recifra com a chave atual toda variável gravada com outra,
// **sem mudar a versão da variável** (o valor é o mesmo, então não pede novo
// deploy; o texto cifrado continua preso ao mesmo projeto, nome e versão). Tudo
// numa transação: se alguma não abrir, nada muda e o erro nomeia a variável,
// nunca o valor. Só o role dono deve chamar (DEC-39 item 6).
func (s *Store) RotateSecrets(ctx context.Context) (RotationReport, error) {
	if s.cipher == nil {
		return RotationReport{}, ErrNoCipher
	}
	current := s.cipher.KeyVersion()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return RotationReport{}, fmt.Errorf("store: abrir transação da rotação: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT id, project_id, name, version, key_version, ciphertext
		FROM env_variables WHERE key_version <> $1 ORDER BY project_id, name FOR UPDATE`, current)
	if err != nil {
		return RotationReport{}, fmt.Errorf("store: ler variáveis a rotacionar: %w", err)
	}
	type item struct {
		id      uuid.UUID
		sealed  []byte
		from    int
		version int
		aad     []byte
	}
	var items []item
	for rows.Next() {
		var it item
		var projectID uuid.UUID
		var name string
		if err := rows.Scan(&it.id, &projectID, &name, &it.version, &it.from, &it.sealed); err != nil {
			rows.Close()
			return RotationReport{}, fmt.Errorf("store: ler variável: %w", err)
		}
		it.aad = envAAD(projectID, name, it.version)
		plain, err := s.cipher.OpenVersion(it.from, it.sealed, it.aad)
		if err != nil {
			rows.Close()
			return RotationReport{}, fmt.Errorf("a variável %s do projeto %s (chave versão %d) não abre: %w", name, projectID, it.from, err)
		}
		resealed, err := s.cipher.Seal(plain, it.aad)
		if err != nil {
			rows.Close()
			return RotationReport{}, err
		}
		it.sealed = resealed
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return RotationReport{}, fmt.Errorf("store: ler variáveis: %w", err)
	}

	report := RotationReport{Rotated: map[int]int{}, Current: current}
	for _, it := range items {
		if _, err := tx.Exec(ctx, `UPDATE env_variables SET ciphertext = $2, key_version = $3 WHERE id = $1`, it.id, it.sealed, current); err != nil {
			return RotationReport{}, fmt.Errorf("store: gravar variável recifrada: %w", err)
		}
		report.Rotated[it.from]++
	}
	if err := tx.Commit(ctx); err != nil {
		return RotationReport{}, fmt.Errorf("store: confirmar rotação: %w", err)
	}
	return report, nil
}

// SecretsVerification é o resultado de abrir toda variável cifrada.
type SecretsVerification struct {
	// Total é quantas variáveis há; Unreadable, as que a chave do chaveiro não abre,
	// como "projeto/NOME" (nunca o valor).
	Total      int
	Unreadable []string
}

// VerifySecrets tenta abrir **todas** as variáveis cifradas com o chaveiro
// carregado, sem alterar nada. É a prova de que uma restauração serve: com a
// chave certa nenhuma fica ilegível; com a errada, todas (DEC-47). Precisa do
// role dono ou do worker, que leem o texto cifrado.
func (s *Store) VerifySecrets(ctx context.Context) (SecretsVerification, error) {
	if s.cipher == nil {
		return SecretsVerification{}, ErrNoCipher
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.slug, e.project_id, e.name, e.version, e.key_version, e.ciphertext
		FROM env_variables e JOIN projects p ON p.id = e.project_id ORDER BY p.slug, e.name`)
	if err != nil {
		return SecretsVerification{}, fmt.Errorf("store: ler variáveis para conferir: %w", err)
	}
	defer rows.Close()
	var out SecretsVerification
	for rows.Next() {
		var slug, name string
		var projectID uuid.UUID
		var version, keyVersion int
		var sealed []byte
		if err := rows.Scan(&slug, &projectID, &name, &version, &keyVersion, &sealed); err != nil {
			return SecretsVerification{}, fmt.Errorf("store: ler variável: %w", err)
		}
		out.Total++
		if _, err := s.cipher.OpenVersion(keyVersion, sealed, envAAD(projectID, name, version)); err != nil {
			out.Unreadable = append(out.Unreadable, slug+"/"+name)
		}
	}
	return out, rows.Err()
}
