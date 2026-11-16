-- DEC-27: administrador, sessões, auditoria e dono dos projetos.

CREATE TABLE users (
    id            UUID PRIMARY KEY,
    login         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Só o hash SHA-256 do token existe aqui: um vazamento do banco não entrega
-- sessões válidas.
CREATE TABLE sessions (
    id           UUID PRIMARY KEY,
    user_id      UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   BYTEA NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at   TIMESTAMPTZ NOT NULL,
    ip           TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_sessions_user ON sessions(user_id);

-- Nunca recebe senha, token nem valor de variável.
CREATE TABLE audit_events (
    id       UUID PRIMARY KEY,
    at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_id UUID REFERENCES users(id) ON DELETE SET NULL,
    action   TEXT NOT NULL,
    target   TEXT NOT NULL DEFAULT '',
    ip       TEXT NOT NULL DEFAULT '',
    outcome  TEXT NOT NULL
);

CREATE INDEX idx_audit_at ON audit_events(at DESC);

-- Projetos anteriores ao primeiro administrador ficam sem dono até ele ser
-- criado, quando os adota.
ALTER TABLE projects ADD COLUMN owner_id UUID REFERENCES users(id);
CREATE INDEX idx_projects_owner ON projects(owner_id);
