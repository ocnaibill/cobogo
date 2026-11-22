-- DEC-46: logs de execução (stdout e stderr) das aplicações e dos bancos gerenciados,
-- capturados pelo worker (que redige os valores secretos) e lidos pela API por
-- cursor (o id). Sem chave estrangeira: a varredura apaga o que passa da retenção
-- e o que é de projeto excluído.

CREATE TABLE runtime_logs (
    id             BIGSERIAL PRIMARY KEY,
    project_id     UUID NOT NULL,
    container_id   TEXT NOT NULL,
    container_name TEXT NOT NULL DEFAULT '',
    deployment_id  UUID,
    -- 'app' (aplicação de um deploy) ou 'postgres' (banco gerenciado do projeto).
    origin         TEXT NOT NULL CHECK (origin IN ('app', 'postgres')),
    -- 'system' são avisos do próprio Cobogó (linhas descartadas pelo limite de taxa).
    stream         TEXT NOT NULL CHECK (stream IN ('stdout', 'stderr', 'system')),
    at             TIMESTAMPTZ NOT NULL,
    line           TEXT NOT NULL
);
CREATE INDEX runtime_logs_by_project ON runtime_logs (project_id, id);
CREATE INDEX runtime_logs_by_container ON runtime_logs (container_id, at DESC);
CREATE INDEX runtime_logs_by_time ON runtime_logs (at);
