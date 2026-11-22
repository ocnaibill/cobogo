package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/ocnaibill/cobogo/internal/domain"
)

// Serviços auxiliares (DEC-42): a intenção do projeto sobre um banco e o vínculo
// com a variável de conexão.

var (
	// ErrVariableDefined: a variável de conexão já tem um valor que a pessoa
	// digitou; o vínculo nunca o sobrescreve (DEC-42 item 5).
	ErrVariableDefined = errors.New("store: a variável de conexão já tem um valor definido pelo usuário")
	// ErrNoManagedService: não há PostgreSQL gerenciado para apagar.
	ErrNoManagedService = errors.New("store: o projeto não tem PostgreSQL gerenciado")
	// ErrManagedExists: já há um PostgreSQL gerenciado; trocar de escolha sem
	// apagá-lo deixaria o banco e os dados órfãos.
	ErrManagedExists = errors.New("store: o projeto já tem um PostgreSQL gerenciado; apague-o antes de escolher outra coisa")
)

// DefinedVariableError diz qual variável de conexão já tem valor digitado.
type DefinedVariableError struct{ Name string }

func (e *DefinedVariableError) Error() string {
	return ErrVariableDefined.Error() + ": " + e.Name
}

func (e *DefinedVariableError) Is(target error) bool { return target == ErrVariableDefined }

const serviceColumns = `id, project_id, engine, mode, variable, variables, tls, container_name, volume_name, status, status_detail, created_at, updated_at`

func scanService(row pgx.Row) (domain.Service, error) {
	var sv domain.Service
	err := row.Scan(&sv.ID, &sv.ProjectID, &sv.Engine, &sv.Mode, &sv.Variable, &sv.Variables, &sv.TLS, &sv.ContainerName, &sv.VolumeName, &sv.Status, &sv.StatusDetail, &sv.CreatedAt, &sv.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Service{}, ErrNotFound
	}
	if err != nil {
		return domain.Service{}, fmt.Errorf("store: ler serviço: %w", err)
	}
	return sv, nil
}

// GetService devolve a intenção vigente do projeto para uma engine, ou
// ErrNotFound. Serve à API e ao worker.
func (s *Store) GetService(ctx context.Context, projectID uuid.UUID, engine string) (domain.Service, error) {
	return scanService(s.pool.QueryRow(ctx, `SELECT `+serviceColumns+` FROM project_services WHERE project_id = $1 AND engine = $2 AND deleted_at IS NULL`, projectID, engine))
}

// ChooseDatabase registra a escolha do dono sobre o banco do projeto (HEU-06.6).
// variables são os nomes das variáveis por papel (DEC-44); values, o valor de
// cada papel que o Cobogó entrega, só no modo gerenciado. No modo gerenciado
// grava cada variável (cifrada, origem `linked`); se alguma já tem um valor que
// não é do vínculo, recusa sem gravar nada. O papel `ca` é entregue na hora de
// iniciar o app e nunca é gravado, mas o nome dele também não pode colidir.
// Troca de uma escolha anterior vale: a intenção vigente é uma só.
func (s *Store) ChooseDatabase(ctx context.Context, projectID, ownerID uuid.UUID, mode string, variables, values map[string]string) (domain.Service, error) {
	if _, err := s.GetProjectFor(ctx, projectID, ownerID); err != nil {
		return domain.Service{}, err
	}
	if current, err := s.GetService(ctx, projectID, domain.ServicePostgres); err == nil && current.Mode == domain.ServiceManaged && current.Status != domain.ServiceDeleting {
		if mode != domain.ServiceManaged {
			return domain.Service{}, ErrManagedExists
		}
		// Escolher o gerenciado de novo não muda nada, e a senha não é trocada.
		return current, nil
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return domain.Service{}, err
	}
	style := domain.StyleURL
	if _, ok := variables[domain.RoleURL]; !ok {
		style = domain.StyleParts
	}
	primary := variables[domain.PrimaryRole(style)]
	var container, volume string
	tls := false
	if mode == domain.ServiceManaged {
		for _, name := range variables {
			existing, err := s.GetEnvVariable(ctx, projectID, name)
			switch {
			case err == nil && existing.Source != domain.EnvSourceLinked:
				return domain.Service{}, &DefinedVariableError{Name: name}
			case err != nil && !errors.Is(err, ErrNotFound):
				return domain.Service{}, err
			}
		}
		container, volume = domain.ServiceContainerName(projectID.String()), domain.ServiceVolumeName(projectID.String())
		tls = true
	}
	sv, err := scanService(s.pool.QueryRow(ctx, `
		INSERT INTO project_services (id, project_id, engine, mode, variable, variables, tls, container_name, volume_name, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'pending')
		ON CONFLICT (project_id, engine) WHERE deleted_at IS NULL DO UPDATE
		SET mode = EXCLUDED.mode, variable = EXCLUDED.variable, variables = EXCLUDED.variables, tls = EXCLUDED.tls,
		    container_name = EXCLUDED.container_name, volume_name = EXCLUDED.volume_name,
		    status = 'pending', status_detail = '', updated_at = now()
		RETURNING `+serviceColumns, uuid.New(), projectID, domain.ServicePostgres, mode, primary, variables, tls, container, volume))
	if err != nil {
		return domain.Service{}, err
	}
	if mode == domain.ServiceManaged {
		for role, name := range variables {
			value, ok := values[role]
			if !ok || role == domain.RoleCA {
				continue
			}
			if _, err := s.PutEnvVariable(ctx, projectID, name, value, domain.EnvSourceLinked); err != nil {
				return domain.Service{}, err
			}
		}
	}
	return sv, nil
}

// SetServiceStatus registra o desfecho da criação do serviço. É do worker.
func (s *Store) SetServiceStatus(ctx context.Context, id uuid.UUID, status, detail string) error {
	_, err := s.pool.Exec(ctx, `UPDATE project_services SET status = $2, status_detail = $3, updated_at = now() WHERE id = $1 AND deleted_at IS NULL AND status <> 'deleting'`, id, status, detail)
	if err != nil {
		return fmt.Errorf("store: registrar estado do serviço: %w", err)
	}
	return nil
}

// RequestDeleteService apaga o vínculo de forma lógica e enfileira a remoção do
// serviço e dos dados (DEC-42 item 6). Irreversível; a confirmação é o nome do
// projeto. A variável de conexão vinculada sai junto.
func (s *Store) RequestDeleteService(ctx context.Context, projectID, ownerID uuid.UUID, confirmSlug string) (domain.Operation, error) {
	p, err := s.GetProjectFor(ctx, projectID, ownerID)
	if err != nil {
		return domain.Operation{}, err
	}
	if confirmSlug != p.Slug {
		return domain.Operation{}, ErrConfirmationMismatch
	}
	sv, err := s.GetService(ctx, projectID, domain.ServicePostgres)
	if err != nil || sv.Mode != domain.ServiceManaged {
		return domain.Operation{}, ErrNoManagedService
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Operation{}, fmt.Errorf("store: abrir transação da exclusão do serviço: %w", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE project_services SET status = 'deleting', updated_at = now() WHERE id = $1`, sv.ID); err != nil {
		return domain.Operation{}, fmt.Errorf("store: marcar serviço para exclusão: %w", err)
	}
	linked := make([]string, 0, len(sv.Variables)+1)
	linked = append(linked, sv.Variable)
	for _, name := range sv.Variables {
		linked = append(linked, name)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM env_variables WHERE project_id = $1 AND name = ANY($2) AND source = 'linked'`, projectID, linked); err != nil {
		return domain.Operation{}, fmt.Errorf("store: apagar a variável vinculada: %w", err)
	}
	op, err := insertOperationTx(ctx, tx, &projectID, domain.OpDeleteService, map[string]any{"engine": sv.Engine}, time.Now().UTC(), &ownerID)
	if err != nil {
		return domain.Operation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Operation{}, fmt.Errorf("store: confirmar exclusão do serviço: %w", err)
	}
	return op, nil
}

// GetServiceForWorker devolve o serviço mesmo marcado para exclusão (o worker
// precisa dele para apagar). Só o vigente: um já apagado não volta.
func (s *Store) GetServiceForWorker(ctx context.Context, projectID uuid.UUID, engine string) (domain.Service, error) {
	return s.GetService(ctx, projectID, engine)
}

// MarkServiceDeleted fecha o serviço: o registro fica como história e uma nova
// escolha cria outro.
func (s *Store) MarkServiceDeleted(ctx context.Context, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE project_services SET deleted_at = now(), status = 'deleting', updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: fechar serviço: %w", err)
	}
	return nil
}

// WaitingForDatabase devolve os deploys do projeto parados esperando a escolha do
// banco (a pendência `database` da análise deles).
func (s *Store) WaitingForDatabase(ctx context.Context, projectID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, analysis_id FROM deployments WHERE project_id = $1 AND state = $2`, projectID, domain.DeploymentNeedsInput)
	if err != nil {
		return nil, fmt.Errorf("store: listar deploys à espera: %w", err)
	}
	type row struct {
		id       uuid.UUID
		analysis *uuid.UUID
	}
	var waiting []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.analysis); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: ler deploy à espera: %w", err)
		}
		waiting = append(waiting, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, r := range waiting {
		if r.analysis == nil {
			continue
		}
		a, err := s.GetAnalysis(ctx, *r.analysis)
		if err != nil {
			continue
		}
		for _, f := range a.Result.Pending {
			if f.Field == domain.FieldDatabase {
				ids = append(ids, r.id)
				break
			}
		}
	}
	return ids, nil
}
