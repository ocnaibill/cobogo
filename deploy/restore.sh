#!/bin/bash
# Restaura o banco interno do Cobogó a partir de um backup (DEC-47), numa VM limpa
# ou na que já existe, e prova que a restauração serve.
#
#   deploy/restore.sh /caminho/cobogo-core-AAAAMMDD-HHMMSS.dump [--force]
#
# O que faz, na ordem:
#  1. confere a soma SHA-256 do arquivo (se o .sha256 está ao lado);
#  2. se não há deploy/.env, GERA um novo, com todas as senhas e tokens recriados
#     (nada depende deles: o dump não leva senha de role). A única coisa que não se
#     refaz é a chave das variáveis: passe a de antes em COBOGO_SECRET_KEY (e, se
#     havia rotação, COBOGO_SECRET_KEYS e COBOGO_SECRET_KEY_CURRENT). Sem ela, gera
#     uma chave nova e AVISA: as variáveis dos projetos ficam ilegíveis;
#  3. sobe só o PostgreSQL e restaura. Se o banco já tem dados, RECUSA, a menos que
#     você passe --force (aí o conteúdo atual é substituído);
#  4. sobe a pilha (o `migrate` leva um dump mais antigo ao esquema do código; com um
#     dump de esquema MAIS NOVO que o código, a API e o worker recusam subir, com a
#     mensagem "o banco é mais novo que este binário": atualize o código e suba de novo);
#  5. roda `rotate-secrets --verify`: abre todas as variáveis e é a prova de que a chave lê os segredos.
#
# Nunca apaga volumes de aplicações: eles são reconciliados pelos labels do Cobogó.
set -euo pipefail
cd "$(dirname "$0")/.."

DUMP="${1:-}"
FORCE=0
[ "${2:-}" = "--force" ] && FORCE=1
[ -n "$DUMP" ] && [ -f "$DUMP" ] || { echo "uso: deploy/restore.sh <arquivo.dump> [--force]" >&2; exit 2; }

ENVF=deploy/.env
DC=(docker compose --env-file "$ENVF" -f deploy/compose.yml)
[ -n "${COBOGO_COMPOSE_EXTRA:-}" ] && DC+=(-f "$COBOGO_COMPOSE_EXTRA")

echo "==> 1/5 conferindo o arquivo"
if [ -f "$DUMP.sha256" ]; then
  want="$(cut -d' ' -f1 "$DUMP.sha256")"
  got="$( (sha256sum "$DUMP" 2>/dev/null || shasum -a 256 "$DUMP") | cut -d' ' -f1)"
  [ "$want" = "$got" ] || { echo "erro: a soma SHA-256 não confere; o arquivo está corrompido" >&2; exit 1; }
  echo "soma SHA-256 confere"
else
  echo "aviso: sem $DUMP.sha256; não deu para conferir a soma" >&2
fi

# Substitui o valor de uma variável no .env (sem sed: o valor pode ter / + =).
setenv() { awk -v k="$1" -v v="$2" 'BEGIN{FS=OFS="="} $1==k {print k "=" v; found=1; next} {print} END{ if(!found) print k "=" v }' "$ENVF" > "$ENVF.tmp" && mv "$ENVF.tmp" "$ENVF"; }
rand() { openssl rand -hex "${1:-24}"; }

echo "==> 2/5 preparando o .env"
if [ -f "$ENVF" ]; then
  echo "usando o $ENVF que já existe"
else
  cp deploy/.env.example "$ENVF"
  chmod 600 "$ENVF"
  for k in POSTGRES_PASSWORD COBOGO_OWNER_PASSWORD COBOGO_API_PASSWORD COBOGO_WORKER_PASSWORD COBOGO_COLLECTOR_PASSWORD COBOGO_BACKUP_PASSWORD; do setenv "$k" "$(rand 24)"; done
  setenv COBOGO_ROUTER_TOKEN "$(rand 32)"
  if [ -n "${COBOGO_SECRET_KEY:-}" ]; then
    setenv COBOGO_SECRET_KEY "$COBOGO_SECRET_KEY"
    [ -n "${COBOGO_SECRET_KEYS:-}" ] && setenv COBOGO_SECRET_KEYS "$COBOGO_SECRET_KEYS"
    [ -n "${COBOGO_SECRET_KEY_CURRENT:-}" ] && setenv COBOGO_SECRET_KEY_CURRENT "$COBOGO_SECRET_KEY_CURRENT"
    echo "senhas e tokens recriados; a chave das variáveis é a que você passou"
  else
    setenv COBOGO_SECRET_KEY "$(openssl rand -base64 32)"
    echo "AVISO: sem COBOGO_SECRET_KEY. Uma chave NOVA foi gerada: as variáveis dos projetos do backup ficarão ilegíveis e terão de ser digitadas de novo." >&2
  fi
  [ -n "${COBOGO_PUBLIC_URL:-}" ] && setenv COBOGO_PUBLIC_URL "$COBOGO_PUBLIC_URL"
  [ -n "${COBOGO_APPS_DOMAIN:-}" ] && setenv COBOGO_APPS_DOMAIN "$COBOGO_APPS_DOMAIN"
  [ -n "${COBOGO_APPS_SCHEME:-}" ] && setenv COBOGO_APPS_SCHEME "$COBOGO_APPS_SCHEME"
  echo "$ENVF criado. Confira COBOGO_PUBLIC_URL e COBOGO_APPS_DOMAIN, e ponha o token do túnel depois (deploy/set-tunnel-token.sh)."
fi

echo "==> 3/5 restaurando o banco"
"${DC[@]}" up -d postgres
for i in $(seq 1 60); do
  [ "$("${DC[@]}" ps --format '{{.Health}}' postgres 2>/dev/null)" = "healthy" ] && break
  sleep 2
done
has_data="$("${DC[@]}" exec -T postgres psql -U cobogo_owner -d cobogo_core -tAc "SELECT to_regclass('schema_migrations') IS NOT NULL AND (SELECT count(*) FROM schema_migrations) > 0" 2>/dev/null || echo f)"
if [ "$has_data" = "t" ] && [ "$FORCE" != "1" ]; then
  echo "erro: o banco já tem dados. Para SUBSTITUIR o conteúdo atual pelo do backup, rode de novo com --force." >&2
  exit 1
fi
flags=(--no-owner --exit-on-error -U cobogo_owner -d cobogo_core)
[ "$has_data" = "t" ] && flags+=(--clean --if-exists)
"${DC[@]}" exec -T postgres pg_restore "${flags[@]}" < "$DUMP"
echo "banco restaurado"

echo "==> 4/5 subindo a pilha (o migrate leva o esquema ao do código; dump mais novo que o código: a API recusa subir)"
"${DC[@]}" up -d --build
sleep 8
"${DC[@]}" ps --format '{{.Service}} {{.Status}}'

echo "==> 5/5 conferindo se a chave lê os segredos"
if "${DC[@]}" --profile tools run --rm rotate-secrets --verify; then
  echo "OK: a restauração serve; os segredos se leem com a chave desta pilha."
else
  echo "ATENÇÃO: há variáveis que a chave desta pilha não lê (acima). Se você não tinha a chave de antes, os projetos precisam ter essas variáveis digitadas de novo." >&2
  exit 3
fi
