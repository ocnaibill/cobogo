-- DEC-52 C: valores só desta execução. Um comando pode levar variáveis que só valem
-- para ele (a senha do administrador): vão cifradas, o worker as entrega ao contêiner
-- descartável e as apaga ao concluir. A API grava o texto cifrado e nunca o lê.
ALTER TABLE project_commands ADD COLUMN input_names TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE project_commands ADD COLUMN inputs BYTEA;
ALTER TABLE project_commands ADD COLUMN inputs_key_version INT;
-- A receita de onde o comando veio (catálogo por evidência), ou vazio.
ALTER TABLE project_commands ADD COLUMN recipe TEXT NOT NULL DEFAULT '';
ALTER TABLE project_commands DROP CONSTRAINT project_commands_source_check;
ALTER TABLE project_commands ADD CONSTRAINT project_commands_source_check CHECK (source IN ('script', 'custom', 'recipe'));
