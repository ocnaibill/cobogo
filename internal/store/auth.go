package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ocnaibill/cobogo/internal/domain"
)

var (
	// ErrAdminExists indica que o administrador já foi criado e a
	// configuração inicial está fechada (DEC-27 item 3).
	ErrAdminExists = errors.New("store: já existe um administrador")
	// ErrLoginTaken indica um login já usado por outro usuário.
	ErrLoginTaken = errors.New("store: login já usado")
)

const setupLockKey = 7305000002

func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: contar usuários: %w", err)
	}
	return n, nil
}

// CreateAdmin cria o administrador **só se ainda não houver nenhum usuário**,
// numa única transação serializada: duas configurações simultâneas não criam
// dois administradores. Os projetos sem dono são adotados por ele.
func (s *Store) CreateAdmin(ctx context.Context, login, passwordHash string) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, fmt.Errorf("store: abrir transação de configuração: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, setupLockKey); err != nil {
		return domain.User{}, fmt.Errorf("store: serializar a configuração inicial: %w", err)
	}
	var existing int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&existing); err != nil {
		return domain.User{}, fmt.Errorf("store: contar usuários: %w", err)
	}
	if existing > 0 {
		return domain.User{}, ErrAdminExists
	}

	u := domain.User{ID: uuid.New(), Login: login, CreatedAt: time.Now().UTC()}
	if _, err := tx.Exec(ctx, `INSERT INTO users (id, login, password_hash, created_at) VALUES ($1, $2, $3, $4)`,
		u.ID, u.Login, passwordHash, u.CreatedAt); err != nil {
		return domain.User{}, fmt.Errorf("store: criar administrador: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE projects SET owner_id = $1 WHERE owner_id IS NULL`, u.ID); err != nil {
		return domain.User{}, fmt.Errorf("store: adotar projetos sem dono: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, fmt.Errorf("store: confirmar configuração inicial: %w", err)
	}
	return u, nil
}

// GetUserByLogin devolve o usuário e o hash da senha, para o login.
func (s *Store) GetUserByLogin(ctx context.Context, login string) (domain.User, string, error) {
	var u domain.User
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT id, login, created_at, password_hash FROM users WHERE login = $1`, login).
		Scan(&u.ID, &u.Login, &u.CreatedAt, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, "", ErrNotFound
	}
	if err != nil {
		return domain.User{}, "", fmt.Errorf("store: buscar usuário: %w", err)
	}
	return u, hash, nil
}

// GetPasswordHash devolve o hash da senha de um usuário, para conferir a
// senha atual numa troca.
func (s *Store) GetPasswordHash(ctx context.Context, userID uuid.UUID) (string, error) {
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("store: buscar hash: %w", err)
	}
	return hash, nil
}

func (s *Store) SetPasswordHash(ctx context.Context, userID uuid.UUID, hash string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, hash, userID)
	if err != nil {
		return fmt.Errorf("store: trocar senha: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetPasswordByLogin redefine a senha de um login existente e encerra todas
// as sessões dele. É a recuperação manual, por quem tem acesso ao host.
func (s *Store) ResetPasswordByLogin(ctx context.Context, login, hash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: abrir transação: %w", err)
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	err = tx.QueryRow(ctx, `UPDATE users SET password_hash = $1 WHERE login = $2 RETURNING id`, hash, login).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("store: redefinir senha: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id); err != nil {
		return fmt.Errorf("store: encerrar sessões: %w", err)
	}
	return tx.Commit(ctx)
}

// CreateSession grava uma sessão nova. expira em absolute, pelo relógio do
// banco. tokenHash é o SHA-256 do token; o token nunca chega aqui.
func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, tokenHash []byte, absolute time.Duration, ip, userAgent string) (domain.Session, error) {
	sess := domain.Session{ID: uuid.New(), UserID: userID}
	err := s.pool.QueryRow(ctx, `
		INSERT INTO sessions (id, user_id, token_hash, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, now() + make_interval(secs => $4), $5, $6)
		RETURNING created_at, last_seen_at, expires_at`,
		sess.ID, userID, tokenHash, absolute.Seconds(), clip(ip, 64), clip(userAgent, 256),
	).Scan(&sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" {
			return domain.Session{}, ErrNotFound
		}
		return domain.Session{}, fmt.Errorf("store: criar sessão: %w", err)
	}
	return sess, nil
}

// GetSession devolve a sessão e o usuário se o token existe, não passou do
// limite absoluto e não ficou ocioso por mais de idle. Renova last_seen_at no
// máximo uma vez por minuto, para não gravar a cada requisição.
func (s *Store) GetSession(ctx context.Context, tokenHash []byte, idle time.Duration) (domain.Session, domain.User, error) {
	var sess domain.Session
	var u domain.User
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.created_at, s.last_seen_at, s.expires_at, s.reauth_until, u.login, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > now() AND s.last_seen_at > now() - make_interval(secs => $2)`,
		tokenHash, idle.Seconds(),
	).Scan(&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.LastSeenAt, &sess.ExpiresAt, &sess.ReauthUntil, &u.Login, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, domain.User{}, ErrNotFound
	}
	if err != nil {
		return domain.Session{}, domain.User{}, fmt.Errorf("store: buscar sessão: %w", err)
	}
	u.ID = sess.UserID
	_, _ = s.pool.Exec(ctx, `UPDATE sessions SET last_seen_at = now() WHERE id = $1 AND last_seen_at < now() - interval '1 minute'`, sess.ID)
	return sess, u, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash); err != nil {
		return fmt.Errorf("store: encerrar sessão: %w", err)
	}
	return nil
}

// DeleteOtherSessions encerra as sessões do usuário, menos a atual.
func (s *Store) DeleteOtherSessions(ctx context.Context, userID uuid.UUID, keep []byte) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1 AND token_hash <> $2`, userID, keep); err != nil {
		return fmt.Errorf("store: encerrar outras sessões: %w", err)
	}
	return nil
}

// DeleteExpiredSessions remove sessões vencidas (absoluto ou ocioso).
func (s *Store) DeleteExpiredSessions(ctx context.Context, idle time.Duration) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM sessions WHERE expires_at <= now() OR last_seen_at <= now() - make_interval(secs => $1)`, idle.Seconds())
	if err != nil {
		return 0, fmt.Errorf("store: limpar sessões: %w", err)
	}
	return tag.RowsAffected(), nil
}

// AppendAudit registra um evento de auditoria. Quem chama garante que não há
// senha, token nem valor de variável nos campos.
func (s *Store) AppendAudit(ctx context.Context, e domain.AuditEvent) error {
	detail, err := marshalMap(e.Detail)
	if err != nil {
		return fmt.Errorf("store: serializar detalhe da auditoria: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO audit_events (id, actor_id, action, target, ip, outcome, detail) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		uuid.New(), e.ActorID, clip(e.Action, 64), clip(e.Target, 256), clip(e.IP, 64), clip(e.Outcome, 32), string(detail))
	if err != nil {
		return fmt.Errorf("store: registrar auditoria: %w", err)
	}
	return nil
}

// ListAudit devolve os eventos mais recentes primeiro.
func (s *Store) ListAudit(ctx context.Context, limit int) ([]domain.AuditEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT actor_id, action, target, ip, outcome FROM audit_events ORDER BY at DESC, id LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: listar auditoria: %w", err)
	}
	defer rows.Close()
	var out []domain.AuditEvent
	for rows.Next() {
		var e domain.AuditEvent
		if err := rows.Scan(&e.ActorID, &e.Action, &e.Target, &e.IP, &e.Outcome); err != nil {
			return nil, fmt.Errorf("store: ler auditoria: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// clip limita o tamanho de um texto vindo do cliente (User-Agent, por
// exemplo) sem cortar um caractere ao meio e sem deixar passar bytes que o
// PostgreSQL recusa (NUL, UTF-8 inválido).
func clip(s string, max int) string {
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r < ' ' || r == 0x7f {
			return -1
		}
		return r
	}, s)
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	for !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
