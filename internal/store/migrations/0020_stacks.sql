-- DEC-52 A: o projeto é o repositório. A tabela `stacks` é o projeto que o painel mostra
-- (um repositório de um dono); a tabela `projects` continua sendo a unidade de deploy,
-- agora chamada de aplicação no painel, com a pasta e o papel dentro do projeto.
CREATE TABLE stacks (
    id             UUID PRIMARY KEY,
    owner_id       UUID REFERENCES users(id),
    slug           TEXT NOT NULL,
    repository_url TEXT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at     TIMESTAMPTZ,
    -- A leitura do repositório antes de criar as aplicações: o que o Cobogó propõe.
    scan_state     TEXT NOT NULL DEFAULT 'none' CHECK (scan_state IN ('none', 'scanning', 'done', 'failed')),
    scan           JSONB,
    scan_error     TEXT NOT NULL DEFAULT '',
    scanned_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX idx_stacks_slug_live ON stacks(slug) WHERE deleted_at IS NULL;
CREATE INDEX idx_stacks_owner ON stacks(owner_id);

ALTER TABLE projects ADD COLUMN stack_id UUID REFERENCES stacks(id);
ALTER TABLE projects ADD COLUMN role TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_projects_stack ON projects(stack_id);

-- Cada repositório de um dono vira um projeto, e as aplicações do mesmo repositório
-- (o `fec` e o `fec-frontend`) entram nele. O nome do projeto é o slug mais curto.
WITH groups AS (
    SELECT owner_id, repository_url, gen_random_uuid() AS id,
           (array_agg(slug ORDER BY length(slug), created_at))[1] AS slug,
           min(created_at) AS created_at
    FROM projects
    WHERE deleted_at IS NULL
    GROUP BY owner_id, repository_url
), created AS (
    INSERT INTO stacks (id, owner_id, slug, repository_url, created_at)
    SELECT id, owner_id, slug, repository_url, created_at FROM groups
    RETURNING id, owner_id, repository_url
)
UPDATE projects p SET stack_id = c.id
FROM created c
WHERE p.deleted_at IS NULL AND p.repository_url = c.repository_url AND p.owner_id IS NOT DISTINCT FROM c.owner_id;

-- O papel de cada aplicação, da última análise (a peça da própria pasta no mapa da DEC-49).
UPDATE projects p SET role = sub.role
FROM (
    SELECT DISTINCT ON (a.project_id) a.project_id, c->>'role' AS role
    FROM analyses a, jsonb_array_elements(COALESCE(a.result->'components', '[]'::jsonb)) c
    WHERE c->>'dir' = '.' AND COALESCE((c->>'sibling')::boolean, false) = false
      AND c->>'role' IN ('frontend', 'backend', 'fullstack', 'app')
    ORDER BY a.project_id, a.created_at DESC
) sub
WHERE p.id = sub.project_id;

-- A leitura do repositório roda no worker, como as demais operações.
ALTER TABLE operations DROP CONSTRAINT operations_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_kind_check
    CHECK (kind IN ('retire_releases', 'delete_project', 'apply_limits', 'sweep', 'stop_app', 'start_app', 'restart_app', 'delete_volume', 'delete_service', 'scan_stack'));
