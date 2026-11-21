-- DEC-42: PostgreSQL auxiliar por projeto. `project_services` guarda a intenção
-- escolhida (criar um banco do Cobogó, usar um externo ou não precisar de banco)
-- e a identidade do serviço gerenciado; o estado real vem do Docker, e o worker
-- só registra aqui o último desfecho.

ALTER TABLE env_variables DROP CONSTRAINT env_variables_source_check;
ALTER TABLE env_variables ADD CONSTRAINT env_variables_source_check CHECK (source IN ('user', 'generated', 'imported', 'linked'));

CREATE TABLE project_services (
    id             UUID PRIMARY KEY,
    project_id     UUID NOT NULL REFERENCES projects(id),
    engine         TEXT NOT NULL CHECK (engine IN ('postgres')),
    mode           TEXT NOT NULL CHECK (mode IN ('managed', 'external', 'none')),
    -- A variável de conexão da aplicação (por exemplo DATABASE_URL).
    variable       TEXT NOT NULL,
    container_name TEXT NOT NULL DEFAULT '',
    volume_name    TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'ready', 'failed', 'deleting')),
    status_detail  TEXT NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ
);
-- Uma intenção vigente por projeto e engine.
CREATE UNIQUE INDEX idx_project_services_live ON project_services(project_id, engine) WHERE deleted_at IS NULL;

-- O volume de um serviço gerenciado não é "sem projeto" só porque o caminho dele
-- não está na lista de caminhos persistentes (DEC-41).
ALTER TABLE project_volumes ADD COLUMN service TEXT NOT NULL DEFAULT '';

ALTER TABLE operations DROP CONSTRAINT operations_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_kind_check
    CHECK (kind IN ('retire_releases', 'delete_project', 'apply_limits', 'sweep', 'stop_app', 'start_app', 'restart_app', 'delete_volume', 'delete_service'));
