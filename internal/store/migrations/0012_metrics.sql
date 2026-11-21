-- DEC-43: amostras de CPU, memória, reinícios e saúde dos contêineres do Cobogó,
-- coletadas por um processo próprio, com o seu role (só escreve aqui). A
-- identidade de uma amostra vem dos labels do contêiner, então não há chave
-- estrangeira: o coletor não lê projetos, releases nem variáveis.

CREATE TABLE metric_samples (
    container_id    TEXT NOT NULL,
    sampled_at      TIMESTAMPTZ NOT NULL,
    project_id      UUID,
    deployment_id   UUID,
    -- 'app' (aplicação de um deploy) ou 'service' (banco do projeto).
    role            TEXT NOT NULL,
    service         TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL,
    state           TEXT NOT NULL,
    health          TEXT NOT NULL DEFAULT '',
    restart_count   INT NOT NULL DEFAULT 0,
    oom_killed      BOOLEAN NOT NULL DEFAULT false,
    started_at      TIMESTAMPTZ,
    -- Nulos quando não há leitura (contêiner parado): ausência não vira zero.
    cpu_cores       DOUBLE PRECISION,
    cpu_limit_cores DOUBLE PRECISION,
    mem_bytes       BIGINT,
    mem_limit_bytes BIGINT,
    PRIMARY KEY (container_id, sampled_at)
);
CREATE INDEX idx_metric_samples_project ON metric_samples(project_id, sampled_at DESC);
CREATE INDEX idx_metric_samples_time ON metric_samples(sampled_at);

-- Agregado por minuto, para as janelas que passam do bruto.
CREATE TABLE metric_rollups (
    container_id    TEXT NOT NULL,
    bucket          TIMESTAMPTZ NOT NULL,
    project_id      UUID,
    deployment_id   UUID,
    role            TEXT NOT NULL,
    service         TEXT NOT NULL DEFAULT '',
    name            TEXT NOT NULL,
    samples         INT NOT NULL,
    running_samples INT NOT NULL,
    cpu_avg         DOUBLE PRECISION,
    cpu_max         DOUBLE PRECISION,
    mem_avg         BIGINT,
    mem_max         BIGINT,
    restarts_max    INT NOT NULL DEFAULT 0,
    PRIMARY KEY (container_id, bucket)
);
CREATE INDEX idx_metric_rollups_project ON metric_rollups(project_id, bucket DESC);

-- Sonda de rota (segunda etapa da DEC-43): uma verificação sintética por Traefik.
CREATE TABLE metric_probes (
    project_id  UUID NOT NULL,
    sampled_at  TIMESTAMPTZ NOT NULL,
    host        TEXT NOT NULL,
    -- responded (2xx e 3xx), client_error (4xx), failed, stopped (aplicação parada).
    outcome     TEXT NOT NULL CHECK (outcome IN ('responded', 'client_error', 'failed', 'stopped')),
    status      INT,
    duration_ms INT,
    PRIMARY KEY (project_id, sampled_at)
);
CREATE INDEX idx_metric_probes_time ON metric_probes(sampled_at);

-- O batimento do coletor: quando rodou, quanto demorou e o que falhou.
CREATE TABLE collector_heartbeat (
    id          INT PRIMARY KEY CHECK (id = 1),
    last_at     TIMESTAMPTZ NOT NULL,
    duration_ms INT NOT NULL,
    containers  INT NOT NULL,
    errors      INT NOT NULL DEFAULT 0,
    last_error  TEXT NOT NULL DEFAULT '',
    interval_ms INT NOT NULL
);
