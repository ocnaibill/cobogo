# DEC-50 — Serviços de dados por engine: MySQL e Redis dentro do Cobogó

> **Estado:** direção aprovada pelo autor em 22/11/2026 (MySQL oficial na versão LTS, primeira versão sem TLS); **partes A (a especificação por engine), B (MySQL gerenciado) e C (Redis gerenciado) implementadas**.
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

## Implementação da parte A (23/11/2026)

- **`dockerrt.Engine` (`internal/dockerrt/engine.go`):** a especificação de uma engine: imagem fixada, diretório de dados, porta, usuário e banco da aplicação, uid dos contêineres de dump e de restauração, limites, capacidades, teste de saúde, variáveis de inicialização (a senha só aqui), gancho de TLS (nulo quando a engine não o suporta), variáveis do cliente, comando de dump e script de restauração. O PostgreSQL é a única linha (`postgresEngine`), com exatamente os valores que o runtime usava antes; `EngineFor` recusa o que o Cobogó não provê.
- **O ciclo de vida é um só:** `EnsurePostgres`, `RemovePostgres`, `DumpPostgres` e `RestorePostgres` viram `EnsureService`, `RemoveService`, `DumpService` e `RestoreService`, que leem a engine (`ServiceSpec` ganha o campo `Engine`). Os contêineres descartáveis que só copiam volumes usam a `UtilityImage` (a do PostgreSQL, já baixada e fixada). O worker passa `svc.Engine` em vez de assumir PostgreSQL, e o diretório de dados do volume vem da engine.
- **Nomes por engine, com os do PostgreSQL intactos:** `domain.ServiceContainerNameFor` e `ServiceVolumeNameFor` usam o código curto e o diretório de dados de cada engine; os nomes do PostgreSQL (`cobogo-pg-<projeto>` e o volume por hash do caminho) existem em produção e são fixados por teste. A escolha do destino do banco (`ChooseDatabase`) ainda é do PostgreSQL, com a engine num único ponto para a parte B trocar.
- **Prova:** testes novos fixam os valores do PostgreSQL (imagem, caminho, porta, uid, saúde, variáveis, dump, restauração em transação única, nomes) e a coerência entre o domínio e o runtime; a suíte completa, com banco e Docker (provisão, TLS, backup, restauração e remoção do PostgreSQL gerenciado, de verdade), passa sem mudança de comportamento.

## Implementação da parte B (23/11/2026)

- **A engine (`mysqlEngine`):** a imagem oficial `mysql:8.4` (LTS) fixada por digest; `utf8mb4`; 512 MiB e 1 CPU; saúde por `mysqladmin ping` no TCP (na inicialização o servidor temporário só ouve no socket); senha do administrador **aleatória e desconhecida** (`MYSQL_RANDOM_ROOT_PASSWORD`), a aplicação usa o usuário `app`, dono só do banco `app`; dump por `mysqldump --single-transaction --routines --triggers`; restauração que confere a marca de término do dump, recria o banco e carrega (o MySQL não troca o esquema numa transação só; o backup de segurança é o caminho de volta). **Sem TLS**, dito na análise e no painel.
- **Autenticação:** o plugin `mysql_native_password` fica ligado e é o padrão dos usuários novos (`--mysql-native-password=ON --authentication-policy=mysql_native_password`). O padrão do 8.4, `caching_sha2_password`, exige uma troca de chave sem TLS que muitos drivers não fazem; o `mysqlclient` (libmariadb) e o `mysql2` autenticaram nos testes reais. É uma troca de compatibilidade por segurança de senha, aceitável com o banco só na rede privada do projeto; entra na revisão quando o TLS chegar.
- **Detecção e pergunta:** a HEU-06 vê MySQL e MariaDB por driver (`mysqlclient`, `pymysql`, `mysql-connector-python`, `aiomysql`, `asyncmy`, `mysql2`, `mysql`), por import em runtime, pelo `ENGINE` do Django, pelo `provider` do Prisma e pelo `type`/`dialect` de ORMs; a conexão em partes (`DB_*`, `MYSQL_*`) ou em URL (`DATABASE_URL`, `MYSQL_URL`, `JAWSDB_URL`…). A pergunta de destino (`database`) é da engine que a análise mais sustenta, com o `label` dela e as três opções (gerenciado, externo, nenhum). O mapa do repositório mostra o MySQL como provido.
- **API e dados:** `POST /projects/{id}/database` aceita `engine` (padrão: a da necessidade achada, ou PostgreSQL); a URL gerada leva o esquema e a porta da engine; um projeto tem **um banco gerenciado** (outra engine é recusada com `managed_exists`). `GetDatabaseService` substitui as buscas fixas em PostgreSQL no store, na API e no worker. A migração 0018 estende as restrições de engine do serviço e de origem dos logs.
- **Backup e restauração:** o item `mysql` (`database.sql`, versão `8.4` no manifest); a restauração recusa um dump de outra engine ou versão antes de tocar em qualquer coisa. Logs: origem `mysql`, e o filtro "Banco" do painel reúne as origens de banco.
- **Painel:** a aba Banco diz a engine, os textos e o aviso "sem TLS" são os da engine, e a escolha envia a engine.
- **Verificação:** testes do analisador, da API e do store; o **MySQL gerenciado de verdade** (provisão, usuário `app` por `mysql_native_password`, `utf8mb4`, root fechado para a senha da aplicação, backup, restauração com a tabela posterior sumindo e acentos preservados, dump truncado recusado sem mexer no banco); um **Django com `mysqlclient` e um Node com `mysql2`**, construídos pelo planner, com `migrate` e leitura e escrita com acentos no MySQL gerenciado; o `fec` na pilha local completa: pergunta com a evidência (`backend/settings.py:105`), escolha pelo painel, MySQL no ar, `DB_*` vinculadas ao app, `manage.py check --database default` conectando de verdade, release `healthy` e os logs do banco com origem `mysql`.

## Implementação da parte C (23/11/2026)

- **A engine (`redisEngine`):** `redis:8-alpine` fixada por digest; senha obrigatória (por ambiente, nunca na linha de comando visível do servidor: o script lê `$REDIS_PASSWORD`); persistência AOF (`everysec`) no volume `/data`; `--maxmemory 160mb` com `noeviction` (um broker nunca descarta uma tarefa para abrir espaço: a memória cheia vira erro de escrita); 256 MiB; sem TLS; o servidor inicia pelo entrypoint da imagem, que larga o root. **Sem dump:** a persistência é o volume, filas e caches são transitórios, e o backup do projeto não inclui o Redis (dito no painel; uma restauração por dump de Redis é recusada).
- **O Redis convive com o banco relacional:** é um serviço independente do mesmo projeto (`GetManagedService` por engine; o banco relacional segue sendo um só). A pergunta de destino é própria (`redis`), a API usa `engine` (`GET /database?engine=redis`, `POST` e `DELETE` com `engine`), a conexão sai como URL com só a senha (`redis://:senha@host:6379/0`) ou em partes (host, porta, senha). A migração 0019 estende as restrições de engine e de origem de log.
- **Heurística (HEU-06 para Redis, `internal/analyzer/redis.go`):** evidência forte (o import do cliente, o backend de cache do Django, o endereço `redis://` escrito no código, o **Celery com o cliente do Redis nas dependências**, achada pelo teste real), só a dependência é sugestão, o Celery sozinho não é Redis. **Diz como o código chega ao serviço:** lendo uma variável (o Cobogó liga a do Redis gerenciado) ou com o endereço escrito no código (`FixedAt`, com arquivo e linha; a pergunta avisa que o serviço do Cobogó só será usado se o código passar a ler uma variável). Nunca guarda nem mostra uma senha escrita no código.
- **Painel:** a aba Serviços mostra o Redis ao lado do banco; a pergunta do deploy tem o cartão do Redis; os logs do banco e dos serviços compartilham o filtro "Serviços".
- **Verificação:** testes do analisador (JS e Python, endereço fixo sem senha, partes `REDIS_*`, Celery), da API e o **Redis gerenciado de verdade** (sem senha: `NOAUTH`; senha errada: `WRONGPASS`; AOF ligado; `noeviction`; sem porta publicada; os dados sobrevivem a um reinício do contêiner).

