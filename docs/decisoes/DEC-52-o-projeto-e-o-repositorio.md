# DEC-52 — O projeto é o repositório: aplicações, serviços e o que a plataforma já sabe

> **Estado:** aprovada pelo autor em 23/11/2026 (respostas no fim); **partes A, B e C implementadas** (ver "Implementação").
>
> **Escopo:** colar o link do repositório e o Cobogó cuidar do resto. Um projeto passa a ser o repositório inteiro, com as aplicações (backend, frontend), os serviços (banco, Redis) e os processos (worker) dentro dele; as variáveis que a plataforma conhece são preenchidas e mantidas por ela; as migrações e o primeiro administrador deixam de ser trabalho manual.
>
> **Relaciona-se com:** DEC-49 (mapa do repositório, irmãs e sugestões entre pares; a alternativa "um projeto com vários componentes" foi adiada lá), DEC-50 (serviços por engine), DEC-51 (processos), DEC-39 (variáveis e sugestões), DEC-42 e DEC-44 (variáveis vinculadas a um serviço), DEC-45 (comandos), RN-13 (migrações), issue #39.

## Contexto: o que a pessoa fez à mão no `fec`

O teste real na VM (23/11/2026) funcionou, mas a pessoa precisou de doze passos manuais, e quase todos eram coisas que o Cobogó já sabia:

| Passo manual | O Cobogó já sabia? |
| --- | --- |
| Criar o projeto do backend e, à parte, o do frontend (`fec`, `fec-frontend`) | sim: o mapa mostra `frontend/` como irmã |
| Responder "onde está o banco?" e "onde está o Redis?" | sim: a evidência era forte e não havia variável definida |
| Acertar os nomes `DB_*` no formulário | sim: a análise leu `DB_HOST` em `settings.py` |
| Gerar o `SECRET_KEY` | sim: era "geradável" |
| `ALLOWED_HOSTS`, `CSRF_TRUSTED_ORIGINS`, `FRONTEND_URL` | sim: são os endereços que ele mesmo publica |
| `VITE_API_URL` no frontend | sim: a URL do backend mais o `/api` lido do código |
| Rodar `migrate` na aba Comandos | sim: é Django com `migrations/` |
| Criar o superusuário (e descobrir que o comando não aceita teclado) | sabe que é Django; não sabe a senha |
| Achar o endereço do admin (`/api/admin/`) | sim: está em `urls.py` |
| Refazer tudo quando o domínio das aplicações mudou | sim: o domínio é dele |

Só o que é segredo de terceiros (o SMTP do e-mail) e a senha do administrador são, de fato, da pessoa.

## Decisão

### 1. Projeto = repositório

O projeto passa a ser o **repositório** (link e ramo). Dentro dele:

- **Aplicações:** cada pasta que é aplicação (a raiz e as irmãs da DEC-49), com o papel (`backend`, `frontend`, `fullstack`), o próprio build, imagem, releases, logs e comandos. Uma aplicação é o que hoje é um "projeto": o deploy, a sonda, o roteamento e o isolamento continuam por aplicação.
- **Serviços:** o banco e o Redis do projeto (DEC-50), ligados à aplicação que os usa.
- **Processos:** o worker e o agendador (DEC-51), dentro da aplicação de onde vêm.

**No modelo:** uma tabela nova agrupa (o projeto) e a tabela de hoje vira a aplicação, com `project_id` e `role`. Deploys, releases, variáveis, volumes, serviços e comandos continuam presos à aplicação: nada disso muda de dono, e o que já funciona continua funcionando. A rede de cada aplicação continua separada; o frontend estático não alcança o banco.

**No painel:** `/projetos/fec` com abas **Visão geral** (o mapa, o estado de cada peça e os endereços), uma aba por aplicação (**Backend**, **Frontend**: deploys, logs, variáveis, comandos) e **Banco de dados** (os serviços e backups). Um botão **Publicar** no projeto publica tudo; cada aba tem o seu, para publicar só aquela aplicação.

**Endereços:** uma aplicação sozinha continua em `<projeto>.<domínio>`. Com frontend e backend, o frontend fica com o endereço do projeto (`fec.cobogo.dev`, é o que se visita) e o backend com `<projeto>-api.<domínio>` (`fec-api.cobogo.dev`). Subdomínios, e não caminhos no mesmo endereço: rotear `/api` para um e o resto para o outro exige adivinhar os prefixos do backend (`/api`, `/admin`, `/media`, `/static`), e um erro ali quebra o site em silêncio.

**Os projetos que já existem:** a migração cria um projeto para cada um, e **junta numa só** as aplicações do mesmo dono com o mesmo repositório (o `fec` e o `fec-frontend` de hoje viram um projeto com duas aplicações). Os endereços que já existem não mudam.

### 2. Colar o link e publicar

Criar um projeto pede o link (e o ramo). O Cobogó analisa a raiz e as irmãs e mostra **o que vai criar**, antes de criar:

```
fec                                     ocnaibill/fec · main
  Frontend   frontend/  React + Vite (site estático)       fec.cobogo.dev
  Backend    /          Django REST, Python 3.13           fec-api.cobogo.dev
             worker     celery -A backend worker           (celery.py:7)
  Banco      MySQL 8.4 do Cobogó                           (settings.py:120)
  Redis      Redis 8 do Cobogó                             (CELERY_BROKER_URL, celery.py:9)
  Variáveis  9 definidas pelo Cobogó · 4 suas, sem valor (EMAIL_*)
```

Um botão **Criar e publicar** faz tudo, na ordem: serviços, backend (com as migrações, item 4), frontend. Cada linha pode ser trocada antes (desligar o worker, usar um banco que já existe, outra pasta), e só o que é ambíguo de verdade vira pergunta: duas engines possíveis, um endereço de serviço escrito no código, dois settings do Django.

### 3. As variáveis que a plataforma conhece são dela

Uma origem nova de variável, **`platform`**, ao lado de `linked` (DEC-42): o valor é calculado pelo Cobogó, **mostrado** na aba Variáveis como "definida pelo Cobogó", com a regra que o gerou, e **recalculado** quando a base muda (o domínio das aplicações, o nome do projeto, um par novo). Só vale para nomes que a análise encontrou no código e que a pessoa não definiu; **editar à mão transforma em `user`**, e o Cobogó para de mexer (um botão volta ao valor da plataforma).

O catálogo é o da DEC-49 seção 3, agora aplicado em vez de sugerido:

- **Do próprio endereço:** `ALLOWED_HOSTS` (o host), `CSRF_TRUSTED_ORIGINS` (a URL, e a do frontend do mesmo projeto), e as variantes `DJANGO_*`.
- **Do frontend do projeto:** `FRONTEND_URL`, `FRONTEND_ORIGIN`, `CLIENT_URL`, `CORS_ALLOWED_ORIGINS`.
- **Do backend do projeto:** `VITE_API_URL`, `REACT_APP_API_URL`, `NEXT_PUBLIC_API_URL`, `VUE_APP_API_URL`, `API_URL`, `BACKEND_URL`, com o caminho que o código usa como padrão (`/api`).
- **Segredos geráveis** (`SECRET_KEY` e os da DEC-39): gerados uma vez, com origem `generated`, como hoje, só que sem clique.

Uma variável de build que muda (o `VITE_API_URL` depois de trocar o domínio) marca o frontend como **desatualizado**, e o painel oferece republicar.

### 4. Migrações como fase de release (revê a RN-13)

A RN-13 diz que migrações "exigem comando declarado e política explícita". A proposta mantém a política explícita, mas deixa de exigir que a pessoa a escreva:

- Um campo novo do plano, **`release_command`**, com origem e evidência como os outros. Para Django com pasta `migrations/`, a análise propõe `python manage.py migrate --noinput` (HEU nova, com o arquivo como evidência). A fase `release` do `Procfile` vence a inferência. Outros frameworks (Alembic, Prisma, Flask-Migrate) só por declaração, por enquanto.
- O comando roda **uma vez por deploy**, num contêiner descartável da imagem nova (a jaula da DEC-45), **depois** dos serviços e **antes** de a aplicação nova subir. Se falha, o deploy falha com o log, e a release anterior segue no ar.
- O plano e a linha do tempo mostram "Migrações: `python manage.py migrate --noinput` (rodam a cada deploy)", e o override `none` desliga. Rollback de imagem continua **não** desfazendo migrações (o aviso fica).

### 5. Comandos com valores só desta execução, e receitas por evidência

> Revisto na implementação (23/11/2026), pela observação do autor: um formulário de administrador seria Django puro, uma feature feita para o caso cuja resposta já se sabe. O que entra é geral; o Django é só a primeira linha de um catálogo.

- **O recurso geral:** um comando da aba Comandos (DEC-45), de qualquer stack, pode levar **valores só desta execução**: variáveis que só aquele contêiner descartável recebe (uma senha, um token de uso único). Vão cifradas da API ao worker, valem sobre as do projeto, saem da saída gravada como qualquer valor de variável e são apagadas ao concluir. Nunca voltam pela API, nem viram variável do projeto. A mesma reautenticação da DEC-45.
- **Receitas:** comandos conhecidos que pedem dados, montados por evidência e rodados por esse caminho. É um catálogo fechado, e só entra uma linha verificada com um projeto real. A primeira é **Criar administrador** do Django: com `django.contrib.auth` no settings, roda `createsuperuser --noinput`, e os campos saem do modelo de usuário (`USERNAME_FIELD` e `REQUIRED_FIELDS` do `AUTH_USER_MODEL`, ou `username` e `email` do padrão). Um modelo próprio que a análise não acha não ganha receita: o formulário não é adivinhado. Outros frameworks (Strapi, Directus, Flask-AppBuilder) entram como novas linhas quando houver um projeto para verificar.
- Os endereços úteis (como o do admin, lido de `urls.py`) ficam fora: seriam outra leitura específica de framework, e o caminho já aparece na rota do projeto.

### 6. Serviços sem pergunta quando a evidência é forte

Com evidência forte (a engine no settings, o broker lido de variável) e nenhuma variável de conexão definida, o Cobogó **cria o serviço gerenciado** e liga os nomes que o código lê, sem perguntar. A linha do tempo diz o que fez e por quê, e a aba Banco de dados permite trocar por um serviço que a pessoa já tem. A pergunta da DEC-50 continua para os casos ambíguos: o endereço escrito no código, duas engines, só a dependência sem uso.

## Ordem de entrega

| Parte | O que entra | Prova |
| --- | --- | --- |
| **A** | Itens 1 e 2: o projeto como repositório, a migração dos projetos atuais, o painel com abas, criar e publicar a partir do link | o `fec` de hoje vira um projeto com duas aplicações; um `fec` novo, só com o link, sobe as duas |
| **B** | Itens 3 e 6: variáveis da plataforma e serviços sem pergunta | o `fec` novo sobe sem nenhuma variável digitada (só o SMTP fica sem valor) e responde 200 pelos dois endereços; trocar o domínio atualiza as variáveis e pede a republicação do frontend |
| **C** | Itens 4 e 5: migrações e administrador | o `fec` novo tem as tabelas no primeiro deploy e o admin faz login com o usuário criado pelo formulário |

Cada parte é um commit e uma subida na VM. Depois da C, o autor apaga os `fec` da VM e cola o link de novo.

## Alternativas consideradas

- **Continuar com projetos pares** (DEC-49): é o que existe; deixa à pessoa o trabalho de juntar as peças e não mostra o projeto como um todo.
- **Uma imagem para o repositório inteiro** (o frontend servido pelo Django, por exemplo): mexe no projeto da pessoa e não serve para backends que não servem arquivos.
- **Um endereço com caminhos** (`/api` para o backend): menos CORS e CSRF, mas depende de adivinhar os prefixos do backend; pode entrar depois como opção.
- **Só sugerir as variáveis** (como hoje): a pessoa ainda precisa clicar e, pior, refazer quando o domínio muda.
- **Migrar só quando a pessoa pedir** (RN-13 como está): é o passo que todo deploy de Django precisa, e esquecer dá 500 em tudo.

## Consequências e limites

- É a maior mudança de modelo desde o começo: o painel e a API ganham um nível (projeto → aplicação). O que é por aplicação não muda de lugar, o que limita o risco.
- O Cobogó passa a mudar o ambiente sozinho (variáveis `platform`, serviços, migrações). Tudo aparece no plano, na linha do tempo e na aba Variáveis, e tudo tem como desligar.
- Uma migração que não volta continua sem volta; o backup antes de cada migração fica como evolução.
- Repositórios com mais de um backend (dois serviços de API) entram no mesmo modelo, mas as variáveis entre eles (qual é "a API") ficam para quando houver o caso.

## Decisões do autor (23/11/2026)

1. **Projeto = repositório**, com as aplicações dentro (a tabela de hoje vira a aplicação), como no item 1.
2. **Variáveis da plataforma aplicadas e mantidas** (origem `platform`), não só sugeridas.
3. **Migrações a cada deploy** do Django, ligadas por padrão e desligáveis; a RN-13 é revista na parte C.
4. **Serviços gerenciados sem pergunta** quando a evidência é forte.
5. Endereços: não perguntado; segue o padrão do item 1 (frontend em `<projeto>.<domínio>`, backend em `<projeto>-api.<domínio>`), a confirmar na leitura.

## Implementação da parte A (23/11/2026)

- **O modelo (migração 0020):** a tabela `stacks` é o projeto (dono, slug, repositório e a leitura do repositório: `scan_state`, `scan`, `scan_error`); `projects` continua sendo a unidade de deploy e ganha `stack_id` e `role`. A migração junta as aplicações vivas do mesmo dono e repositório num projeto (o nome é o slug mais curto) e tira o papel da última análise. Na cópia dos dados da VM: `fec` e `fec-frontend` viraram o projeto `fec`, com os papéis backend e frontend. `POST /projects` (o caminho antigo e o botão das irmãs) põe a aplicação no projeto do mesmo repositório; excluir a última aplicação exclui o projeto e libera o nome.
- **Colar o link:** `POST /stacks` cria o projeto e enfileira a operação `scan_stack`; o worker clona, analisa a raiz e cada irmã (`analyzer.Survey`, com a mesma conferência de pasta do deploy) e grava a proposta com o papel, o perfil, o que cada uma pede e o endereço (`domain.ProposeAppSlugs`: uma aplicação fica com o nome do projeto; com várias, o frontend fica com ele e o backend com `<projeto>-api`; o que colide com uma aplicação viva ou um nome reservado ganha `-2`). `POST /stacks/{id}/apps` cria as escolhidas e publica, o backend antes do frontend; `POST /stacks/{id}/deployments` publica tudo de novo; `DELETE /stacks/{id}` exclui todas pela DEC-38. O worker grava o papel de cada aplicação a cada análise (privilégio só na coluna `role`).
- **Painel:** a lista da visão geral mostra projetos, com as aplicações e os endereços de cada um; "Novo projeto" pede só o link (e o nome, opcional); `/projetos/<slug>` tem as abas **Visão geral** (a proposta com "Criar e publicar", a tabela das aplicações com o que cada uma pede), uma por aplicação (a página de antes, sem a trilha e sem a aba Banco) e **Banco de dados** (os serviços da aplicação que não é só frontend). Um link antigo `/projetos/<id da aplicação>` leva à aba dela.
- **Verificação:** testes do analisador (o `fec` como backend e frontend; raiz vazia com duas pastas), dos slugs, do store (agrupar por repositório, ler, propor, criar, nome repetido, excluir), da API e da matriz de privilégios; a migração aplicada a uma cópia dos dados da VM; e, na pilha local completa, o `fec` só com o link: a leitura em 4 s propôs `fec-api` (Django REST, pede MySQL, Redis e Celery) e `fec` (React + Vite), os deploys saíram na ordem do backend para o frontend, e com as respostas e variáveis que as partes B e C vão automatizar, os dois ficaram no ar (`/api/hello/` e o login do admin em `fec-api`, o React em `fec`), com MySQL, Redis e o worker; o painel no navegador com as quatro abas.
- **Ainda manual depois da parte A:** as perguntas do banco e do Redis, `SECRET_KEY`, `ALLOWED_HOSTS`, `CSRF_TRUSTED_ORIGINS`, `FRONTEND_URL`, `VITE_API_URL` (resolvidos na parte B), as migrações e o administrador (parte C).

## Implementação da parte B (23/11/2026)

- **Quem grava é a API**, porque só ela cifra valores (DEC-33; o worker só lê variáveis). A preparação (`prepareStack`) roda ao confirmar a proposta, ao publicar o projeto ou uma aplicação e ao trocar o domínio ou o esquema das aplicações. Ela usa a última análise da aplicação ou, antes do primeiro deploy, a leitura do repositório, que agora guarda as variáveis, os serviços e o caminho da API de cada aplicação.
- **Variáveis da plataforma (origem `platform`, migração 0021):** o catálogo fechado da DEC-49 aplicado só a nomes que o código lê: `ALLOWED_HOSTS` (o host, sem porta), `CSRF_TRUSTED_ORIGINS` (a URL da aplicação e a do frontend), `FRONTEND_URL` e afins (a do frontend, numa aplicação que não é o frontend), `CORS_*` (as do frontend), `VITE_API_URL` e afins (a do backend com o caminho que o código usa, numa aplicação que não é o backend), `APP_URL` e afins (a própria). A tabela `platform_env` guarda o valor gravado e a regra (endereços públicos, nunca segredos) para recalcular sem decifrar; a lista de variáveis os devolve, e o painel mostra "definida pelo Cobogó", o valor e a regra. Trocar o valor torna a variável da pessoa, e a plataforma para de mexer; apagá-la a devolve à plataforma no próximo cálculo. Um valor de build que mudou depois do último deploy pede "Publicar agora".
- **Segredos geráveis** (`SECRET_KEY`): gerados uma vez, com a origem `generated`, só se ainda não existem.
- **Serviços sem pergunta (item 6):** uso com evidência forte, o endereço lido de variável e uma só engine relacional criam o serviço gerenciado, com os nomes que o código lê, se a aplicação nunca teve um serviço daquela engine (apagar o serviço do Cobogó é uma decisão que a plataforma não desfaz) e nenhuma variável de conexão tem valor. Endereço escrito no código, duas engines ou só a dependência continuam como pergunta.
- **Verificação:** teste da API com o `fec` inteiro (os valores e as origens, o `SECRET_KEY` gerado, MySQL e Redis vinculados, nada para `EMAIL_*` e `PORT`, o valor do frontend com `/api`, a lista com o valor e sem o segredo, troca de domínio recalculando sem mexer no que a pessoa digitou, Redis apagado que não volta); e, na pilha local completa, o `fec` apagado e criado de novo **só com o link, sem nenhuma resposta nem variável digitada**: a preparação gravou `VITE_API_URL` no frontend, `ALLOWED_HOSTS`, `CSRF_TRUSTED_ORIGINS` e `FRONTEND_URL` no backend, gerou o `SECRET_KEY` e criou MySQL e Redis; os dois deploys saíram no ar com a release `healthy`, `/api/hello/` e o login do admin responderam 200, e o JavaScript do frontend traz a URL da API do projeto.

## Implementação da parte C (23/11/2026)

- **A fase de release (HEU-04.8, campo `release_command`):** declarada pelo `release:` do Procfile (qualquer comando) ou, num Django com migrações no repositório (a primeira `migrations/0001_*.py` é a evidência), inferida como `python manage.py migrate --noinput`. O override troca o comando, e `none` desliga. O aviso antigo "o Cobogó não roda a fase release" saiu. O plano a mostra; o worker a roda **depois** dos serviços e dos volumes (com a release anterior já pausada, se há volume) e **antes** de criar a aplicação nova, num contêiner descartável da imagem nova (`cobogo-release-<deploy>`, papel `release`, a jaula dos comandos), com as variáveis, a rede, os volumes e os arquivos da aplicação, até 10 min. Uma saída diferente de zero falha o deploy com a saída (sem os valores das variáveis) e o código, e a versão anterior segue no ar. A linha do tempo mostra o comando, o fim da saída e a duração. Só o perfil Python por enquanto: o Node não lê o Procfile.
- **RN-13 revista:** a migração agora pode ser inferida, mas a política continua explícita, porque está no plano, com evidência, e desliga com `none`. Rollback de imagem continua não desfazendo migração.
- **Valores só desta execução (migração 0022):** `project_commands` ganha `input_names` (os nomes, que a API mostra), `inputs` (o JSON cifrado com a chave das variáveis e o id do comando como dado associado) e `recipe`. A API grava, mas não tem `SELECT` na coluna cifrada (DEC-33). O worker abre os valores, entrega-os ao contêiner, os junta à redação da saída e os apaga ao concluir, inclusive quando o comando é interrompido. Limites: 10 valores, nomes em maiúsculas, até 4096 bytes cada, nunca `PORT` nem `COBOGO_*`.
- **Receitas:** `recipes` na lista de comandos e `recipe` no pedido. A API confere que a receita existe para o projeto, que os campos obrigatórios vieram e que nada além deles veio. No painel, a seção "Ações do projeto" tem o formulário (senha como campo de senha), e o comando livre ganha "Valores só desta execução"; o histórico mostra os nomes, nunca os valores.
- **Verificação:** testes do analisador (fase release do Procfile, do Django com e sem migrações, `none`, o modelo de usuário com `USERNAME_FIELD` e `REQUIRED_FIELDS`), da API (receita do modelo próprio e do padrão, modelo não achado sem receita, as recusas, o valor que não volta na lista, no comando nem no log, e que some ao concluir) e a suíte completa com Docker; na pilha local, o `fec` apagado e criado de novo só com o link: o plano trouxe a fase de release, a migração rodou no primeiro deploy (2 s, a última linha `Applying users.0003… OK`), a API respondeu sem erro de tabela, a receita criou o administrador (`Superuser created successfully.`, sem a senha na saída), e o login no `/api/admin/` funcionou com ele.
