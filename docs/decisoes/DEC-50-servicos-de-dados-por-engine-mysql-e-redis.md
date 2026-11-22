# DEC-50 — Serviços de dados por engine: MySQL e Redis dentro do Cobogó

> **Estado:** direção aprovada pelo autor em 22/11/2026 (MySQL oficial na versão LTS, primeira versão sem TLS); **o texto abaixo aguarda a leitura do autor antes de implementar**.
>
> **Escopo:** generalizar o serviço gerenciado (hoje só PostgreSQL, DEC-42/44/47) para uma especificação por engine e entregar MySQL e Redis sobre ela: detectar, perguntar, provisionar, ligar às variáveis do projeto, fazer backup e restaurar.
>
> **Relaciona-se com:** DEC-42 (PostgreSQL auxiliar), DEC-44 (conexão em partes e TLS), DEC-47 (backup e restauração), DEC-43 e DEC-46 (métricas e logs), DEC-41 (volumes), DEC-49 (mapa do repositório), DEC-51 (processos, onde o Celery precisa do Redis), HEU-06.

## Contexto

O objetivo do Cobogó é ter **cada stack do projeto dentro dele**. Hoje só o PostgreSQL é provido. O `fec` usa MySQL e Redis (Celery), e a análise nem os reconhece. O modelo já tem o campo `engine` (`Service.Engine`, `ServiceNeed.Engine`), mas cerca de 18 arquivos assumem PostgreSQL: store, worker, dockerrt, backup e analisador. A tarefa é tirar o "postgres" do meio do caminho sem mudar o comportamento dele.

## Decisão

### 1. A especificação de engine

Uma tabela fechada, em código, descreve cada engine: nome, imagem fixada, porta, diretório de dados, variáveis de inicialização, sonda de saúde, comando de dump e de restore, estilos de conexão (URL com o esquema, partes), limite de memória padrão e se suporta TLS e backup. O PostgreSQL passa a ser uma linha dessa tabela e **não muda de comportamento** (os testes atuais são a prova). Adicionar uma engine é uma linha mais o que a engine tem de próprio (init, dump, restore). O serviço passa a ser único por **projeto e engine**, então um projeto pode ter MySQL e Redis juntos (a restrição da tabela de serviços será conferida na implementação).

### 2. MySQL

- **Imagem oficial do MySQL, na versão LTS vigente** (fixada no catálogo; MariaDB entra depois como outra engine, para não misturar sem querer). Conjunto de caracteres `utf8mb4`.
- **Credenciais:** a senha do administrador do banco é gerada e nunca exposta; o banco e o usuário da aplicação nascem das variáveis de inicialização da imagem. A senha da aplicação vive na variável cifrada, como a do PostgreSQL.
- **Conexão:** estilo em partes (o que o Django e a maioria do ecossistema leem: host, porta, usuário, senha, banco) e estilo URL (`mysql://`). A família de nomes da DEC-44 se amplia com `DB_*`, `MYSQL_*` e `DATABASE_*`. O destino de cada papel vem do que o código lê (no `fec`: `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`).
- **TLS: primeira versão sem TLS.** O banco só existe na rede privada do projeto. O driver MySQL do Django não lê a CA por variável de ambiente (ela vai em `OPTIONS` no `settings.py`, que o Cobogó não edita), então o mecanismo do PostgreSQL (`PGSSLROOTCERT`) não serve. A análise diz isso. **Meta futura, a pedido do autor:** cobrir TLS para a maior parte dos casos, com caminhos por driver (arquivo de opções de cliente para quem lê, variável para quem aceita, e o que mais aparecer no corpus), numa DEC própria.
- **Memória:** o limite padrão sobe em relação ao do PostgreSQL (a ser medido: o MySQL ocioso ocupa mais), configurável pelo mesmo mecanismo de limites.

### 3. Redis

- **Imagem oficial do Redis, LTS**, com senha obrigatória (gerada) e sem porta publicada.
- **Persistência ligada por padrão** (AOF com disco do projeto): uma fila que perde as tarefas a cada reinício é pior do que o disco. Quem usa só como cache pode desligar por override. Política de memória `noeviction` quando o uso é fila.
- **Conexão:** URL `redis://:senha@host:6379/0`. Nomes lidos do código: `REDIS_URL`, `CELERY_BROKER_URL`, `BROKER_URL`, `CACHE_URL` e as partes `REDIS_HOST`/`REDIS_PORT`/`REDIS_PASSWORD`.

### 4. Detecção (HEU-06 por engine)

Uma tabela de assinaturas, por engine, alimenta a mesma lógica que hoje só vê o PostgreSQL: a configuração que fixa a engine (`ENGINE` do Django, `provider` do Prisma, `type` do TypeORM), a dependência (`mysqlclient`, `pymysql`, `mysql-connector-python`, `mysql2`, `mysql`; `redis`, `django-redis`, `celery`, `ioredis`, `bullmq`) e o esquema de uma URL (`mysql://`, `redis://`). A necessidade tem os níveis `required` e `possible`, com arquivo e linha. A pergunta de banco que existe (`database`) passa a valer por engine: `gerenciado`, `externo` ou `nenhum`.

Quando o endereço está **fixo no código** (o `redis://localhost:6379/0` do `fec`), a análise avisa que o serviço do Cobogó, que vive em outro contêiner, não será alcançado sem trocar o código para ler de variável.

### 5. Backup e restauração

- **MySQL:** contêiner passageiro da imagem do MySQL roda `mysqldump` (consistente, com rotinas e gatilhos) para o volume de backups, como o `pg_dump` hoje. A restauração faz primeiro o backup de segurança (`before_restore`, DEC-47), depois recria o banco e carrega o dump. Ao contrário do PostgreSQL, o MySQL não faz a troca de esquema numa transação única: o texto da restauração diz isso e o backup de segurança é o caminho de volta.
- **Redis:** a persistência vive num volume e entra na cópia de volumes que já existe (DEC-47 parte B).

### 6. Painel, métricas e logs

A aba **Banco** vira **Serviços**, com uma seção por engine presente. Logs e métricas seguem o que o serviço gerenciado já usa (DEC-43 e DEC-46), por rótulo e não por engine.

## Ordem de entrega

| Parte | O que entra | Prova |
| --- | --- | --- |
| **A** | A especificação de engine, com o PostgreSQL migrado para ela e **nenhum comportamento novo** | toda a suíte atual (incluindo backup, restauração e TLS do PostgreSQL) passa sem mudança |
| **B** | MySQL: detecção, pergunta, provisão, variáveis, backup e restauração | um Django com `mysqlclient` e um Node com `mysql2`, reais, com `migrate` e dados; backup, restauração e remoção |
| **C** | Redis: detecção, provisão, variáveis, volume | um Flask e um Node reais com Redis; o broker lido de variável |

Cada parte é um commit e uma subida na VM.

## Alternativas consideradas

- **Só avisar que MySQL e Redis não são suportados:** contraria o objetivo do Cobogó de ter cada stack dentro dele.
- **Um serviço genérico (qualquer imagem Docker):** flexível, mas sem backup, sem variáveis certas e sem diagnóstico; é o que o Compose faz, e o Cobogó existe para ir além disso (DEC-23).
- **MariaDB como padrão:** drop-in na maioria dos casos, mas o `fec` e a documentação dele dizem MySQL.
- **TLS obrigatório já na primeira versão:** o driver do Django o configura no código, que não editamos; adiar com a rede privada como proteção é o caminho honesto.

## Consequências e limites

- Mais imagens a manter fixadas e a baixar de antemão (o catálogo de imagens cresce).
- O MySQL consome mais memória que o PostgreSQL; o limite padrão precisa ser medido, e a VM tem teto.
- Sem TLS, um erro de rede entre contêineres do mesmo projeto não é coberto por cifra; a isolação da rede é a barreira.
- Restaurar um MySQL é menos atômico que um PostgreSQL.

## Decisões do autor (22/11/2026)

1. **MySQL oficial na versão LTS** como padrão; MariaDB como outra engine mais tarde.
2. **Primeira versão sem TLS**, dito na análise; TLS compatível com a maior parte dos casos como meta futura.
3. Pendentes na leitura: persistência do Redis ligada por padrão; a ordem A, B, C.
