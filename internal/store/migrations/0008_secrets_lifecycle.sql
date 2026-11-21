-- DEC-39: variáveis obrigatórias por projeto e auditoria com detalhe
-- (versão e origem da variável, nunca o valor).

ALTER TABLE projects ADD COLUMN required_env TEXT[] NOT NULL DEFAULT '{}';

ALTER TABLE audit_events ADD COLUMN detail JSONB NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX idx_audit_target ON audit_events(target, at DESC);

-- Variáveis importadas de um .env colado têm origem própria.
ALTER TABLE env_variables DROP CONSTRAINT env_variables_source_check;
ALTER TABLE env_variables ADD CONSTRAINT env_variables_source_check CHECK (source IN ('user', 'generated', 'imported'));
