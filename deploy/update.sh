#!/bin/bash
# Atualiza o Cobogó na VM com um backup antes (DEC-47): se o backup falhar, não
# atualiza. Roda no diretório do repositório:
#   ssh cobogo@192.168.1.27 /opt/cobogo/deploy/update.sh
#
# O backup tem o rótulo pre-<commit atual> e entra na retenção como um backup com
# rótulo (os 10 mais novos ficam). A chave das variáveis NÃO entra no backup.
set -euo pipefail
cd "$(dirname "$0")/.."

ENVF=deploy/.env
[ -f "$ENVF" ] || { echo "erro: $ENVF não existe" >&2; exit 1; }
DC=(docker compose --env-file "$ENVF" -f deploy/compose.yml)
[ -n "${COBOGO_COMPOSE_EXTRA:-}" ] && DC+=(-f "$COBOGO_COMPOSE_EXTRA")

before="$(git rev-parse --short HEAD)"
echo "==> backup antes de atualizar (pre-$before)"
if ! "${DC[@]}" run --rm backup once "pre-$before"; then
  echo "erro: o backup falhou; nada foi atualizado. Veja: ${DC[*]} logs backup" >&2
  exit 1
fi

echo "==> atualizando o código"
git pull --ff-only
after="$(git rev-parse --short HEAD)"
if [ "$before" = "$after" ]; then
  echo "o código já estava em $after"
else
  echo "$before -> $after"
fi

echo "==> subindo a pilha"
"${DC[@]}" up -d --build
sleep 8
"${DC[@]}" ps --format '{{.Service}} {{.Status}}'
