# DEC-28 — Contrato da API para o painel

> **Estado:** decidida com o autor em 17/11/2026; **implementada** (itens 1 a 10, exceto a URL da release, adiada de propósito).
>
> **Escopo:** o que a API expõe para o painel React (parte 4b): listas, análise, plano, eventos incrementais, cancelamento e o arquivo OpenAPI que descreve tudo isso.
>
> **Relaciona-se com:** DEC-26, DEC-27, RF-03, RF-07, RF-08, §15.2 do mestre.

## Contexto

A API de 4a cobre autenticação, criar projeto, criar deploy, responder pendência e variáveis. Ao painel faltam: listar projetos e deploys, ver a análise e o plano, acompanhar o progresso e desistir de um deploy. Além disso, `GET /deployments/{id}` devolve todos os eventos de uma vez; um build do Node gera centenas de linhas e a resposta cresce sem limite.

O painel será escrito pelo autor, mas o Cobogó é de código aberto: o contrato precisa estar escrito num lugar que não seja o código Go.

## Decisão

1. **Resumo do deploy.** `GET /deployments/{id}` deixa de embutir os eventos e devolve `{deployment, release?, last_event_sequence}`. Quem mostra o terminal usa o item 2. É uma quebra de resposta, aceita agora porque nenhum cliente consome a API ainda. O que a tela exibe do deploy (e como) fica para depois, com as telas.
2. **Eventos incrementais.** `GET /deployments/{id}/events?after=N&limit=M` devolve os eventos com `sequence > N`, em ordem. Padrão 200, máximo 1000. A resposta traz `next_after` (o último `sequence` devolvido, ou o `after` recebido), `has_more`, `state` e `settled`.
3. **`settled`.** O worker marca o deploy como final **antes** de gravar o último evento (`succeeded`/`failed`), então o estado sozinho não diz quando parar de consultar. `settled` é verdadeiro quando o job do deploy está `DONE`: nada mais será gravado sem uma ação do usuário (resposta a pendência). É lido **antes** dos eventos, na mesma consulta, para que `settled = true` implique que a resposta já contém tudo. Regra para o cliente: continuar consultando até receber `settled = true` e `has_more = false`; um deploy em `NEEDS_INPUT` fica `settled` até ser respondido.
4. **Atualização por polling**, a cada 2 a 3 segundos, com `after`. SSE é uma evolução compatível: seria a mesma busca empurrada pelo servidor, sem mudar os dados. Webhooks não se aplicam (servidor para servidor, não para o navegador). Fica para quando a tela real mostrar que o atraso incomoda.
5. **Rotas novas** (todas autenticadas e filtradas pelo dono; recurso de outro dono é `404`):
   - `GET /projects`, mais recentes primeiro, cada item com o último deploy (`id`, `state`, `created_at`);
   - `GET /projects/{id}/deployments`;
   - `GET /deployments/{id}/analysis`: o snapshot da análise (campos resolvidos com origem, evidências, pendências com opções, variáveis, avisos, cobertura);
   - `GET /deployments/{id}/plan`: o plano e o Dockerfile, derivados da análise do deploy pelo planner (o mesmo que o worker usa). Só existe quando a análise é `READY`;
   - `POST /deployments/{id}/cancel`.
6. **Paginação** das duas listas por cursor: `?limit=` (padrão 20, máximo 100) e `?before=<cursor>`, um valor opaco (instante e ID do último item) que a resposta devolve em `next_before` (vazio no fim). Ordenação por `created_at` decrescente com desempate pelo `id`: itens criados no mesmo instante não se perdem entre páginas.
7. **Cancelamento.** Só em `QUEUED` e `NEEDS_INPUT` (o mestre limita ao que ainda não executou). Na mesma transação o deploy vai a `CANCELLED`, o job a `DONE` e um evento `cancelled` é gravado. Um job `LEASED` (o worker já o reivindicou) recusa: `409` `deployment_started`. Já terminal: `409` `deployment_finished`. A trava da linha do job serializa o cancelamento contra a reivindicação.
8. **Erros.** Continua `{"error":{"code","message"}}`. O painel decide pelo `code`, nunca pelo texto. Além dos códigos por status do DEC-27, entram `not_awaiting_input`, `deployment_started`, `deployment_finished` e `analysis_unavailable` (a análise ainda não existe), `plan_unavailable` (análise que não é `READY`).
9. **O contrato é o arquivo `docs/api/openapi.yaml`**, escrito e mantido à mão no repositório. Um teste compara as rotas registradas no roteador com as do arquivo e falha se faltar ou sobrar alguma, para ele não envelhecer. Gerar o arquivo a partir do código exigiria anotações ou mais uma dependência, sem ganho para uma API deste tamanho. O teste confere rotas e métodos, não os formatos de corpo.
10. **URL da release fica fora.** A release guarda só a porta no host. O endereço público depende do roteamento por Traefik (DEC-19, parte 5); inventar uma URL agora seria prometer o que a API não sabe. O painel recebe `host_port` e a URL entra quando o roteamento existir.

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| Manter eventos no detalhe do deploy | Cresce sem limite; o painel o rebaixaria inteiro a cada atualização |
| Parar o polling quando o estado é final | O último evento é gravado depois da transição; a tela perderia a mensagem de falha e o log |
| SSE já | Mais complexo (reconexão, conexões presas, tempo ocioso no túnel) e sem necessidade comprovada; é compatível com o item 2 |
| Gerar o OpenAPI do código | Anotações ou dependência nova; o teste de paridade cobre a divergência |
| Cancelar durante o build | O mestre limita ao que não executou; exigiria parar contêiner e build com segurança |
| URL da release agora | Seria um valor inventado (127.0.0.1:porta); a rota pública é da parte 5 |

## Consequências e limites conhecidos

- O plano de `/plan` é recalculado com o planner atual; se o planner mudar depois, um deploy antigo pode mostrar um Dockerfile diferente do que rodou. A imagem executada está nos eventos `generating`. Se o corpus mostrar divergência, o plano passa a ser gravado com o deploy.
- O teste de paridade não confere formatos de corpo; um erro de esquema no YAML só seria visto por quem gerar o cliente.
- `settled` descreve o job, não a aplicação: um deploy `SUCCEEDED` continua rodando depois.
- O cancelamento de um deploy `LEASED` que ainda está em `QUEUED` (o worker acabou de reivindicar) recusa em vez de esperar; o painel pode tentar de novo.

- **Verificado em execução:** com a API e o worker reais e o repositório de exemplo da Heroku, o fluxo inteiro (configuração, projeto, dois deploys, cancelamento do que esperava na fila, pendência de porta, resposta, build e sucesso) foi percorrido por um cliente que só usa as rotas do contrato. O laço de `/events` leu os 75 eventos em páginas de 50, parou exatamente em `settled`, nas duas esperas (`NEEDS_INPUT` e `SUCCEEDED`), e o total coincidiu com `last_event_sequence`. Toda resposta real foi conferida contra os esquemas de `openapi.yaml` (campos obrigatórios, enums, campos não documentados, status documentados) e nada divergiu; a conferência foi um roteiro avulso, não um teste do repositório.
- **Testes que o provam:** a paridade rotas x contrato (renomear um caminho no arquivo falha), o desempate do cursor (sem ele, itens do mesmo instante somem), a trava do job no cancelamento (sem ela, o mesmo deploy é cancelado **e** reivindicado) e o `settled` com o job ainda arrendado depois de o estado ficar final.

## Critérios de revisão

Rever se o painel mostrar atraso incômodo no polling (SSE), se o `/plan` divergir do que rodou (gravar o plano), ou se a paginação por `created_at` se mostrar frágil com muitos deploys no mesmo instante.
