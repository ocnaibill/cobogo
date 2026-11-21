-- DEC-40: parar, iniciar e reiniciar a aplicação. A release ativa continua
-- "active" (a faxina e a aposentadoria a protegem como antes); `stopped_at`
-- diz que o contêiner dela foi parado de propósito. A rota do projeto parado
-- aponta para a página de inativo.
ALTER TABLE releases ADD COLUMN stopped_at TIMESTAMPTZ;

ALTER TABLE operations DROP CONSTRAINT operations_kind_check;
ALTER TABLE operations ADD CONSTRAINT operations_kind_check
    CHECK (kind IN ('retire_releases', 'delete_project', 'apply_limits', 'sweep', 'stop_app', 'start_app', 'restart_app'));
