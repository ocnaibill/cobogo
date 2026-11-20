# DEC-32 — Implantação da D0 em contêineres

> **Estado:** decidida com o autor em 20/11/2026; **implementada** (itens 1 a 8). Parte 5d1.
>
> **Escopo:** como o Cobogó roda na VM do homelab: imagens, serviços, redes, acesso ao Docker, sonda de readiness, rota do painel e túnel. Os roles do PostgreSQL (5d2) e o roteiro de instalação na VM (5d3) vêm depois.
>
> **Relaciona-se com:** DEC-19 (topologia e proxies), DEC-21 (PostgreSQL), DEC-26 (sonda), DEC-27 (proxies confiáveis), DEC-31 (roteamento), RNF-03, §17.2 do mestre.

## Contexto

Até aqui a API e o worker rodam como processos no host de desenvolvimento, falando direto com o socket do Docker e publicando as aplicações em portas do loopback. Para a D0 o Cobogó precisa rodar na VM do homelab, acessível pelo túnel da Cloudflare, sem que a API pública nem as aplicações toquem o Docker (RNF-03).

## Decisão

**Serviços** (um `compose` em `deploy/`): PostgreSQL, proxy do Docker, API, worker, Traefik e `cloudflared`. O frontend entra depois, como um servidor estático atrás do Traefik. As imagens do Cobogó são construídas na própria VM (`docker compose build`), sem registry.

**Redes** (quatro, com nomes fixos, uma a mais que a DEC-19):

| Rede | Participantes | Regra |
| --- | --- | --- |
| `cobogo-control` | PostgreSQL, API, worker, proxy do Docker | Nenhuma aplicação nem o Traefik participam |
| `cobogo-routing` | API e Traefik | Por aqui o Traefik lê as rotas (`:8081`) e leva o tráfego do painel (`:8080`); nada mais |
| `cobogo-ingress` | Traefik, `cloudflared` e as releases roteadas | Só roteamento |
| `cobogo-proj-<id>` | Uma aplicação (e, durante o deploy, o worker) | Criada pelo worker |

A rede de roteamento tem **sub-rede fixa**, para a API confiar só nela como origem de cabeçalhos de proxy (DEC-27, item 11). A API **não** entra na rede de ingress: uma aplicação roteada não a alcança, nem consegue forjar `CF-Connecting-IP` direto contra ela.

**Acesso ao Docker (RNF-03)**
1. Só o **worker** fala com o Docker, e por um proxy filtrado (`docker-socket-proxy`), que é o único serviço com o socket montado. O proxy não publica porta e fica só na rede de controle. A API e o Traefik não têm Docker (o Traefik lê rotas por HTTP, DEC-31).
2. O proxy libera `CONTAINERS`, `IMAGES`, `NETWORKS`, `BUILD` e `POST`, e deixa `EXEC`, `SECRETS`, `AUTH`, `SWARM`, `VOLUMES` e o resto em zero. Ele filtra por método e prefixo de caminho, não pelo corpo: um worker comprometido ainda poderia pedir um contêiner privilegiado. É o limite assumido da DEC-19; o proxy protege contra erro de implementação, não contém um worker comprometido.

**Imagens e processos**
3. API, worker e Traefik executam **sem root** (o Traefik escuta em `:8000`, acima das portas privilegiadas, então não precisa de capacidade nenhuma). API e worker em imagens de vários estágios, com sistema de arquivos somente leitura, sem capacidades e com `no-new-privileges`. O worker leva `git` e escreve só em um `tmpfs` (o workspace de clonagem). Imagens base fixadas por **digest** (DEC-19, mitigação 6).
4. O PostgreSQL não publica porta. A API e o Traefik só publicam porta em um arquivo de sobreposição de desenvolvimento, nunca no `compose` de produção.

**Sonda por rede (DEC-31 item 12)**
5. Dentro de um contêiner, `127.0.0.1` é o próprio worker, então ele não alcança as portas publicadas no host. Com `COBOGO_PROBE=network` a sonda fala com o contêiner pelo nome e pela porta, na rede do projeto, e o worker se conecta a essa rede **só durante o deploy** (desconecta ao terminar). O padrão continua `host`, para o desenvolvimento fora de contêiner.

**Rota do painel**
6. Com `COBOGO_PANEL_TARGET` definido (por exemplo `http://api:8080`), a tabela de rotas ganha uma rota fixa que leva o host de `COBOGO_PUBLIC_URL` a esse destino. O nome do roteador (`cobogo_panel`) tem um caractere que um slug não pode ter, então nenhum projeto colide com ele. Se uma rota de aplicação tiver o mesmo host do painel, ela é omitida e registrada: o painel nunca é sobrescrito por um projeto.

**Túnel**
7. `cloudflared` roda no perfil `tunnel` do `compose` e usa um túnel gerenciado pela Cloudflare (`TUNNEL_TOKEN`). Os hostnames públicos (o painel e o curinga das aplicações) apontam para `http://traefik:8000` e são configurados no painel da Cloudflare, não no repositório. Sem o perfil, a pilha sobe sem túnel, o que permite testá-la localmente.

**Configuração**
8. Segredos e endereços ficam em um `.env` fora do Git, com um `.env.example` no repositório (senha do banco, chave de cifra, token das rotas, URL pública, token do túnel).

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| Worker com o socket do Docker montado | Equivale a root no host; o RNF-03 exige o proxy filtrado |
| API na rede de ingress | Uma aplicação roteada a alcançaria e poderia forjar `CF-Connecting-IP` contra o limite de tentativas de login |
| Sonda pelo gateway do host | Exige publicar as portas das aplicações em todas as interfaces do host |
| Registry para as imagens | Mais um serviço antes da D0; a construção local na VM basta |
| Traefik com o socket (provider do Docker) | Descartado na DEC-31 |

## Consequências e limites conhecidos

- O proxy do Docker é o ponto mais sensível: quem controla o worker controla o daemon (DEC-19). O que o desenho garante é que a API, o Traefik e as aplicações não chegam a ele.
- A API só é alcançável pelo Traefik e pela rede de controle: sem o Traefik no ar o painel fica indisponível, e o diagnóstico passa a ser pelo host.
- O worker ligado a uma rede de projeto durante o deploy alcança aquela aplicação (e só ela); se o worker cair no meio, a conexão fica até a limpeza do próximo deploy do projeto.
- O frontend, os roles do banco, o backup e a observação de recursos não fazem parte desta fatia.

- **O proxy é mais frouxo do que parece.** Verificado: ele nega `volumes`, `info`, `swarm`, `secrets`, `plugins` e **iniciar** um `exec` (`/exec/{id}/start`, 403), mas deixa **criar** um `exec` (`/containers/{id}/exec`, que chega ao daemon) e copiar arquivos de e para contêineres, porque a liberação de `CONTAINERS` cobre esses caminhos. Sem iniciar o `exec` isso não executa nada, mas confirma o limite da DEC-19: o filtro é por prefixo, e quem controla o worker controla o que ele pede ao daemon.
- **Reiniciar a VM não religa as aplicações.** Os contêineres das aplicações não têm política de reinício (DEC-29, item 4), então depois de uma reinicialização as rotas apontam para contêineres parados até um novo deploy. A reconciliação (RF-16, no coletor) é o que resolve; fica fora da D0.
- **Verificado em execução** (a pilha inteira em contêineres, no Docker 29, com o repositório de exemplo da Heroku): o painel responde por `localhost:8088` através do Traefik (a rota fixa do painel); dois deploys do mesmo projeto concluem, com a sonda pela rede do projeto, e 65 pedidos ao endereço público durante o segundo deploy deram 200, nenhum com erro; o IP do cliente que a API registra é o real (o do cabeçalho do Traefik), não o do proxy. A API não tem socket nem `DOCKER_HOST`; uma aplicação não resolve `api`, `postgres` nem `docker-proxy`, só alcança o Traefik; o worker volta a ficar só na rede de controle depois do deploy; o Postgres não publica porta; API, worker e Traefik rodam sem root, com sistema de arquivos somente leitura e sem capacidades.
- **Dois achados da execução real, já corrigidos:** o proxy precisa de `tmpfs` em `/run` (o haproxy grava o pidfile) e o `compose` não pode exigir `TUNNEL_TOKEN` fora do perfil do túnel.

## Critérios de revisão

Rever se o worker passar a precisar de mais endpoints do Docker, se o desempenho do build pelo proxy for um problema, ou quando houver um segundo worker.
