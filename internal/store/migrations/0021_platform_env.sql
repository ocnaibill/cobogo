-- DEC-52 B: variáveis que o Cobogó calcula e mantém (o endereço do projeto em
-- ALLOWED_HOSTS, a URL da API em VITE_API_URL). O valor vai cifrado em env_variables,
-- como qualquer outro; aqui fica o que a plataforma gravou (são endereços públicos,
-- nunca segredos) e a regra, para saber quando recalcular sem decifrar nada.
ALTER TABLE env_variables DROP CONSTRAINT env_variables_source_check;
ALTER TABLE env_variables ADD CONSTRAINT env_variables_source_check CHECK (source IN ('user', 'generated', 'imported', 'linked', 'platform'));

CREATE TABLE platform_env (
    project_id UUID NOT NULL REFERENCES projects(id),
    name       TEXT NOT NULL,
    value      TEXT NOT NULL,
    reason     TEXT NOT NULL,
    -- O valor entra no build (variável pública, DEC-37): mudar exige um novo build.
    build_time BOOLEAN NOT NULL DEFAULT false,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, name)
);
