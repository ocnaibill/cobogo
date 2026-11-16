# DEC-26 — Posse, retomada e readiness do pipeline

> **Estado:** proposta de 16/11/2026, aguardando revisão do autor. **3d e 3e implementados** (itens 1 a 13).
>
> **Escopo:** como o worker reivindica, mantém, perde e retoma um job (3d) e como confirma que a aplicação subiu e explica a falha (3e, que absorve o diagnóstico 3c do DEC-24). Fecha a parte 3 do backend.
>
> **Relaciona-se com:** DEC-21, DEC-24, DEC-25, RN-01, RN-11, RF-06, RF-16, RNF-05, RNF-07, RNF-09, §12.3, §15 e §15.2 do mestre, issue #12.

## Contexto

A leitura do worker, do store e do adaptador Docker contra o mestre mostrou lacunas reais:

1. `ClaimNextJob` só reivindica `state = 'PENDING'`; um job deixado `LEASED` por um worker que caiu nunca é retomado, apesar do comentário dizer o contrário. O deploy fica parado no meio para sempre.
2. O lease é fixo em 5 minutos, sem heartbeat (RNF-07 pede 60 s e 15 s), e `ownership_generation` é incrementado mas nunca consultado: um worker que perdeu a posse ainda poderia gravar estado e publicar resultado, o que o §12.3 proíbe.
3. A retomada não é idempotente: o nome do contêiner (`cobogo-<slug>-<sha7>`) colide numa segunda tentativa.
4. Um desligamento limpo (SIGTERM) cancela o contexto, as gravações falham e o job fica preso.
5. A máquina de estados do §15.2 não é imposta: qualquer estado pode ser gravado sobre qualquer outro, e os erros dessas gravações são ignorados.
6. Não há limite de tentativas nem timeout de build (RNF-05: 10 min).
7. A sonda aceita só `200` em `/`, por 90 s; não detecta contêiner que saiu; e a falha não guarda o log do contêiner.

## Decisão

**Posse e retomada (3d)**

1. **Lease de 60 s com heartbeat de 15 s.** Uma goroutine renova `lease_until` do job enquanto o pipeline roda, condicionada à geração de posse. Se a renovação não afetar nenhuma linha, a posse foi perdida: o contexto do pipeline é cancelado com a causa "lease perdido" e o worker não grava mais nada.
2. **Reivindicação de jobs expirados.** `ClaimNextJob` passa a pegar `PENDING` ou `LEASED` com `lease_until` vencido, incrementando `attempt_count` e `ownership_generation`. Ao atingir **3 tentativas** sem concluir, o job é encerrado e o deploy vai a `FAILED` com "tentativas esgotadas", registrado em evento.
3. **Verificação de posse em toda escrita.** As gravações do worker (estado, SHA, análise, eventos, release, fim do job) carregam `{job, geração}` e só se aplicam se o job ainda é daquela geração e está `LEASED`, na mesma instrução SQL. Falha vira `ErrLeaseLost`. Efeitos fora do banco (contêiner) não são cobertos por isso: a posse é conferida imediatamente antes de iniciar o contêiner e antes de criar a release, e uma candidata de quem perdeu a posse é removida. Não se promete execução "exatamente uma vez" (§12.3).
4. **Transições impostas no banco.** Cada atualização de estado declara de quais estados parte (`WHERE state = ANY(...)`), seguindo o §15.2. `SUCCEEDED`, `FAILED`, `UNSUPPORTED` e `CANCELLED` são imutáveis. Uma retomada volta a `FETCHING` a partir de qualquer estado ativo; `NEEDS_INPUT` só sai por resposta (`QUEUED`) ou cancelamento. Transição inválida é erro (`ErrInvalidTransition`) e aborta o pipeline; os erros de gravação deixam de ser ignorados.
5. **Efeitos idempotentes.** Contêineres gerenciados ganham o label `cobogo.deployment=<id>`. Antes de iniciar, o worker remove os contêineres com esse label (e o de mesmo nome) deixados por uma tentativa anterior. A tag da imagem por SHA é reconstruída sem problema. Apenas recursos com os labels do Cobogó são tocados (RF-18).
6. **Desligamento limpo.** Com SIGTERM o worker para de reivindicar, cancela o pipeline em curso e, com um contexto novo de poucos segundos, devolve o job a `PENDING` **sem contar a tentativa**. A próxima reivindicação retoma do início, sobre o mesmo SHA.
7. **Prazos.** Build com timeout de 10 min (RNF-05), por contexto. Para o primeiro `pull` das imagens (cerca de 1 GB) não consumir esse prazo, o worker baixa as imagens do catálogo (DEC-25) ao iniciar, em segundo plano, com prazo próprio; falha é só registrada em log e não impede o worker.

**Readiness e diagnóstico (3e)**

8. **Sonda.** Três respostas HTTP consecutivas com status **abaixo de 500**, sem seguir redirecionamentos, dentro de **120 s** (RNF-05). O que ela prova é que há um servidor HTTP respondendo na porta certa; o status real é registrado no evento e, se não for 2xx, um aviso o acompanha ("respondeu 404 em /"). Alternativa mais estrita (exigir 2xx/3xx) fica como evolução se o corpus mostrar falsos sucessos.
9. **`health_path`** é um campo do plano, sobrescrevível, padrão `/` (começa com `/`, sem espaço nem caractere de controle, até 256 bytes). Código esperado exato fica para depois.
10. **Saída precoce.** A cada tentativa da sonda o worker confere se o contêiner ainda roda; se saiu, a verificação falha na hora com o código de saída, em vez de esperar o prazo.
11. **Log da falha.** Em falha de início ou de readiness, o worker guarda as **últimas 50 linhas** do log do contêiner (limite de 8 KiB) no evento de falha. Antes de gravar, remove toda ocorrência dos **valores** das variáveis do projeto (comprimento mínimo de 4 caracteres; abaixo disso não há como diferenciar de texto comum). Isso cumpre RN-11 sem prometer detectar qualquer segredo que a aplicação imprima.
12. **Diagnóstico (DEC-24 item 11).** O worker cruza o log com o inventário HEU-09 da análise do deploy (variáveis referenciadas no código, mesmo ausentes do exemplo) e as variáveis do projeto sem valor:
    - `confirmed`: uma linha do log cita o nome de uma variável sem valor **e** reclama de falta, por um padrão fechado ("is not defined", "is required", "missing", "not set", "must be set"...). A mensagem diz "provável causa: `X` não foi preenchida". Refinamento da implementação: o nome citado **sem** reclamação (por exemplo, impresso num log de início normal) conta como `possible`, não `confirmed`.
    - `possible`: o contêiner saiu e há variáveis sem valor com aviso `likely_required`, `undeclared` ou `referenced_only`; lista-as sem afirmar.
    - `unknown`: nenhum dos dois.
    O diagnóstico é registrado no evento de falha e em `failure_reason`; nunca afirma causa sem o nome no log.
13. **Tentativas visíveis.** Cada tentativa registra um evento `attempt` com o número e o motivo da retomada; eventos anteriores nunca são apagados (§15.2).

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| Lease fixo longo sem heartbeat | Um job de 10 min e um lease de 5 travam ou duplicam; lease curto sem heartbeat reivindica jobs vivos |
| Confiar só na verificação de posse no início | Um worker lento ainda grava depois de perder a posse |
| Retentativas ilimitadas | Um deploy que sempre derruba o worker o travaria em ciclo |
| Cancelar o job no SIGTERM (marcar `FAILED`) | Uma atualização do próprio Cobogó falharia os deploys em andamento |
| Sonda exata em `200` | Reprova aplicações que respondem 404/302/401 na raiz, apesar de estarem no ar |
| Exigir 2xx/3xx e `health_path` obrigatório | Atrito alto e pouco ganho antes de o corpus mostrar falsos sucessos |
| Redigir segredos por padrão de nome | Falha com valores sem padrão; a lista dos valores conhecidos é exata |

## Consequências e limites conhecidos

- Com mais de um worker seria preciso também **serializar por projeto** (o §12.3 pede isso antes de efeitos críticos). Em M1 há um worker e um build simultâneo (RNF-05); fica registrado como pré-requisito de escalar.
- A verificação de posse protege o banco, não o Docker; o intervalo entre a última checagem e a ação no daemon permanece.
- Valores secretos com menos de 4 caracteres não são redigidos do log.
- O diagnóstico depende dos formatos de mensagem de cada runtime; o corpus mede quanto acerta antes de confiar nele para mais que `possible`.
- O diagnóstico só enxerga variáveis do inventário da análise (nomes literais no código e no `.env.example`); uma aplicação que reclama de uma variável que a análise não viu (acesso dinâmico, biblioteca) fica `unknown`, ainda com o log e o código de saída. Verificado em execução.
- Cancelamento pelo usuário de uma etapa ativa continua fora de escopo (o mestre limita a antes da execução ou durante a espera por informação).
- A readiness aceita 404 em `/`: prova servidor HTTP no ar, não aplicação funcional (o oráculo funcional por repositório é do experimento, RN-01).

- **Verificado em execução (3d):** com `SIGKILL` do worker durante o build, o job fica `LEASED`, o novo worker espera o lease vencer (não rouba um lease válido), reivindica na geração 2, registra o evento `attempt` com `resumed: true` e conclui sobre o mesmo SHA, com um único contêiner. Com `SIGTERM`, o job volta a `PENDING` com 0 tentativas e o novo worker o conclui na primeira tentativa.
- **Colisão de nomes (encontrada e corrigida):** o nome do contêiner era `cobogo-<slug>-<sha7>`; um segundo deploy do mesmo projeto e commit falhava com "nome já em uso" enquanto o primeiro existisse. Observado em execução. O nome passou a levar o deploy (`cobogo-<slug>-<deploy8>`): o mesmo deploy mantém o nome em qualquer tentativa e deploys distintos convivem. Aposentar a release anterior continua sendo o RF-15 (MVP).

- **Verificado em execução (3e):** um deploy cuja aplicação sai com código 2 depois de imprimir o valor de uma variável do projeto falha em `VERIFYING` na hora (sem esperar os 120 s), com `exit_code`, as linhas do log com o valor trocado por `[REDIGIDO]` (e ausente do JSON inteiro da API), nenhum contêiner sobrando, e um novo deploy do mesmo projeto e commit conclui. Com a aplicação saudável, o evento `probe` registra caminho e status.
- **Registry:** `PullParent` foi desligado no build. Uma falha transitória do Docker Hub (`Bad Gateway`, observada em execução) derrubava um build cujas imagens já estavam no cache. As imagens são garantidas antes do build (`EnsureImage`) e atualizadas pelo worker ao iniciar (`PullImage`).

## Critérios de revisão

Rever se o corpus mostrar sondas aprovadas em aplicações que não funcionam (endurecer a sonda), jobs retomados que repetem efeitos visíveis, ou diagnósticos `confirmed` errados com frequência relevante.
