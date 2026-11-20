#!/bin/bash
# Cria o administrador do Cobogó (DEC-27) sem expor a senha nem o token de
# configuração. Roda na VM; fala com a API pelo Traefik, com o host e a origem
# do painel (COBOGO_PUBLIC_URL), então o caminho é o mesmo do navegador.
#
#   ssh -t cobogo@192.168.1.27 /opt/cobogo/deploy/create-admin.sh
#   ssh cobogo@192.168.1.27 /opt/cobogo/deploy/create-admin.sh --check   # só confere, não cria nada
#
# O token de uso único é lido do log da API; o login e a senha são pedidos aqui
# (a senha não aparece na tela nem na lista de processos).
set -euo pipefail
cd "$(dirname "$0")/.."

ENVF=deploy/.env
[ -f "$ENVF" ] || { echo "erro: $ENVF não existe" >&2; exit 1; }
PUBLIC_URL="$(grep '^COBOGO_PUBLIC_URL=' "$ENVF" | cut -d= -f2-)"
HOST="${PUBLIC_URL#*://}"; HOST="${HOST%%/*}"
IP="$(docker inspect cobogo-traefik-1 --format '{{(index .NetworkSettings.Networks "cobogo-ingress").IPAddress}}')"
[ -n "$IP" ] || { echo "erro: não achei o Traefik (a pilha está no ar?)" >&2; exit 1; }
BASE="http://$IP:8000"

status="$(curl -s --max-time 10 -H "Host: $HOST" "$BASE/api/v1/setup/status")"
if ! printf '%s' "$status" | grep -q '"needs_setup":true'; then
  echo "O administrador já foi criado (setup/status: $status). Nada a fazer." >&2
  exit 1
fi

token="$(docker compose --env-file "$ENVF" -f deploy/compose.yml logs --no-log-prefix api 2>&1 \
  | grep -A3 'token de configuração' | grep -E '^[[:space:]]+[A-Za-z0-9_-]{16,}[[:space:]]*$' | tail -1 | tr -d '[:space:]')"
[ -n "$token" ] || { echo "erro: não achei o token de configuração no log da API (reinicie a API: docker compose ... restart api)" >&2; exit 1; }

if [ "${1:-}" = "--check" ]; then
  echo "painel: $PUBLIC_URL (host $HOST) via $BASE"
  echo "needs_setup: true"
  echo "token de configuração encontrado (${#token} caracteres); nada foi criado"
  exit 0
fi

read -rp "Login do administrador [admin]: " login
login="${login:-admin}"
read -rsp "Senha (12 a 128 caracteres): " pass; echo
read -rsp "Repita a senha: " pass2; echo
[ "$pass" = "$pass2" ] || { echo "erro: as senhas não conferem" >&2; exit 1; }
[ "${#pass}" -ge 12 ] || { echo "erro: a senha precisa de ao menos 12 caracteres" >&2; exit 1; }

body="$(TOKEN="$token" LOGIN="$login" PASS="$pass" python3 -c 'import json,os; print(json.dumps({"token": os.environ["TOKEN"], "login": os.environ["LOGIN"], "password": os.environ["PASS"]}))')"
resp="$(printf '%s' "$body" | curl -s --max-time 30 -w '\n%{http_code}' -H "Host: $HOST" -H "Origin: $PUBLIC_URL" \
  -H 'Content-Type: application/json' --data-binary @- "$BASE/api/v1/setup")"
code="${resp##*$'\n'}"
msg="${resp%$'\n'*}"
unset pass pass2 body
if [ "$code" = "201" ]; then
  echo "Administrador criado: $(printf '%s' "$msg" | python3 -c 'import json,sys; print(json.load(sys.stdin)["user"]["login"])')"
  echo "Entre em $PUBLIC_URL (a API ainda não tem tela: use POST /api/v1/auth/login)."
else
  echo "A API recusou (HTTP $code): $(printf '%s' "$msg" | python3 -c 'import json,sys; e=json.load(sys.stdin)["error"]; print(e["code"]+": "+e["message"])' 2>/dev/null || echo "$msg" | head -c 200)" >&2
  exit 1
fi
