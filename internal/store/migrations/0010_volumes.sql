-- DEC-41: dados persistentes. O usuário declara os caminhos; o worker cria um
-- volume nomeado por projeto e caminho e o registra aqui, para o painel listar
-- (com o tamanho) e para os volumes sem projeto não ficarem invisíveis.
ALTER TABLE projects ADD COLUMN persistent_paths TEXT[] NOT NULL DEFAULT '{}';

CREATE TABLE project_volumes (
    -- O nome do volume no Docker (derivado do projeto e do caminho).
    name        TEXT PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES projects(id),
    path        TEXT NOT NULL,
    size_bytes  BIGINT,
    measured_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Preenchido quando o volume foi apagado (o registro fica como história).
    deleted_at  TIMESTAMPTZ
);
CREATE INDEX idx_project_volumes_project ON project_volumes(project_id);

ALTER TABLE operations DROP CONSTRAINT operations_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_kind_check
    CHECK (kind IN ('retire_releases', 'delete_project', 'apply_limits', 'sweep', 'stop_app', 'start_app', 'restart_app', 'delete_volume'));
