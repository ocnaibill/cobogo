#!/bin/bash
# Grava o token do túnel da Cloudflare no deploy/.env sem exibi-lo (DEC-32).
#
#   ssh -t cobogo@192.168.1.27 /opt/cobogo/deploy/set-tunnel-token.sh
#
# Cole o token no prompt (não aparece na tela) e tecle Enter. Também aceita o
# token pela entrada padrão (`pbpaste | ssh ... set-tunnel-token.sh`). Se você
# colar o comando inteiro que a Cloudflare mostra (`cloudflared service install
# eyJ...` ou `docker run ... --token eyJ...`), usa só o último campo. Só grava
# se o valor tiver o formato de um token; imprime apenas o tamanho.
set -euo pipefail

ENV_FILE="${ENV_FILE:-$(dirname "$0")/.env}"
[ -f "$ENV_FILE" ] || { echo "erro: $ENV_FILE não existe" >&2; exit 1; }
grep -q '^TUNNEL_TOKEN=' "$ENV_FILE" || { echo "erro: $ENV_FILE não tem a linha TUNNEL_TOKEN=" >&2; exit 1; }

if [ -t 0 ]; then
  printf 'Cole o token do túnel (não aparece) e tecle Enter: ' >&2
  IFS= read -rs raw
  echo >&2
else
  raw="$(cat)"
fi

# Remove quebras de linha e espaços das pontas; se sobrar mais de um campo, fica o último.
raw="$(printf '%s' "$raw" | tr -d '\r\n' | sed -E 's/^[[:space:]]+//; s/[[:space:]]+$//')"
token="${raw##* }"
if [ "$token" != "$raw" ]; then
  echo "aviso: o texto tinha espaços; usando só o último campo" >&2
fi

if [ -z "$token" ]; then
  echo "erro: nada foi colado" >&2; exit 1
fi
if ! printf '%s' "$token" | grep -Eq '^[A-Za-z0-9+/=_-]{60,}$'; then
  echo "erro: o valor não tem o formato de um token da Cloudflare (letras, números, + / = _ -, ao menos 60 caracteres); nada foi gravado" >&2
  exit 1
fi

tmp="$(mktemp "$ENV_FILE.XXXXXX")"
trap 'rm -f "$tmp"' EXIT
# O token vai por ENVIRON, não por -v: sem interpretação de escapes e fora da lista de processos.
T="$token" awk 'BEGIN{FS=OFS="="} /^TUNNEL_TOKEN=/{print "TUNNEL_TOKEN=" ENVIRON["T"]; next} {print}' "$ENV_FILE" > "$tmp"
chmod 600 "$tmp"
mv "$tmp" "$ENV_FILE"
trap - EXIT

got="$(grep '^TUNNEL_TOKEN=' "$ENV_FILE" | cut -d= -f2- | tr -d '\n' | wc -c | tr -d ' ')"
echo "token gravado em $ENV_FILE (${got} caracteres)"
