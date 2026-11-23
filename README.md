# Project Cobogó

> **Status:** MVP em operação (painel, API, worker, coletor e backup em contêineres) | Licença: AGPLv3

Uma plataforma open-source de **_Platform as a Service_ (PaaS)** guiada por heurísticas: você informa o link de um repositório e o Cobogó analisa o código, produz um plano de deploy explicável, gera o Dockerfile, prepara os serviços de que a aplicação precisa e conduz tudo até contêineres verificados e publicados.

**Foco:** Developer Experience (DX), explicabilidade das decisões e auto-hospedagem.

## Documentação do projeto

O protótipo original em Node.js/TypeScript foi preservado na tag [`prototype-node`](https://github.com/ocnaibill/cobogo/tree/prototype-node) e removido do `main` após a primeira fatia vertical em Go entrar em funcionamento (DEC-22). O código atual implementa, em Go, o recorte M1 do documento mestre e o que veio depois:

- **Análise explicável e geração de Dockerfile**, com build e contêiner reais para os perfis Node.js/npm (P-NODE), Bun (P-BUN), site estático (P-STATIC) e Python (P-PY: pip ou uv; Flask, FastAPI, Django e outros alvos WSGI/ASGI; DEC-48).
- **O projeto é o repositório (DEC-49 a DEC-52):** um link vira uma pilha de aplicações (por exemplo, frontend e backend no mesmo repositório), com os serviços gerenciados que o código pede (PostgreSQL, MySQL e Redis; DEC-42, DEC-44 e DEC-50), processos adicionais como worker e agendador (DEC-51), as variáveis que a plataforma já conhece preenchidas e ligadas entre as aplicações, a fase de release com migrações e comandos pontuais com receitas, como criar o administrador do Django.
- **Operação:** painel React, parar/iniciar/reiniciar, volumes persistentes, variáveis e segredos guiados, logs de execução, métricas por projeto, comandos pontuais e backup e restauração, da plataforma e dos dados de cada projeto.

Documentos de referência:

- [Documento mestre: escopo, requisitos, arquitetura, telas e roadmap](docs/PROJETO-MESTRE.md)
- [Motor de heurísticas e apoio por modelos de decisão](docs/MOTOR-DE-HEURISTICAS.md)
- [Requisitos funcionais](docs/REQUISITOS-FUNCIONAIS.md), [não funcionais](docs/REQUISITOS-NAO-FUNCIONAIS.md) e [regras de negócio](docs/REGRAS-DE-NEGOCIO.md)
- [Decisões de arquitetura (DEC-19 a DEC-52)](docs/decisoes/)
- [Contrato da API (OpenAPI)](docs/api/openapi.yaml)
- [Instalação na VM](deploy/README.md)
- [Referências comentadas e trabalhos relacionados](docs/REFERENCIAS-COMENTADAS.md)
- [Pesquisa sobre heurísticas, evidências e decisão assistida](docs/pesquisa/HEURISTICAS-REFERENCIAS.md)

Os documentos incluem diagramas Mermaid e links para suas prévias SVG. O motor de heurísticas que está no código pode estar à frente do texto do documento do motor; na dúvida, vale o código.

## 💡 A identidade

O nome **Cobogó** faz referência ao elemento arquitetônico modernista, ícone de Brasília. Assim como o cobogó é um bloco modular que permite a permeabilidade da luz e ventilação sem comprometer a estrutura, esta plataforma atua como uma estrutura modular para containers, gerenciando o fluxo de tráfego e dados enquanto garante o isolamento e segurança de cada serviço.

## 🛠️ Tech stack

- **Backend:** Go, com a API, o worker, o coletor de métricas e o serviço de backup como processos separados (`cmd/api`, `cmd/worker`, `cmd/collector`, `cmd/backup`; DEC-01, DEC-02, DEC-43, DEC-47)
- **Banco de dados:** PostgreSQL, com fila de jobs via `SKIP LOCKED` (DEC-03, §12.3) e roles separados por componente (DEC-33)
- **Build e execução:** Docker Engine SDK oficial (`github.com/docker/docker`), sempre por um proxy filtrado do socket
- **Frontend:** painel em React 19 com Vite e TanStack Query, embutido no binário da API (DEC-34)
- **Entrada:** Traefik, que lê as rotas de um listener interno do Cobogó (DEC-31), e Cloudflare Tunnel na VM (DEC-32)

Ver [§12 do mestre](docs/PROJETO-MESTRE.md#12-arquitetura-proposta) para a arquitetura completa e [§17.2](docs/PROJETO-MESTRE.md#172-execução-dod-e-isolamento-entre-projetos) para a topologia de execução no homelab (DEC-19). Na VM, só o proxy do Docker monta o socket (DEC-32); no desenvolvimento fora de contêiner, o worker fala direto com o daemon Docker local.

## **🚀 Como rodar localmente (desenvolvimento)**

### **1. Pré-requisitos**

- **Go** 1.26 ou superior
- **Docker** (rodando e acessível localmente)
- **PostgreSQL** (pode ser o do `infra/docker-compose.dev.yml`)
- **Git**
- **Node.js** 22 ou superior, só para construir ou desenvolver o painel (`web/`)

### **2. Banco de dados**

```bash
docker compose -f infra/docker-compose.dev.yml up -d postgres
```

As migrations são aplicadas automaticamente pela API/worker na inicialização.

### **3. Rodando API e worker**

Antes de subir, defina a configuração obrigatória. Os valores das variáveis de ambiente dos projetos são cifrados em repouso com uma chave que fica fora do banco; API e worker precisam da mesma:

```bash
export COBOGO_SECRET_KEY="$(openssl rand -base64 32)"
export COBOGO_PUBLIC_URL="http://localhost:8080"   # a origem do painel; em produção, https://seu-dominio
export COBOGO_COOKIE_INSECURE=1                    # SÓ em desenvolvimento por HTTP; em produção o cookie é Secure
```

Guarde a chave: sem ela os valores já gravados não podem ser lidos. Outras variáveis opcionais:

- `COBOGO_TRUSTED_PROXIES`: redes de que a API aceita `CF-Connecting-IP` e `X-Forwarded-For`, por exemplo a rede do Traefik (`172.18.0.0/16`). Sem isso, atrás de um proxy ou túnel todos os clientes parecem ter o mesmo IP, e o limite de tentativas de login passa a valer para todos de uma vez; a API avisa no log quando vê esses cabeçalhos de uma origem não confiável.
- `COBOGO_EXTRA_GIT_HOSTS`: hosts de repositório além de `github.com`, `gitlab.com`, `codeberg.org`, `bitbucket.org` e `git.sr.ht`.
- Roteamento por hostname (DEC-31): `COBOGO_ROUTER_TOKEN` (24 caracteres ou mais; gere com `openssl rand -hex 32`) liga um listener **interno** (`COBOGO_ROUTER_ADDR`, padrão `:8081`) que serve ao Traefik a tabela de rotas. Sem o token o listener não sobe. **Não publique essa porta**: ela deve existir só na rede entre o Cobogó e o Traefik. O domínio das aplicações é a configuração `apps_domain` (e `apps_scheme`), editável pelo painel; `COBOGO_APPS_DOMAIN` e `COBOGO_APPS_SCHEME` são o padrão inicial.
- Política operacional, opcional (DEC-29 e DEC-30): `COBOGO_APP_MEMORY_MB` (padrão 1024), `COBOGO_APP_CPUS` (1), `COBOGO_APP_PIDS` (256) para cada aplicação publicada, `COBOGO_BUILD_MEMORY_MB` (4096) e `COBOGO_BUILD_CPUS` (2) para cada build, e `COBOGO_EXTRA_GIT_HOSTS`. Valem como **padrão inicial**: o administrador os troca pelo painel (`/api/v1/settings`) sem reiniciar, e o valor do banco sobrepõe o do ambiente. Um projeto ainda pode sobrepor os limites (`/api/v1/projects/<id>/limits`). Passar do limite de memória mata a aplicação, e a falha diz isso. Todo contêiner de aplicação também roda sem capacidades do Linux e sem poder ganhar privilégio, com log limitado a 30 MiB.

Em dois terminais, na raiz do projeto (com a chave exportada em ambos):

```bash
# Terminal 1 — API
DATABASE_URL="postgres://cobogo:devpassword@localhost:5432/cobogo_core?sslmode=disable" go run ./cmd/api
```

```bash
# Terminal 2 — Worker
DATABASE_URL="postgres://cobogo:devpassword@localhost:5432/cobogo_core?sslmode=disable" go run ./cmd/worker
```

A API sobe em `http://localhost:8080` por padrão (`API_ADDR` para trocar) e serve o painel embutido. Para o painel aparecer, construa-o antes de compilar a API (`cd web && npm ci && npm run build`); para desenvolvê-lo com recarga ao vivo, `npm run dev` dentro de `web/`.

## **🧪 Como testar (fluxo de deploy)**

O painel faz tudo o que está abaixo pela interface; esta seção mostra a mesma API com `curl` (ou outro cliente HTTP), que é o contrato descrito em [`docs/api/openapi.yaml`](docs/api/openapi.yaml). Toda a API fica em `/api/v1` e exige sessão, menos a configuração inicial e o login. Requisições de alteração (`POST`, `PUT`, `DELETE`) precisam do cabeçalho `Origin` igual a `COBOGO_PUBLIC_URL` (o navegador envia sozinho; no `curl`, passe `-H "Origin: $COBOGO_PUBLIC_URL"`) e corpo `application/json`.

```bash
API=http://localhost:8080
H=(-H "Origin: $COBOGO_PUBLIC_URL" -H 'content-type: application/json' -b /tmp/cobogo.cookies -c /tmp/cobogo.cookies)
```

### **0. Criar o administrador (só na primeira vez)**

Sem administrador cadastrado, a API imprime no log, **uma vez**, um token de configuração de uso único. Use-o para criar o administrador (a senha tem de 12 a 128 caracteres); depois disso a rota fecha:

```bash
curl "${H[@]}" -X POST $API/api/v1/setup -d '{"token": "<token do log>", "login": "admin", "password": "<uma senha longa>"}'
```

Nas próximas vezes, entre com `POST /api/v1/auth/login` (`{"login": "...", "password": "..."}`). A sessão dura 2 h sem atividade e 12 h no máximo; `POST /api/v1/auth/logout` a encerra e `POST /api/v1/auth/password` troca a senha (encerrando as outras sessões). Cinco falhas de senha do mesmo IP e login bloqueiam esse par por 15 min. Se esquecer a senha, quem tem acesso ao host a redefine com `go run ./cmd/api admin reset-password --login admin`.

### **1. Registrar um projeto**

```bash
curl "${H[@]}" -X POST $API/api/v1/projects -d '{
  "slug": "meu-app-teste",
  "repository_url": "https://github.com/exemplo/repositorio",
  "root_directory": ""
}'
```

O repositório precisa ser público e estar num host permitido. `root_directory` é o diretório do projeto dentro do repositório (`apps/web`, por exemplo); `..` e caminhos que escapam do repositório são recusados.

**O projeto é o repositório (DEC-52).** Pelo painel, basta colar o link: `POST $API/api/v1/stacks` com `{"repository_url": "..."}` cria o projeto, `POST .../stacks/<id>/scan` lê o repositório de novo, e o Cobogó propõe as aplicações que encontra (frontend e backend, por exemplo) com um nome para cada uma. `POST .../stacks/<id>/deployments` publica todas, o backend antes do frontend. A plataforma preenche e liga sozinha o que ela já sabe (endereços entre as aplicações, `ALLOWED_HOSTS` e `CSRF_TRUSTED_ORIGINS` do Django, chaves secretas de uma lista fechada), cria o PostgreSQL, o MySQL ou o Redis que o código pede quando a evidência é forte e a variável de conexão não tem valor, e roda a fase de release (`python manage.py migrate` em Django com migrações, ou o `release:` do `Procfile`). Cada decisão aparece na análise com o arquivo e a linha que a motivou. Os endpoints por aplicação abaixo continuam valendo para cada uma delas.

### **2. Disparar o deploy**

```bash
curl "${H[@]}" -X POST $API/api/v1/projects/<id>/deployments -d '{
  "idempotency_key": "tentativa-1"
}'
```

Se a análise não encontrar um valor que não pode inferir, como a porta (RN-02), o deploy para em `NEEDS_INPUT` em vez de inventar um valor. Também é possível antecipar a resposta enviando `"overrides": {"port": "3000"}` neste corpo.

### **3. Responder pendências**

```bash
curl "${H[@]}" -X POST $API/api/v1/deployments/<id>/answers -d '{
  "overrides": {"port": "3000"}
}'
```

Os campos aceitos dependem do perfil (RN-03): `port`, `start_command`, `build_command`, `execution_mode`, `package_manager`, `node_version`, `bun_version`, `output_dir`, `spa_fallback` e `health_path` no ecossistema JavaScript; `python_version`, `app_target`, `python_framework`, `django_settings`, `release_command` e os processos adicionais (`process_worker`, `process_scheduler`) em Python. A resposta da análise lista, para cada pendência, as opções e a evidência. A resposta reenfileira o **mesmo deploy** no **mesmo commit** já analisado (RF-08, RN-04), mesmo que a branch tenha avançado. Valores inválidos são recusados na hora (`400`); um deploy que não está em `NEEDS_INPUT` responde `409`.

### **4. Variáveis de ambiente do projeto**

```bash
# definir (o valor nunca é devolvido)
curl "${H[@]}" -X PUT $API/api/v1/projects/<id>/env/DATABASE_URL -d '{"value": "postgres://..."}'

# gerar um segredo que o próprio app inventa (JWT_SECRET, SESSION_SECRET, SECRET_KEY_BASE...)
curl "${H[@]}" -X POST $API/api/v1/projects/<id>/env/JWT_SECRET/generate

# listar nomes e versões, ou remover
curl "${H[@]}" $API/api/v1/projects/<id>/env
curl "${H[@]}" -X DELETE $API/api/v1/projects/<id>/env/DATABASE_URL
```

O Cobogó só gera segredos da lista fechada acima; credenciais de terceiros (`STRIPE_KEY`, `SMTP_PASSWORD`...) você informa. Gerar de novo um segredo que já existe exige `{"replace": true}`, porque invalida o que foi assinado com o anterior. Trocar um valor não altera um deploy em execução: faça um novo deploy. `PORT` é da plataforma e não pode ser definida.

A análise do deploy traz `config_variables` (classe, estado no `.env.example`, aviso e, para valores padrão não secretos, `default_value` para você confirmar) e `warnings` (por exemplo, credenciais reais encontradas no repositório).

**Importar um `.env` colado (DEC-39).** Em duas etapas, com o mesmo texto: `preview` lê o texto sem executar nem expandir nada e diz, por nome, o que aconteceria (`create`, `replace` ou `blocked`) e os avisos (placeholder, formato de credencial, valor vazio, `$VAR` que não é expandido, nome repetido). **Nunca devolve valores.** `apply` grava só os nomes que você escolher:

```bash
curl "${H[@]}" -X POST $API/api/v1/projects/<id>/env/import -d '{"mode": "preview", "text": "DATABASE_URL=postgres://...\nJWT_SECRET=..."}'
curl "${H[@]}" -X POST $API/api/v1/projects/<id>/env/import -d '{"mode": "apply", "names": ["DATABASE_URL"], "text": "DATABASE_URL=postgres://...\nJWT_SECRET=..."}'
```

**Sugestões** (`GET .../env/suggestions`): para `<SLUG>_URL`, `APP_URL`, `SITE_URL`, `BASE_URL`, `PUBLIC_URL` e `NEXT_PUBLIC_SITE_URL` que o código usa e que ainda não têm valor, o Cobogó oferece o endereço público do projeto; nada é gravado sem você enviar o `PUT`. **Variáveis obrigatórias** (`PUT .../required-env` com `{"names": [...]}`): se alguma não tiver valor, o deploy falha logo depois da análise, antes do build, dizendo quais faltam; `GET .../required-env` traz também as `likely` (prováveis) que a análise indicou. **Histórico** (`GET .../env-history`): quem mudou o quê, com versão e origem (`user`, `generated` ou `imported`), sem valor algum, nem os antigos.

Para trocar a chave de cifra (`COBOGO_SECRET_KEYS` e `COBOGO_SECRET_KEY_CURRENT`; `COBOGO_SECRET_KEY` sozinha continua valendo), rode `cobogo-api rotate-secrets` com o role dono do banco; `--check` só conta as variáveis por versão de chave. O passo a passo na VM está em [`deploy/README.md`](deploy/README.md). Rotacionar a chave não muda nenhum valor e não pede novo deploy.

**Parar, iniciar e reiniciar (DEC-40).** `POST $API/api/v1/projects/<id>/app/stop|start|restart` enfileira a operação (202) e o worker a executa; acompanhe em `GET /operations/<id>`. Parar **mantém o contêiner** e o endereço do projeto passa a mostrar uma página de "projeto inativo" (503); `app_state` do projeto vira `stopped`, que não é falha. Iniciar espera a aplicação responder antes de a rota voltar; reiniciar não relê variáveis (um novo deploy relê). Parar o que já está parado, ou iniciar o que está no ar, responde 409.

**Dados persistentes (DEC-41).** Para guardar dados entre deploys (o SQLite e as folhas do caderno, por exemplo), **você** declara os caminhos dentro do contêiner: `PUT $API/api/v1/projects/<id>/persistent-paths` com `{"paths": ["/app/data", "/app/content"]}`. Vale a partir do próximo deploy: o Cobogó cria um volume por caminho (na primeira vez ele recebe o conteúdo da imagem; depois o volume prevalece) e, em projeto com volume, **a release anterior para antes de a nova subir** (alguns segundos fora do ar); se a nova falhar, a anterior volta. `GET .../persistent-paths` lista os volumes com o tamanho aproximado. Excluir o projeto **mantém os volumes**, que passam a ser "volumes sem projeto" (`GET /api/v1/volumes/orphans`), a menos que você envie `"delete_data": true`; `DELETE /api/v1/volumes/<nome>` com `{"confirm_name": "<nome>"}` apaga um volume sem projeto (irreversível). Não vale para site estático. A análise **sugere** caminhos (`GET .../persistent-paths` traz `suggestions`, e a análise traz `persistent_suggestions`), a partir do que o código abre ou escreve; nada é aplicado sem você declarar.

**PostgreSQL auxiliar (DEC-42).** Quando o código usa PostgreSQL (evidência forte, como importar `pg` num arquivo de runtime) e a variável de conexão não tem valor, o deploy **para antes do build** e pergunta onde está o banco; a análise traz `service_needs`. `POST $API/api/v1/projects/<id>/database` com `{"choice": "managed"}` (criar um PostgreSQL do Cobogó), `"external"` (você informa a URL como variável) ou `"none"` responde à pergunta e reenfileira o deploy; **nada é criado antes da sua escolha**. No modo gerenciado, o próximo deploy cria um PostgreSQL 16 só deste projeto (256 MiB, só na rede do projeto, sem porta pública, dados em volume), espera ele ficar saudável e entrega a conexão na variável, que passa a ser `linked` (não se edita; a senha nunca é devolvida). `GET .../database` mostra o estado e o tamanho, e `DELETE .../database` com `{"confirm_slug": "<nome>"}` apaga o banco **e os dados**. O banco nasce vazio. Em projeto Python/Django com migrações no repositório, a fase de release roda `migrate` a cada deploy (DEC-52); nos demais, o Cobogó não roda migrações e a aplicação precisa criar as tabelas ao subir (ou você usa a aba Comandos). **Conexão em partes e TLS (DEC-44).** A análise detecta como o código lê a conexão: uma URL (`DATABASE_URL`) ou uma família de variáveis (`POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, também `PG`, `DB_`, `DATABASE_`), e a pergunta mostra cada variável com o nome editável; `POST .../database` aceita `{"choice": "managed", "variables": {"host": "MEU_HOST", "password": "MINHA_SENHA"}}` (papéis `url`, `host`, `port`, `user`, `password`, `database`, `ca`). O banco gerenciado novo aceita **TLS** com certificado próprio; a aplicação recebe a CA em `/etc/cobogo/db-ca.pem`, apontada por `NODE_EXTRA_CA_CERTS` e `PGSSLROOTCERT` (e na variável de CA que o código ler, em tempo de execução), então `ssl: true` valida sem mudar o código. Bancos criados antes não têm TLS e não são migrados. Limites: clientes que verificam TLS sem usar nenhum desses mecanismos não conectam (veja a DEC-44). **MySQL e Redis (DEC-50 e DEC-51).** O mesmo fluxo vale para outras engines: o código que lê uma conexão MySQL ou Redis gera a mesma pergunta de destino (e, em um projeto novo cuja evidência é forte, o serviço é criado sem perguntar, por DEC-52); o serviço gerenciado tem rede e volume próprios, sem porta pública, e entra nos backups. Um Django com Celery ganha processos adicionais (`worker` e agendador) a partir do mesmo código. MongoDB, RabbitMQ e outras engines não são gerenciados.

**Comandos pontuais (DEC-45).** Na aba "Comandos" do projeto (ou por `POST $API/api/v1/projects/<id>/commands` com `{"script": "reindex"}` ou `{"command": "ls /app/data"}`) você roda um comando no ambiente do projeto: um **contêiner descartável** da imagem da release ativa, com as variáveis do projeto, só na rede dele, sem porta pública, sem capacidades, com limite de memória (512 MiB), CPU, processos e tempo (5 min por padrão, até 10), removido ao terminar. Não é `exec` (o proxy do Docker segue com `EXEC=0`). Um por vez por projeto; a saída (256 KiB, começo e fim) e o texto saem com os valores das variáveis trocados por `[REDIGIDO]`. Como um comando enxerga as variáveis, **a senha é pedida de novo** (`POST /api/v1/auth/reauth`) e vale por 10 minutos, só naquela sessão. Os botões são os scripts do `package.json` que não são de início, build, desenvolvimento nem teste; `pause_app` para a aplicação durante o comando. Não há terminal interativo, e não vale para site estático. Limites e o que não se resolve (fuga do contêiner, saída de rede): DEC-45 e as issues #41 e #42.

**Logs de execução (DEC-46).** Na aba "Logs" do projeto (ou por `GET $API/api/v1/projects/<id>/logs`) você vê o que a **aplicação e o PostgreSQL gerenciado** imprimem depois do deploy (o log de build e de deploy continua no histórico de deploys). O worker segue o stdout e o stderr dos contêineres, **troca os valores das variáveis do projeto por `[REDIGIDO]`** (só os que o Cobogó conhece: um segredo montado ou codificado pela aplicação passa) e guarda no banco as **últimas 24 horas e até 20 000 linhas por projeto**; um limite de 200 linhas por segundo por contêiner descarta o excesso e avisa quantas. A consulta é por cursor (`after` e `before` com o `id` da linha, `origin=app|postgres`) e o painel acompanha por polling de 2 s. Fica de fora: exportar, SSE e os logs do Traefik e da plataforma.

**Backup e restauração da plataforma (DEC-47, parte A).** O serviço `backup` faz todo dia um dump consistente do banco interno (com os segredos **cifrados**) num volume, guarda **7 dias e 4 semanas** (configurável) e confere que cada arquivo abre. A página **Backups** do painel mostra o estado, avisa quando o último backup bom passa de 36 horas e **baixa os arquivos** (pede a senha de novo): a cópia que vale é a que você leva para fora da VM, porque as cópias locais não protegem da perda do disco. A `COBOGO_SECRET_KEY` **não** entra no backup e se guarda à parte; é a única coisa do `.env` que não se refaz. `deploy/update.sh` atualiza com um backup antes, e `deploy/restore.sh` restaura numa VM limpa (gerando um `.env` novo) e prova que serviu com `cobogo-api rotate-secrets --verify`. **Backup dos dados de cada projeto (DEC-47, parte B).** Na aba **Dados** de um projeto, "Fazer backup agora" (ou `POST $API/api/v1/projects/<id>/backups` com `{"pause_app": false}`) copia os **volumes declarados** (um `tar.gz` de cada, lido somente leitura por um contêiner descartável) e o **PostgreSQL gerenciado** (`pg_dump`, nunca o arquivo em uso) para o volume de backups, com `manifest.json` e somas SHA-256. A opção "pausar a aplicação" fecha a janela em que um SQLite em uso poderia ser copiado no meio de uma escrita; vem desligada. O backup falha inteiro se qualquer parte falhar. Guarda os últimos 3 por projeto (configurável). **Baixar** entrega um `.tar` (com a senha de novo); cada dono baixa os backups dos seus projetos (subir um backup pelo painel ainda não existe: atrás de um túnel da Cloudflare o corpo é limitado a 100 MB; a alternativa é copiar o arquivo para o volume de backups, issue #43). Os dados não são cifrados e as variáveis de ambiente não entram (estão no backup da plataforma). A exclusão do projeto ou do banco pede **um backup antes** (ou "apagar sem backup"), e excluir o projeto apaga os backups dele: baixe antes. **Restaurar (parte C).** Em cada backup bom, "Restaurar…" substitui o conteúdo atual dos volumes e do banco pelo do backup (`POST .../backups/<id>/restore` com `{"confirm_slug": "<nome>"}`; pede a senha de novo e o nome do projeto). O Cobogó faz antes um **backup de segurança** do estado atual (se ele falhar, nada é tocado), confere o tamanho e a soma de cada arquivo (corrompido: recusa sem alterar nada), pausa a aplicação, troca cada volume sem passar por estado pela metade e o banco numa transação só, e inicia a aplicação de novo se estava no ar. Caminhos que o projeto não declara mais são ignorados e ditos; volumes nunca são criados nem apagados à revelia.

**Recursos e saúde dos contêineres (DEC-43).** Um coletor próprio (`cmd/collector`, serviço `collector` do compose) lê o Docker por um proxy **só de leitura** e grava, a cada 5 s, o estado, os reinícios, a CPU (em núcleos) e a memória (sem o cache) de cada contêiner do Cobogó, com o seu role do banco, que só escreve as amostras. `GET $API/api/v1/projects/<id>/metrics?window=15m|1h|6h|24h|7d` traz os contêineres, a série da janela (lacunas e valores sem leitura são `null`, nunca zero), os marcadores de deploy e de reinício e a **sonda de rota** (uma requisição `GET /` pelo Traefik, a cada 15 s); `GET /api/v1/metrics/overview` agrega o consumo. Uma leitura com mais de três intervalos é **antiga** e a saúde vira desconhecida. Docker stats mede o contêiner, não a aplicação: não há latência de usuários nem taxa de erro. No ambiente local sem contêineres, rode `go run ./cmd/collector` com o `DATABASE_URL` e o Docker local.

### **5. Acompanhar o progresso**

O contrato completo está em [`docs/api/openapi.yaml`](docs/api/openapi.yaml) (OpenAPI 3; cole-o no Swagger Editor ou gere um cliente a partir dele). Para acompanhar um deploy:

```bash
# resumo: estado, commit, motivo da falha e, ao suceder, a release (sem eventos)
curl "${H[@]}" $API/api/v1/deployments/<id>

# eventos novos desde a sequência N (comece em 0; use next_after na próxima consulta)
curl "${H[@]}" "$API/api/v1/deployments/<id>/events?after=0&limit=200"

# o que a análise encontrou, e o plano com o Dockerfile (só com a análise pronta)
curl "${H[@]}" $API/api/v1/deployments/<id>/analysis
curl "${H[@]}" $API/api/v1/deployments/<id>/plan

# listas, do mais novo ao mais antigo (limit até 100; before = next_before da página anterior)
curl "${H[@]}" "$API/api/v1/projects?limit=20"
curl "${H[@]}" "$API/api/v1/projects/<id>/deployments"
```

Consulte `/events` a cada 2 a 3 segundos e **pare quando `settled` for `true` e `has_more` for `false`**: o estado final do deploy é gravado antes do último evento, então olhar só o estado perde a mensagem de falha. Um deploy em `NEEDS_INPUT` fica `settled` até ser respondido.

Um deploy que ainda não executou (`QUEUED` ou `NEEDS_INPUT`) pode ser cancelado; depois que o worker o pegou, não:

```bash
curl "${H[@]}" -X POST $API/api/v1/deployments/<id>/cancel
```

Ao suceder, a `release` traz a `url` pública da aplicação (roteamento por hostname, seção 7) e a porta publicada no host para acesso direto ao contêiner, em desenvolvimento.

**Verificação (readiness):** a aplicação passa quando responde três vezes seguidas, em até 120 s, com qualquer status HTTP abaixo de 500 (redirecionamentos não são seguidos). O evento `verifying/probe` registra caminho e status e avisa se não foi 2xx. Para sondar outra rota, informe `health_path` (por exemplo `{"overrides": {"health_path": "/healthz"}}`).

**Quando falha:** o evento `failed` traz o código de saída, as últimas 50 linhas do log do contêiner (com os valores das variáveis do projeto trocados por `[REDIGIDO]`) e um `diagnosis`. Ele diz `confirmed` quando o log reclama de uma variável sem valor, `possible` quando há variáveis sem valor que o código usa, e `unknown` caso contrário. O Cobogó não afirma uma causa sem o nome dela no log.

**Job interrompido:** se o worker cair, outro retoma o deploy do início, no mesmo commit, depois que o lease (60 s) vencer. Com `SIGTERM` o job volta à fila na hora. Depois de 3 tentativas interrompidas o deploy falha.

### **6. Configurações e limites de recursos**

```bash
# política operacional: valor efetivo, origem (database, env, default), padrão e faixa
curl "${H[@]}" $API/api/v1/settings
curl "${H[@]}" -X PUT $API/api/v1/settings/build_memory_mb -d '{"value": 8192}'
curl "${H[@]}" -X DELETE $API/api/v1/settings/build_memory_mb   # volta ao ambiente ou ao padrão
curl "${H[@]}" -X PUT $API/api/v1/settings/extra_git_hosts -d '{"value": ["git.exemplo.com"]}'

# sobreposição por projeto (campo ausente herda; {} limpa)
curl "${H[@]}" -X PUT $API/api/v1/projects/<id>/limits -d '{"app_memory_mb": 2048}'
curl "${H[@]}" $API/api/v1/projects/<id>/limits
```

Limites valem a partir do **próximo deploy**; um contêiner em execução mantém os que tinha. Hosts de repositório valem na hora. Banco, chave, origem pública e proxies confiáveis só mudam pelo ambiente. Se uma aplicação for morta por falta de memória, o evento `failed` traz `oom_killed` e um diagnóstico `confirmed` com o limite que valia.

### **7. Endereço público das aplicações (desenvolvimento)**

O Traefik de desenvolvimento lê as rotas do listener interno do Cobogó e não acessa o Docker:

```bash
export COBOGO_ROUTER_TOKEN="$(openssl rand -hex 32)"      # o mesmo para a API e para o Traefik
docker network create cobogo-ingress 2>/dev/null || true
docker compose -f deploy/dev/traefik.yml up -d
# suba a API e o worker com COBOGO_ROUTER_TOKEN exportado, e então:
curl "${H[@]}" -X PUT $API/api/v1/settings/apps_domain -d '{"value": "localhost:8088"}'
curl "${H[@]}" -X PUT $API/api/v1/settings/apps_scheme -d '{"value": "http"}'
```

A partir do próximo deploy, a aplicação do projeto `meu-app` responde em `http://meu-app.localhost:8088/` e a `release` traz essa `url`. A rota muda até 2 s depois de o deploy concluir. A release anterior continua até a nova passar na sonda de prontidão; depois que a rota troca, uma faxina aposenta as releases antigas e preserva a ativa, a anterior protegida e os volumes (DEC-38).

### **8. Pilha completa em contêineres (D0)**

Para testar a pilha inteira na própria máquina, sem túnel (DEC-32): API, worker, PostgreSQL, Traefik e o proxy do Docker, cada um no seu contêiner, só o proxy com o socket:

```bash
cp deploy/.env.example deploy/.env.local     # e preencha; o Git ignora este arquivo
# em .env.local: COBOGO_PUBLIC_URL=http://localhost:8088  COBOGO_APPS_DOMAIN=localhost:8088  COBOGO_APPS_SCHEME=http
# e as senhas (POSTGRES_PASSWORD, COBOGO_OWNER_PASSWORD, COBOGO_API_PASSWORD, COBOGO_WORKER_PASSWORD): openssl rand -hex 24
docker compose --env-file deploy/.env.local -f deploy/compose.yml -f deploy/compose.local.yml up -d --build
```

O painel responde em `http://localhost:8088` (pelo Traefik) e a API também em `127.0.0.1:18080`. Na VM, use só `deploy/compose.yml`, que não publica porta nenhuma, e o perfil `tunnel` para o `cloudflared`. O PostgreSQL da pilha tem roles separados (DEC-33): o serviço `migrate` roda uma vez, com o role dono do esquema, aplica as migrations e os privilégios, e só então a API (`cobogo_api`) e o worker (`cobogo_worker`) sobem. Eles não migram (`COBOGO_AUTO_MIGRATE=0`): conferem que o esquema é o esperado e param com uma mensagem clara se não for. No desenvolvimento fora de contêiner nada muda: um usuário e migração automática (`COBOGO_AUTO_MIGRATE` vale 1). Para aplicar migrations e privilégios à mão: `DATABASE_URL=<role dono> go run ./cmd/api migrate`. O roteiro de instalação na VM, com o túnel da Cloudflare, o backup diário e a atualização, está em [`deploy/README.md`](deploy/README.md).

## Testes automatizados

```bash
go test ./...
```

Os testes de integração com PostgreSQL (`store`, `httpapi`) usam um schema isolado por teste e são pulados se `COBOGO_TEST_DATABASE_URL` não estiver definida. Com o banco do passo 2 no ar:

```bash
COBOGO_TEST_DATABASE_URL="postgres://cobogo:devpassword@localhost:5432/cobogo_core?sslmode=disable" go test ./...
```

## Licença

Este projeto é licenciado sob a [AGPLv3](LICENSE).
