-- DEC-24 (§14.2 do mestre): variáveis de ambiente por projeto. O valor existe
-- só cifrado (AES-256-GCM, chave fora do banco); metadados em claro.

CREATE TABLE env_variables (
    id          UUID PRIMARY KEY,
    project_id  UUID NOT NULL REFERENCES projects(id),
    name        TEXT NOT NULL,
    version     INT NOT NULL,
    source      TEXT NOT NULL CHECK (source IN ('user', 'generated')),
    ciphertext  BYTEA NOT NULL,
    key_version INT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (project_id, name)
);
