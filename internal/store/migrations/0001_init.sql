-- Esquema mínimo da fatia M1 (§14.2 do mestre, recortado para RF-01 a RF-09).
-- IDs são gerados pela aplicação (google/uuid), não pelo banco.

CREATE TABLE projects (
    id              UUID PRIMARY KEY,
    slug            TEXT NOT NULL UNIQUE,
    repository_url  TEXT NOT NULL,
    root_directory  TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE analyses (
    id              UUID PRIMARY KEY,
    project_id      UUID NOT NULL REFERENCES projects(id),
    commit_sha      TEXT NOT NULL,
    rules_version   TEXT NOT NULL,
    outcome         TEXT NOT NULL,
    result          JSONB NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_analyses_project ON analyses(project_id, created_at DESC);

CREATE TABLE deployments (
    id              UUID PRIMARY KEY,
    project_id      UUID NOT NULL REFERENCES projects(id),
    analysis_id     UUID REFERENCES analyses(id),
    commit_sha      TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    port_override   INT,
    failure_reason  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, idempotency_key)
);

CREATE INDEX idx_deployments_project ON deployments(project_id, created_at DESC);

CREATE TABLE jobs (
    id                      UUID PRIMARY KEY,
    deployment_id           UUID NOT NULL UNIQUE REFERENCES deployments(id),
    state                   TEXT NOT NULL,
    lease_until             TIMESTAMPTZ,
    ownership_generation    INT NOT NULL DEFAULT 0,
    attempt_count           INT NOT NULL DEFAULT 0,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_jobs_claimable ON jobs(state, lease_until);

CREATE TABLE deployment_events (
    id              UUID PRIMARY KEY,
    deployment_id   UUID NOT NULL REFERENCES deployments(id),
    sequence        INT NOT NULL,
    stage           TEXT NOT NULL,
    event_type      TEXT NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload         JSONB NOT NULL DEFAULT '{}'::jsonb,
    UNIQUE (deployment_id, sequence)
);

CREATE INDEX idx_events_deployment ON deployment_events(deployment_id, sequence);

CREATE TABLE releases (
    id                  UUID PRIMARY KEY,
    deployment_id       UUID NOT NULL REFERENCES deployments(id),
    image_id            TEXT NOT NULL,
    container_id        TEXT NOT NULL,
    operational_state   TEXT NOT NULL,
    health              TEXT NOT NULL,
    host_port           INT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_releases_deployment ON releases(deployment_id);
