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

### **5. Acompanhar o plano e os eventos**

```bash
curl "${H[@]}" $API/api/v1/deployments/<id>
```

Ao suceder, a resposta inclui a `release` com a porta publicada no host para acesso direto ao contêiner.

**Verificação (readiness):** a aplicação passa quando responde três vezes seguidas, em até 120 s, com qualquer status HTTP abaixo de 500 (redirecionamentos não são seguidos). O evento `verifying/probe` registra caminho e status e avisa se não foi 2xx. Para sondar outra rota, informe `health_path` (por exemplo `{"overrides": {"health_path": "/healthz"}}`).

**Quando falha:** o evento `failed` traz o código de saída, as últimas 50 linhas do log do contêiner (com os valores das variáveis do projeto trocados por `[REDIGIDO]`) e um `diagnosis`. Ele diz `confirmed` quando o log reclama de uma variável sem valor, `possible` quando há variáveis sem valor que o código usa, e `unknown` caso contrário. O Cobogó não afirma uma causa sem o nome dela no log.

**Job interrompido:** se o worker cair, outro retoma o deploy do início, no mesmo commit, depois que o lease (60 s) vencer. Com `SIGTERM` o job volta à fila na hora. Depois de 3 tentativas interrompidas o deploy falha.

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
