# DEC-40 — Parar, iniciar e reiniciar a aplicação de um projeto

> **Estado:** proposta discutida com o autor em 21/11/2026; **implementada**. Segunda leva do MVP (DEC-40 a DEC-42, mais a do PostgreSQL auxiliar).
>
> **Escopo:** parar, iniciar e reiniciar a release ativa, como o painel mostra um projeto parado e o que o endereço público responde enquanto ele está parado.
>
> **Relaciona-se com:** RF-14, RN-12, DEC-31 (rotas), DEC-38 (fila de operações, faxina), DEC-41 (volumes: reaproveita "iniciar a anterior"), issue #37.

## Contexto

A DEC-38 cobre remover. Faltam parar, iniciar e reiniciar a release ativa. Hoje um projeto só tem dois jeitos de sair do ar: um deploy que falha ou a exclusão. E um endereço sem rota responde o 404 do Traefik, que não diz nada ao visitante.

## Decisão

1. **Três operações na fila da DEC-38:** `stop_app`, `start_app` e `restart_app`, executadas pelo worker. A API só as pede (`POST /projects/{id}/app/stop|start|restart`, resposta 202 com a operação) e tem auditoria com ator e projeto. Só tocam em contêiner com os labels do Cobogó e do projeto (RF-18).
2. **Parar mantém o contêiner** (`docker stop`; nada é removido). Iniciar de novo reaproveita contêiner, imagem e configuração, então é rápido. Parar com a aplicação já parada, ou iniciar com ela no ar, responde sucesso sem fazer nada (idempotente).
3. **Estado do projeto: `running` ou `stopped`**, guardado na release ativa (`operational_state`) e **distinto de falhou** (RN-12: sucesso histórico e saúde atual são informações diferentes). O painel mostra "Parado" na visão geral e no projeto, com o botão de iniciar.
4. **A rota acompanha o estado.** A tabela de rotas (DEC-31) deixa de apontar o endereço do projeto parado para o contêiner e passa a apontá-lo para uma **rota interna da API** que serve a **página de projeto inativo**: identidade visual do painel, status **503** (com `Retry-After`), o nome do projeto e a frase de que ele não está ativo no momento. Nada mais do projeto sai nessa página. Ao iniciar e passar na verificação de prontidão, a rota volta ao contêiner.
5. **Reiniciar** é parar e iniciar, com a mesma verificação de prontidão de um deploy; se não ficar pronto no prazo, o resultado é falha da operação, dita na operação, e o projeto fica `stopped` (não finge estar no ar).
6. **A faxina e a aposentadoria (DEC-38) não removem uma release ativa parada.** Um contêiner parado de release ativa é elegível a nada; só deixa de ser protegido quando deixa de ser a release ativa.
7. **O estado parado sobrevive** a reinício do worker e da pilha: ele vem do banco, não da memória. Depois de reiniciar a VM, a aplicação parada continua parada. Uma que estava no ar **não sobe sozinha** (DEC-29/DEC-32) e o banco ainda diz "no ar": o endereço responde erro do proxy até alguém usar **Reiniciar**, que sobe o contêiner mantido. Detectar essa divergência é trabalho do coletor (#23) e da reconciliação (#24).
8. **Um deploy novo num projeto parado** constrói e sobe a candidata normalmente e, ao promovê-la, o projeto volta a `running`: quem pede um deploy quer o projeto no ar.

## Alternativas consideradas

- **Parar removendo o contêiner:** libera memória, mas perde o que o contêiner tinha em disco e torna iniciar um redeploy. Descartado pelo autor.
- **Sem rota enquanto parado (404 do Traefik):** mais simples, mas deixa o visitante sem explicação. Descartado: a rota interna reaproveita a estrutura existente.
- **Reiniciar política do Docker (`restart: unless-stopped`) nas aplicações:** mudaria a decisão das DEC-29/DEC-32 e esconderia o estado real. Fica para depois, com o coletor.

## Consequências e limites

- Parado não é "pausado": nada preserva memória nem conexões abertas.
- A página de inativo mostra o nome do projeto a qualquer visitante do endereço; o nome já é o próprio endereço.
- Uma parada não faz backup nem toca em volumes (DEC-41).

## Verificação

- **Testes:** com PostgreSQL real, o estado da aplicação (`none`, `running`, `stopped`), os pedidos recusados quando não mudam nada (parar o parado, iniciar o que está no ar), a rota que acompanha o estado, a release parada continuar a ativa para a faxina, um deploy novo trazendo o projeto de volta e a porta de host atualizada; a API (202, 409 `no_active_release`, `app_already_stopped` e `app_already_running`, 404, 401), a tabela de rotas (o parado aponta para a API, o outro segue no contêiner) e a página de inativo (503, nome, `no-store`, CSP fechada; nenhum outro host, nem o do painel, é afetado, e nenhum caminho da API vaza por ela); com Docker real, parar mantém o contêiner, iniciar o traz de volta e nada disso toca um contêiner sem os labels do Cobogó.
- **Em execução** (API e worker locais, deploy real de um repositório público): parar deixou o contêiner `exited` e **existente**, a rota passou para a API e a página de inativo respondeu 503 com o nome do projeto; parar de novo foi recusado com 409; iniciar esperou a prontidão, a rota voltou ao contêiner e a aplicação respondeu 200; reiniciar mudou a **porta de host** (32891 para 32892), o que confirmou a necessidade de atualizá-la no banco; com o projeto parado, reiniciar o worker e esperar a faxina de partida **não removeu** o contêiner; um novo deploy com o projeto parado o colocou de volta no ar e a release parada foi aposentada normalmente. No painel, "Parado" na visão geral e no projeto, o aviso, o botão Iniciar acompanhando a operação até "no ar".

## Critérios de revisão

- Medir quanto leva iniciar uma aplicação parada e ajustar o prazo de prontidão.
- Rever a página de inativo se o estudo mostrar confusão entre "parado" e "falhou".
