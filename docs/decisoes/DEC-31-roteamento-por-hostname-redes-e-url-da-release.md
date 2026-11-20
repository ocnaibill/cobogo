# DEC-31 — Roteamento por hostname, redes por projeto e URL da release

> **Estado:** decidida com o autor em 17 e 20/11/2026; **implementada** (itens 1 a 14). Parte 5b. **Substitui, no ponto do provider de roteamento, a DEC-19**: o Traefik deixa de descobrir contêineres pelo Docker e passa a ler uma tabela de rotas servida pelo Cobogó (item 7).
>
> **Escopo:** como uma release ganha um endereço público (`<slug>.<domínio>`), em que redes o contêiner roda, quando ele passa a receber tráfego, de onde o Traefik lê as rotas e como a API informa a URL. A retirada da release anterior fica para a parte 5c.
>
> **Relaciona-se com:** DEC-19 (redes e proxies), DEC-26 (sonda), DEC-29 e DEC-30 (limites e configuração), RF-06, RF-15, RNF-03, §17.2 do mestre.

## Contexto

Hoje todo contêiner roda na rede `bridge` padrão e publica uma porta efêmera no loopback do host. Não há endereço público, e a release só informa `host_port`.

O autor tem `ocnaibill.dev` na Cloudflare e vai comprar um domínio dedicado às aplicações (`cobogo.dev`). Com o túnel da Cloudflare o HTTPS termina na borda, e o certificado gratuito cobre só um nível de subdomínio: o esquema `<slug>.<domínio>` precisa de **um** nível. O painel fica em `cobogo.ocnaibill.dev`, em outro domínio registrável.

**Por que não labels do Docker.** A primeira implementação seguiu a DEC-19: o Traefik descobria os contêineres pelo provider do Docker, por labels. A verificação real (dois deploys do mesmo projeto, com o Traefik 3.6 e o Docker 29) mostrou duas falhas que os testes unitários não pegariam: (a) o provider só reage a eventos de contêiner (`start`, `die`), não a conectar ou desconectar de rede, então um contêiner descoberto antes de entrar no ingress fica com um endereço inalcançável; (b) a release antiga, mesmo desconectada do ingress, mantém o seu roteador, e dois roteadores com a mesma regra dão **502 persistente**. Labels não conseguem dizer "este contêiner ainda não deve receber tráfego", que é o que a promoção em etapas exige.

## Decisão

**Configuração**

1. Duas configurações editáveis pelo painel, com padrão inicial no ambiente (DEC-30): `apps_domain` (nome de host, com porta opcional; vazio desliga o roteamento; `localhost` é aceito para desenvolvimento) e `apps_scheme` (`http` ou `https`, padrão `https`). Variáveis: `COBOGO_APPS_DOMAIN` e `COBOGO_APPS_SCHEME`.
2. **Sem `apps_domain` nada muda além da rede do item 5:** não há rota, a release não tem URL, e o acesso é a porta no loopback.
3. A mudança de domínio vale a partir do próximo deploy: a rota de uma release já gravada não muda.

**Endereço**

4. O hostname de uma release é `<slug>.<apps_domain>` (sem a porta) e a URL é `<apps_scheme>://<slug>.<apps_domain>[:porta]/`. O slug e o domínio são validados, e o endpoint de rotas ainda confere cada valor antes de emiti-lo. **Slugs reservados** (recusados ao criar projeto): `www`, `mail`, `docs`, `api`, `admin`, `painel`, `cobogo` e `traefik`. Projetos já existentes não são afetados.

**Redes**

5. Cada projeto tem a sua rede `cobogo-proj-<id do projeto>`, criada sob demanda e etiquetada como gerenciada. O contêiner roda nela, isolado das aplicações de outros projetos. A rede de ingress, `cobogo-ingress`, é compartilhada e serve só ao roteamento.
6. A rede de ingress é **plana**: contêineres roteados conseguem se alcançar por ela. A DEC-19 aceita isso no nível A de confiança. Desligar a comunicação entre contêineres bloquearia também o Traefik, então não é uma opção.

**Roteamento: provider HTTP**

7. O Traefik usa o **provider HTTP**: a cada poucos segundos consulta um endpoint do Cobogó que devolve a configuração dinâmica (roteadores e serviços). **O Traefik não acessa o Docker**: não precisa do socket nem do proxy de descoberta da DEC-19, o que reduz a superfície de RNF-03.
8. O endpoint é `GET /traefik/config`, em um **listener interno separado** (`COBOGO_ROUTER_ADDR`, padrão `:8081`), que só deve existir na rede de controle e nunca ser publicado. Não está no contrato público da API, não usa sessão nem CSRF e exige `Authorization: Bearer <COBOGO_ROUTER_TOKEN>`, comparado em tempo constante. Sem token configurado (mínimo de 24 caracteres) o listener **não sobe**. Em listener próprio, a rede já isola a rota e o token é a segunda camada; uma rota `/internal/...` na API pública ficaria alcançável pelo túnel.
9. A tabela vem **só do banco**: cada projeto tem, no máximo, uma release **ativa** com rota; a rota guarda o host e o destino (`http://<contêiner>:<porta>`, resolvido pelo DNS da rede de ingress). Um projeto sem release ativa roteada não aparece. O Cobogó confere host e destino contra padrões fechados antes de emitir; um valor fora do padrão é omitido e registrado no log, nunca emitido.

**Quando recebe tráfego**

10. O contêiner **sobe fora do ingress** e só é alcançável pela porta do loopback. Depois de passar na sonda (DEC-26), a **promoção** é: conferir a posse; conectar o contêiner ao ingress; **gravar, numa única transação**, o estado `SUCCEEDED` do deploy, a release (`active`, com host e destino) e a troca das releases anteriores do projeto para `superseded`. A partir do próximo ciclo de consulta do Traefik a rota aponta para a candidata. **As releases anteriores não são tocadas neste ponto**: continuam no ingress e rodando, sem rota. Desconectá-las já poderia derrubar pedidos que o Traefik ainda envia a elas durante o ciclo de consulta; rebaixar e retirar, com um período de espera maior que o ciclo, é o RF-15 (parte 5c). Falha em qualquer passo antes da transação remove a candidata e deixa a release anterior no ar; a transação única impede um `SUCCEEDED` sem release, ou uma release sem `SUCCEEDED`.
11. A mudança de rota leva até um ciclo de consulta (`pollInterval`, 2 s no compose de desenvolvimento). Nesse intervalo as duas releases estão no ingress e funcionando; só o roteamento muda. O adaptador já sabe desconectar do ingress (`Demote`), pronto para a 5c.
12. A sonda continua usando a porta publicada no loopback (o worker ainda roda no host). Quando o worker for para um contêiner (5d), ela passa a usar a rede do projeto.

**URL**

13. A release guarda a `url` com que foi roteada, e a API a devolve junto de `host_port`. Mudar o domínio depois não reescreve releases antigas. O evento `verifying/succeeded` informa a mesma URL, ou o endereço no loopback quando não há roteamento.

**Desenvolvimento**

14. Um `compose` de desenvolvimento (`deploy/dev/traefik.yml`) sobe o Traefik na rede de ingress, com o provider HTTP apontando para o listener interno do host e a porta 80 do contêiner publicada só no loopback (8088). O `compose` não monta o socket do Docker. Os navegadores resolvem `*.localhost` para a máquina, então `http://<slug>.localhost:8088/` funciona sem DNS.

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| Provider do Docker com labels (a primeira versão) | Não expressa promoção em etapas; não reage a rede; dois roteadores iguais dão 502 (verificado) |
| Provider do Docker com nomes estáveis e retirada imediata da antiga | A candidata receberia tráfego durante o boot, com janela de 502, e o roteamento dependeria de eventos de contêiner |
| Provider de arquivo | Exige diretório compartilhado entre worker e Traefik; a notificação de mudança é pouco confiável em volumes do Docker Desktop |
| Endpoint de rotas na API pública | Alcançável pelo túnel; só o token o protegeria |
| Conectar o contêiner ao ingress já na criação | A candidata receberia tráfego antes de validada |

## Consequências e limites conhecidos

- A DEC-19 previa um terceiro proxy do Docker (descoberta). Com o provider HTTP ele não é necessário; a DEC-19 recebe uma nota apontando para esta decisão.
- Se o Cobogó (ou o endpoint) cair, o Traefik mantém a última configuração que recebeu; novas promoções só aparecem quando ele voltar. Se o Traefik reiniciar com o endpoint fora do ar, fica sem rotas até ele responder.
- Com uma nova release promovida, os contêineres antigos seguem consumindo recursos, sem receber tráfego, até a 5c os retirar.
- A rede de ingress plana permite que uma aplicação alcance outra que também esteja roteada. É um limite assumido do nível A.
- O token é um segredo do ambiente do plano de controle (Cobogó e Traefik); quem o tem lê a tabela de rotas, que só contém nomes públicos e destinos internos.

- **Verificado em execução** (API, worker e Traefik 3.6 reais, Docker 29, repositório de exemplo da Heroku): o primeiro deploy ficou acessível em `http://heroku-node.localhost:8088/` (200), um host desconhecido deu 404, e a release trouxe a `url`. Durante o **segundo deploy** do mesmo projeto, 76 pedidos seguidos ao endereço público deram **200, nenhum com erro** (a falha que derrubou o desenho por labels, 502 persistente, não ocorre). A tabela do banco ficou com a release antiga `superseded` e a nova `active`. A API pública devolve 404 para `/traefik/config`, e o listener interno devolve 401 sem o token.
- **Dois detalhes que só a execução real mostrou:** o Traefik recusa mapas vazios na configuração ("routers cannot be a standalone element"), então uma tabela vazia é `{}`; e ele leva alguns segundos para ver o endpoint depois de a API subir, sem efeito além de erros de consulta no log nesse intervalo.
- **Testes que o provam** (cada um falha quando o código correspondente é removido): a transação única da promoção (substituir as anteriores, e tudo-ou-nada), a tabela só com a release ativa e com rota, a exigência do token, e a omissão de host ou destino fora do padrão.

## Critérios de revisão

Rever se o intervalo de consulta incomodar, se o corpus mostrar aplicações que dependem da rede `bridge` padrão, se a rede de ingress plana se mostrar um risco real, ou quando o worker passar a rodar em contêiner (a sonda e a criação de redes mudam).
