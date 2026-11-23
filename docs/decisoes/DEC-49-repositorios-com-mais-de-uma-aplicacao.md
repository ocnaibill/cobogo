# DEC-49 — O mapa do repositório: papéis, aplicações irmãs e avisos antes da falha

> **Estado:** direção aprovada pelo autor em 22/11/2026 (a proposta foi dividida em três: esta, a DEC-50 e a DEC-51); **partes A e B implementadas** (avisos silenciosos, explicação do 400 e release degradada; mapa do repositório, aplicações irmãs e sugestões entre pares); parte C aberta.
>
> **Escopo:** o que o Cobogó diz e faz quando o repositório tem mais de uma peça (backend e frontend na mesma árvore, banco, fila, worker): descrever cada peça com o seu papel, oferecer a criação do que falta, sugerir os valores que a plataforma já conhece e avisar cedo do que no código do projeto vai falhar em silêncio. Parte do teste real de `ocnaibill/fec` (Django, Celery, MySQL e React/Vite).
>
> **Relaciona-se com:** DEC-48 (formas F-4 e F-5; o aviso de monorepo ficou pela metade), DEC-39 (sugestões de variáveis), DEC-37 (variáveis públicas de build), DEC-24 e RN-13 (o Cobogó não edita o código do projeto), DEC-50 (serviços de dados por engine) e DEC-51 (processos adicionais), issue #39.

## Contexto: o que aconteceu com o `fec`

O Cobogó analisou o Django como pronto, avisou do `ALLOWED_HOSTS` fixo e publicou. O resto só apareceu ao usar:

| A análise disse | Aconteceu |
| --- | --- |
| Pronto: P-PY, Django, `collectstatic`, volume `/app/media`, `SECRET_KEY` geradável | build e deploy passaram |
| Aviso: `ALLOWED_HOSTS` fixo | `fec.cobogo.dev` responde 400 em tudo, e o deploy aparece como "no ar" (a sonda aceita qualquer resposta abaixo de 500) |
| Nada sobre `frontend/` | só o Django foi servido |
| Nada sobre MySQL, Celery, Redis | rotas com dados dão 500; o envio de certificados falha |

## Quem é responsável por quê

**Defeitos do código do projeto** (o Cobogó não os conserta; no máximo avisa): `ALLOWED_HOSTS` e `CSRF_TRUSTED_ORIGINS` com o domínio da universidade; `SRF_COOKIE_SECURE` e `'httpss'` digitados errado; broker do Celery fixo em `redis://localhost`; `.gitignore` que ignora `events/migrations/*` (faltam as migrações 0002 a 0006); `/media/` servido só com `DEBUG` ligado; `SECRET_KEY` e `FRONTEND_URL` com padrão que esconde a falta da variável; endereços de produção escritos no frontend; sem `.env.example`. A lista do que falharia também num deploy manual sai **depois** das partes do Cobogó, a pedido do autor.

**Lacunas reais do Cobogó** (esta DEC): a segunda aplicação do repositório passa despercebida; nada liga os projetos do mesmo repositório, e a plataforma já conhece as URLs; um 400 do Django vira "no ar" sem explicação.

**Tecnologia que o Cobogó ainda não provê**, e que o autor quer dentro dele ("cada stack do projeto"): MySQL e Redis (**DEC-50**) e processos de worker (**DEC-51**).

**Avisos sobre o código do projeto** (cortesia, só os que falham **em silêncio**): mídia sem servidor, `SECRET_KEY` com padrão público, hosts e origens fixos, e o frontend que cai para `localhost`.

## Decisão

### 1. O mapa do repositório

A análise passa a descrever as **peças** do repositório, cada uma com papel e evidência (arquivo e linha):

```
/            backend   Django REST (Python 3.13, gunicorn)      [DRF em requirements.txt, WSGI_APPLICATION]
frontend/    frontend  React + Vite (site estático)             [package.json: vite build, react]
serviços     banco: MySQL · fila: Redis                         [ENGINE mysql, CELERY_BROKER_URL]
processos    worker: Celery                                     [celery.py, tasks.py]
```

Papéis: `frontend` (site estático, ou aplicação JavaScript sem servidor de API), `backend` (framework web com sinal de API: Django REST, FastAPI, Flask, Express, Fastify, Hono), `fullstack` (Django com templates, Next com servidor), `worker` (consumidor de fila), `banco`, `cache`, `fila`. É inferência: cada papel traz a evidência e, quando há dúvida, diz que há. A peça que o Cobogó já provê aparece como **disponível**, a que ainda não, como **fora de suporte**, com o motivo.

O painel mostra o mapa na primeira análise, com um botão por peça: criar o projeto da pasta, criar o serviço, adicionar o processo.

### 2. Aplicações irmãs

A lista `siblings` reúne subpastas (até dois níveis) que parecem aplicação, mesmo quando a raiz já tem uma. Sinais para evitar falso positivo (`docs/`, `examples/`, `packages/`): JavaScript com script `build` ou `start` e dependência de aplicação (Vite, Next, `react-scripts`, Express…); Python com `manage.py` ou `requirements.txt`/`uv.lock` com framework web; ficam de fora `docs`, `examples`, `test(s)`, `scripts`, `tools` e workspaces. O botão **Criar projeto para esta pasta** cria o projeto (mesmo repositório, `root_directory` da irmã, slug `<projeto>-<pasta>` editável) e **não faz deploy sozinho**.

### 3. Sugestões de valor entre projetos do mesmo repositório

Sem tabela nova: dois projetos com o mesmo `repository_url` e outra pasta são pares. O catálogo **fechado** de DEC-39 ganha nomes (nada se aplica sozinho; a pessoa confirma):

- **Do próprio projeto:** `ALLOWED_HOSTS`, `DJANGO_ALLOWED_HOSTS` → o host; `CSRF_TRUSTED_ORIGINS`, `DJANGO_CSRF_TRUSTED_ORIGINS` → a URL.
- **Do par:** `FRONTEND_URL`, `FRONTEND_ORIGIN`, `CLIENT_URL`, `CORS_ALLOWED_ORIGINS` → a URL do frontend; `VITE_API_URL`, `REACT_APP_API_URL`, `NEXT_PUBLIC_API_URL`, `VUE_APP_API_URL`, `API_URL`, `BACKEND_URL` → a URL do backend **mais o caminho que o código mostra** (o `fec` usa `VITE_API_URL || 'http://localhost:8000/api'`, então a sugestão é `<URL do backend>/api`, com o trecho como evidência).
- A URL de um par é determinística (slug e domínio das aplicações): a ordem de deploy não importa. Só para nomes que a análise encontrou e que ainda não têm valor. Um host sozinho serve a lista separada por vírgula e o valor único; a sugestão diz "confira o formato".
- A tabela de ligação (`project_links`) fica para a issue #39, quando os pares forem de repositórios diferentes.

### 4. Avisos silenciosos sobre o código do projeto

Todos dizem que a correção é no código do projeto e citam o arquivo e a linha:

- **Mídia sem servidor:** `MEDIA_ROOT` definido e `urls.py` servindo `/media/` só por `static(settings.MEDIA_URL…)`, que não faz nada com `DEBUG` desligado: as imagens e os PDFs enviados dão 404 em produção.
- **`SECRET_KEY` com padrão público:** `os.environ.get('SECRET_KEY', '<literal>')`. Esquecer a variável não falha: o site roda com a chave que está no repositório.
- **Hosts e origens fixos** (amplia a HEU-04.5): `ALLOWED_HOSTS` ou `CSRF_TRUSTED_ORIGINS` literais que não trazem o host do projeto. Hoje o aviso de CSRF só vê a ausência.
- **Frontend que cai para `localhost`:** variável pública de build com padrão `localhost` no código e sem valor definido. O site publicado chamaria `localhost` **do navegador de quem visita**.

### 5. Um 400 do Django não passa por "no ar" sem explicação

Com `DEBUG` desligado o Django não escreve a causa de um `DisallowedHost` no log. Quando a sonda responde 400 e o projeto é Django, a página do deploy e a visão geral dizem a causa provável e apontam o aviso da análise. A release continua promovida (pode ser uma rota que de fato responde 400), mas fica **degradada** (`health: degraded`) quando a sonda do deploy tem resposta 4xx; a visão geral mostra o aviso e aponta o deploy. Não há sonda periódica hoje: o estado vale até o próximo deploy.

### 6. Ajuste do Django feito pela plataforma (opcional)

Para `ALLOWED_HOSTS` e `CSRF_TRUSTED_ORIGINS` fixos, o Cobogó pode gerar **na imagem, nunca no repositório**, um módulo `cobogo_platform_settings.py` que importa o settings do projeto e **acrescenta** o host (de uma variável da plataforma, como a `PORT`) e a URL. O `DJANGO_SETTINGS_MODULE` da imagem aponta para ele. Nada do que o projeto definiu se remove, e a mudança aparece no plano e no Dockerfile.

- **Nunca por padrão:** a análise pergunta, com `não` como valor inicial (override do plano, DEC-24).
- Não toca em `SECURE_PROXY_SSL_HEADER`, cookies nem curingas.

## Ordem de entrega

| Parte | O que entra | Prova |
| --- | --- | --- |
| **A** | Seções 4 e 5 (avisos silenciosos, explicação do 400, release degradada) | o `fec` mostra os avisos na primeira análise; os repositórios de antes não ganham avisos falsos |
| **B** | Seções 1 a 3 (mapa, irmãs, criação do par, sugestões) | o `fec` vira dois projetos com um clique; o frontend sobe com `VITE_API_URL` sugerido; mais dois monorepos reais (FastAPI com React, Express com React) |
| **C** | Seção 6 (ajuste do Django, opcional) | o `fec` responde 200 e o admin faz login com o override ligado |

Cada parte é um commit e uma subida na VM. O `fec` só fica completo com as DEC-50 (MySQL e Redis) e DEC-51 (worker).

## Alternativas consideradas

- **Detectar irmãs só com a raiz vazia** (o que existe): perde o caso mais comum, um backend na raiz e o frontend numa subpasta.
- **Um projeto com vários componentes** (a imaginação da #39): exige mudar imagem e release por componente. Projetos pares entregam o essencial hoje, e a DEC-51 resolve o que é do mesmo código com processos.
- **Aplicar as sugestões sozinho:** muda o ambiente sem a pessoa ver (RN-13).
- **Ajustar o `ALLOWED_HOSTS` sempre:** faria o `fec` funcionar sem pergunta, mas altera o comportamento de segurança de qualquer Django sem aviso. Fica opcional.
- **Detector de migrações quebradas:** o defeito é do projeto e o `migrate` já falha com mensagem clara na aba Comandos. Fica de fora.

## Consequências e limites

- O mapa e a lista de irmãs são heurísticos: podem oferecer uma pasta que não é aplicação (nunca a criam sozinhos) e deixar de ver um layout incomum.
- O caminho da API sugerido vem de um literal no código; sem ele a sugestão é só a URL.
- O ajuste do Django cobre settings de módulo único e de pacote; um que lê o host de outro jeito continua pedindo mudança no código.

## Decisões do autor (22/11/2026)

1. A proposta se divide em três DECs (49, 50 e 51), nessa ordem.
2. Valores sugeridos entre pares **sem tabela de ligação** por enquanto; a tabela entra com a #39.
3. Ajuste do Django opcional, com `não` por padrão (a confirmar na leitura desta DEC).
4. Os avisos são só os silenciosos; o detector de migrações sai.
5. Depois das partes do Cobogó, a lista dos erros do `fec` que também falhariam num deploy manual, para o autor corrigir; então o teste de novo.

## Implementação da parte A (22/11/2026)

- **Avisos do Django (HEU-04.5):** `CSRF_TRUSTED_ORIGINS` literal (a lista é citada); `SECRET_KEY` com padrão literal (`os.environ.get('X', 'literal')`, `config('X', default='…')`), sem repetir o valor; mídia servida só por `static(MEDIA_URL…)` no módulo de `ROOT_URLCONF`, com `MEDIA_ROOT` definido e `DEBUG` não literal verdadeiro. Valem para o settings que vai rodar, como os demais.
- **Frontend que cai para `localhost`:** o extrator JavaScript vê `import.meta.env.X || 'http://localhost…'` (e `??`, `process.env`), fora de testes; a HEU-09 põe na variável a dica `localhost_default`, que cita o arquivo e a linha.
- **Sonda:** resposta 4xx marca a release como `degraded`; um Django que responde 400 ganha a causa provável no evento da sonda (`diagnosis.ProbeCause`); o painel mostra a etiqueta "degradada" e o aviso na visão geral.
- **Verificação:** testes do analisador (casos novos de Django, dica do frontend), do diagnóstico e do painel; os dez repositórios reais da DEC-48 sem aviso falso (o único disparo novo, `render-examples/django`, é verdadeiro: `SECRET_KEY` com padrão); o `fec` na pilha local completa: deploy no ar, sonda 400, release degradada com a causa, os cinco avisos na análise (`ALLOWED_HOSTS`, `CSRF_TRUSTED_ORIGINS`, `SECRET_KEY`, mídia, migrações) e o painel com o aviso.

## Implementação da parte B (22/11/2026)

- **O mapa (`components` na análise, `internal/analyzer/components.go`):** a aplicação da pasta, os serviços e processos que o código pede e as aplicações irmãs. O papel sai das dependências e dos scripts (`frontend` por UI ou ferramenta de build, `backend` por framework de servidor ou Django REST, `fullstack` por Next, Nuxt, Remix, SvelteKit, Astro ou Django com templates). Serviços: PostgreSQL (provido) e, **fora de suporte e com o motivo**, MySQL, MongoDB, Redis (fila quando há worker, cache nos outros casos) e worker (Celery, RQ, BullMQ). Cada peça traz a evidência.
- **Irmãs:** subpastas até dois níveis, sem entrar numa pasta que já é aplicação; um sinal de aplicação (JavaScript: script `build` ou `start` com dependência ou ferramenta conhecida; Python: `manage.py` ou framework web nas dependências); ficam de fora `docs`, `examples`, `tests`, `scripts`, `tools`, `packages`, `static`, `media` e similares, e workspaces. A mesma busca substitui a do aviso de monorepo com a raiz vazia.
- **Sugestões entre pares (`/env/suggestions`):** sem tabela nova; o par é outro projeto do mesmo dono com o mesmo `repository_url`. O papel do par vem do mapa desta análise (o par dentro desta pasta é uma irmã) ou da análise dele. Catálogo fechado e nada aplicado sozinho: `ALLOWED_HOSTS` (host sem porta), `CSRF_TRUSTED_ORIGINS` (URL), URL do frontend (`FRONTEND_URL`…) e URL do backend (`VITE_API_URL`…) mais o caminho que o código usa como padrão (`/api`).
- **Painel:** o cartão "Mapa do repositório" na visão geral do projeto, com o botão **Criar projeto para esta pasta** nas irmãs (cria o projeto com o `root_directory` e o slug `<projeto>-<pasta>`, sem deploy) e, se o par já existe, o link para ele.
- **Verificação:** testes do analisador (papéis, irmãs, pastas que não são aplicação), da API (pares, repositório diferente, variável já definida) e do painel; o mapa nos dez repositórios reais da DEC-48 sem irmã falsa; o `fec` na pilha local completa: backend no ar, mapa com Django REST, MySQL, Redis, Celery e `frontend/` como irmã, criação do projeto da irmã, `VITE_API_URL` sugerido como `http://fec.localhost:8088/api` e `FRONTEND_URL` como `http://fec-frontend.localhost:8088`, e, depois de gravar a sugestão, o frontend publicado com a URL do backend dentro do JavaScript. Não cliquei o botão do painel (a chamada é a mesma que fiz pela API); o link para o par existente foi visto no navegador.

