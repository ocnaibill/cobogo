# Instalação do Cobogó na VM

Roteiro para colocar o Cobogó (D0) numa VM Linux do homelab, acessível pelo túnel da Cloudflare, sem abrir nenhuma porta de entrada. As decisões por trás estão nas [DEC-31](../docs/decisoes/DEC-31-roteamento-por-hostname-redes-e-url-da-release.md), [DEC-32](../docs/decisoes/DEC-32-implantacao-da-d0-em-conteineres.md) e [DEC-33](../docs/decisoes/DEC-33-roles-do-postgresql-e-migrations-em-passo-proprio.md).

```
Internet ── Cloudflare ── túnel ── cloudflared ── Traefik ─┬─ API (painel, rede cobogo-routing)
                                                           └─ aplicações (rede cobogo-ingress)
                          API ── PostgreSQL        worker ── proxy do Docker ── socket
```

Nada é publicado no host. Só o proxy do Docker monta o socket; a API e o Traefik não têm acesso ao Docker.

## 1. Pré-requisitos

- **VM Linux** com Docker Engine e o plugin do Compose v2 (`docker compose version`). O desenvolvimento foi testado com Docker 29. A VM do projeto tem 16 GiB de RAM; 8 GiB é o mínimo razoável para o Cobogó mais algumas aplicações (os limites padrão são 1 GiB por aplicação e 4 GiB por build, DEC-30).
- **Saída para a internet**: o worker clona repositórios (GitHub, GitLab, etc.) e o Docker baixa imagens do Docker Hub. Nenhuma porta de **entrada** é necessária.
- **Relógio sincronizado** (`timedatectl status` com `System clock synchronized: yes`): sessões e cookies dependem dele.
- `git`.
- Uma conta Cloudflare com as zonas `ocnaibill.dev` (o painel) e `cobogo.dev` (as aplicações).

## 2. Cloudflare

Faça isto antes de subir a pilha: o `cloudflared` precisa do token.

1. **Zona `cobogo.dev`** ativa na sua conta (nameservers apontados para a Cloudflare).
2. Em **Zero Trust → Networks → Tunnels → Create a tunnel** (tipo *Cloudflared*), dê o nome `cobogo`. **Guarde o token** (`TUNNEL_TOKEN`): ele dá acesso ao túnel, nunca o versione.
3. Em **Public hostnames** do túnel, adicione dois, ambos com serviço `HTTP` e URL `traefik:8000`:

   | Hostname público | Serviço |
   | --- | --- |
   | `cobogo.ocnaibill.dev` | `http://traefik:8000` |
   | `*.cobogo.dev` | `http://traefik:8000` |

4. **Confira os registros DNS.** O painel da Cloudflare cria, por conta própria, um `CNAME` proxied para `<UUID>.cfargotunnel.com` para cada hostname publicado. Em **DNS → Records** de cada zona, veja se existem `cobogo` (em `ocnaibill.dev`) e `*` (em `cobogo.dev`). Se o curinga `*` não apareceu, crie-o à mão: `CNAME`, nome `*`, destino `<UUID>.cfargotunnel.com`, **Proxied**. (A documentação não deixa claro se o curinga é criado sozinho; confira.)
5. **Certificado.** `cobogo.ocnaibill.dev` e `*.cobogo.dev` têm **um** nível de subdomínio, então o certificado gratuito da Cloudflare cobre os dois. Um hostname de dois níveis (por exemplo `app.painel.cobogo.dev`) **não** seria coberto sem o Advanced Certificate Manager; por isso o Cobogó gera `<slug>.cobogo.dev` e o painel não fica sob `cobogo.dev`.

## 3. A VM

```bash
# Docker e o plugin do Compose: siga https://docs.docker.com/engine/install/ para a sua distribuição.
sudo mkdir -p /opt/cobogo && sudo chown "$USER" /opt/cobogo
git clone https://github.com/ocnaibill/cobogo.git /opt/cobogo
cd /opt/cobogo
```

Quem está no grupo `docker` equivale a root na VM. Use um usuário só para operar o Cobogó.

## 4. Configuração

```bash
cp deploy/.env.example deploy/.env
chmod 600 deploy/.env
```

Preencha o `deploy/.env` (cada linha tem o comando que gera o valor):

| Variável | Valor |
| --- | --- |
| `POSTGRES_PASSWORD`, `COBOGO_OWNER_PASSWORD`, `COBOGO_API_PASSWORD`, `COBOGO_WORKER_PASSWORD` | `openssl rand -hex 24` (uma diferente para cada) |
| `COBOGO_SECRET_KEY` | `openssl rand -base64 32` |
| `COBOGO_ROUTER_TOKEN` | `openssl rand -hex 32` |
| `COBOGO_PUBLIC_URL` | `https://cobogo.ocnaibill.dev` |
| `COBOGO_APPS_DOMAIN` / `COBOGO_APPS_SCHEME` | `cobogo.dev` / `https` |
| `TUNNEL_TOKEN` | o token do passo 2 |

**Guarde uma cópia do `COBOGO_SECRET_KEY` fora da VM** (gerenciador de senhas). Sem ela, os valores cifrados das variáveis dos projetos não voltam, mesmo com o banco restaurado.

## 5. Subir

```bash
docker compose --env-file deploy/.env --profile tunnel -f deploy/compose.yml up -d --build
docker compose --env-file deploy/.env --profile tunnel -f deploy/compose.yml ps
```

O primeiro `up` constrói as imagens (alguns minutos), cria os roles do banco e roda o serviço `migrate`, que sai com código 0 e fica como `Exited (0)`; a API e o worker só sobem depois dele. Saudável quando `api` mostra `(healthy)` e `cloudflared`, `traefik`, `worker` e `docker-proxy` estão `Up`.

## 6. Primeiro acesso

Sem administrador cadastrado, a API imprime um token de configuração, uma vez, no log:

```bash
docker compose --env-file deploy/.env -f deploy/compose.yml logs --no-log-prefix api | grep -A3 'token de configuração'
```

Ainda não há frontend: use a API (o guia é a seção "Como testar (fluxo de deploy)" do [README](../README.md)). O cabeçalho `Origin` precisa ser a URL pública:

```bash
export API=https://cobogo.ocnaibill.dev
H=(-H "Origin: $API" -H 'content-type: application/json' -b /tmp/cobogo.cookies -c /tmp/cobogo.cookies)
curl "${H[@]}" -X POST $API/api/v1/setup -d '{"token": "<token do log>", "login": "admin", "password": "<uma senha longa>"}'
```

Reinicie a API (`docker compose ... restart api`) se perder o token: um novo é impresso enquanto não houver administrador.

## 7. Verificação

```bash
curl -s https://cobogo.ocnaibill.dev/healthz                       # {"status":"ok"}
curl -s https://cobogo.ocnaibill.dev/api/v1/setup/status           # {"needs_setup":false} depois do passo 6
ss -tlnp | grep -vE 'sshd|systemd|127\.0\.0\.(53|54)'               # nada do Cobogó escutando no host
docker ps --format '{{.Names}} {{.Ports}}' | grep -- '->'          # nenhuma linha: nada do Cobogó está publicado no host
```

Crie um projeto e faça um deploy (README, passos 1 a 5). Ao concluir, a `release` traz a `url` (`https://<slug>.cobogo.dev/`) e ela responde pelo túnel.

## 8. Operação

- **Logs:** `docker compose --env-file deploy/.env -f deploy/compose.yml logs -f api worker traefik`.
- **Releases antigas:** depois de uma release nova entrar no ar, a anterior sai sozinha (para, é removida e as imagens além das 2 mais recentes são podadas) depois do período de espera, que é de 30 s e se edita pela API de configurações (`PUT /api/v1/settings/retire_grace_seconds` e `release_images_kept`; o painel ainda não tem tela para elas). O worker também varre, a cada 5 minutos, os contêineres do Cobogó que sobraram (DEC-38); ele só toca em contêiner com os labels do Cobogó e nunca na release ativa. O que ele fez aparece como operação "Faxina" no banco (`operations`).
- **Parar e reiniciar uma aplicação** pelo painel (DEC-40): parar mantém o contêiner e o endereço mostra a página de projeto inativo. Depois de reiniciar a VM as aplicações não sobem sozinhas e o painel continua dizendo "no ar": use **Reiniciar** no projeto (sobe o contêiner mantido) ou faça um novo deploy.
- **Coletor de métricas** (DEC-43): o serviço `collector` e o `docker-proxy-ro` (proxy do Docker só de leitura) sobem com a pilha. Ele usa o role `cobogo_collector` do banco, com a senha `COBOGO_COLLECTOR_PASSWORD` do `.env` (gere com `openssl rand -hex 24`). **Numa VM que já existia**, o script de roles não roda de novo sozinho: ponha a senha no `.env` e rode uma vez `docker compose --env-file deploy/.env -f deploy/compose.yml up -d postgres && docker compose --env-file deploy/.env -f deploy/compose.yml exec postgres sh /docker-entrypoint-initdb.d/10-roles.sh` (é idempotente), e então `up -d --build`; o `migrate` aplica os privilégios. Custo medido com 9 contêineres: o coletor usa ~0,4% de CPU e ~20 MiB, e o banco ganha cerca de 75 MiB por dia de amostras brutas (retenção de 24 h; `COBOGO_COLLECTOR_INTERVAL` e `COBOGO_COLLECTOR_RAW_RETENTION` ajustam). Se o painel diz "sem coleta", veja `logs collector`.
- **Dados persistentes** (DEC-41): o proxy do Docker agora libera `VOLUMES` e `SYSTEM` (só `/system/df`, para medir o tamanho). Depois de atualizar, recrie o proxy (`up -d` faz isso, pois o `compose.yml` mudou). Os volumes ficam no disco da VM: **sem backup (#28) a perda do disco perde os dados**, e eles entram no backup do banco só se você fizer o dump deles à parte. `docker volume ls | grep cobogo-vol` lista os volumes do Cobogó.
- **TLS e variáveis do banco gerenciado** (DEC-44): sem mudança de configuração; o `migrate` aplica a migração 0013 e o proxy atual já deixa passar a cópia de arquivos que injeta a CA na aplicação (`POST=1`, medido). Os bancos gerenciados que já existem continuam sem TLS e com os dados na raiz do volume; só os criados depois têm TLS.
- **Comandos pontuais** (DEC-45): sem mudança de configuração. O `migrate` aplica a migração 0014 (tabela `project_commands` e a janela de senha nas sessões) e o proxy do Docker **continua com `EXEC=0`**: o comando é um contêiner novo, não um `exec`. O worker tem um laço próprio para os comandos (até três ao mesmo tempo na máquina, um por projeto) e, ao subir, marca como interrompidos os que ficaram rodando e remove os contêineres `cobogo-cmd-*` sobrados.
- **Logs de execução** (DEC-46): sem mudança de configuração. O `migrate` aplica a migração 0015 (tabela `runtime_logs`) e o privilégio de sequência do worker; o proxy do Docker já libera a leitura de logs (`CONTAINERS=1`). Na primeira subida o worker captura as últimas 500 linhas de cada contêiner de projeto e segue dali. O custo é no banco do Cobogó: no máximo 20 000 linhas por projeto, 24 horas. O `docker logs` na VM continua valendo e guarda o que o limite de taxa descartou.
- **Excluir um projeto** pelo painel tira o endereço do ar na hora, apaga as variáveis cifradas e remove contêineres, rede e imagens do projeto; **o histórico de deploys fica** e volumes nunca são tocados. Apagar o histórico de vez é a issue #36.
- **Atualizar:** `git pull` e repita o `up -d --build` do passo 5. O `migrate` roda sozinho antes da API e do worker; se faltar uma migration, eles recusam subir com uma mensagem clara.
- **Reiniciar a VM:** a pilha do Cobogó volta sozinha (`restart: unless-stopped`), mas **as aplicações publicadas não**: elas não têm política de reinício (DEC-29/DEC-32), então as rotas apontam para contêineres parados até um novo deploy de cada projeto. Evite reiniciar a VM perto de uma demonstração.
- **Parar:** `docker compose ... down` (mantém o banco). Com `-v` apaga o volume do PostgreSQL.
- **Backup do banco interno (DEC-47).** O serviço `backup` faz, todo dia às 03:00 (`COBOGO_BACKUP_AT`, no fuso `COBOGO_BACKUP_TZ`, padrão UTC), um dump consistente com um role só de leitura (`cobogo_backup`), confere que ele abre e o guarda no volume `backups` com a soma SHA-256. A retenção padrão é **7 dias e 4 semanas** (e os 10 backups com rótulo mais novos), e se muda no painel (página "Backups") ou em `PUT /settings/backup_keep_daily` e `backup_keep_weekly`. Ao subir sem um backup recente ele faz um na hora. **Um backup agora:** `docker compose --env-file deploy/.env -f deploy/compose.yml run --rm backup once meu-rotulo`. A página "Backups" do painel mostra o estado, **acende um aviso se o último backup bom tem mais de 36 horas**, e baixa os arquivos (pede a senha de novo). **Numa VM que já existia:** ponha `COBOGO_BACKUP_PASSWORD=$(openssl rand -hex 24)` no `.env`, crie o role com `set -a; . deploy/.env; set +a; docker compose --env-file deploy/.env -f deploy/compose.yml exec -T -e COBOGO_BACKUP_PASSWORD postgres sh -s < deploy/postgres/10-roles.sh` (idempotente; **mande o script pela entrada padrão**: depois de um `git pull` o arquivo montado no contêiner é o antigo, e o `sh /docker-entrypoint-initdb.d/10-roles.sh` rodaria a versão velha sem criar o role) e rode `up -d --build`.
- **O que o backup protege e o que não.** As cópias ficam **na mesma VM**: protegem de erro, **não da perda do disco**. A cópia que vale é a que você **baixa pelo painel (ou copia com `scp`/`rsync` de `docker volume inspect cobogo_backups`) e guarda fora da VM**. O dump leva as variáveis dos projetos **cifradas**: a `COBOGO_SECRET_KEY` (e `COBOGO_SECRET_KEYS`/`COBOGO_SECRET_KEY_CURRENT`, se há rotação) **não vai no backup** e se guarda **à parte, em outro lugar**, nunca junto do dump. É a única coisa do `.env` que não se refaz: as senhas do banco e o token das rotas se recriam sem perder dado, e o token do túnel se pega de novo na Cloudflare. Sem a chave de antes, a restauração funciona, mas as variáveis dos projetos ficam ilegíveis e precisam ser digitadas de novo. Falta de backup recente não é silenciosa: o painel avisa.
- **Atualizar com backup:** `deploy/update.sh` (na VM, ou `ssh cobogo@192.168.1.27 /opt/cobogo/deploy/update.sh`) faz um backup `pre-<commit>`, e **só se ele der certo** faz o `git pull` e o `up -d --build`. É o que substitui o `pg_dump` manual.
- **Restaurar** (numa VM limpa ou na que existe): `deploy/restore.sh /caminho/cobogo-core-AAAAMMDD-HHMMSS.dump` (com `COBOGO_SECRET_KEY=<a chave de antes>` no ambiente, e `COBOGO_PUBLIC_URL`/`COBOGO_APPS_DOMAIN` se não há `.env`). Ele confere a soma, **gera um `.env` novo com todas as senhas recriadas** (se não existe), sobe o PostgreSQL, restaura, sobe a pilha e roda `rotate-secrets --verify`, que **abre todas as variáveis** e sai com código 3 se alguma não abrir. Com o banco já com dados, ele recusa; `--force` substitui o conteúdo atual. Um dump de esquema mais novo que o código faz a API e o worker recusarem subir ("o banco é mais novo que este binário"): atualize o código. **Nenhum volume de aplicação é apagado.** Para levar um backup a uma VM nova, copie o arquivo para ela (o painel não recebe upload: o túnel da Cloudflare limita o envio a 100 MB).
- **Trocar uma senha do banco:** edite o `.env` e rode `docker compose ... exec postgres sh /docker-entrypoint-initdb.d/10-roles.sh` (idempotente), depois `up -d` para recriar os contêineres com a senha nova.
- **Trocar a chave das variáveis (`COBOGO_SECRET_KEY`)** (DEC-39). A rotação **recifra** os valores com uma chave nova e **não muda os valores**: nenhum projeto precisa de novo deploy. (Trocar a senha de um serviço externo é outra coisa: é digitar o valor novo.) Em ordem, e só passe ao próximo se o anterior deu certo:
  1. Gere a chave nova (`openssl rand -base64 32`), guarde uma cópia fora da VM e ponha no `.env`: `COBOGO_SECRET_KEYS=1:<a chave de hoje>,2:<a nova>` e `COBOGO_SECRET_KEY_CURRENT=2`. Se `COBOGO_SECRET_KEY` continuar preenchida, ela é a versão 1 e precisa ser igual à da lista.
  2. `docker compose --env-file deploy/.env -f deploy/compose.yml --profile tools run --rm rotate-secrets --check` mostra quantas variáveis há por versão de chave, sem alterar nada (e `--verify` **abre todas** com a chave carregada, também sem alterar nada).
  3. `docker compose ... --profile tools run --rm rotate-secrets` recifra tudo numa transação: ou todas as variáveis passam para a chave nova, ou nenhuma (se alguma não abrir, o erro diz qual, nunca o valor). Pode rodar antes ou depois de `up -d`, e rodar de novo não faz mal.
  4. `up -d` para a API e o worker passarem a gravar com a chave nova.
  5. Quando o `--check` disser que nenhuma variável usa chave antiga, tire a antiga: `COBOGO_SECRET_KEYS=2:<a nova>`, `COBOGO_SECRET_KEY_CURRENT=2`, esvazie `COBOGO_SECRET_KEY` e rode `up -d`. **Faça um backup do banco antes do passo 3** e guarde as duas chaves até confirmar um deploy depois da troca.
- **Trocar o token das rotas:** edite `COBOGO_ROUTER_TOKEN` e rode `up -d` (recria a API e o Traefik).
- **Esquecer a senha do administrador:** `docker compose --env-file deploy/.env -f deploy/compose.yml run --rm --no-deps api admin reset-password --login admin` (pede a nova senha no terminal; encerra as sessões do administrador). Rode em contêiner próprio (`run`), não com `exec`: o `exec` divide a memória do contêiner da API, e o argon2id usa 64 MiB por verificação.

## 9. Segurança

- Não abra portas de entrada na VM nem no roteador: o acesso é só pelo túnel. Um firewall que bloqueia tudo de entrada exceto SSH (restrito à sua rede) não afeta o Cobogó.
- `deploy/.env` com permissão `600`, fora do Git (o `.gitignore` já o cobre). Não cole tokens em issues, logs nem mensagens.
- O proxy do Docker é o ponto mais sensível: quem controla o worker controla o daemon (DEC-19). O desenho garante que a API, o Traefik e as aplicações não chegam a ele.
- Aplicações de terceiros rodam na mesma VM: o nível de confiança da D0 é o do operador e do corpus escolhido (DEC-20), não o de usuários desconhecidos.

## 10. Quando algo falha

| Sintoma | Causa provável | O que fazer |
| --- | --- | --- |
| `cobogo.ocnaibill.dev` responde erro 1033 ou `Bad gateway` da Cloudflare | `cloudflared` fora do ar ou token errado | `logs cloudflared`; recrie o token no painel e atualize o `.env` |
| Certificado inválido em `app.algo.cobogo.dev` | Hostname com dois níveis | O Cobogó só gera `<slug>.cobogo.dev`; confira `COBOGO_APPS_DOMAIN` |
| `*.cobogo.dev` não resolve | Faltou o registro `*` na zona `cobogo.dev` | Passo 2.4 |
| Painel responde `404` do Traefik | O Traefik não leu a tabela de rotas | `logs traefik`: procure `cannot fetch configuration`; confira `COBOGO_ROUTER_TOKEN` igual nos dois e que a API está `healthy` |
| `migrate` termina com erro `tabelas fora da matriz` | Uma migration nova sem decisão de privilégio | É um erro de versão do código: avise quem mantém o Cobogó (DEC-33 item 4) |
| API ou worker reiniciando com `banco atrasado` | O `migrate` não rodou | `up -d` de novo; veja `logs migrate` |
| Deploy falha em `STARTING` com erro de rede | O worker não alcança o proxy do Docker | `logs docker-proxy`; confira que o worker está na rede `cobogo-control` |
| Aplicação some depois de reiniciar a VM | Aplicações não reiniciam sozinhas | Novo deploy do projeto |

## Ambiente atual do projeto (homelab)

- **Proxmox** `192.168.1.30`, VM **107 `cobogo`**: Debian 13, 4 vCPUs, 6 GiB de RAM, 40 GiB, IP fixo `192.168.1.27`, usuário `cobogo` (SSH só por chave, `sudo` sem senha), `ufw` negando toda entrada exceto SSH da LAN. Docker Engine 29 e Compose pelo repositório oficial da Docker; o código em `/opt/cobogo`.
- Criada com a imagem `debian-13-genericcloud-amd64.qcow2` e cloud-init (usuário, chave e IP). A pilha sobe sem o perfil `tunnel` até o token da Cloudflare estar no `deploy/.env`.
- **Colocar o token do túnel sem expô-lo.** O script `deploy/set-tunnel-token.sh` pede o token em um prompt que não ecoa, confere o formato (recusa texto que não pareça um token e, se você colar o comando inteiro da Cloudflare, usa só o último campo) e grava no `deploy/.env`, imprimindo apenas o tamanho:

```bash
ssh -t cobogo@192.168.1.27 /opt/cobogo/deploy/set-tunnel-token.sh
ssh cobogo@192.168.1.27 'cd /opt/cobogo && docker compose --env-file deploy/.env --profile tunnel -f deploy/compose.yml up -d cloudflared'
```

  Não escreva o token dentro de um comando (ele ficaria no histórico do shell e na lista de processos).

## Limites desta versão

Não há frontend (a operação é pela API), backup automático nem coleta de métricas; a atualização de uma aplicação mantém a release anterior no ar e sem rota até a parte 5c a retirar; reiniciar a VM não religa as aplicações. Está tudo registrado nas DEC-29 a DEC-33.
