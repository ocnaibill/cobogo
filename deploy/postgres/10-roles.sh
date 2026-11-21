#!/bin/sh
# Cria (ou atualiza) os roles do Cobogó no PostgreSQL (DEC-33 item 8).
# Roda sozinho na primeira inicialização do contêiner (docker-entrypoint-initdb.d)
# e pode ser rodado de novo em um volume existente:
#   docker compose --env-file deploy/.env -f deploy/compose.yml exec postgres sh /docker-entrypoint-initdb.d/10-roles.sh
# É idempotente. Quem o executa é o superusuário do contêiner; a aplicação nunca
# conecta com ele.
set -eu

: "${COBOGO_OWNER_PASSWORD:?defina COBOGO_OWNER_PASSWORD}"
: "${COBOGO_API_PASSWORD:?defina COBOGO_API_PASSWORD}"
: "${COBOGO_WORKER_PASSWORD:?defina COBOGO_WORKER_PASSWORD}"
: "${COBOGO_COLLECTOR_PASSWORD:?defina COBOGO_COLLECTOR_PASSWORD}"

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" \
  -v owner_pw="$COBOGO_OWNER_PASSWORD" -v api_pw="$COBOGO_API_PASSWORD" \
  -v worker_pw="$COBOGO_WORKER_PASSWORD" -v collector_pw="$COBOGO_COLLECTOR_PASSWORD" -v db="$POSTGRES_DB" <<'SQL'
-- Cria os roles que faltam (sem senha ainda).
SELECT format('CREATE ROLE %I LOGIN', r)
FROM unnest(ARRAY['cobogo_owner', 'cobogo_api', 'cobogo_worker', 'cobogo_collector']) AS r
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) \gexec

ALTER ROLE cobogo_owner  PASSWORD :'owner_pw';
ALTER ROLE cobogo_api    PASSWORD :'api_pw';
ALTER ROLE cobogo_worker PASSWORD :'worker_pw';
ALTER ROLE cobogo_collector PASSWORD :'collector_pw';

-- O dono do banco e do esquema é quem roda as migrations; ninguém mais cria nada.
ALTER DATABASE :"db" OWNER TO cobogo_owner;
ALTER SCHEMA public OWNER TO cobogo_owner;
REVOKE ALL ON DATABASE :"db" FROM PUBLIC;
GRANT CONNECT ON DATABASE :"db" TO cobogo_owner, cobogo_api, cobogo_worker, cobogo_collector;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;

-- Num banco que já tinha tabelas (criadas por outro usuário), passa-as ao dono.
SELECT format('ALTER TABLE %I.%I OWNER TO cobogo_owner', schemaname, tablename)
FROM pg_tables WHERE schemaname = 'public' AND tableowner <> 'cobogo_owner' \gexec
SQL
