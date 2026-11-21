-- DEC-38: ciclo de vida. Exclusão lógica de projeto (o histórico fica) e fila de
-- operações que o worker executa (a API não tem acesso ao Docker).

ALTER TABLE projects ADD COLUMN deleted_at TIMESTAMPTZ;

-- O slug só precisa ser único entre os projetos que existem: o de um projeto
-- excluído fica livre para reuso.
ALTER TABLE projects DROP CONSTRAINT projects_slug_key;
CREATE UNIQUE INDEX idx_projects_slug_live ON projects(slug) WHERE deleted_at IS NULL;

CREATE TABLE operations (
    id           UUID PRIMARY KEY,
    -- Nulo na faxina, que varre todos os projetos.
    project_id   UUID REFERENCES projects(id),
    kind         TEXT NOT NULL CHECK (kind IN ('retire_releases', 'delete_project', 'apply_limits', 'sweep')),
    state        TEXT NOT NULL CHECK (state IN ('PENDING', 'RUNNING', 'DONE', 'FAILED')),
    params       JSONB NOT NULL DEFAULT '{}'::jsonb,
    result       JSONB NOT NULL DEFAULT '{}'::jsonb,
    error        TEXT NOT NULL DEFAULT '',
    attempts     INT NOT NULL DEFAULT 0,
    not_before   TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_until  TIMESTAMPTZ,
    requested_by UUID REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_operations_claimable ON operations(state, not_before);
CREATE INDEX idx_operations_project ON operations(project_id, created_at DESC);
