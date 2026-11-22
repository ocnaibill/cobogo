-- DEC-45: comandos pontuais no ambiente do projeto. Cada execução é uma linha:
-- a API a cria (fila), o worker a executa num contêiner descartável e grava o
-- desfecho. A linha fica como o registro do que foi rodado (quem, o quê, como terminou).

CREATE TABLE project_commands (
    id               UUID PRIMARY KEY,
    project_id       UUID NOT NULL REFERENCES projects(id),
    requested_by     UUID REFERENCES users(id),
    -- O texto do comando, com os valores das variáveis do projeto já trocados por
    -- [REDIGIDO] (a API conhece só os nomes, então a troca é do worker, ao concluir;
    -- até lá o texto é o que a pessoa digitou).
    command          TEXT NOT NULL CHECK (length(command) BETWEEN 1 AND 2000),
    -- 'script' (um botão dos scripts do package.json) ou 'custom' (campo livre).
    source           TEXT NOT NULL CHECK (source IN ('script', 'custom')),
    pause_app        BOOLEAN NOT NULL DEFAULT false,
    state            TEXT NOT NULL DEFAULT 'queued'
                     CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'timed_out', 'canceled', 'interrupted')),
    cancel_requested BOOLEAN NOT NULL DEFAULT false,
    timeout_seconds  INT NOT NULL DEFAULT 300 CHECK (timeout_seconds BETWEEN 5 AND 600),
    exit_code        INT,
    output           TEXT NOT NULL DEFAULT '',
    output_truncated BOOLEAN NOT NULL DEFAULT false,
    error            TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at       TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ
);

-- Um comando por vez por projeto (DEC-45 item 3): garantido pelo banco.
CREATE UNIQUE INDEX project_commands_one_active ON project_commands (project_id) WHERE state IN ('queued', 'running');
CREATE INDEX project_commands_by_project ON project_commands (project_id, created_at DESC);

-- A janela de reautenticação vale para a sessão (DEC-45 item 4).
ALTER TABLE sessions ADD COLUMN reauth_until TIMESTAMPTZ;
