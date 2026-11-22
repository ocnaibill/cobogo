# Project Cobogó

> **Status:** M1 em Go (fatia vertical inicial) | Licença: AGPLv3

Uma plataforma open-source de **_Platform as a Service_ (PaaS)** guiada por heurísticas: analisa um repositório, produz um plano de deploy explicável, gera o Dockerfile e conduz a aplicação até um contêiner verificado.

**Foco:** Developer Experience (DX), explicabilidade das decisões e auto-hospedagem.

## Documentação do projeto

O protótipo original em Node.js/TypeScript foi preservado na tag [`prototype-node`](https://github.com/ocnaibill/cobogo/tree/prototype-node) e removido do `main` após a primeira fatia vertical em Go entrar em funcionamento (DEC-22). O código atual implementa o recorte M1 do documento mestre: um perfil (Node.js/npm), análise explicável, geração de Dockerfile, build e contêiner reais.

- [Documento mestre: escopo, requisitos, arquitetura, telas e roadmap](docs/PROJETO-MESTRE.md)
- [Motor de heurísticas e apoio por modelos de decisão](docs/MOTOR-DE-HEURISTICAS.md)
- [Referências comentadas e trabalhos relacionados](docs/REFERENCIAS-COMENTADAS.md)
- [Pesquisa sobre heurísticas, evidências e decisão assistida](docs/pesquisa/HEURISTICAS-REFERENCIAS.md)

Os documentos incluem diagramas Mermaid e links para suas prévias SVG.

## 💡 A identidade

O nome **Cobogó** faz referência ao elemento arquitetônico modernista, ícone de Brasília. Assim como o cobogó é um bloco modular que permite a permeabilidade da luz e ventilação sem comprometer a estrutura, esta plataforma atua como uma estrutura modular para containers, gerenciando o fluxo de tráfego e dados enquanto garante o isolamento e segurança de cada serviço.

## 🛠️ Tech stack

- **Backend:** Go, API e worker como processos separados (DEC-01, DEC-02)
- **Banco de dados:** PostgreSQL, com fila de jobs via `SKIP LOCKED` (DEC-03, §12.3)
- **Build e execução:** Docker Engine SDK oficial (`github.com/docker/docker`)
- **Frontend:** React (ainda não iniciado; chega em M2)

Ver [§12 do mestre](docs/PROJETO-MESTRE.md#12-arquitetura-proposta) para a arquitetura completa e [§17.2](docs/PROJETO-MESTRE.md#172-execução-dod-e-isolamento-entre-projetos) para a topologia de execução prevista no homelab (DEC-19). Em M1, o worker fala direto com o daemon Docker local; os três proxies filtrados entram ao implantar na VM do Proxmox.

## **🚀 Como rodar localmente (desenvolvimento)**

### **1. Pré-requisitos**

- **Go** 1.25 ou superior
- **Docker** (rodando e acessível localmente)
- **PostgreSQL** (pode ser o do `infra/docker-compose.dev.yml`)
- **Git**

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

A API sobe em `http://localhost:8080` por padrão (`API_ADDR` para trocar).

## **🧪 Como testar (fluxo de deploy)**

Ainda sem frontend (chega em M2); use `curl` ou um cliente HTTP. Toda a API fica em `/api/v1` e exige sessão, menos a configuração inicial e o login. Requisições de alteração (`POST`, `PUT`, `DELETE`) precisam do cabeçalho `Origin` igual a `COBOGO_PUBLIC_URL` (o navegador envia sozinho; no `curl`, passe `-H "Origin: $COBOGO_PUBLIC_URL"`) e corpo `application/json`.

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

Os campos aceitos são `port`, `start_command` e `build_command` (RN-03). A resposta reenfileira o **mesmo deploy** no **mesmo commit** já analisado (RF-08, RN-04), mesmo que a branch tenha avançado. Valores inválidos são recusados na hora (`400`); um deploy que não está em `NEEDS_INPUT` responde `409`.

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

**PostgreSQL auxiliar (DEC-42).** Quando o código usa PostgreSQL (evidência forte, como importar `pg` num arquivo de runtime) e a variável de conexão não tem valor, o deploy **para antes do build** e pergunta onde está o banco; a análise traz `service_needs`. `POST $API/api/v1/projects/<id>/database` com `{"choice": "managed"}` (criar um PostgreSQL do Cobogó), `"external"` (você informa a URL como variável) ou `"none"` responde à pergunta e reenfileira o deploy; **nada é criado antes da sua escolha**. No modo gerenciado, o próximo deploy cria um PostgreSQL 16 só deste projeto (256 MiB, só na rede do projeto, sem porta pública, dados em volume), espera ele ficar saudável e entrega a conexão na variável, que passa a ser `linked` (não se edita; a senha nunca é devolvida). `GET .../database` mostra o estado e o tamanho, e `DELETE .../database` com `{"confirm_slug": "<nome>"}` apaga o banco **e os dados**. O banco nasce vazio e o Cobogó não roda migrações. **Conexão em partes e TLS (DEC-44).** A análise detecta como o código lê a conexão: uma URL (`DATABASE_URL`) ou uma família de variáveis (`POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, também `PG`, `DB_`, `DATABASE_`), e a pergunta mostra cada variável com o nome editável; `POST .../database` aceita `{"choice": "managed", "variables": {"host": "MEU_HOST", "password": "MINHA_SENHA"}}` (papéis `url`, `host`, `port`, `user`, `password`, `database`, `ca`). O banco gerenciado novo aceita **TLS** com certificado próprio; a aplicação recebe a CA em `/etc/cobogo/db-ca.pem`, apontada por `NODE_EXTRA_CA_CERTS` e `PGSSLROOTCERT` (e na variável de CA que o código ler, em tempo de execução), então `ssl: true` valida sem mudar o código. Bancos criados antes não têm TLS e não são migrados. Limites: só PostgreSQL, e clientes que verificam TLS sem usar nenhum desses mecanismos não conectam (veja a DEC-44).

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

Ao suceder, a `release` traz a porta publicada no host para acesso direto ao contêiner (a URL pública vem com o roteamento, ainda não implementado).

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

A partir do próximo deploy, a aplicação do projeto `meu-app` responde em `http://meu-app.localhost:8088/` e a `release` traz essa `url`. A rota muda até 2 s depois de o deploy concluir. Os contêineres de releases anteriores continuam rodando, sem receber tráfego, até a atualização com release preservada (parte 5c).

### **8. Pilha completa em contêineres (D0)**

Para testar a pilha inteira na própria máquina, sem túnel (DEC-32): API, worker, PostgreSQL, Traefik e o proxy do Docker, cada um no seu contêiner, só o proxy com o socket:

```bash
cp deploy/.env.example deploy/.env.local     # e preencha; o Git ignora este arquivo
# em .env.local: COBOGO_PUBLIC_URL=http://localhost:8088  COBOGO_APPS_DOMAIN=localhost:8088  COBOGO_APPS_SCHEME=http
# e as senhas (POSTGRES_PASSWORD, COBOGO_OWNER_PASSWORD, COBOGO_API_PASSWORD, COBOGO_WORKER_PASSWORD): openssl rand -hex 24
docker compose --env-file deploy/.env.local -f deploy/compose.yml -f deploy/compose.local.yml up -d --build
```

O painel responde em `http://localhost:8088` (pelo Traefik) e a API também em `127.0.0.1:18080`. Na VM, use só `deploy/compose.yml`, que não publica porta nenhuma, e o perfil `tunnel` para o `cloudflared`. O PostgreSQL da pilha tem roles separados (DEC-33): o serviço `migrate` roda uma vez, com o role dono do esquema, aplica as migrations e os privilégios, e só então a API (`cobogo_api`) e o worker (`cobogo_worker`) sobem. Eles não migram (`COBOGO_AUTO_MIGRATE=0`): conferem que o esquema é o esperado e param com uma mensagem clara se não for. No desenvolvimento fora de contêiner nada muda: um usuário e migração automática (`COBOGO_AUTO_MIGRATE` vale 1). Para aplicar migrations e privilégios à mão: `DATABASE_URL=<role dono> go run ./cmd/api migrate`. O roteiro de instalação na VM vem na próxima parte.

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
