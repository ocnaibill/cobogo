# DEC-51 — Processos adicionais por projeto: worker e agendador

> **Estado:** direção aprovada pelo autor em 22/11/2026 (processos dentro do projeto, e não projetos separados); **implementada** (ver "Implementação") para o perfil Python.
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

## Implementação (23/11/2026)

- **O modelo, como campos do plano:** `process_worker` e `process_scheduler` (o comando, ou `none` para desligar), com origem e evidência como qualquer campo, em vez de uma lista solta: o resolvedor, os overrides e o painel já sabiam lidar com eles. O plano ganha `processes` (`name`, `kind`, `command`). Só o perfil Python por enquanto; o Node entra pelo mesmo caminho quando houver o que detectar.
- **A heurística (HEU-04.7, a parte "estrela"):** do **`Procfile`** (declaração: `worker`, `workers`, `celery` e nomes terminados em `worker` são worker; `beat`, `clock`, `scheduler` e terminados em `beat` são agendador; qualquer outro nome é dito como ignorado) e do **Celery** (inferência, com arquivo e linha): o app `app = Celery(...)` de módulo; em `<pacote>/celery.py` vira `celery -A <pacote> worker -l info --concurrency=2` (a concorrência é um padrão sensato para o limite de memória), em outro arquivo `-A modulo:atributo`; o beat por `CELERY_BEAT_SCHEDULE`, `beat_schedule` ou `django_celery_beat` (que usa o agendador do banco). O Procfile vence a inferência. Celery sem sinal de broker ganha um aviso. A proposta não pergunta: é um campo com evidência que o override troca ou desliga.
- **O pipeline:** os processos sobem depois do web, da **mesma imagem**, com as mesmas variáveis, volumes e serviços, só na rede do projeto (sem porta, sem ingress); a verificação é a **estabilidade** (`processStableWindow`, 10 s de pé; se sai, o deploy falha com o log e o código de saída, e a release anterior segue no ar); qualquer saída sem sucesso remove os processos junto com a candidata. A promoção move só o web para o ingress.
- **O ciclo de vida:** parar e iniciar (DEC-40), a pausa da release anterior por causa de um volume (DEC-41) e os limites (DEC-30) valem para o web **e** os processos; `retirable` protege os processos da release ativa e os remove com a release substituída, depois da espera (`ContainerFact.ActiveDeployment`). Logs e métricas usam o rótulo do contêiner (`cobogo.process`); no painel, o plano mostra o worker e o agendador e a linha do tempo, os eventos `process` e `processes`.
- **Mapa do repositório (DEC-49):** o Celery aparece como provido (com o comando) quando a análise o propôs; RQ e BullMQ dizem que rodam pelo `Procfile`.
- **Verificação:** testes do analisador (Procfile, Celery em `pacote/celery.py` e em `tasks.py`, beat, override `none`, comando de várias linhas recusado, aviso de broker), da aposentadoria e dos contêineres reais (de pé, sai com o código, sem porta, para e inicia); o **caminho inteiro com Docker**: um Django com Celery, a análise propondo o worker, o Redis gerenciado como broker, web e worker da mesma imagem, e uma tarefa enfileirada pelo web e **executada de verdade pelo worker**; e o `fec` na pilha local completa: as três perguntas (hosts do Django, MySQL, Redis, com o aviso do endereço escrito no código), MySQL, Redis, web e worker no ar, parar e iniciar levando os dois, e uma segunda release trocando web e worker e aposentando os da anterior.
- **Limites desta versão:** o worker não tem sonda de saúde além de estar de pé; a sobreposição de versões do worker na troca de release dura a espera da aposentadoria; uma réplica por processo; sem Node ainda.

