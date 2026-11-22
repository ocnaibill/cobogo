-- DEC-44: o vínculo do banco passa a ser um conjunto de variáveis (a URL, ou as
-- partes host/porta/usuário/senha/banco), e o banco gerenciado pode ter TLS.
-- `variable` continua sendo a variável principal (a URL, ou a da senha).
ALTER TABLE project_services ADD COLUMN variables JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE project_services ADD COLUMN tls BOOLEAN NOT NULL DEFAULT false;
-- Os vínculos de antes tinham só a URL.
UPDATE project_services SET variables = jsonb_build_object('url', variable) WHERE variables = '{}'::jsonb;
