-- DEC-50: o MySQL gerenciado: a origem dos logs e a engine do serviço.
ALTER TABLE runtime_logs DROP CONSTRAINT runtime_logs_origin_check;
ALTER TABLE runtime_logs ADD CONSTRAINT runtime_logs_origin_check CHECK (origin IN ('app', 'postgres', 'mysql'));

-- E o serviço gerenciado aceita a engine nova.
ALTER TABLE project_services DROP CONSTRAINT project_services_engine_check;
ALTER TABLE project_services ADD CONSTRAINT project_services_engine_check CHECK (engine IN ('postgres', 'mysql'));
