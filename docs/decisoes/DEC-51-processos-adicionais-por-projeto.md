# DEC-51 — Processos adicionais por projeto: worker e agendador

> **Estado:** direção aprovada pelo autor em 22/11/2026 (processos dentro do projeto, e não projetos separados); **o texto abaixo aguarda a leitura do autor antes de implementar**.
>
> **Escopo:** um projeto passa a ter, além do processo `web`, outros processos da **mesma imagem** (worker de fila, agendador), sem porta nem rota, com as mesmas variáveis e os mesmos serviços. Cobre a descoberta (o `Procfile` e o Celery), o ciclo de deploy, os logs, as métricas e o painel.
>
> **Relaciona-se com:** DEC-48 (o `Procfile` hoje ignora o que não é `web`), DEC-50 (o Celery precisa de um broker Redis), DEC-40 (parar, iniciar e reiniciar), DEC-43 e DEC-46 (métricas e logs), DEC-31 e DEC-38 (promoção e aposentadoria de releases), DEC-49 (mapa do repositório).

## Contexto

Hoje um projeto é **um processo**: um contêiner HTTP com sonda, rota e porta. Isso deixa de fora um padrão comum: a aplicação web que enfileira tarefas e um **worker** da mesma base de código que as executa (Celery, RQ, Dramatiq, Sidekiq-like). No `fec`, o envio de certificados por e-mail depende disso (`send_certificate_task.delay`, `celery.py`, `tasks.py`). A DEC-48 disse que um processo que não é web "é ignorado com aviso"; aqui isso passa a ser suportado.

## Decisão

### 1. O plano ganha uma lista de processos

O plano passa de um comando de início para **`processes`**: `{name, kind, command}`, com `kind` em `web` (um só: porta, rota e sonda, como hoje), `worker` e `scheduler`. Todos usam a imagem do deploy. Cada um roda com **uma réplica** (escala fica fora). As variáveis, os serviços (DEC-50), os volumes e os limites padrão são os do projeto.

### 2. De onde vêm os processos

- **`Procfile`:** cada linha vira um processo; `web` é o web, `worker` e afins são workers, `release` não vira processo (continua um aviso: o Cobogó não roda fase de release no deploy, e o comando é oferecido na aba Comandos).
- **Detecção (Python):** `celery` nas dependências e um arquivo com `Celery('nome')` (o `fec` tem `backend/celery.py`) propõem `celery -A <módulo> worker -l info`; `django_celery_beat` ou `CELERY_BEAT_SCHEDULE` propõem o `beat`. Para RQ, `rq worker`. É uma **proposta com evidência**, que a pessoa confirma (um override do plano, DEC-24); o padrão inicial é `adicionar`, desde que o broker venha de variável (se está fixo em `localhost`, vale o aviso da DEC-50: o processo sobe, mas não alcança o Redis).
- **Override:** a pessoa pode adicionar, trocar o comando ou remover um processo.

### 3. Deploy e promoção

- Um contêiner por processo e release, nomeado `<projeto>-<release>-<processo>`. O `web` segue o fluxo de hoje (sonda de prontidão, promoção na rota).
- **Verificação de um worker:** não há HTTP; vale a **estabilidade**: o contêiner precisa continuar rodando por uma janela curta (a definir, da ordem de 10 s) sem sair. Se sair, o deploy falha com o log, como acontece com o web.
- **Troca de release:** os workers novos sobem antes de os antigos serem parados; os antigos recebem `SIGTERM` e têm o prazo de encerramento (o Celery encerra "a quente": termina a tarefa em curso). Durante a janela de sobreposição, duas versões do worker coexistem; a DEC diz isso na análise.
- **Reversão e aposentadoria** (DEC-31 e DEC-38) tratam os processos de uma release como um conjunto.

### 4. Logs, métricas, parar e reiniciar

- **Logs (DEC-46):** o rótulo do contêiner leva o nome do processo, e a aba Logs filtra por processo.
- **Métricas (DEC-43):** cada contêiner continua uma série; a visão do projeto soma e separa por processo.
- **Parar, iniciar e reiniciar (DEC-40):** valem para o projeto inteiro. Reiniciar só um processo fica como evolução.
- **Comandos (DEC-45):** não mudam; rodam num contêiner descartável, não nos processos.

### 5. Limites e segurança

Cada processo tem a mesma jaula da aplicação (sem capacidades, limites de memória, CPU e processos, só a rede do projeto). Os limites do projeto passam a valer **por processo**, e o total é mostrado; a política de limites totais fica para a implementação medir.

### 6. Painel

A visão geral mostra a lista de processos com estado e último reinício; o plano mostra o comando de cada um; o mapa do repositório (DEC-49) já os traz como peças `worker` e `agendador`.

## Ordem de entrega

| Parte | O que entra | Prova |
| --- | --- | --- |
| **A** | Plano com `processes`, `Procfile` completo, deploy e promoção com worker, logs e métricas por processo | um Flask com `Procfile` (`web` e `worker`) real, no ar, com o worker estável |
| **B** | Detecção do Celery (e `beat`, RQ), proposta com evidência e confirmação | um Django com Celery e Redis (DEC-50 C) reais: a tarefa enfileirada pelo web é executada pelo worker; reinício e troca de release sem perder tarefa em curso |

Cada parte é um commit e uma subida na VM. Pré-requisito da parte B: Redis gerenciado (DEC-50 parte C).

## Alternativas consideradas

- **Um projeto separado para o worker** (mesmo repositório, comando diferente): reaproveita o que existe, mas repete variáveis e serviços nos dois, não tem o ciclo de vida junto e obriga o worker a ter sonda HTTP.
- **Um contêiner com os dois processos (supervisor):** esconde a falha de um deles e quebra o `SIGTERM` de cada um.
- **Rodar o worker como comando pontual (DEC-45):** é descartável e com prazo; um worker é um processo que fica.

## Consequências e limites

- O modelo de release deixa de ser "um contêiner por release"; as partes que assumem isso (promoção, aposentadoria, parar e iniciar, métricas) precisam do ajuste da implementação.
- Memória e CPU do projeto crescem com cada processo; a VM tem teto.
- A sobreposição de versões do worker na troca de release pode exigir cuidado do projeto com tarefas que mudam de formato.
- Escala (várias réplicas de um processo) e um processo que só roda sob agenda ficam fora.

## Decisões do autor (22/11/2026)

1. **Processos dentro do projeto**, e não projetos separados.
2. Pendentes na leitura: a verificação do worker por estabilidade e a ordem A, B.
