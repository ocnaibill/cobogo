-- DEC-30: política operacional editável pelo painel e limites por projeto.

-- Só chaves da lista de internal/settings; o valor é texto validado pela API.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by UUID REFERENCES users(id) ON DELETE SET NULL
);

-- Sobreposições de recursos do projeto; campo ausente herda da configuração
-- global.
ALTER TABLE projects ADD COLUMN limits JSONB NOT NULL DEFAULT '{}'::jsonb;
