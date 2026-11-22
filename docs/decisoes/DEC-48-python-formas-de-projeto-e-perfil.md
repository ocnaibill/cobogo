# DEC-48 — Python: as formas de projeto, o perfil e a ordem de entrega

> **Estado:** aprovada pelo autor em 22/11/2026, com as respostas ao fim (a única mudança: as versões incluem 3.10 e 3.11); **fase 1 implementada** (ver "Implementação da fase 1"); fases 2 e 3 abertas.
>
> **Escopo:** como o Cobogó reconhece, planeja e publica uma aplicação Python **sem executar nada do repositório na análise**, cobrindo as formas em que projetos Python aparecem (API de um arquivo, pacote, Django, Django com frontend, monorepo, aplicações com servidor próprio) e dizendo com clareza o que fica de fora. Fecha a pergunta aberta AB-06 do mestre ("FastAPI, Flask ou Django?").
>
> **Relaciona-se com:** mestre §7 (perfis P-PY-ASGI e P-PY-WSGI, RF-11, OBJ-03), motor HEU-04 (alvos ASGI/WSGI), HEU-05 (porta), HEU-06 e DEC-42/44 (banco), HEU-09 (configuração), DEC-23 (motor), DEC-24 (overrides e perguntas), DEC-25/36 (versão de runtime), DEC-37 (estático e build args), DEC-39 (sugestões de variáveis), DEC-41 (volumes), DEC-45 (comandos), issue #39 (vários repositórios).

## Contexto

O mestre prevê Python no MVP com dois perfis (P-PY-ASGI e P-PY-WSGI, `requirements.txt` e alvo inequívoco), deixa Django, Poetry e uv para depois e proíbe **importar módulos para descobrir o `app`** (importar executa código). O autor pediu que Python seja tratado com cuidado de estrutura: um repositório Python pode ser uma API de um arquivo, um Django completo, um Django com React no mesmo repositório, um monorepo, um bot ou um notebook, e o Cobogó precisa reconhecer cada forma pelo que ela é, não por um palpite como "rodar `python app.py`".

Plataformas de referência resolvem isso por arquivos-marcadores e comandos-padrão por framework. O Railpack reconhece pip, Poetry, PDM, uv e Pipenv pelos arquivos de cada um, escolhe a versão por `.python-version`/`runtime.txt` (padrão 3.13), monta comandos fixos por framework (gunicorn para Flask, uvicorn para FastAPI, gunicorn no módulo WSGI para Django, achado por `WSGI_APPLICATION`) e mapeia algumas dependências para pacotes do sistema (o driver do PostgreSQL ganha `libpq` no build e no runtime). O Heroku reconhece `requirements.txt`, `poetry.lock`, `uv.lock` e `Pipfile.lock`, cada um com o seu comando de instalação, e o processo vem do `Procfile`. As duas rodam `migrate` do Django por conta própria em algum caminho; o Cobogó **não** (RN-13).

O Cobogó tem três diferenças que guiam o desenho: (1) a análise é **explicável** (cada valor vem de uma evidência com arquivo e linha, ou de uma pergunta); (2) **pergunta em vez de adivinhar** (DEC-24); (3) um projeto é **um processo** web por deploy. Monorepos já se resolvem com o diretório do projeto (`root_directory`).

## As formas de projeto Python

| # | Forma | Como se reconhece (sem executar) | O que o Cobogó faz |
| --- | --- | --- | --- |
| F-1 | **API ou app de um módulo** (Flask, FastAPI, Starlette, Quart, Litestar, Falcon) | dependência do framework + uma atribuição de módulo como `app = FastAPI(...)`, `app = Flask(__name__)` | alvo `modulo:app`, servidor de produção pela interface (WSGI → gunicorn, ASGI → uvicorn) |
| F-2 | **Pacote ou fábrica** (`src/` ou pacote com `__init__.py`, `def create_app()`) | a mesma atribuição dentro de um pacote; uma função fábrica | alvo `pacote.modulo:app` ou `pacote:create_app()` (gunicorn) / `--factory` (uvicorn) |
| F-3 | **Django** | `manage.py` + Django nas dependências; `DJANGO_SETTINGS_MODULE` literal no `manage.py`; `WSGI_APPLICATION`/`ASGI_APPLICATION` literal no settings | alvo `projeto.wsgi:application` (ou ASGI com Channels); contrato próprio de static, hosts e migração (item 9) |
| F-4 | **Django servindo um frontend construído** (React/Vite dentro do repositório, saída nos estáticos do Django; `django-vite`, `django-webpack-loader`) | `package.json` numa subpasta + o settings apontando para a saída do build | **depois** (fase 3): build em dois estágios, Node e Python; até lá, a pergunta explica e oferece separar |
| F-5 | **Monorepo com backend e frontend independentes** (`backend/` Python e `frontend/` React) | marcadores de cada ecossistema em pastas diferentes | **já funciona** como dois projetos com o diretório de cada um; o Cobogó diz isso e o que ligar entre eles (URL pública da API no frontend, CORS e hosts no backend) |
| F-6 | **Os dois ecossistemas na mesma pasta** (`package.json` e `requirements.txt` lado a lado) | marcadores dos dois na raiz analisada | **pergunta**: qual é a aplicação desta pasta, com a opção de usar outro diretório |
| F-7 | **Servidor próprio** (Streamlit, Gradio, aiohttp, Sanic, Tornado, Dash) | dependência + chamada de execução reconhecida | fase 2, por comando declarado (o Cobogó sabe como passar a porta a cada um) |
| F-8 | **Não é serviço web** (script, bot, worker Celery/RQ, notebook, biblioteca, CLI) | nenhum alvo HTTP; `Procfile` só com `worker:`; só `setup.py`/`pyproject` de biblioteca | **não suportado**, dizendo por quê; worker é um tipo de processo futuro |

## Decisão

### 1. Primeiro o ecossistema, depois o perfil

A análise passa a ter um passo anterior às regras: **qual ecossistema é a aplicação desta pasta**. Marcadores de JavaScript (`package.json`, `index.html` estático) e de Python (`requirements.txt`, `pyproject.toml`, `uv.lock`, `poetry.lock`, `Pipfile`, `manage.py`, `Procfile` com comando Python) escolhem o catálogo de regras (o JS de hoje ou o Python novo). Os dois na mesma pasta viram **pergunta** (F-6), com a pasta que cada um sugere para outro projeto. O mesmo vale para qualquer escolha que o projeto declare: o override do usuário decide.

### 2. Um perfil Python, com a interface como campo

Um perfil, **P-PY**, com campos próprios: `python_version`, `package_manager` (`pip` ou `uv` na fase 1), `python_interface` (`wsgi`, `asgi` ou `command`), `app_target` (`modulo:atributo` ou `modulo:fabrica()`), `python_framework` (informativo: `django`, `flask`, `fastapi`, `starlette`, `quart`, `litestar`, `falcon`, `outro`) e os de hoje (`start_command`, `build_command`, `port`, `health_path`). Os dois perfis do mestre (P-PY-WSGI e P-PY-ASGI) viram **os dois valores da interface**, não dois perfis: o build é o mesmo e só o servidor muda; o Django pode ser os dois. O painel mostra o ícone do Python e o framework.

### 3. Gerenciadores de dependência

- **Fase 1:** `requirements.txt` com **pip** (inclusive `-r` para arquivos dentro do repositório) e **uv** (`pyproject.toml` + `uv.lock`, instalado com `uv sync --locked --no-dev`).
- **Fase 2:** Poetry (`poetry.lock`), Pipenv (`Pipfile.lock`) e PDM (`pdm.lock`); `pyproject.toml` só com `[project].dependencies` e sem lock (instalado com pip, com aviso de reprodutibilidade).
- **Mais de um** desses na mesma pasta: **pergunta**, como npm e Bun (DEC-36).
- `requirements.txt` sem versões fixadas é aceito com **aviso** (o pip não tem lock; o deploy de amanhã pode instalar outra versão). Índice privado (`--index-url` com credencial), dependência editável local fora do repositório (`-e ../x`) e conda (`environment.yml`) são **não suportados**, dizendo por quê.

### 4. Versão do Python

Da declaração mais explícita para a mais fraca: `.python-version`, `runtime.txt` (`python-3.13.1`), `requires-python` do `pyproject.toml`, `python_version` do `Pipfile`. Catálogo: **3.10, 3.11, 3.12, 3.13, 3.14 e 3.15** (decisão do autor), padrão **3.13** (o que as plataformas de referência usam hoje e onde quase todo pacote já tem wheel). O **3.10 saiu de suporte em outubro de 2026**: escolhê-lo (ou ele vir declarado) gera um aviso de que não recebe mais correções de segurança, sem bloquear. Uma declaração fora do catálogo (3.9 ou anterior, ou uma versão que não existe) vira aviso e a mais próxima admissível, como no Node (DEC-25). A imagem é a oficial `python:<versão>-slim`, fixada por digest como as demais.

### 5. O alvo, sem executar

A ordem das evidências, da mais forte para a mais fraca:

1. **`Procfile` com `web:`** é a declaração do projeto (origem "declaração"). Se o comando é de desenvolvimento (`flask run`, `manage.py runserver`, `uvicorn --reload`, `fastapi dev`), vale o mesmo contrato do Node: modo de execução e pergunta (DEC-24). Uma linha `worker:` é ignorada com aviso (um processo por projeto).
2. **Django:** o `DJANGO_SETTINGS_MODULE` literal do `manage.py` leva ao settings; o `WSGI_APPLICATION` (ou `ASGI_APPLICATION`) literal dá o alvo. Mais de um settings plausível (`settings/production.py`, `settings/dev.py`) vira pergunta.
3. **Atribuição de módulo:** em arquivos de execução (fora de `tests/`, `migrations/`, ambientes virtuais e exemplos), uma linha de nível de módulo `NOME = Flask(` / `FastAPI(` / `Starlette(` / `Quart(` / `Litestar(` / `falcon.App(`; o módulo vem do caminho (`src/` e pacotes com `__init__.py` entendidos). Uma função `create_app()` que devolve o app vira alvo de fábrica.
4. **Bloco de execução:** `uvicorn.run("main:app")` com o alvo em texto, ou `app.run()` no `if __name__ == "__main__"`: só **reforçam** o alvo; nunca viram o comando (são servidores de desenvolvimento).

Um alvo só → pronto. **Mais de um → pergunta** ("Encontrei dois alvos ASGI; escolha qual iniciar", como o mestre já prevê). Nenhum → pergunta com o campo `app_target` (`modulo:atributo`) e a explicação do que se procurou. Nada disso importa o módulo.

### 6. O servidor de produção e a porta

- **WSGI → gunicorn**, **ASGI → uvicorn**, sempre com `--bind`/`--host 0.0.0.0 --port $PORT`: a plataforma escolhe a porta (8080, origem `platform`) e o processo a ouve. Diferente do Node, o código não precisa ler `PORT`: quem abre a porta é o servidor que a plataforma inicia.
- Workers: gunicorn com `--workers 2` (ajustado pelo limite de memória do projeto, de 1 a 4) e `--access-logfile -`; uvicorn com um processo. Os dois encerram limpo no `SIGTERM` (o init do Docker repassa o sinal, DEC-40).
- Se o servidor **está nas dependências**, vale a versão do projeto. Se **não está**, ver a pergunta 2 ao fim.
- Um `Procfile` que fixa a porta (`--bind 0.0.0.0:8000`) faz a porta ser a declarada, como no Node.

### 7. O build

Dois estágios. O **de build** usa `python:<versão>` completo (tem compilador e cabeçalhos para os poucos pacotes sem wheel), cria um ambiente virtual em `/opt/venv` e instala as dependências (`pip install --no-cache-dir -r requirements.txt` ou `uv sync --locked --no-dev`, com o uv fixado por versão). O **de execução** usa `python:<versão>-slim`, copia o ambiente e o código, roda como usuário sem privilégio e define `PYTHONUNBUFFERED=1` e `PYTHONDONTWRITEBYTECODE=1`. O `.dockerignore` da plataforma deixa de fora `.venv`, `venv`, `__pycache__` e `.env` (o `.env` já fica, DEC-24).

**Pacotes do sistema:** um catálogo fechado mapeia dependências para pacotes de execução (o driver `psycopg2` sem `-binary` ganha `libpq5`; `mysqlclient` ganha a biblioteca do MariaDB). Nenhum `apt-get` arbitrário vindo do repositório. Uma dependência conhecida por exigir pacote de sistema fora do catálogo (GDAL, por exemplo) vira aviso antes do build, e a falha do build é diagnosticada com o nome do pacote.

Instalar dependências executa código delas (o `setup.py` de um pacote sem wheel), **no contêiner de build**, com os mesmos limites e isolamento do build Node (DEC-29). A análise continua sem executar nada.

### 8. Configuração, banco e dados

- **HEU-09 para Python:** `os.environ["X"]`, `os.environ.get("X")`, `os.getenv("X")`, `environ["X"]`, `env("X")`/`env.str("X")` (django-environ), `config("X")` (python-decouple). `os.environ["X"]` sem padrão conta como obrigatória (DEC-39 avisa antes do build). Campos de `BaseSettings` (pydantic-settings) viram variáveis **prováveis**, com o prefixo quando literal.
- **HEU-06 / DEC-42/44 para Python:** `psycopg`, `psycopg2`, `asyncpg`, SQLAlchemy com URL de PostgreSQL e o `ENGINE` `django.db.backends.postgresql` são evidência de banco; o estilo de conexão (URL ou partes) é lido do mesmo jeito (`dj-database-url`/`env.db()` → URL; `DATABASES` com `os.environ["POSTGRES_HOST"]` → partes). A CA do banco chega pelos mecanismos que o libpq lê (`PGSSLROOTCERT`).
- **DEC-41 para Python:** SQLite (`sqlite3`, `sqlite:///`, o `db.sqlite3` padrão do Django) e pastas de upload (`MEDIA_ROOT`, `UPLOAD_FOLDER`) viram **sugestões** de caminho persistente. O `db.sqlite3` na raiz do projeto (`/app`) não pode ser volume (a raiz é a aplicação): a sugestão explica que o arquivo precisa ir para uma pasta, por variável.
- **DEC-45 para Python:** os botões de comando oferecem os comandos de gerenciamento reconhecidos (`python manage.py migrate`, `collectstatic`, os comandos do próprio projeto em `management/commands/`, `flask db upgrade`, `alembic upgrade head`). O que é interativo (`createsuperuser` sem `--noinput`) fica de fora, porque o comando não tem teclado.

### 9. O contrato do Django

O Django tem regras de produção que, quebradas, deixam a aplicação no ar e quebrada. O Cobogó as lê do settings (texto, sem importar) e **avisa antes**, nunca muda o código:

- **Hosts:** `ALLOWED_HOSTS` e `CSRF_TRUSTED_ORIGINS` precisam do endereço público. Lidos do ambiente → o painel **sugere o valor** (o domínio do projeto) na aba Variáveis (DEC-39); fixos no código sem o domínio → aviso com o nome e a linha. A sonda de prontidão aceita a resposta 400 de host não permitido como "vivo, com aviso", e o aviso diz o porquê.
- **Estáticos:** com `STATIC_ROOT` definido, o build roda `collectstatic --noinput`; o settings pode exigir `SECRET_KEY` só para carregar, e o build recebe uma chave **descartável, só do build** (nunca a de execução: build só recebe variável pública, DEC-37). Sem `whitenoise` (ou outro servidor de estáticos) e com `DEBUG` desligado, os estáticos dão 404: aviso com a solução.
- **`DEBUG = True` fixo** e **`SECRET_KEY` fixa no código**: avisos (segurança), não bloqueio.
- **Migração:** nunca automática (RN-13). Depois do primeiro deploy, o painel destaca o botão `python manage.py migrate` na aba Comandos e a pendência de banco diz que as tabelas são criadas por ele.

### 10. Django com frontend no mesmo repositório

- **Separados** (F-5): dois projetos, um com o diretório do backend e outro com o do frontend (estático, DEC-37). O Cobogó detecta a forma e diz isso na análise, com o que configurar entre eles (a URL pública da API numa variável pública do frontend, e o domínio do frontend no CORS e no `CSRF_TRUSTED_ORIGINS` do backend). Ligar os dois automaticamente é a issue #39.
- **Integrados** (F-4, o Django serve o frontend construído): fica para a **fase 3**, numa decisão própria, porque exige um build em dois ecossistemas (Node constrói, Python serve) e a ordem `npm run build` → `collectstatic`. Até lá, a análise reconhece a forma e diz que ainda não é suportada, oferecendo o caminho separado.

### 11. O que fica de fora, dito na análise

Processo que não é web (F-8: script, bot, worker, notebook, biblioteca, CLI), conda, índice privado com credencial, dependência local fora do repositório, pacote de sistema fora do catálogo, mais de um processo no mesmo projeto (web e worker), navegadores do Playwright, GPU. Cada um vira "não suportado" com o motivo e, quando existe, o caminho (outro diretório, comando declarado, banco externo).

### 12. Ordem de entrega

| Fase | O que entra | Prova |
| --- | --- | --- |
| **1** | Despacho por ecossistema e a pergunta F-6; P-PY com pip e uv; versão; alvos F-1/F-2 (Flask, FastAPI, Starlette, Quart, Litestar, Falcon) e `Procfile`; gunicorn/uvicorn; HEU-09, banco (DEC-42/44), sugestões de dados e comandos para Python; **Django backend** (F-3, item 9); o aviso de monorepo (F-5) | deploys reais de 2 Flask, 2 FastAPI e 2 Django, mais os negativos (script, bot, biblioteca, conda, os dois ecossistemas na raiz) |
| **2** | Poetry, Pipenv, PDM, `pyproject` sem lock; aplicações com servidor próprio (F-7: Streamlit, Gradio e afins) por comando declarado | um repositório de cada |
| **3** | Django servindo o frontend construído (F-4), em DEC própria | um Django + React real |

Cada fase é um commit e uma subida na VM, como as anteriores. O corpus de avaliação do mestre (seis repositórios Python, mais negativos) sai da fase 1.

## Alternativas consideradas

- **Usar o Railpack, o Nixpacks ou os Buildpacks para gerar a imagem:** resolvem muitos casos, mas o plano deixa de ser explicável (DEC-23), não perguntam (adivinham, e o Railpack roda `migrate` por conta própria no Django) e trazem uma peça grande de fora para o caminho do build. Ficam como **referência** de marcadores e comandos.
- **Importar o módulo para descobrir o `app`:** proibido pelo mestre (executa código do repositório na análise).
- **`python app.py` como comando padrão:** sobe o servidor de desenvolvimento do framework em produção.
- **Um perfil por framework** (P-FLASK, P-FASTAPI, P-DJANGO…): multiplica regras e templates para o que é, no fundo, a mesma imagem com um servidor diferente. O framework fica como informação e como fonte de regras específicas (o Django tem o seu contrato), não como perfil.
- **Exigir `Procfile` sempre:** simples e explícito, mas a maioria dos projetos de estudo não tem; o `Procfile`, quando existe, vence.
- **Instalar o servidor só se o projeto o declarar:** mais fiel ao projeto, mas a maioria dos tutoriais de Flask e Django não lista o gunicorn (ver a pergunta 2).

## Consequências e limites

- O analisador ganha um segundo catálogo e um passo de despacho; o resolvedor, os overrides, as perguntas, os avisos e o painel são os mesmos.
- Ler Python sem importar é heurístico: alvos construídos dinamicamente (`app = criar(config)` com nome vindo de variável, `getattr`) não são achados e viram pergunta, nunca palpite.
- Uma dependência sem wheel para a versão escolhida compila no build, mais devagar; uma que precisa de pacote de sistema fora do catálogo falha, com diagnóstico.
- Um TOML precisa de leitor no analisador (`pyproject.toml`, `uv.lock`): uma dependência Go nova, só de leitura.

## Critérios de revisão

- Medir no corpus quantos projetos caem em cada forma (F-1 a F-8) e quantos precisam de pergunta; rever as fases pelo que aparecer.
- Rever o catálogo de pacotes de sistema pelas falhas de build reais.
- Reabrir o F-4 quando a fase 1 estiver na VM.

## Decisões do autor (22/11/2026)

1. **Django backend na fase 1**: "temos que lidar com essa possibilidade já".
2. **Servidor fora das dependências:** a plataforma instala uma versão fixada, numa camada à parte, e diz isso no plano.
3. **Os dois ecossistemas na mesma pasta:** sempre perguntar.
4. **Versões:** 3.10 a 3.15, padrão 3.13 (com o aviso de fim de suporte no 3.10).

## Implementação da fase 1 (22/11/2026)

- **Despacho (HEU-00, `internal/analyzer/ecosystem.go`):** marcadores na raiz escolhem o catálogo (`p-js-0.5` ou `p-py-0.1`); os dois juntos viram a pergunta `ecosystem` (F-6), respondida por override. Raiz sem marcador e com aplicações em subpastas (até dois níveis) vira "não suportado" com as pastas e o que ligar entre elas (F-5).
- **Catálogo Python (`python_extract.go`, `rules_python.go`):** extratores de manifesto (requirements com `-r` até 4 níveis, `pyproject.toml` por leitor TOML, locks, `.python-version`, `runtime.txt`, `Procfile`, `alembic.ini`) e de código (até 3.000 arquivos `.py`, sem `venv`, testes e migrações; variáveis lidas por `os.environ`, `getenv`, `django-environ` e `decouple`; alvos, fábricas, `uvicorn.run`, imports de driver PostgreSQL; o contrato do Django lido no settings padrão e nos módulos irmãos). Regras HEU-04.1 a HEU-04.5 (ver o motor), HEU-09 e HEU-08 reaproveitadas. Os avisos do Django valem para o settings que vai rodar: o de desenvolvimento só conta quando é o escolhido.
- **Porta:** sem `Procfile`, a da plataforma (o servidor que ela inicia ouve nela). Com `Procfile`: `$PORT` ou gunicorn sem `--bind` (que lê `PORT`) → plataforma; uvicorn sem `--host`/`--port` → plataforma, porque a imagem define `UVICORN_HOST=0.0.0.0` e `UVICORN_PORT` (opções na linha vencem); porta explícita → declarada; nada disso e o código não lê `PORT` → pergunta.
- **Banco e dados (`python_services.go`):** import de `psycopg2`/`psycopg`/`asyncpg`/`pg8000` em runtime ou `ENGINE` PostgreSQL do Django → necessidade `required`; só a dependência → `possible`; o estilo (URL ou partes, `PGHOST`…) é o da DEC-44. A heurística de volumes ganhou a linguagem como parâmetro: SQLite (inclusive a URL `sqlite:///` do SQLAlchemy), escrita em pasta e `MEDIA_ROOT` do Django; arquivos de teste e o `.save()` do ORM não contam.
- **Planner (`internal/planner/python.go`):** dois estágios (`python:X` com o ambiente em `/opt/venv`; `python:X-slim` com usuário `app`, uid 10001); pip (`pip install -r requirements.txt`) ou uv fixado (`uv sync --locked --no-dev --no-install-project`); o servidor (gunicorn `26.2.0`, uvicorn `0.54.0`) só é instalado se não importa; `exec` no comando, para o SIGTERM chegar; `PYTHONPATH=/app/src` no layout src; `DJANGO_SETTINGS_MODULE` no ambiente; `collectstatic` no build com valores descartáveis só no `RUN` para as variáveis que o settings lê sem padrão; catálogo fechado de pacotes de sistema (`psycopg2` → `libpq5`, `mysqlclient` → `libmariadb3`, e poucos outros). O contexto de build deixa de fora `.venv` e `__pycache__`. Só a imagem da versão padrão é baixada de antemão.
- **Worker:** `WEB_CONCURRENCY` pelo limite de memória (um worker a cada 256 MB, de 1 a 4) quando o projeto não define o seu; o diagnóstico "a aplicação ignorou a PORT" não se aplica quando quem abre a porta é o servidor da plataforma.
- **Comandos (DEC-45):** `python manage.py migrate`, `showmigrations`, `check --deploy` e os comandos do próprio projeto (`management/commands/`), `alembic upgrade head`, `flask --app <alvo> db upgrade` (com Flask-Migrate) e a fase `release` do `Procfile`. O `collectstatic` fica de fora: roda no build.
- **Painel:** marca do Python, rótulos dos campos novos e, no plano, a versão, o alvo com a interface e os pacotes de sistema.
- **gunicorn antigo:** um gunicorn fixado antes da 22 importa o `pkg_resources`, que o venv do Python 3.12+ não traz; o build instala o `setuptools<81` e a análise avisa para atualizar (achado no deploy real do exemplo da Koyeb).

### Verificação

- **Testes de unidade:** o catálogo Python com 24 casos (Flask, FastAPI com uv e src, fábrica, Django completo e de tutorial, settings de desenvolvimento e produção, os dois ecossistemas, monorepo, Poetry, duas aplicações, script, bot com Procfile, portas do Procfile, versão fora do catálogo, `runtime.txt`, pip e uv juntos, índice privado), banco e volumes, `requires-python` e overrides; o planner (comando do servidor, Dockerfile de pip, uv, Django e Procfile).
- **Builds reais (Docker):** Flask sem gunicorn (a plataforma instala; roda como uid 10001, sem compilador na execução, para em menos de 8 s no SIGTERM), FastAPI com uv, layout src e fábrica (`--factory`, Python 3.13), Django com `collectstatic` e o CSS do admin servido pelo whitenoise (host fora do `ALLOWED_HOSTS` → 400), `psycopg2` compilado no build com `libpq5` na execução.
- **Pilha completa em contêineres, deploys pelo Cobogó:** `render-examples/flask-hello-world` (gunicorn da plataforma), `koyeb/example-flask` (Procfile com `$PORT`), `railwayapp-templates/fastapi` (uvicorn da plataforma), `koyeb/example-fastapi` (Procfile, Python 3.12 pelo `runtime.txt`), `heroku/python-getting-started` (Django, gunicorn com arquivo de configuração), `koyeb/example-django` (gunicorn 20.1 fixado), todos no ar pelo Traefik; `railwayapp-templates/django` com o PostgreSQL gerenciado no estilo partes (`PGHOST`…), `migrate` e `showmigrations` pela aba Comandos, admin e estáticos respondendo 200; `psf/requests` (biblioteca) recusado com o motivo. Analisados sem deploy: `railwayapp-templates/flask` e `render-examples/{fastapi,django}`, todos prontos.

