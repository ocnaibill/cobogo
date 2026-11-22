-- DEC-47 parte C: restaurar os dados de um projeto a partir de um backup que está na VM.
-- A API cria, na mesma transação, o backup de segurança (before_restore) e o pedido de
-- restauração; o worker restaura quando o backup de segurança termina bem.

CREATE TABLE project_restores (
    id                UUID PRIMARY KEY,
    project_id        UUID NOT NULL REFERENCES projects(id),
    -- O backup que se restaura e o de segurança que o precede.
    backup_id         UUID NOT NULL REFERENCES project_backups(id),
    safety_backup_id  UUID NOT NULL REFERENCES project_backups(id),
    requested_by      UUID REFERENCES users(id),
    state             TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
    -- Em que passo está (para o painel): texto curto, só o nome do passo.
    step              TEXT NOT NULL DEFAULT '',
    error             TEXT NOT NULL DEFAULT '',
    -- O que foi restaurado, o que foi ignorado e os avisos.
    result            JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at        TIMESTAMPTZ,
    finished_at       TIMESTAMPTZ
);

-- Uma restauração por vez por projeto.
CREATE UNIQUE INDEX project_restores_one_active ON project_restores (project_id) WHERE state IN ('queued', 'running');
CREATE INDEX project_restores_by_project ON project_restores (project_id, created_at DESC);
