package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// O projeto é o repositório (DEC-52): a tabela stacks agrupa as aplicações (projects)
// de um repositório de um dono, e guarda a leitura do repositório que propõe quais criar.

var (
	// ErrSlugTaken indica um slug (de projeto ou de aplicação) que já está em uso.
	ErrSlugTaken = errors.New("store: o slug já está em uso")
	// ErrScanBusy indica uma leitura do repositório já em andamento.
	ErrScanBusy = errors.New("store: a leitura do repositório já está em andamento")
)

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

const stackColumns = `id, owner_id, slug, repository_url, created_at, deleted_at, scan_state, scan, scan_error, scanned_at`

func scanStack(row pgx.Row) (domain.Stack, error) {
	var st domain.Stack
	var raw []byte
	err := row.Scan(&st.ID, &st.OwnerID, &st.Slug, &st.RepositoryURL, &st.CreatedAt, &st.DeletedAt, &st.ScanState, &raw, &st.ScanError, &st.ScannedAt)
	if err != nil {
		return domain.Stack{}, err
	}
	if len(raw) > 0 {
		var sc domain.StackScan
		if err := json.Unmarshal(raw, &sc); err != nil {
			return domain.Stack{}, fmt.Errorf("store: ler a leitura do repositório: %w", err)
		}
		st.Scan = &sc
	}
	st.Apps = []domain.ProjectSummary{}
	return st, nil
}

// CreateStack registra um projeto (um repositório) sem aplicações, e já pede a leitura
// do repositório ao worker (DEC-52 item 2).
func (s *Store) CreateStack(ctx context.Context, ownerID uuid.UUID, slug, repositoryURL string) (domain.Stack, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Stack{}, fmt.Errorf("store: criar projeto: %w", err)
	}
	defer tx.Rollback(ctx)
	id := uuid.New()
	if _, err := tx.Exec(ctx, `
		INSERT INTO stacks (id, owner_id, slug, repository_url, scan_state) VALUES ($1, $2, $3, $4, 'scanning')`,
		id, ownerID, slug, repositoryURL); err != nil {
		if isUniqueViolation(err) {
			return domain.Stack{}, fmt.Errorf("%w: %s", ErrSlugTaken, slug)
		}
		return domain.Stack{}, fmt.Errorf("store: criar projeto: %w", err)
	}
	if err := enqueueScanTx(ctx, tx, id, ownerID); err != nil {
		return domain.Stack{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Stack{}, fmt.Errorf("store: criar projeto: %w", err)
	}
	return s.GetStackFor(ctx, id, ownerID)
}

func enqueueScanTx(ctx context.Context, tx pgx.Tx, stackID, ownerID uuid.UUID) error {
	_, err := insertOperationTx(ctx, tx, nil, domain.OpScanStack, map[string]any{"stack_id": stackID.String()}, time.Now(), &ownerID)
	if err != nil {
		return fmt.Errorf("store: pedir a leitura do repositório: %w", err)
	}
	return nil
}

// RequestStackScan pede uma nova leitura do repositório do projeto.
func (s *Store) RequestStackScan(ctx context.Context, stackID, ownerID uuid.UUID) (domain.Stack, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Stack{}, fmt.Errorf("store: ler o repositório: %w", err)
	}
	defer tx.Rollback(ctx)
	var state string
	err = tx.QueryRow(ctx, `SELECT scan_state FROM stacks WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL FOR UPDATE`, stackID, ownerID).Scan(&state)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Stack{}, ErrNotFound
	}
	if err != nil {
		return domain.Stack{}, fmt.Errorf("store: ler o repositório: %w", err)
	}
	if state == domain.ScanScanning {
		return domain.Stack{}, ErrScanBusy
	}
	if _, err := tx.Exec(ctx, `UPDATE stacks SET scan_state = 'scanning', scan_error = '' WHERE id = $1`, stackID); err != nil {
		return domain.Stack{}, fmt.Errorf("store: ler o repositório: %w", err)
	}
	if err := enqueueScanTx(ctx, tx, stackID, ownerID); err != nil {
		return domain.Stack{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Stack{}, fmt.Errorf("store: ler o repositório: %w", err)
	}
	return s.GetStackFor(ctx, stackID, ownerID)
}

// GetStack lê um projeto sem filtrar por dono (para o worker), sem as aplicações.
func (s *Store) GetStack(ctx context.Context, id uuid.UUID) (domain.Stack, error) {
	st, err := scanStack(s.pool.QueryRow(ctx, `SELECT `+stackColumns+` FROM stacks WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Stack{}, ErrNotFound
	}
	if err != nil {
		return domain.Stack{}, fmt.Errorf("store: buscar projeto: %w", err)
	}
	return st, nil
}

// GetStackFor lê um projeto do dono com as aplicações. Um projeto de outro dono, ou
// excluído, não existe para a API.
func (s *Store) GetStackFor(ctx context.Context, id, ownerID uuid.UUID) (domain.Stack, error) {
	st, err := s.GetStack(ctx, id)
	if err != nil {
		return domain.Stack{}, err
	}
	if st.OwnerID == nil || *st.OwnerID != ownerID || st.DeletedAt != nil {
		return domain.Stack{}, ErrNotFound
	}
	apps, err := s.appsOf(ctx, ownerID, []uuid.UUID{id})
	if err != nil {
		return domain.Stack{}, err
	}
	st.Apps = apps[id]
	if st.Apps == nil {
		st.Apps = []domain.ProjectSummary{}
	}
	return st, nil
}

// GetStackBySlugFor lê um projeto do dono pelo slug.
func (s *Store) GetStackBySlugFor(ctx context.Context, slug string, ownerID uuid.UUID) (domain.Stack, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM stacks WHERE slug = $1 AND owner_id = $2 AND deleted_at IS NULL`, slug, ownerID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Stack{}, ErrNotFound
	}
	if err != nil {
		return domain.Stack{}, fmt.Errorf("store: buscar projeto: %w", err)
	}
	return s.GetStackFor(ctx, id, ownerID)
}

// ListStacksFor lista os projetos do dono, do mais novo ao mais antigo, cada um com
// as aplicações (e o último deploy de cada).
func (s *Store) ListStacksFor(ctx context.Context, ownerID uuid.UUID) ([]domain.Stack, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+stackColumns+` FROM stacks
		WHERE owner_id = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC, id DESC
		LIMIT 500`, ownerID)
	if err != nil {
		return nil, fmt.Errorf("store: listar projetos: %w", err)
	}
	out := []domain.Stack{}
	ids := []uuid.UUID{}
	for rows.Next() {
		st, err := scanStack(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: ler projeto: %w", err)
		}
		out = append(out, st)
		ids = append(ids, st.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: listar projetos: %w", err)
	}
	apps, err := s.appsOf(ctx, ownerID, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if a := apps[out[i].ID]; a != nil {
			out[i].Apps = a
		}
	}
	return out, nil
}

// appsOf devolve as aplicações vivas dos projetos dados, na ordem do papel (frontend,
// backend, os demais) e da criação.
func (s *Store) appsOf(ctx context.Context, ownerID uuid.UUID, stackIDs []uuid.UUID) (map[uuid.UUID][]domain.ProjectSummary, error) {
	all, _, err := s.ListProjectsFor(ctx, ownerID, 1000, nil)
	if err != nil {
		return nil, err
	}
	want := map[uuid.UUID]bool{}
	for _, id := range stackIDs {
		want[id] = true
	}
	out := map[uuid.UUID][]domain.ProjectSummary{}
	// ListProjectsFor vem do mais novo ao mais antigo; aqui a ordem é a da criação.
	for i := len(all) - 1; i >= 0; i-- {
		a := all[i]
		if a.StackID != nil && want[*a.StackID] {
			out[*a.StackID] = append(out[*a.StackID], a)
		}
	}
	for id := range out {
		sortApps(out[id])
	}
	return out, nil
}

// roleOrder põe o que se visita primeiro: o frontend, depois o backend.
var roleOrder = map[string]int{domain.CompFrontend: 0, domain.CompFullstack: 1, domain.CompBackend: 2, domain.CompApp: 3, "": 4}

func sortApps(apps []domain.ProjectSummary) {
	for i := 1; i < len(apps); i++ {
		for j := i; j > 0 && roleOrder[apps[j].Role] < roleOrder[apps[j-1].Role]; j-- {
			apps[j], apps[j-1] = apps[j-1], apps[j]
		}
	}
}

// SaveStackScan grava a proposta da leitura do repositório (o worker).
func (s *Store) SaveStackScan(ctx context.Context, stackID uuid.UUID, scan domain.StackScan) error {
	raw, err := json.Marshal(scan)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE stacks SET scan_state = 'done', scan = $2, scan_error = '', scanned_at = $3 WHERE id = $1`,
		stackID, raw, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("store: gravar a leitura do repositório: %w", err)
	}
	return nil
}

// FailStackScan registra que a leitura do repositório falhou, com o motivo.
func (s *Store) FailStackScan(ctx context.Context, stackID uuid.UUID, cause string) error {
	_, err := s.pool.Exec(ctx, `UPDATE stacks SET scan_state = 'failed', scan_error = $2, scanned_at = $3 WHERE id = $1`,
		stackID, cause, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("store: registrar a falha da leitura: %w", err)
	}
	return nil
}

// NewApp é uma aplicação a criar num projeto: a pasta, o slug (o endereço) e o papel.
type NewApp struct {
	RootDirectory string
	Slug          string
	Role          string
	// Ecosystem fixa o ecossistema quando a pasta é dividida por duas aplicações (DEC-53 F).
	Ecosystem string
}

// CreateApps cria as aplicações no projeto, numa transação, e um deploy para cada uma
// quando deploy é verdadeiro (o backend antes do frontend, para a fila seguir a ordem).
// Uma pasta que já é aplicação do projeto é ignorada.
func (s *Store) CreateApps(ctx context.Context, stackID, ownerID uuid.UUID, apps []NewApp) ([]domain.Project, error) {
	st, err := s.GetStackFor(ctx, stackID, ownerID)
	if err != nil {
		return nil, err
	}
	existing := map[string]bool{}
	for _, a := range st.Apps {
		existing[AppKey(a.RootDirectory, a.Ecosystem)] = true
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: criar aplicações: %w", err)
	}
	defer tx.Rollback(ctx)
	out := []domain.Project{}
	for _, a := range apps {
		if existing[AppKey(a.RootDirectory, a.Ecosystem)] {
			continue
		}
		existing[AppKey(a.RootDirectory, a.Ecosystem)] = true
		p, err := insertApp(ctx, tx, ownerID, stackID, a.Slug, st.RepositoryURL, a.RootDirectory, a.Role, a.Ecosystem)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("store: criar aplicações: %w", err)
	}
	return out, nil
}

// SetProjectRole grava o papel de uma aplicação, como a análise o viu.
func (s *Store) SetProjectRole(ctx context.Context, projectID uuid.UUID, role string) error {
	_, err := s.pool.Exec(ctx, `UPDATE projects SET role = $2 WHERE id = $1 AND role <> $2`, projectID, role)
	if err != nil {
		return fmt.Errorf("store: gravar o papel da aplicação: %w", err)
	}
	return nil
}

// MarkStackDeletedIfEmpty marca o projeto como excluído quando não lhe resta
// aplicação viva (a exclusão de cada uma segue o caminho da DEC-38).
func (s *Store) MarkStackDeletedIfEmpty(ctx context.Context, stackID, ownerID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE stacks SET deleted_at = now()
		WHERE id = $1 AND owner_id = $2 AND deleted_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM projects WHERE stack_id = $1 AND deleted_at IS NULL)`, stackID, ownerID)
	if err != nil {
		return fmt.Errorf("store: excluir projeto: %w", err)
	}
	return nil
}

// ProjectSlugInUse diz se uma aplicação viva já usa o slug (o endereço).
func (s *Store) ProjectSlugInUse(ctx context.Context, slug string) (bool, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE slug = $1 AND deleted_at IS NULL`, slug).Scan(&n); err != nil {
		return false, fmt.Errorf("store: conferir o slug: %w", err)
	}
	return n > 0, nil
}

// StackApps devolve as aplicações vivas de um projeto (para o worker).
func (s *Store) StackApps(ctx context.Context, stackID uuid.UUID) ([]domain.Project, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, slug, root_directory, role, ecosystem FROM projects
		WHERE stack_id = $1 AND deleted_at IS NULL ORDER BY created_at`, stackID)
	if err != nil {
		return nil, fmt.Errorf("store: listar aplicações: %w", err)
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		var p domain.Project
		if err := rows.Scan(&p.ID, &p.Slug, &p.RootDirectory, &p.Role, &p.Ecosystem); err != nil {
			return nil, fmt.Errorf("store: ler aplicação: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AppKey identifica uma aplicação do projeto: a pasta e, quando duas dividem a mesma pasta,
// o ecossistema de cada uma (DEC-53 F).
func AppKey(rootDirectory, ecosystem string) string { return rootDirectory + "\x00" + ecosystem }
