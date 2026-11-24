# 03 — Python (HEU-04.x), serviços (HEU-06), persistência e mapa do repositório

Catálogo Python: `rules_python.go:16-54`, versão `p-py-0.3` (a da DEC-54; a DEC-53 foi a `p-py-0.2`). Extratores: `py.manifest`, `env-files` (o mesmo do JS), `py.source`. Regras na ordem de aplicação (`27`): **04.1, 04.2, 04.3, 04.4, 04.5, 04.6, 04.7, 04.8, 09, 08**. Todas as 04.x exigem a 04.1 (se ela recusa, as demais são puladas).

Princípio do catálogo (DEC-48): **nada é importado nem executado**; "o alvo vem só de texto" (`python_extract.go:18-19`). Isso custa cobertura (um alvo montado dinamicamente não é achado) e compra segurança.

---

## Extração Python (`python_extract.go`)

### Manifestos (`extractPythonManifest`, `144-189`)
- Lockfiles/gerenciadores (`145-151`): `uv.lock`→uv; `poetry.lock`, `Pipfile(.lock)`, `pdm.lock`, `environment.yml` viram evidência de gerenciador **não suportado**.
- `requirements.txt` na raiz (`152-155`); **sem ele, `chooseRequirements` (`python_extract.go:731-759`, DEC-53 E)** escolhe outro arquivo (ver abaixo); e **`readRequirements` (`193-282`)**: segue `-r outro.txt` só para arquivos do próprio repositório, até 4 níveis, sem repetir (`194`). Trata linhas continuadas com `\` (`215-218`), comentários (`221`) e distingue:
  - `-e ../fora` ou `-r ../fora` → `editable_outside` (`238-240`, `250-251`);
  - URL/`git+` → `vcs` (`252-253`, `264-265`);
  - `--index-url`/`--extra-index-url` com `@` ou `${` → **`private_index`** (índice com credencial); sem credencial → `index` (`255-261`);
  - dependência normal → evidência `py.dependency` com o nome normalizado pela PEP 503 (`normalizePy`, `140-142`) e, se não tem `==`, também `py.unpinned` (`277-279`).
- `pyproject.toml` (`286-309`): dependências de `[project]`, `requires-python`, e `[tool.poetry]` → evidência de Poetry.
- `.python-version` e `runtime.txt` (`165-171`): primeira linha.
- `Procfile` (`172-178`): cada linha `tipo: comando` vira `py.procfile` (ignora comentários).
- `alembic.ini` → registra o comando `alembic upgrade head` como comando disponível (`180-182`, DEC-45).

### Código-fonte (`extractPythonSource`, `333-522`)
- Percorre `.py` pulando pastas de ambiente/artefatos (`pySkippedDirs`, `68-72`) e pastas ocultas; limite de 3000 arquivos (`358-361`); lê em ordem estável (`365`).
- Layout `src/` (pasta `src` sem `__init__.py`; `335-338`) → registra `py.src_layout` e depois o Dockerfile põe `PYTHONPATH=/app/src` (`planner/python.go:255-257`).
- **Escopo**: testes são reconhecidos (`isPyTestFile`, `524-530`: `test_*.py`, `*_test.py`, `conftest.py`, `tests.py`, pasta `tests/`). Só arquivos de **runtime**, fora de `migrations/`, com caminho de módulo válido, entram na detecção de aplicação e serviços (`408`).
- **Aplicação web** (`421-451`): atribuição de nível de módulo `app = Flask(...)`, `FastAPI(...)`, `Starlette`, `Quart`, `Litestar`, `Sanic`, `falcon.App/asgi.App` (`pyAppAssign`, `82`). **Só conta se o arquivo importa o framework** (`447`): `Flask(...)` sem `import flask` é outra coisa. Evidência: `módulo:atributo`.
- **Fábrica** (`452-459`): `def create_app()`, `make_app`, `app_factory`, `get_app`, `build_app` → `módulo:create_app()`.
- **Celery** (`460-474`): `app = Celery(...)`; em `<pacote>/celery.py` com `app` o alvo é só o pacote (`-A pacote`), senão `modulo:atributo`. `beat` se há `CELERY_BEAT_SCHEDULE`/`beat_schedule`/`django_celery_beat`.
- **Redis** (`475-486`): import do cliente (`redis`, `aioredis`, `django_redis`, `rq`, `channels_redis`) e URL `redis://` escrita no código; **diferencia** URL fixa (`url`) do padrão de uma variável (`url_default`, via `isEnvDefault`).
- **Drivers de banco** (`487-492`): MySQL (`MySQLdb`, `pymysql`, `mysql.connector`, `aiomysql`, `asyncmy`) e PostgreSQL (`psycopg2`, `psycopg`, `asyncpg`, `pg8000`).
- **Migrações do Django** (`376-378`): `migrations/0001_*.py` → `django.migration`, só a **primeira** achada (evidência para a HEU-04.8).
- **Modelo de usuário próprio** (`380-394`): classe com `USERNAME_FIELD` + `REQUIRED_FIELDS` → `django.user_model` (usado pela receita "Criar administrador").
- **Comandos de gerenciamento** (`497-500`): `app/management/commands/x.py` vira comando disponível.

### Arquivos de requirements fora da raiz (DEC-53 E, `python_extract.go:660-766`)
Sem `requirements.txt` na raiz, `requirementsCandidates` (`674-707`) lista: `requirements/*.txt`, `requirements-*.txt`/`requirements_*.txt` na raiz e, numa subpasta de primeiro nível que **não** é outro projeto (sem `manage.py`, `pyproject.toml` nem `setup.py`), `requirements.txt` ou `requirements/*.txt` (assim `deploy/requirements.txt` e `backend/requirements/…` entram). `requirementsClass` (`714-726`) classifica pelo **nome** em `development` (`dev`, `local`, `test`, `ci`, `lint`, `docs`, `debug`, `tox`, `coverage`), `production` (`prod`, `production`, `deploy`, `release`) ou `neutral`. `chooseRequirements` (`731-759`): o override `requirements_file` vence; senão, o **único de produção**; senão, o único `neutral` que sobra (os de desenvolvimento são descartados); mais de um possível → emite `py.requirements_candidate` para cada um e a HEU-04.1 **pergunta**. O arquivo escolhido é lido com `readRequirements` (que segue `-r base.txt`) e emite a evidência `requirements.txt` com o caminho em `Source`. **Convenção:** é o layout dos modelos de projeto Django (`requirements/base.txt`, `production.txt`, `local.txt`) e das imagens de deploy.

**Por quê:** 4 de 37 repositórios do corpus externo tinham as dependências fora da raiz e eram recusados como "nenhum arquivo de dependências".

### O contrato do Django (`extractDjango`, `534-660`)
**`manage.py` numa subpasta (DEC-53 E).** `djangoManageDir` (`python_extract.go:768-799`) acha o `manage.py` até **dois níveis** abaixo, **só se houver um** (com dois, não escolhe). O contrato é lido de lá (`fs.Sub`, `python_extract.go:506-519`), as fontes voltam com o caminho completo e a evidência `django.dir` guarda a pasta; a HEU-04.3 a propõe como `django_dir` e o planner passa a rodar build e execução nessa pasta (`planner/python.go:264-267`, `300-303` e `330-332`, `WORKDIR /app/<pasta>`; o módulo de hosts da plataforma é gravado nela). `djangoDirPattern` (`planner/python.go`) limita a pasta a um ou dois segmentos simples, porque entra no Dockerfile.

Só roda se existe `manage.py` com `DJANGO_SETTINGS_MODULE` padrão (`539-544`). Então:
- registra o **módulo de settings** padrão e os comandos `migrate`, `showmigrations`, `check --deploy` (`546-548`);
- se o settings é um **pacote** (`settings/base.py`, `production.py`...), os arquivos irmãos viram **candidatos** (`559-576`) e todos são lidos (`578-585`), porque um importa do outro;
- em cada arquivo lê `WSGI_APPLICATION`/`ASGI_APPLICATION` (`600-605`) e uma bateria de configurações (`606-613`, `615-658`): `STATIC_ROOT`, `DEBUG = True`, `SECRET_KEY` literal, `SECRET_KEY` com padrão no código (`djangoSecretDefault`, `120`), `ALLOWED_HOSTS` literal/de variável/estendido, `CSRF_TRUSTED_ORIGINS` literal/de variável, `WhiteNoise`, banco (SQLite/PostgreSQL/MySQL), cache Redis, `MEDIA_ROOT`, `django.contrib.auth`, `AUTH_USER_MODEL`, e se a mídia é servida só por `static(MEDIA_URL...)` nas URLs (`645-658`).

---

## HEU-04.1 — Perfil e gerenciador (`rules_python.go:134-261`, versão 0.2)

**Campos:** `profile`, `package_manager`, `requirements_file` (DEC-53 E).

**Como:**
1. **`142-153`: recusas e avisos pelas evidências especiais.**
   - `private_index` → **não suportado**: "o Cobogó não guarda credencial de índice no build".
   - `editable_outside` → não suportado: "o build só enxerga o que está no repositório".
   - `vcs` → aviso (a dependência vem do Git e não fica fixada como as do PyPI). `index` → aviso.
2. **`157-229`: escolha do gerenciador.**
   - `requirements.txt` **e** `uv.lock` → **pergunta** com duas opções (`170-184`), a menos que haja override.
   - só `uv.lock` → uv; só `requirements.txt` → pip (`185-192`). Se o `requirements.txt` veio de fora da raiz, a regra **propõe `requirements_file`** com o caminho (`189-192`) e o planner faz `pip install -r <arquivo>` (`planner/python.go:221-225`).
   - sem `requirements.txt` na raiz e **mais de um** candidato → **pergunta** `requirements_file` com a lista; os de desenvolvimento aparecem marcados como tal (`193-205`).
   - só Poetry/Pipenv/PDM/conda → **não suportado**, explica a saída: "exporte um requirements.txt (`poetry export`) ou use uv" (`206-208`).
   - `pyproject.toml` sem lock **e** `uv.lock`/`poetry.lock` **numa pasta acima** (DEC-53 G) → não suportado, dizendo qual arquivo e que é de um workspace (`209-222`; evidência `ancestors.go`, que o `Survey` e o worker alimentam com `Input.Repo`/`Input.Dir`).
   - só `pyproject.toml`/`setup.py` sem lock → não suportado: "gere `pip freeze` ou `uv lock`" (`223-225`).
   - nada → não suportado: "nenhum arquivo de dependências (requirements.txt na raiz, em requirements/ ou numa subpasta de primeiro nível, ou uv.lock)" (`226-228`).
3. **`230-253`: avisos que não bloqueiam.**
   - gerenciador adicional presente é ignorado (`230-232`);
   - **gunicorn fixado antes da 22** → aviso: ele importa `pkg_resources`, que o venv do Python 3.12+ não traz; o build instala `setuptools<81` (`233-238`; `domain.OldGunicorn`, `domain/python.go:126-134`);
   - **pip sem versões fixadas** → aviso listando até 6 pacotes: "o pip não tem lock, então um deploy futuro pode instalar outra versão" (`239-253`).

**Por quê:** reprodutibilidade dos builds e honestidade sobre o que ainda não é suportado. "pyproject sem lock" não é suportado porque instalar sem lock produz builds diferentes a cada deploy.

---

## HEU-04.2 — Versão do Python (`rules_python.go:324-447`, versão 0.2)

**Campo:** `python_version`. Catálogo 3.10–3.15, padrão 3.13 (`domain/python.go:68-70`). **Desde a DEC-54:** 3.8 e 3.9 existem como **legados**, só por confirmação (`domain.LegacyPythonVersions`, `domain/python.go:76-100`).

**Hierarquia da declaração, da mais explícita para a mais fraca:** `.python-version` > `runtime.txt` > `requires-python` (`362-410`).
- Versão exata do catálogo → propõe (origem `declaration`) (`371-372`). É **a pessoa que decidiu**: o passo da época (abaixo) só avisa.
- **Versão legada (3.8 ou 3.9) declarada** → **pergunta** `python_version`, com a legada e a do catálogo mais próxima como opções, dizendo que saiu de suporte (`373-381`). Declaração nunca ativa um legado sozinha (no molde da issue #32).
- Fora do catálogo e fora dos legados → a **mais próxima** (a primeira se é menor que o catálogo, senão a última) com aviso (`339-346`, `382-389`).
- `requires-python` (`390-407`): se o **padrão 3.13** satisfaz, usa o padrão; senão, a **maior** versão do catálogo que satisfaz; se nenhuma, aviso e padrão. As versões que o `requires-python` admite também limitam a sugestão do passo seguinte (`allowed`, `348-356`).
- 3.8, 3.9 e 3.10 → aviso de **fim de suporte** (`329-337`; mapa `UnsupportedUpstreamPython`).

**A época das dependências (DEC-54 A, `413-445`).** Depois de escolher a versão, e **só se a pessoa não respondeu** `python_version`, a regra confere os pacotes fixados com `==`:
1. `pinnedRanges` (`pycompat.go:94-106`) lê as dependências (`py.dependency`, `exactPin` pega a versão) e, para cada pacote da **lista fechada**, `pythonRangeOf` (`70-82`) devolve o intervalo de Python que **aquele release** declara.
2. `compatiblePythonWhere` (`139-160`) acha o **maior** Python (catálogo ou legado) que todos admitem e que o `requires-python` aceita, e quais pacotes não admitem a versão escolhida.
3. Se há bloqueadores e existe uma versão melhor → **pergunta** `python_version`: a primeira opção é a melhor (com a nota de que é legada e vale só por confirmação, se for), a segunda é manter a do Cobogó ("o build pode falhar"). A mensagem cita cada pacote, a versão e o intervalo (`pyRangeText`, `163-171`).
4. Se a pessoa **declarou** a versão → só aviso. Se nenhuma versão admite todos → só aviso.

**A tabela (`pycompat.json`, `pycompat.go:35-48`).** É **gerada** por `scripts/gen-pycompat/main.go`: para cada release de 29 pacotes (Django, Pillow, numpy, pandas, scipy, psycopg2, lxml, cryptography…), a menor e a maior versão do Python que os **classificadores do PyPI** declaram, guardadas só onde o intervalo muda; a busca usa a maior entrada que não passa da versão fixada (`versionLess` compara números, `51-66`). **Por quê por release, e não por série:** a primeira versão tentada (um intervalo por `major.minor`) dizia que o `psycopg2` 2.9.9 só ia até o 3.9 (o 2.9.0), quando o 2.9.9 vai até o 3.12; perguntar sem necessidade a quem já dava certo seria regressão. **Limite:** classificador é o que o release **declara**, não garantia de wheel; por isso é pergunta, nunca escolha sozinha. Pacote não fixado com `==`, ou fora da lista, o motor não conhece.

**`satisfies` (`281-319`)** interpreta especificações PEP 440 (`>=`, `>`, `<=`, `<`, `==`, `~=`, `!=`, vírgulas) comparando no nível da **versão menor** (`major*100+minor`), que é o que o catálogo escolhe. Casos sutis: `>3.12` pede 3.13 em diante; `<3.12.4` ainda admite 3.12 (`patch` na cláusula, `292-302`).

**Planner para o Python legado (`planner/python.go:180-192`):** `python:3.9`/`3.8` e servidores que ele aceita (`gunicorn 23.0.0`, `uvicorn 0.30.6`, `domain.LegacyGunicornVersion`/`LegacyUvicornVersion`), porque os do catálogo exigem um Python mais novo.

**Por quê preferir o padrão quando ele satisfaz:** é a versão mais testada pelo Cobogó; mudar só traz risco se a declaração ou os pacotes fixados não exigem.

---

## HEU-04.3 — Alvo, interface e framework (`rules_python.go:459-607`)

**Campos:** `app_target`, `python_interface`, `python_framework`, `django_settings`, `django_dir` (DEC-53 E, versão 0.2). Sem importar nada.

**Cascata, em ordem:**

1. **Só processos que não são web** (`473-478`): o Procfile declara `worker`, `bot` etc. e não há app web → **não suportado** ("o Cobogó publica um serviço web por projeto").
2. **Django** (`481-523`), se há `manage.py` com módulo de settings **e** `django` nas dependências:
   - framework `django` (inferência), settings (declaração), e, se o `manage.py` está numa subpasta, `django_dir` (inferência; `rules_python.go:499-501`);
   - **settings de desenvolvimento como padrão e de produção ao lado** (`485-497`): `isDevSettings` (`dev`, `development`, `local`, `debug`) + candidato `isProdSettings` (`prod`, `production`, `deploy`, `live`) → **pergunta** com as duas opções, marcando qual é de produção;
   - alvo WSGI/ASGI do settings (`502-522`): WSGI por padrão; ASGI se `channels` ou `daphne` estão nas dependências; sem `WSGI_APPLICATION` literal → **pergunta** `app_target`.
3. **Procfile `web:`** (`526-545`): alvo `modulo:atributo` extraído do comando por regex (`procTarget`, `451`; ex.: `gunicorn app:app`); senão, se o código tem **exatamente um** app, usa-o (inferência); senão, se o Procfile é de desenvolvimento (`runserver`, `flask run`, `--reload`, `fastapi dev`, `debug=True`; `devCommand`, `450`) → pergunta. Interface por palavra-chave do servidor (`uvicorn`, `hypercorn`, `daphne`, `granian` → ASGI; `asgiServerRe`, `454`).
4. **Atribuições de módulo** (`548-603`): um alvo → pronto; vários → **pergunta** com cada um e `arquivo:linha`; nenhum → pergunta com mensagem explicando o que procurar e **oferece `uvicorn.run("m:a")` como opção** (`598-600`).

**Por quê o Django vem antes:** o `manage.py` + `WSGI_APPLICATION` é a declaração mais autoritativa que o framework oferece. **Por quê perguntar nos demais casos:** executar o código para descobrir o `app` é proibido, e um alvo dinâmico (`app = criar(config)`) simplesmente não é achado; perguntar é a resposta honesta.

---

## HEU-04.4 — Comando, porta, modo e build (`rules_python.go:677-748`)

**Campos:** `start_command`, `port`, `execution_mode`, `build_command`, `health_path`.

**Modo:** `production` por padrão (`684`).

**Com Procfile `web:`** (`704-729`):
- Comando de desenvolvimento (`devCommand`) e modo produção → **não usa o do Procfile**: aviso explicando que o Cobogó usa o servidor de produção do perfil com o mesmo alvo; porta = a da plataforma (`706-710`). Para manter o do Procfile, escolha o modo `development`.
- Senão, o comando do Procfile é a declaração, e a **porta** vem de, em ordem (`713-728`): `$PORT` no comando → plataforma; flag `--port`/`--bind`/`-b`/`-p` → o número declarado; gunicorn/uvicorn sem porta → plataforma (o gunicorn ouve em `PORT`; o uvicorn lê `UVICORN_PORT`, que a imagem define); o código lê `PORT` → plataforma; `fastapi run` → 8000 (padrão da ferramenta); nada disso → **pergunta**.
- Processos do Procfile que não são web/release: se não são worker nem agendador, aviso "não roda" (`690-703`).

**Sem Procfile:** porta da plataforma (`730-732`) e comando montado pelo planner (`planner/python.go:165-176`).

**Avisos extras:**
- Sem gunicorn/uvicorn nas dependências e a plataforma vai iniciar o servidor → aviso de que instala `gunicorn 26.2.0` ou `uvicorn 0.54.0` (`733-738`).
- **Django com `STATIC_ROOT`** → propõe `python manage.py collectstatic --noinput` como build (`739-744`), origem `inference`, citando a linha do `STATIC_ROOT`.

---

## HEU-04.5 — Contrato de produção do Django (`rules_python.go:752-813`)

**Só avisa; nunca muda o código do projeto.** Roda só em Django e só olha o settings que vai rodar (`djangoSetting`, `817-830`): um settings de desenvolvimento ou de teste só conta se foi o escolhido (`isDevSettings`/`isTestSettings`).

| Aviso | Gatilho | Por quê |
| --- | --- | --- |
| `ALLOWED_HOSTS` fixo | literal sem `*` (`766-773`) | O Django responde **400** a host fora da lista |
| `ALLOWED_HOSTS` de variável | lê de `X` (`774-776`) | Precisa definir `X` com o domínio |
| `CSRF_TRUSTED_ORIGINS` fixo ou ausente | literal / não definido (`777-782`) | Atrás de proxy HTTPS, formulários (login do admin) dão **403 CSRF** |
| `DEBUG = True` fixo | (`783-785`) | Em produção expõe código e configurações |
| `SECRET_KEY` no código | literal ou **com padrão** no código (`786-791`) | A chave do repositório é pública; forja sessões e tokens |
| Mídia só por `static()` | `MEDIA_ROOT` + `static(MEDIA_URL)` + `DEBUG` desligado (`792-794`) | `/media/` responde 404 em produção |
| Sem `STATIC_ROOT` / sem WhiteNoise | (`795-801`) | CSS do admin não aparece |
| SQLite | (`802-804`) | O arquivo some a cada deploy sem volume |

> O último aviso da regra ("nenhum comando de migração roda nos deploys deste projeto") só aparece quando a HEU-04.8 não vai rodar um comando de release (`releaseWillRun`, `rules_python.go:983-994`). Antes da correção ele aparecia sempre, mesmo com a migração automática ativa.

---

## HEU-04.6 — Hosts do Django pela plataforma (`rules_python.go:836-879`)

**Campo:** `django_platform_hosts` (bool).

**Quando pergunta:** o settings (o que vai rodar) fixa `ALLOWED_HOSTS` (sem `*`, sem variável, sem extensão posterior) **ou** `CSRF_TRUSTED_ORIGINS` literal, **sem** o endereço do projeto (`851-866`). A pergunta (`870-875`): "quer que a plataforma acrescente o endereço do projeto a essas listas, num módulo gerado na imagem e sem mudar o repositório?" — opções `false` (conserte o settings) e `true`.

**O que "sim" faz:** o planner gera, na imagem de **execução** (não no build), um módulo `cobogo_platform_settings.py` (`planner/python.go:356-376`) que importa o settings do projeto, **acrescenta** o host e a URL públicos às listas e **nunca remove** nada; `DJANGO_SETTINGS_MODULE` passa a apontá-lo (`333-339`).

**Por quê nunca por padrão:** alterar a configuração de segurança de alguém sem consentimento é o tipo de coisa que não se faz sozinha. A resposta é da pessoa. Espera enquanto a escolha dev/prod do settings está aberta (`846`).

---

## HEU-04.7 — Processos adicionais: worker e agendador (`rules_python.go:903-955`)

**Campos:** `process_worker`, `process_scheduler`. Um projeto roda o **web** e, do mesmo código, um worker e um agendador (DEC-51).

**Como:**
- **Procfile (declaração)** (`912-925`): `processKindOf` (`882-891`) reconhece pelo nome: scheduler (`beat`, `clock`, `scheduler`, `celerybeat`, `celery-beat`, sufixo `beat`) e worker (`worker`, `workers`, `celery`, sufixo `worker`).
- **Celery (inferência)** (`926-948`): exige `celery` nas dependências **e** um `app = Celery(...)` achado. Comando do worker: `celery -A <alvo> worker -l info --concurrency=2` (`940`); do agendador (só se há sinal de beat): `celery -A <alvo> beat -l info`, acrescentando `--scheduler django_celery_beat.schedulers:DatabaseScheduler` se `django-celery-beat` está instalado (`942-947`).
- Aviso se há Celery mas **nenhum sinal de broker Redis** (`949-951`).
- Override com o comando troca; `none` desliga (`validateProcess`, `893-898`).

**Por quê:** workers e agendadores são parte da aplicação; um Django+Celery sem worker sobe e não processa tarefas, em silêncio. Evidência citada: arquivo e linha do `Celery(...)`.

---

## HEU-04.8 — Fase de release (migrações) (`rules_python.go:962-981`)

**Campo:** `release_command`. Evolução da HEU-06.11 (DEC-52 item 4).

**Como:**
- Procfile com `release:` → o comando é a **declaração** (`970-973`).
- Senão, **Django com migrações no repositório** (`kindDjangoModule` + `kindDjangoMigration`) → `python manage.py migrate --noinput`, origem **inferência**, citando o `manage.py` e a primeira migração (`974-977`).
- `none` desliga; override com outro comando troca.

**Execução** (`worker/release.go:41-95`): um contêiner descartável (`cobogo-release-<dep8>`) da **imagem nova**, na rede do projeto e com os volumes, **antes** de a versão nova subir; limite de 10 min; falha → o deploy falha e **a versão anterior continua no ar**; a saída passa por redação (`redact.Values`, `64`).

**Por quê rodar a cada deploy:** sem migração o app novo, com o esquema velho, quebra. **Por quê só com evidência:** nunca inferir um comando de migração "por convenção"; por isso Prisma, Alembic e Flask-Migrate seguem só por declaração. **Limite explícito (RN-13):** rollback da imagem não desfaz migração já aplicada.

---

## HEU-06 — Necessidade de serviços de dados (`services.go`, `redis.go`, `python_services.go`)

Não é uma `Rule` do laço: é a função `Catalog.Services`, chamada **depois** das regras porque precisa do inventário de variáveis (`analyzer.go:160-162`). O resultado é `[]ServiceNeed`.

### O formato da conclusão (`domain.ServiceNeed`)
- **`Engine`**: `postgres`, `mysql` ou `redis`.
- **`Level`**: `required` (evidência forte de **uso** em código de runtime) ou `possible` (só a dependência instalada).
- **`Reason`** e **`Evidence`**: texto e `arquivo:linha` (até 3).
- **`Style`/`Family`/`Variables`/`Variable`**: **como o código lê a conexão**: uma URL ou partes (host, porta, usuário, senha, banco).
- **`FixedAt`** (Redis): lugares onde o endereço está escrito no código (não é lido de variável).

### Como o nível é decidido (PostgreSQL, JS: `services.go:174-259`)

```
runtime importa o driver   ──► required  ("o código usa PostgreSQL")        [235-241]
schema Prisma + cliente    ──► required  ("o schema do Prisma usa...")       [242-244]
driver só nas dependências ──► possible  ("fala com PostgreSQL, mas nenhum arquivo de runtime a usa") [245-247]
schema Prisma sem cliente  ──► possible                                      [248-250]
```

- **Só arquivos de runtime contam** (`203`): `classifyScope` exclui testes e `isToolingPath` exclui `scripts/`, `tools/`, `build/`. Isto implementa a sub-regra "referência restrita a teste não exige banco" (HEU-06.4 da especificação).
- **Prisma** (`219-233`): o schema fixa a engine (`provider = "postgresql"`) e a **variável da URL** (`url = env("X")`). É a única fonte que nomeia a variável com certeza.
- O mesmo desenho vale para **MySQL** (`103-172`: `mysql2`, `mysql`, adaptadores do Drizzle e `provider = "mysql"`).
- **Python** (`python_services.go`): `ENGINE` do Django (`django.db.backends.postgresql|mysql`) ou import do driver em runtime → `required`; só `psycopg2`/`mysqlclient`... em `requirements` → `possible` (`27-141`).

### Redis (`redis.go`, `python_services.go:35-70`)
- Forte: import do cliente em runtime (`ioredis`, `redis`, `bullmq`, `bull`...; `jsRedisImport`, `46`), backend de cache do Django (`django_redis` ou `backends.redis.RedisCache`), ou endereço `redis://` **escrito no código** (`redisURLLiteral`, `25`; `redisAddress` valida e **remove credenciais**, `30-40`: "nunca se guarda ou se mostra uma senha lida do código").
- **Padrão de variável não é endereço fixo** (`envDefaultBefore`, `70`): `os.environ.get('X', 'redis://...')` e `process.env.X || 'redis://...'` significam que o código **lê a variável**; o literal só vale sem ela. Por isso `FixedAt` só recebe endereços realmente fixos (`services.go:89`; `python_extract.go:480-483`).
- **Celery + cliente Redis nas dependências** → `required` mesmo se a URL vem só de variável (`python_services.go:59-68`: o caso comum `CELERY_BROKER_URL = os.environ[...]`).
- Celery **sozinho** não implica Redis: o broker pode ser RabbitMQ.

### Como a conexão é lida: `connectionStyleFor` (`services.go:289-356`)
1. **URL** vence: variável da URL do Prisma, ou a primeira conhecida em ordem de preferência (PostgreSQL: `DATABASE_URL`, `POSTGRES_URL`, `DB_URL`; MySQL: `DATABASE_URL`, `MYSQL_URL`, `DB_URL`, `CLEARDB_DATABASE_URL`, `JAWSDB_URL`; Redis: `REDIS_URL`, `CELERY_BROKER_URL`, `BROKER_URL`, `CACHE_URL`, `REDISCLOUD_URL`, `REDIS_TLS_URL`) (`301-312`).
2. Senão **partes**: agrupa variáveis por prefixo (`POSTGRES_`, `PG`, `DB_`, `DATABASE_`, `MYSQL_`, `REDIS_`) e papel (`HOST`, `PORT`, `USER`, `PASSWORD`, `DB`/`NAME`) por regex fechadas (`263-277`). Escolhe a família com **mais papéis** que tenha ao menos **host e senha** (`328-337`): sem eles o serviço gerenciado não teria como se ligar.
3. Nada reconhecido → estilo URL com o primeiro nome da lista (`339-341`).
4. PostgreSQL ainda procura uma variável de **CA** (`POSTGRES_CA`, `DB_CA`...; `347-353`).

**Por quê fechadas:** "nome fora delas se escreve na pergunta" (comentário em `261-262`). A conexão é o ponto onde um erro de nome deixa o app sem banco; preferir perguntar a adivinhar a variável.

### Mapeamento especificação ↔ código (importante para a banca)

O documento `MOTOR-DE-HEURISTICAS.md` descreve HEU-06.1 a 06.11. No código, elas **não são onze regras separadas**; estão distribuídas assim:

| Spec | Código |
| --- | --- |
| 06.1 presença do driver | `dependencies(fsys)` + `hasPgDep` (`services.go:177-184`, `245-247`) → nível `possible` |
| 06.2 schema declarativo (Prisma) | `services.go:219-233` |
| 06.3 produção inequívoca | import em arquivo de runtime → nível `required` (`235-241`) |
| 06.4 restrita a teste | exclusão por `classifyScope`/`isToolingPath` (`203`) |
| 06.5 condição opcional | **não implementada** |
| 06.6 pendência de intenção (destino) | a pergunta no deploy / aba Banco (DEC-42, DEC-50); desde a DEC-52, exceção abaixo |
| 06.7 a 06.9 existente / novo / externo | `store.ChooseDatabaseFor` (modos managed/external) |
| 06.10 conflito de evidências | **não implementada** como regra; o resolvedor só trata conflito de campos |
| 06.11 migração separada | HEU-04.8 (`release_command`) |

---

## Persistência: caminhos que precisam de volume (`persistence.go`, DEC-41)

Heurística de **sugestão**: nunca declara o volume sozinha; o usuário confirma. Roda para JS (`suggestPersistence`, `52-65`) e Python (`python_services.go:152-173`) com a mesma base (`suggestPersistenceWith`, `82-225`).

**Sinais:**
- **SQLite**: literal que termina em `.sqlite`, `.sqlite3`, `.db`, `.db3` (`dbFileLiteral`, `32`; ignora `:memory:` e literais com `${`, `+`, `*`), URL do SQLAlchemy `sqlite:///...` (`153-155`), import do SQLite embutido (`bun:sqlite`, `node:sqlite`, `sqlite3`, `aiosqlite`), dependência (`better-sqlite3`, `sqlite3`, `lowdb`, `nedb`, `level`...; `sqliteDeps`, `25`).
- **Pasta de escrita**: no mesmo arquivo há uma **chamada que escreve** (`writeFile`, `mkdir`, `createWriteStream`, `open(..., "w")`, `os.makedirs`, `shutil.copy`...) **e** uma pasta de dados citada (`data`, `content`, `uploads`, `storage`, `files`, `db`, `database`; `dirNames`, `39`), como literal ou argumento de `join/resolve/Path`.
- **Django `MEDIA_ROOT`** → sugestão da pasta (`python_services.go:158-171`).

**Confiança:** `strong` quando o caminho do SQLite é dentro de `/app/`; `weak` nos demais casos (`166-170`). Máximo de 6 sugestões, fortes primeiro (`22`, `214-224`).

**Salvaguardas:**
- `containerDir` (`239-254`): devolve `""` se o arquivo está na **raiz** do app (um volume em `/app` esconderia o código) ou usa `..`.
- `add` (`86-94`): caminho que o Cobogó não deixa persistir (`validate.PersistentPaths`) vira "não sei onde" (fraca).
- `importContext` (`43`, `181`): `from "../db/client.ts"` é importação de código, não pasta de dados.

**Por quê:** o disco do contêiner some a cada deploy; sem volume, o SQLite e os uploads desaparecem. "Erra para os dois lados: um falso positivo vira um clique ignorado, um falso negativo vira um caminho declarado à mão" (`persistence.go:16-20`).

---

## O mapa do repositório: papéis das peças (`components.go`, DEC-49)

Responde "o que é cada pasta?" para o painel, para o `Survey` e para a plataforma ligar as aplicações.

**`describeDir` (`146-150`) e `describeDirAs` (`152-227`):** lê só a pasta (`readDirDeps`, `36-96`) e decide o **papel** por dependências:

| Ecossistema | Regra (em ordem) | Papel |
| --- | --- | --- |
| JS | framework "full" (`next`, `nuxt`, `@remix-run/*`, `@sveltejs/kit`, `astro`) | `fullstack` |
| JS | servidor (`express`, `fastify`, `koa`, `hono`, `@nestjs/core`, `elysia`, `@hapi/hapi`, `restify`) | `backend` |
| JS | UI (`react`, `vue`, `svelte`, `solid-js`, `preact`, `@angular/core`, `lit`) ou ferramenta (`vite`, `react-scripts`, `parcel`, `webpack`...) | `frontend` |
| JS | só scripts `build`/`start` com ferramenta conhecida | `app` |
| Python | `django` + `djangorestframework` ou `django-ninja` | `backend` ("Django REST") |
| Python | `django` sozinho | `fullstack` |
| Python | outro framework web (`flask`, `fastapi`, `starlette`, `quart`, `litestar`, `falcon`, `sanic`) | `backend` |

`describeDirAs` aceita um ecossistema para a pasta que mistura os dois (`ecosystemBoth`): é por ele que o `Survey` pergunta ao mapa, **por ecossistema**, se cada parte é aplicação (DEC-53 F). **Uma pasta Python só com `requirements` não é aplicação** (`deploy/`, `docker/`): sem `manage.py`, tem de haver algum `.py` nela (`hasPythonCode`, `components.go:359-379`, usada em `202-205`; DEC-53 E). Um `package.json` com `workspaces` não é aplicação (`160-162`); pasta JS sem scripts e sem servidor também não (`186-188`).

**`findSiblings` (`238-266`):** (nomes com `{{ }}`, `%` ou `$` — modelos de gerador como o cookiecutter — são ignorados, `249`; DEC-53 E) procura aplicações em subpastas até **2 níveis**, pulando `docs`, `examples`, `tests`, `scripts`, `packages`, `static`, `media`, `public`, `dist`, `build`, `assets`, `vendor` (`230-234`), pastas ocultas e de ambiente; **não entra** numa pasta que já é aplicação; no máximo **6**.

**`buildComponents` (`286-356`):** monta o mapa: a aplicação da pasta, os bancos que o código usa (`required` e relacional), o Redis como **fila** se há worker (Celery, RQ, BullMQ) ou **cache** nos outros casos, os serviços ainda **sem suporte** (MongoDB, com nota "use um banco externo"), o worker do Celery (marcado como suportado se a HEU-04.7 achou o comando), e as irmãs.

### `Survey` (`survey.go:15-66`, DEC-52 e DEC-53)

Lê o repositório inteiro **antes de criar o projeto**: analisa a raiz e, para cada irmã, **a própria pasta** (`59`) — não a visão de fora. `surveyApp` (`78-124`) monta a proposta: papel (a análise da própria pasta "sabe mais do que a peça vista de fora", `92-100`), perfil, serviços que o código pede, variáveis, perguntas em aberto (`112-116`) e `APIPaths`.

**`APIPaths` (`128-139`):** do padrão `VITE_API_URL || 'http://localhost:8000/api'` extrai `/api`; mais tarde a plataforma monta `https://<api>/api`.

**Raiz mista (DEC-53 F, `survey.go:21-39` e `154-174`).** Se a raiz tem marcadores dos dois ecossistemas, `mixedRootApps` propõe uma aplicação por ecossistema que o mapa reconhece, cada uma com `PinnedEcosystem`; as irmãs de subpasta continuam sendo propostas. **A raiz só vira aplicação** quando o mapa a reconhece ou a análise não ficou esperando a escolha de instalar sem lockfile (`survey.go:47-49`, `asksUnlocked` em `141-150`): sem isso, um `package.json` de ferramentas, sem lockfile, apareceria como aplicação pendente. As irmãs são analisadas com `Input{Repo: root, Dir: c.Dir}` (`59`), para a recusa saber onde está o lockfile de um workspace (DEC-53 G).
