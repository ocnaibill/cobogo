-- DEC-47 parte B: backup dos dados de um projeto (volumes e PostgreSQL gerenciado).
-- A API cria o pedido; o worker faz a cópia, num volume de backups, e grava o desfecho.
-- Os arquivos ficam em projects/<projeto>/<backup>/ no volume; aqui fica o registro.

CREATE TABLE project_backups (
    id               UUID PRIMARY KEY,
    project_id       UUID NOT NULL REFERENCES projects(id),
    requested_by     UUID REFERENCES users(id),
    -- Por que existe: pedido pela pessoa, antes de apagar o projeto ou o banco, ou
    -- o de segurança que a restauração faz antes de tocar em qualquer coisa.
    reason           TEXT NOT NULL CHECK (reason IN ('manual', 'before_delete', 'before_restore')),
    pause_app        BOOLEAN NOT NULL DEFAULT false,
    state            TEXT NOT NULL DEFAULT 'queued' CHECK (state IN ('queued', 'running', 'succeeded', 'failed')),
    error            TEXT NOT NULL DEFAULT '',
    size_bytes       BIGINT NOT NULL DEFAULT 0,
    -- O que foi copiado, com tamanhos e somas (o mesmo manifest.json do arquivo).
    manifest         JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- A API só marca o pedido de apagar; quem apaga os arquivos é o worker.
    delete_requested BOOLEAN NOT NULL DEFAULT false,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ,
    deleted_at       TIMESTAMPTZ
);

-- Um backup por vez por projeto (um pedido na fila ou rodando ocupa a vaga).
CREATE UNIQUE INDEX project_backups_one_active ON project_backups (project_id)
    WHERE state IN ('queued', 'running') AND deleted_at IS NULL;
CREATE INDEX project_backups_by_project ON project_backups (project_id, created_at DESC) WHERE deleted_at IS NULL;
