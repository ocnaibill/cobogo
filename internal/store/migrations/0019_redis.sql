-- DEC-50 C: o Redis gerenciado: a engine do serviço e a origem dos logs.
ALTER TABLE project_services DROP CONSTRAINT project_services_engine_check;
ALTER TABLE project_services ADD CONSTRAINT project_services_engine_check CHECK (engine IN ('postgres', 'mysql', 'redis'));
ALTER TABLE runtime_logs DROP CONSTRAINT runtime_logs_origin_check;
ALTER TABLE runtime_logs ADD CONSTRAINT runtime_logs_origin_check CHECK (origin IN ('app', 'postgres', 'mysql', 'redis'));
