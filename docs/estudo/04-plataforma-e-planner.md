# 04 — O que a plataforma decide sozinha, o planner e o percurso completo

Até aqui o motor **leu** o repositório. Esta parte mostra o que o Cobogó **faz** com o que leu: ligar as aplicações (DEC-52 parte B), gerar o Dockerfile (planner), rodar a fase de release (parte C) e o percurso do link ao site no ar.

---

## 1. Projeto = repositório (DEC-52 parte A)

- **Stack/projeto** = o repositório; **aplicações** = as partes que viram deploy (uma linha em `projects`, com `stack_id` e `role`).
- Criar o projeto a partir do link dispara a operação `scan_stack` no **worker** (`worker/stacks.go:23`). Ela clona o repositório (`gitsource.Fetch`, `61`), valida a URL contra os hosts Git permitidos (`57`) e chama `analyzer.Survey` (`78`). Cada subpasta passa pela mesma conferência do deploy: **não pode sair do clone** (`70-77`).

### Slugs (nomes e endereços) — `domain/stack.go`
- `StackSlugFromRepository` (`32-41`): o último trecho da URL, sem `.git`, reduzido a slug (`Slugify`, `16-28`: minúsculas, `[a-z0-9-]`, sem hífens duplicados, até N caracteres).
- `ProposeAppSlugs` (`47-79`): uma aplicação só → o nome do projeto. Com várias: o **primeiro frontend** fica com o nome do projeto (é o que se visita), o **primeiro backend** com `<projeto>-api`, as demais `<projeto>-<pasta>`. Sem frontend, o backend vira o que se visita (`63-66`).
- `UniqueSlug` (`102-118`): colisão (com outro slug ou nome reservado, `validate.ReservedSlug`) ganha `-2`, `-3`... respeitando o limite de 63 caracteres.
- Quem já é aplicação do projeto **mantém o slug** (`worker/stacks.go:126-131`). A identidade de uma aplicação é a **pasta mais o ecossistema fixado** (`store.AppKey`, `store/stacks.go:356-358`; usada em `worker/stacks.go:91`, `111` e `128`, em `store/stacks.go:280-292` e em `httpapi/stacks.go:179-186`): é assim que duas aplicações dividem a pasta de uma raiz Python + JavaScript (DEC-53 F). O pedido de criação (`POST /stacks/{id}/apps`) aceita `ecosystem` por aplicação (`pinned_ecosystem` da proposta), e o projeto o guarda em `projects.ecosystem` (migração `0023`).

**Por quê:** o slug é o endereço (`<slug>.<domínio>`). O que as pessoas abrem no navegador deve ter o nome limpo; a API é um detalhe.

### Ordem do deploy (`httpapi/stacks.go:252-269`)
`deployOrder`: backend (0) → fullstack (1) → app (2) → sem papel (3) → **frontend (4)**. "O que serve dados sobe antes de quem os consome". Cada aplicação ganha um deployment com chave de idempotência `<chave>:<id>`.

---

## 2. A plataforma prepara as aplicações (DEC-52 parte B)

Arquivo: `internal/httpapi/platformenv.go`. **Só a API faz isto** porque só ela cifra valores (DEC-33); o worker só lê.

`prepareStack` (`146-210`) roda **quando**: criar as aplicações, fazer deploy do projeto ou de uma aplicação, ou mudar o domínio/esquema das aplicações (`afterRoutingChange`, `277-289`). Para cada aplicação, nesta ordem:

1. **Serviços primeiro** (`177-186`): "as variáveis de conexão ficam vinculadas a eles, e o catálogo de endereços nunca as disputa".
2. **Segredos geráveis** (`187-197`): para cada variável classe `generable`, gera um valor aleatório **só se a variável ainda não existe** (`PutGeneratedIfMissing`, `store/platformenv.go:97-111`).
3. **Variáveis de endereço da plataforma** (`198-204`), se o domínio das aplicações está configurado.

**Nada que a pessoa definiu é tocado.** `ApplyPlatformEnv` (`store/platformenv.go:54-93`): uma variável de origem diferente de `platform` (digitada, importada, gerada, vinculada) é pulada (`70-72`). Editar um valor da plataforma o torna **da pessoa**; apagá-lo o **devolve** à plataforma no próximo cálculo. Os valores ficam registrados em `platform_env` com o **motivo** (para o painel mostrar "por que este valor").

### De onde vem o conhecimento do código (`knowledgeOf`, `32-44`)
Primeiro a **última análise** da aplicação; se ainda não houve deploy, a **leitura do repositório** (o `scan`). Por isso dá para preparar variáveis **antes** do primeiro build.

### O catálogo de endereços (`desiredPlatformEnv`, `53-111`)
Catálogo **fechado** de nomes (`secrets.go:329-337`). Para cada variável que o código lê, se o nome está no catálogo, a plataforma calcula o valor:

| Nomes (`secrets.go`) | Valor | Motivo mostrado |
| --- | --- | --- |
| `ALLOWED_HOSTS`, `DJANGO_ALLOWED_HOSTS` | host da própria aplicação, sem esquema nem porta | "o Django compara com o Host do pedido" |
| `CSRF_TRUSTED_ORIGINS`, `DJANGO_CSRF_TRUSTED_ORIGINS` | URL própria **+** URL dos frontends do projeto | "de onde vêm o login e os formulários" |
| `FRONTEND_URL`, `FRONTEND_ORIGIN`, `CLIENT_URL`, `WEB_URL` | URL do primeiro frontend | só se esta aplicação **não** é frontend |
| `CORS_ALLOWED_ORIGINS`, `CORS_ORIGINS` | lista dos frontends | idem |
| `VITE_API_URL`, `REACT_APP_API_URL`, `NEXT_PUBLIC_API_URL`, `VUE_APP_API_URL`, `API_URL`, `BACKEND_URL`, `PUBLIC_API_URL` | URL do primeiro backend **+** o caminho padrão que o código usa (`/api`) | só se esta aplicação **não** é backend |
| `<SLUG>_URL`, `APP_URL`, `SITE_URL`, `BASE_URL`, `PUBLIC_URL`, `NEXT_PUBLIC_SITE_URL` | URL pública da própria aplicação (`suggestableNames`, `secrets.go:260-266`) | "o endereço público desta aplicação" |

Detalhes que mostram o cuidado:
- A variável **de build** (`ClassPublic`: `VITE_...`) é marcada `BuildTime` (`80`): ela precisa estar pronta **no build**, não só na execução, porque vai para o bundle.
- O caminho `/api` vem da evidência `localhost_default` (`survey.go:128-139`): o código dizia `|| 'http://localhost:8000/api'`.
- O mesmo catálogo alimenta o fluxo **antigo** de sugestões por projeto (`pairSuggestions`, `secrets.go:361-433`), que continua existindo para repositórios com projetos separados (pares pelo mesmo `RepositoryURL`).

**Por quê um catálogo fechado:** valor de configuração errado tira o app do ar sem erro claro. Cada nome do catálogo tem semântica conhecida; um nome fora dele continua sendo da pessoa.

### Serviços sem pergunta (`autoManagedNeeds`, `116-133`; `autoManagedService`, `214-249`)

A plataforma cria o banco/Redis **sem perguntar** quando **todas** estas condições valem:

1. A necessidade é `required` (uso comprovado no código, não só a dependência).
2. A engine é gerenciada pelo Cobogó.
3. O endereço **não** está escrito no código (`FixedAt` vazio): o código lê uma variável, então ligar nosso serviço funciona.
4. Para engines relacionais: **só uma** (PostgreSQL ou MySQL, não as duas) (`123-131`). Se há duas, ficam como pergunta.
5. A aplicação **nunca teve** um serviço dessa engine (`EverHadService`, `store/platformenv.go:116-126`; para relacional, qualquer banco relacional). "Quem apagou o serviço do Cobogó decidiu por isso, e a plataforma não o recria sozinha."
6. Nenhuma variável de conexão já tem valor (`226-234`): "a pessoa já conectou o serviço por conta própria".

Então gera a senha (`secrets.Generate`), monta os valores de conexão pelo estilo detectado (URL ou partes) e chama `ChooseDatabaseFor` em modo **managed**.

**Por quê (e o que mudou):** a RN-05 original dizia "criar banco exige seleção explícita". A DEC-52 item 6 relaxou isso **só** para o caso de evidência forte e sem ambiguidade; qualquer dúvida volta para pergunta. É a tensão central do projeto: automatizar ao máximo sem decidir pelo usuário o que é dele.

---

## 3. Valores só desta execução e receitas (DEC-52 parte C)

### Valores só desta execução
Um comando pontual (aba Comandos) pode receber variáveis que valem **apenas** para aquela execução: cifradas com o `AAD = command-inputs/<id>` (`store/commands.go:60`, `86`), a API não tem `SELECT` na coluna cifrada, o worker as abre (`CommandInputs`, `105-126`), junta ao ambiente e à lista de **redação** de saída, e as apaga ao terminar (`FinishCommand`, `InterruptRunningCommands`).

`checkCommandInputs` (`httpapi/recipes.go:131-146`): no máximo 10 valores; nome `[A-Z_][A-Z0-9_]{0,63}`; `PORT` e `COBOGO_*` são da plataforma; valor até 4096 bytes, UTF-8, sem NUL.

### Receitas por evidência (`httpapi/recipes.go`)
Catálogo **fechado** de comandos conhecidos que pedem dados; cada linha declara de que evidência depende. Hoje, uma: **Criar administrador do Django** (`djangoSuperuserRecipe`, `51-112`).
- Só aparece se há `django.settings_module` e `django.contrib.auth` (`64`).
- Os campos vêm do **modelo de usuário do próprio projeto**: com `AUTH_USER_MODEL`, procura a classe com `USERNAME_FIELD` e `REQUIRED_FIELDS` (`69-88`); **se não acha o modelo, não mostra formulário** ("sem achar o modelo, não se adivinha o formulário", `85-88`). Sem modelo próprio: `username` + `email`.
- Os campos viram variáveis `DJANGO_SUPERUSER_<CAMPO>` e `DJANGO_SUPERUSER_PASSWORD` (secreta); o comando é `python manage.py createsuperuser --noinput` (`101-108`).

**Por quê a distinção "recurso geral" vs "receita":** o recurso geral (valores só desta execução) serve a qualquer stack; a receita só **preenche** o formulário quando a análise sabe qual é o comando. Evita um produto cheio de casos especiais; só entra receita verificada em projeto real.

---

## 4. O planner: do resultado da análise ao Dockerfile (`internal/planner/`)

`FromAnalysis` (`planner.go:78-146`) **só aceita análise `ready`** (`79-81`) e escolhe o gerador por perfil. É **determinístico**: o mesmo resultado dá o mesmo Dockerfile.

### P-NODE (`planner.go:150-229`)
- Build na imagem `node:<versão>` (completa: compilador e Python para módulos nativos), execução em `node:<versão>-slim` (`23-24`): "ambas são da mesma distribuição (glibc), então binários compilados no build valem na execução".
- `npm ci --omit=dev`; se há build ou modo desenvolvimento, `npm ci --include=dev` (`165-171`). **Sem lockfile e com `allow_unlocked=true` (DEC-53 H):** `npm install --no-audit --no-fund --omit=dev` (ou `--include=dev`), e o Dockerfile copia só o `package.json` (`169-171`, `202-206`). Com build, `npm prune --omit=dev` depois (`214-217`).
- **Build com o OpenSSL antigo (DEC-54 B):** com `build_legacy_openssl=true`, o `RUN` do build ganha o prefixo `NODE_OPTIONS=--openssl-legacy-provider` (`buildRun`, `planner.go:61-74`, usado em `215`; e em `static.go:145`, só com npm). É um **prefixo do comando**, nunca um `ENV`: a imagem que executa a aplicação não o recebe.
- `NODE_ENV` conforme o modo; `PATH` inclui `node_modules/.bin` (`197`) para os scripts rodarem como estão.
- Executa como usuário `node` (`225`); `COPY --chown`.
- `CMD ["sh","-c", ...]` com `ShellStart` (`227`).

### `ShellStart` (`shellstart.go:36-67`): sinal de término
Um `sh` como processo 1 **não repassa SIGTERM** ao filho; o Docker espera 10 s e mata a aplicação à força, atrasando parar, reiniciar e trocar de release (DEC-40/41). Quando é seguro, o **último comando** passa a rodar com `exec`, que troca o `sh` pela aplicação.
- Conservador: se há aspas desbalanceadas, metacaracteres de shell (`|&;<>()$` crase, barra, nova linha; `shellMeta`, `27`), ou o início é palavra reservada/`cd`/`export`/`if`/`for`... (`execBlocked`, `17-21`), **devolve o comando como veio**.
- Trata `a && b && c` (só o último recebe `exec`) e prefixos `VAR=x` (ficam antes do `exec`).
- **Por quê:** em dúvida, o pior caso é o comportamento antigo (mais lento para parar), nunca um comando quebrado.

### P-PY (`python.go`)
- Build em `python:<versão>` com venv em `/opt/venv`; execução em `python:<versão>-slim`, usuário **sem privilégio** `app` (uid 10001), sem shell de login (`327`).
- **Instalação** (`221-227`): `pip install -r requirements.txt` (ou `pip install -r <arquivo>` quando a HEU-04.1 escolheu um `requirements_file`, DEC-53 E) ou `uv sync --locked --no-dev --no-install-project`.
- **Python legado (DEC-54 A):** com `python_version` 3.8 ou 3.9 (só por confirmação), a imagem é `python:3.9`/`3.8` (a versão é validada por `PythonVersionKnown`, `domain/python.go:82-93`) e o servidor que a plataforma instala muda de versão (`pythonServer`, `planner/python.go:180-192`): `gunicorn 23.0.0` e `uvicorn 0.30.6`, porque os do catálogo exigem um Python mais novo.
- **Pasta do `manage.py`** (`264-267`, `300-303`, `330-332`; DEC-53 E): com `django_dir`, depois da instalação (feita em `/app`, onde está o arquivo de dependências) o `WORKDIR` passa a ser `/app/<pasta>` no estágio de build (o `collectstatic` roda lá) e na imagem final; o módulo de hosts da plataforma é gravado nessa pasta. `djangoDirPattern` (`planner/python.go`) recusa nomes fora de um ou dois segmentos simples.
- **Servidor de produção que a plataforma monta** (`PythonServerCommand`, `165-176`): ASGI → `exec uvicorn <alvo> --host 0.0.0.0 --port "$PORT" --proxy-headers --forwarded-allow-ips '*'` (com `--factory` se o alvo termina em `()`); WSGI → `exec gunicorn --bind "0.0.0.0:$PORT" --workers "${WEB_CONCURRENCY:-2}" --access-logfile - --forwarded-allow-ips '*' <alvo>`. Se o projeto não traz o servidor, a imagem o instala **numa versão fixada** (`gunicorn 26.2.0`, `uvicorn 0.54.0`; `283-291`) **só se `import` falha** (`python -c "import x" || pip install x==v`).
- **Pacotes de sistema**: catálogo **fechado** (`26-33`): `psycopg2→libpq5`, `mysqlclient→libmariadb3`, `python-ldap→libldap2`, `pyodbc→unixodbc`, `cairocffi→libcairo2`, `weasyprint→libpango-1.0-0`. "Nada de `apt-get` vindo do repositório" (`24-25`).
- **`BuildEnv` (`149-156`)**: no Django com comando de build (`collectstatic`), as variáveis lidas **sem padrão** no settings e o `SECRET_KEY` por variável recebem um **valor descartável** (`cobogo-build-only-not-a-secret`) **só no `RUN` do build**; "não ficam na imagem". Sem isso o settings não carrega no `collectstatic`.
- **`UVICORN_HOST` e `UVICORN_PORT`** na imagem (`263`): um `uvicorn` do Procfile sem `--host` ouviria só em 127.0.0.1.
- **Módulo de hosts da plataforma** (HEU-04.6, `333-339`, `356-376`).
- `setuptools<81` se o gunicorn é antigo (`293-299`).

### P-STATIC (`static.go`)
`staticFromAnalysis` (`39-85`): exige diretório de saída; gerenciador npm/bun/none; build com dependências de desenvolvimento num estágio e **só o diretório de saída** na imagem final `nginxinc/nginx-unprivileged:stable-alpine` (sem root, porta 8080; `11-13`). `BuildArgs` (`78-82`) = as variáveis `public` (`VITE_...`), para entrarem **no build**. A configuração do nginx é escrita na imagem (`planner/static.go:157`, `nginxConf`) e inclui o fallback de SPA quando a HEU-03 o pediu. Sem lockfile e com `allow_unlocked=true` (DEC-53 H): `npm install`/`bun install` sem copiar o lockfile (`static.go:116-120` e `128-132`).

### P-BUN (`bun.go`)
Mesma ideia do Node, com a imagem `oven/bun:<versão>` (build) e `oven/bun:<versão>-slim` (execução) (`bun.go:13-14`), e `bun install --frozen-lockfile`, com `--production` quando não há build (`bun.go:67-69`). Sem lockfile e com `allow_unlocked=true`, sem `--frozen-lockfile` e sem copiar o lockfile (`bun.go:71-73`, `101-105`).

---

## 5. A fase de release no worker (`worker/release.go:41-95`)

1. Remove restos de uma tentativa anterior (`45`).
2. Cria um contêiner **descartável** `cobogo-release-<primeiros 8 do deploy>` da **imagem nova**, na rede e com os volumes do projeto, com os **mesmos limites** de memória/CPU/PIDs da aplicação (`46-53`).
3. Acompanha até terminar, com limite de **10 minutos**; se o contexto é cancelado, para o contêiner (`66-72`).
4. **OOM** → erro claro com o limite de memória (`80-82`). **Código ≠ 0** → "terminou com o código N; **a versão anterior continua no ar**" (`83-85`). Estouro de tempo → interrompe (`88-93`).
5. A saída passa por `redact.Values(redact.Clean(...))` (`64`): valores secretos do projeto não aparecem.
6. O contêiner é removido ao fim (`59-63`), mesmo em falha.
7. Eventos de deploy: `release` e `release_done`; o plano exibe o comando.

**Por quê antes de subir a versão nova:** a aplicação nova espera o esquema novo. **Por quê descartável e com limites:** o código do usuário roda; isolamento é a regra (DEC-29).

---

## 5b. Falha de build de uma época: o diagnóstico com a correção proposta (DEC-54 C)

Quando o `docker build` falha, o worker já não devolve só "o build falhou":
1. **Guarda o final do log** (120 linhas, `buildTailLines`, `worker.go:274-275`; o laço que acumula está em `571-575`).
2. **`buildFailure`** (`worker.go:280-290`, chamada em `582`) passa o log (já limpo por `redact.Clean`) e o plano (perfil e versão do Python) a `diagnosis.Build`. Se há assinatura conhecida, a falha ganha um `detailedError` com o **final do log** e o **diagnóstico**; sem assinatura, a falha segue como antes.
3. **`diagnosis.Build`** (`diagnosis/build.go:48-89`) reconhece, **só pelo log**:

| Assinatura no log | Perfil | Confiança | Sugestão |
| --- | --- | --- | --- |
| `ERR_OSSL_EVP_UNSUPPORTED`, `error:0308010C` | P-NODE, P-STATIC | confirmada | `build_legacy_openssl=true` |
| `No module named 'distutils'` | P-PY | confirmada | `python_version=3.11` (se o atual é mais novo) |
| `Failed building wheel for X`, `Could not build wheels for X`, `Failed to build X` | P-PY | possível | o Python **logo abaixo** do atual (`olderPython`, `102-112`) |
| `No matching distribution found for X` | P-PY | possível | **nenhuma**: a causa é ambígua (release removido, índice privado, outro Python) |

   A mensagem **cita a linha do log** (`firstLine`, `30-43`, tira o prefixo `#13 1.2` do BuildKit). O tipo `Suggestion` (`diagnosis/diagnosis.go`) tem `overrides` e `label`.
4. O evento `failed` leva `diagnosis.suggestion`; o painel (`web/src/pages/Deployment.tsx`, `FailurePanel`) mostra **"Novo deploy com <rótulo>"** ao lado de "Novo deploy sem mudar". É a generalização do botão "novo deploy com esta porta" da DEC-35. **Nada é aplicado em silêncio**: a pessoa clica, e o deploy novo leva o override (a API já os aceita e valida).

**Por quê só o log decide:** uma tabela decide antes do build e pode errar; o log nomeia a causa depois dele. Por isso o `No matching distribution` não sugere um Python: no teste real (`uvicorn==0.14.0`) o Python 3.12 não resolvia, e uma sugestão errada custaria um build condenado a mais.

---

## 6. Percurso completo com o `fec` (do link ao site no ar)

Use este quadro como **roteiro da demonstração ao vivo**. Cada linha diz o que aparece na tela e qual heurística está trabalhando.

| # | O que você faz / vê | O que o Cobogó faz | Onde |
| --- | --- | --- | --- |
| 1 | Cola `github.com/ocnaibill/fec` em "Novo projeto" | Cria o projeto (slug `fec`) e enfileira `scan_stack` | `httpapi/stacks.go:40`, `worker/stacks.go:23` |
| 2 | "Lendo o repositório..." | Clona (valida host, não sai do clone), roda `Survey` | `worker/stacks.go:52-133` |
| 3 | Na raiz não há aplicação, mas há `frontend/` e a raiz Django | `describeDir`: raiz → Django REST → `backend`; `frontend/` → React+Vite → `frontend` | `components.go:146-227`, `survey.go:15-66` |
| 4 | Proposta: **fec-api** (backend) e **fec** (frontend) | Slugs: frontend com o nome do projeto, backend `-api` | `domain/stack.go:47-79` |
| 5 | Cada app mostra serviços detectados: **MySQL, Redis, Celery** | HEU-06 (drivers, `ENGINE` do Django, import de Redis) e HEU-04.7 (`Celery(...)`) | `python_services.go`, `rules_python.go:903-955` |
| 6 | Clica "Criar e publicar" | Cria as aplicações; **prepara** | `httpapi/stacks.go:141`, `platformenv.go:146` |
| 7 | Variáveis já preenchidas: `ALLOWED_HOSTS`, `CSRF_TRUSTED_ORIGINS`, `FRONTEND_URL`, `VITE_API_URL` (com `/api`), `SECRET_KEY` gerada | Catálogo de endereços + geráveis | `platformenv.go:53-111`, `envclass.go:19-23` |
| 8 | MySQL e Redis criados sem perguntar | `autoManagedNeeds` (uso forte, endereço lido de variável, uma engine relacional) | `platformenv.go:116-133` |
| 9 | Deploy: backend primeiro, depois frontend | `deployOrder` | `httpapi/stacks.go:252` |
| 10 | Linha do tempo do **backend**: leitura → plano → build (pip, collectstatic) | HEU-04.1 a 04.4; planner P-PY | `rules_python.go`, `planner/python.go` |
| 11 | Fase `release`: `python manage.py migrate --noinput` | HEU-04.8 (Django + `migrations/0001_*.py`) | `rules_python.go:962-981`, `worker/release.go` |
| 12 | Sonda de saúde passa; endereço do backend ativo | HEU-05 (porta da plataforma) + readiness | `rules_python.go:677-748` |
| 13 | Linha do tempo do **frontend**: build com `VITE_API_URL` já definido | HEU-03 (estático), `BuildArgs` | `static.go:211-211`, `planner/static.go:78-82` |
| 14 | Aba Comandos do backend → "Criar administrador" | Receita por evidência (modelo de usuário) | `httpapi/recipes.go:51-112` |
| 15 | Login em `/api/admin/` | Fecha o ciclo: tudo ligado sem tocar em variável | — |

**Frase de fechamento da demo:** "Eu só colei o link. O Cobogó descobriu que eram duas aplicações, criou o banco e o Redis, ligou os endereços, gerou o segredo, rodou a migração e publicou. Só o administrador eu criei, porque a senha é minha."
