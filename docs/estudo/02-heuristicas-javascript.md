# 02 — Heurísticas do catálogo JavaScript (`p-js-0.7`)

Catálogo definido em `catalog.go:105-138` (versão `p-js-0.7`, a da DEC-54; a DEC-53 foi a `p-js-0.6`). Extratores: `manifest`, `env-files`, `source`, `static`. Regras: HEU-01, 02, 03, 05, 08, 09, 10, 11. Campos do plano que o catálogo conhece: perfil, gerenciador, comando de início, comando de build, modo, versão do Node, versão do Bun, diretório de saída, fallback de SPA, porta, caminho de saúde, ecossistema, a escolha de instalar sem lockfile (`allow_unlocked`, DEC-53 H) e a de rodar o build com o OpenSSL antigo (`build_legacy_openssl`, DEC-54 B) (`catalog.go:121-136`).

Formato de cada ficha: **o que decide → evidências → como (código) → por quê → quando pergunta/avisa/recusa → limite**.

---

## HEU-00 — Despacho de ecossistema (`ecosystem.go`)

**O que decide:** a pasta é JavaScript, Python, os dois, ou um monorepo?

**Como:**
- Marcadores na raiz: `package.json` (JS) e `requirements.txt`, `pyproject.toml`, `uv.lock`, `poetry.lock`, `Pipfile`, `Pipfile.lock`, `pdm.lock`, `setup.py`, `manage.py`, `runtime.txt`, `environment.yml` (Python) (`ecosystem.go:22-23`).
- `detectEcosystem` (`33-52`): os dois presentes → `both`; só Python → Python; senão JavaScript.
- `both` vira a **pergunta HEU-00** (`ecosystemQuestion`, `63-88`) com duas opções e uma nota explicando cada uma. A resposta é o override `ecosystem` e a análise roda de novo.
- Raiz sem nada → `findSiblings` (`components.go:238-266`) → `monorepoResult` (`ecosystem.go:92-120`): resultado `unsupported` que lista as pastas achadas e manda criar um projeto por pasta. Se há Python **e** JS, a mensagem já ensina a ligar frontend e backend (variável pública do build + CORS).

**Por quê:** no mundo real um repositório Django costuma ter `package.json` só para Tailwind/ESLint. Escolher errado gera um build que não faz sentido. Perguntar custa uma resposta; errar custa um deploy quebrado.

**Observação:** com a DEC-52 o caso monorepo deixou de ser beco sem saída: o `Survey` (`survey.go`) analisa cada subpasta e propõe as aplicações. O `monorepoResult` continua valendo para a análise de **uma** pasta.

**Raiz mista (DEC-53 F).** O caso `both` (Python **e** JavaScript na mesma pasta) apareceu em 9 de 37 repositórios do corpus externo (Django com o React em `src/`, Flask com `package.json` ao lado...). Por isso o `Survey` não para mais na pergunta: `mixedRootApps` (`survey.go:154-174`, chamada em `survey.go:21-39`) usa `describeDirAs` (`components.go:152-160`) para perguntar ao mapa, **por ecossistema**, se aquela parte é uma aplicação, analisa cada uma com o override `ecosystem` e as propõe como **duas aplicações na mesma pasta**, cada uma com `PinnedEcosystem`. Um `package.json` só de ferramentas (Tailwind) não é reconhecido como aplicação e não vira proposta. A pergunta HEU-00 continua existindo para quem cria o projeto por outro caminho e quando **nenhuma** das partes é uma aplicação. Persistência: coluna `projects.ecosystem` (migração `0023`), chave de aplicação = pasta + ecossistema (`store.AppKey`, `store/stacks.go`), e `CreateDeploymentWithJob` (`store/store.go:200-213`) acrescenta o override `ecosystem` a todo deploy dela, para a pergunta não voltar.

---

## HEU-01 — Perfil e gerenciador (`rules_node.go:64-166`, versão 0.5)

**Campos que possui:** `profile`, `package_manager`, `allow_unlocked` (DEC-53 H).

**Evidências:** `package.json` válido, lockfiles (`manifest.go:18-24`: `package-lock.json`→npm, `yarn.lock`, `pnpm-lock.yaml`, `bun.lock`/`bun.lockb`→bun), `workspaces`, lockfiles **acima da pasta** (`ancestors.go`, DEC-53 G) e a decisão estática (HEU-03).

**Como, na ordem das linhas:**
1. `77-80`: `package.json` inválido (JSON quebrado, symlink, grande demais) → **não suportado** com o motivo.
2. `81-89`: `jsProfile` (`static.go:257-287`) decide o perfil. Site estático **sem build** (só `index.html`) → perfil `P-STATIC`, gerenciador `none`, sem exigir lockfile.
3. `90-93`: sem `package.json` e sem `index.html` → não suportado.
4. `97-110`: lockfile de **yarn/pnpm** → não suportado (com o nome). Mesmo se também houver npm/bun, é "múltiplos lockfiles" (`108-109`).
5. `111-115`: `workspaces` declarado → não suportado (monorepo JS ainda fora).
6. `117-139`: **sem nenhum lockfile** (DEC-53 G e H). O extrator da manifest sempre emite um lockfile "provável" quando não há nenhum (`manifest.go:122-131`: `npm`, ou `bun` se `packageManager` diz `bun@`), com `Value = "unlocked"` (`unlockedMarker`, `manifest.go:137`). Assim as demais regras analisam como se houvesse um, e **todas as perguntas vêm de uma vez**. A HEU-01 então decide:
   - há lockfile numa pasta **acima** (`kindAncestorLock`) → **não suportado**, dizendo qual arquivo e que é de um workspace (`122-126`);
   - o usuário respondeu `allow_unlocked=false` → não suportado, com a causa (`127-129`);
   - respondeu `true` → **aviso**: "o build resolve as versões mais novas que o package.json permite" (`130-131`);
   - sem resposta → **pergunta** `allow_unlocked` (opção `true`), que diz o risco (não reprodutível) (`132-138`). Antes da DEC-53 isto era recusa.
7. `141-150`: `package-lock.json` **e** `bun.lock` → **pergunta** `package_manager` com as duas opções (`jsManager`, `32-58`, devolve vazio nesse caso até o override).
8. `152-157`: override de perfil que não casa com o gerenciador → **conflito**.
9. `159-163`: propostas de origem `inference` (perfil e gerenciador).

**Por quê:** o lockfile é a evidência mais forte de qual gerenciador o projeto usa e garante instalação reproduzível (`npm ci`). Sem lockfile o build não é reprodutível; por isso **perguntar com o risco dito** (e não aceitar em silêncio). É a política que o Python já tinha para `requirements.txt` sem versões fixadas. Yarn/pnpm foram deixados para depois por decisão de escopo (DEC-25/36) e continuam recusados com a causa: eram 9 de 37 repositórios do corpus externo.

**Planner (DEC-53 H):** com `allow_unlocked=true`, `npm ci` vira `npm install --no-audit --no-fund`, o Dockerfile não copia o `package-lock.json` que não existe, e o Bun perde o `--frozen-lockfile` (`planner/planner.go:169-171` e `202-206`, `planner/bun.go:71-73` e `101-105`, `planner/static.go:116-120` e `128-132`).

**Limite:** `bun.lock` (texto) tem precedência sobre `bun.lockb` quando os dois existem (`35-38`).

---

## HEU-02 — Comando de execução, build e modo (`rules_node.go:170-240`)

**Campos:** `start_command`, `build_command`, `execution_mode`. **Requer** HEU-01.

**Como:**
- **Modo** (`180-182`): sempre `production` por padrão (origem `default`). **Nunca é inferido** (DEC-25); só override muda.
- **Comando de início**, em `190-217`:
  - perfil estático → não há comando a pedir;
  - sem `scripts.start` → **pendência**, com a lista de scripts como **opções** (`startOptions`);
  - `scripts.start` chama ferramenta de desenvolvimento (`devServerTool`) → **pendência**: "informe o comando de produção". **DEC-53 C:** se é o caso simples `nodemon <arquivo>.js` (a ferramenta e um arquivo JavaScript, sem opções), a pendência oferece `node <arquivo>.js` como **primeira opção**, derivada por `productionEquivalent` (`rules_node.go:716-728`, usada em `206-212`). Nunca é escolhida sozinha: a nota diz que é o mesmo arquivo sem recarga e pede conferência;
  - senão → proposta de origem `declaration`.
- **Build** (`232-236`): `scripts.build`, se existe, é proposto como `declaration`.
- **`scripts.prepare` que só instala ganchos do Git** (husky) → aviso (`218-231`); o Dockerfile remove esse script antes do `npm ci` (`planner/planner.go:207-209`). Detecção em `domain/plan.go:51` (`GitHookOnly`).

**`devServerTool` (`541-587`)** reconhece, por lista fechada: `nodemon`, `ts-node-dev`, `webpack-dev-server`, `next|nuxt|nuxi|astro|remix dev`, `webpack serve`, `react-scripts start`, `tsx watch`, `vite` (sem argumento, `dev`, `serve` ou flag), as flags `--watch`/`--hot` e, desde a DEC-53, `-w` em `webpack`, `tsc` e `rollup`. Ele quebra o comando por `&&`, `||`, `;`, `|` (`commandSegments`) e, em vez de só ignorar `VAR=x`, `npx` e `bunx`, usa a **normalização de comandos** (próxima seção).

**Aviso de arquivo de variáveis ausente (DEC-53 D).** Se `scripts.start` ou `scripts.build` manda carregar um arquivo de variáveis (`node --env-file=x`, `dotenv -e x`, `env-cmd -f x`, `dotenvx run -f x`; `referencedEnvFiles`, `manifest.go:178-210`) e o arquivo **não está no repositório** (costuma estar no `.gitignore`), o extrator emite `script.env_file_missing` (`emitMissingEnvFiles`, `manifest.go:211`) e a HEU-02 avisa (`rules_node.go:223-231`), citando o arquivo e o `.example`/`.sample` vizinho, se há. `--env-file-if-exists` não avisa (aceita a falta). Sem o arquivo o Node recusa iniciar.

### Normalização de comandos (`commandnorm.go`, DEC-53 A)

Um script como `bunx --bun vite build`, `npx -y vite build`, `cross-env NODE_ENV=production vite build`, `dotenv -e .env -- next start` ou `./node_modules/.bin/vite build` roda a **mesma ferramenta** que `vite build`. As heurísticas que reconhecem ferramenta (`staticBuild`, `staticServeTool`, `devServerTool`) passam a olhar a ferramenta, não o que a embrulha:
- `commandTokens` (`31-57`) remove, em laço: atribuições `VAR=x`, `env`, `cross-env`, `npx`/`bunx`/`pnpx` (com as opções deles: `-y`, `--yes`, `--bun`, `-p pacote`), `npm|pnpm|yarn exec`, `bun x`, `dotenv`/`env-cmd` (com `-e`/`-f` e o arquivo) e `dotenvx run`;
- `skipFlags` (`59-73`) descarta opções no início, o valor das que levam um, e o `--`;
- `normalizeToolPath` (`75-90`): `./node_modules/.bin/vite` → `vite`; `node ./node_modules/<pacote>/bin/x.js` → `<pacote>` (com escopo, `@x/y`; `packageOfNodeModulesPath`, `92-107`).

**Por quê:** a lista antiga só pulava `npx`/`bunx` **sem opções**; um `--bun` ou `-y` fazia a ferramenta passar despercebida, e um frontend Vite com Bun virava P-BUN pedindo um comando de início que não existe. **Limite:** lista fechada de executores; um executor desconhecido deixa o comando como está.

**`startOptions` (`473-517`)** monta as opções de escolha:
- omite o que não é jeito de executar: `build`, `test`, `lint`, `format`, `typecheck`, `clean` (`461`), ganchos `pre*`/`post*` de outro script (`521`), e nomes com caracteres inseguros para um `sh -c` (`scriptNamePattern`, `454`);
- classifica cada uma como **produção** ou **desenvolvimento** (ferramenta de dev, ou nome `dev`/`watch`, `465`);
- ordena produção primeiro. O que foi omitido volta na resposta (`Omitted`) para o painel mostrar por quê.
- No Bun, os campos `module`/`main` do `package.json` viram opções `bun run <arquivo>` (`entryOptions`, `592-608`).

**Por quê:** `npm start` frequentemente roda o servidor de **desenvolvimento** (`vite`, `next dev`), que em produção é lento e inseguro. Nome do script não prova nada (por isso só *reforça* a classificação, `463-465`); o que decide é o comando. E o motor **não assume entrypoint** ("não é permitido assumir um entrypoint arbitrário", `196`).

**Limite:** scripts indiretos (`npm run dev` dentro de outro script) não são resolvidos (`devServerTool` é lista curta de propósito, `538-540`).

---

## HEU-03 — Site estático (`rules_node.go:612-639`, `static.go`)

**Campos:** `output_dir`, `spa_fallback`. A **decisão** estático vs servidor está em `detectStatic` (`static.go:208-251`) e alimenta a HEU-01.

**`detectStatic`, em ordem (DEC-37):**
1. Dependência SSR (`@sveltejs/kit`, `nuxt`, `@remix-run/react`, `@remix-run/node`; `static.go:29`) → **não é estático** (`209-215`).
2. Marcador SSR na configuração (Astro `output: 'server'|'hybrid'` ou `adapter:`; `static.go:33-34`, `86-93`) → não é estático (`216-218`).
3. Existe `scripts.build` (`221`): `staticBuild` (`132-158`) procura, **no último trecho que casa** do comando (`tsc -b && vite build` vale `vite build`), uma ferramenta estática: `vite`, `react-scripts`, `vue-cli-service`, `parcel`, `astro`, `next`, ou `bun build` com entrada `.html`. Sem ferramenta → não é estático.
   - `next` só é estático se a configuração tem `output: 'export'` (`226-229`).
   - Se há `scripts.start` e ele **é um servidor de verdade**, o projeto é de servidor (`232`). `staticServeTool` (`175-193`) lista o que *não* conta como servidor de produção: `vite`, `serve`, `http-server`, `live-server`, `sirv`, `react-scripts start`, `astro preview`...
4. Sem build mas com `index.html` na raiz e sem `start` real → site estático sem build, saída `.` (`246-249`).

**Diretório de saída (`236-243`), por prioridade:** opção do comando (`--outDir`, `--dest`, `--dist-dir`, `--outdir`; `flagValue`, `160-170`) > `outDir` literal na configuração (`configOutDir`, `32`; Vite e afins) > padrão da ferramenta (`staticTools`, `124-127`: vite→`dist`, react-scripts→`build`, vue→`dist`, parcel→`dist`, astro→`dist`, next→`out`). As duas primeiras são `declaration`; a terceira é `inference`. Sem saída → **pergunta** (`rules_node.go:625-627`).

**Fallback de SPA (`rules_node.go:631-635`):** vem da ferramenta (`staticTools[tool].spa`): Vite, CRA, Vue CLI, Parcel e Bun geram SPA (rota desconhecida → `index.html`); Astro e Next exportado, não.

**`cleanOutDir` (`static.go:105-115`)** é segurança: só aceita caminho relativo dentro do projeto (sem `/`, `..`, aspas, `$`, crase, espaço).

**Quando não é estático** mas parece: `ssrNotice` (`rules_node.go:643-670`) explica por que (ex.: "Next.js sem output: 'export' renderiza no servidor") e que precisa de um comando de produção.

**Por quê:** servir estático com nginx é mais barato, rápido e seguro que manter um processo Node. Mas **classificar SSR como estático quebra o site em silêncio**; por isso a regra é conservadora: qualquer sinal de SSR derruba a decisão. React também pode ter SSR; dependência não decide hospedagem sozinha.

**Exemplo (fec):** frontend Vite+React, `build: vite build`, sem `start` de servidor → P-STATIC, saída `dist`, SPA verdadeiro, nginx.

---

## HEU-05 — Porta e caminho de saúde (`rules_node.go:246-319`, versão 0.4)

**Campos:** `port`, `health_path`. **Requer** HEU-01.

**Ideia central (RN-02, DEC-35):** a plataforma **escolhe** a porta (8080) e a entrega na variável `PORT`. A regra só precisa saber se o código **lê** `PORT`.

**Como:**
- Caminho de saúde: padrão `/` (`256`), só override muda (DEC-26).
- Estático → porta da plataforma (nginx), origem `platform` (`258-263`).
- Refs a `PORT` em código **de runtime** (`264-270`; o escopo vem de `classifyScope`, `configvars.go:227-243`) → porta 8080, origem `platform`, evidências citadas (`277-282`).
- No Bun, `Bun.serve(...)` sem `port` e o `export default` do arquivo de entrada **leem `PORT` sozinhos** (`273-275`; extração em `configvars.go:172-179`).
- **Porta escrita no código, sem ler `PORT` (DEC-53 B, `289-302`).** `app.listen(8000)`, `app.listen(PORT)` com `const PORT = 8000;` no **mesmo arquivo**, `listen({ port: 8000 })` e `serve({ port: 8000 })` em código de execução viram a evidência `listen.port` (`listenPorts`, `configvars.go:77-98`; `numericConst`, `62-68`). Com **um** valor, a porta é a dele, origem `declaration`, citando arquivo e linha. Com dois valores diferentes → pendência listando-os. Teste e ferramentas não contam (mesmo `classifyScope`). **Isto revisa a DEC-36**, que perguntava a porta fixa de `Bun.serve`: o teste foi renomeado ("a fixed port in Bun.serve is a declaration of the code", `bun_test.go`).
- **Variáveis lidas por um schema (DEC-53 B).** `const env = schema.parse(process.env)` com a chave `PORT:` no schema (zod, envalid, t3-env) conta como leitura de `PORT`: o contrato da DEC-35 (`configvars.go:166-170`; `envPassedWhole`/`schemaPortKey`, `57-58`). Uma chave `PORT:` sem o ambiente inteiro ligado ao schema não prova nada.
- Só referência em teste/ferramenta → **pendência** "PORT referenciada apenas em código de teste... informe a porta" (`304-306`).
- Nada → pendência "nenhum contrato de porta identificado" (`308-311`).

**Por quê:** "o valor de reserva escrito no código (`PORT || 5006`) **nunca é adotado**": ele não é contrato, é um palpite do autor para rodar localmente. Se o código lê `PORT`, a plataforma entrega. **A diferença da DEC-53 B:** `app.listen(8000)` **sem** ler `PORT` não é um valor de reserva, é a única porta que o código usa; adotá-la como declaração (com override) é honesto, e a sonda de prontidão continua sendo a prova. Se o código nem lê `PORT` nem escreve uma porta que se ache, a plataforma **não sabe** em qual porta ele ouve, então pergunta.

---

## HEU-08 — Dockerfile e compose já presentes (`rules_node.go:676-694`)

Só **avisa**: "o repositório traz Dockerfile. O Cobogó não o usa nem o executa; gera o próprio plano". Roda mesmo se o perfil não é suportado (é informação, não decisão). Extração: `static.go:49-53` (JS) e `python_extract.go:183-187` (Python).

**Por quê:** usar o Dockerfile do usuário tiraria do Cobogó a capacidade de explicar cada decisão e de garantir limites e isolamento (DEC-37). E um compose costuma descrever o ambiente de **desenvolvimento**, não produção.

---

## HEU-09 — Variáveis de configuração (`rules_node.go:324-404`)

A regra mais "fina" do motor, porque conecta análise, segurança e a experiência do usuário. Não bloqueia o plano: só informa.

### Onde as variáveis são descobertas

| Fonte | Extração | Observação |
| --- | --- | --- |
| `.env.example`, `.sample`, `.template` | `envfiles.go:89-138` | Cada linha vira evidência com **estado** (vazia, placeholder, padrão, comentada, exposta) |
| `.env`, `.env.local`... (arquivos reais) | `envfiles.go:118-119` | **Só avisa** que estão versionados; os valores **nunca** são aproveitados |
| `process.env.X`, `process.env?.X`, `process.env["X"]`, `Bun.env.X`, desestruturação `{A, B} = process.env` | `configvars.go:30-59`, `186-205` | Acesso dinâmico → lacuna |
| `import.meta.env.X` | `configvars.go:40`, `151-156` | Configuração **de build** (Vite); ignora `MODE`, `DEV`, `PROD`, `SSR`, `BASE_URL` (`294`) |
| Python `os.environ["X"]`, `.get`, `getenv`, `env("X")`, `config("X")` | `python_extract.go:103-110`, `400-407` | `environ["X"]` marca `required` |

**`parseEnvLines` (`envfiles.go:48-67`)** lê `NOME=valor` sem executar nem expandir; linha comentada só conta no formato `# NOME_EM_MAIUSCULAS=` (prosa em comentário é ignorada).

### Classificação de cada variável (`envclass.go:33-47`)

| Classe | Regra | Efeito |
| --- | --- | --- |
| `platform` | Nome em `platformVars` (`PORT`) | A plataforma fornece; sem aviso ao usuário |
| `generable` | Nome em lista **fechada** de 11: `JWT_SECRET`, `SESSION_SECRET`, `COOKIE_SECRET`, `CSRF_SECRET`, `SECRET_KEY`, `SECRET_KEY_BASE`, `APP_SECRET`, `APP_KEY`, `AUTH_SECRET`, `NEXTAUTH_SECRET`, `ENCRYPTION_KEY` (`19-23`) | O Cobogó gera um valor aleatório |
| `public` | Prefixo `VITE_`, `REACT_APP_`, `VUE_APP_`, `PUBLIC_`, `NEXT_PUBLIC_` (`31`) | Vai para o bundle do navegador: **nunca é segredo** |
| `user` | O resto | A pessoa informa |

**Por que a lista de geráveis é fechada:** "nunca por padrão genérico (`*_KEY`, `*_TOKEN`, `*_PASSWORD`): uma credencial de terceiro gerada produziria um app quebrado sem erro claro" (comentário em `envclass.go:15-18`). Gerar uma `STRIPE_KEY` aleatória seria pior que pedir. Falso negativo vira entrada manual; falso positivo quebra o app.

### Estado no exemplo e a dica por variável

`exampleState` (`envclass.go:156-169`), em ordem:
1. comentada → `commented`
2. vazia → `empty` ("provavelmente opcional")
3. parece credencial real (`credentialKind`) → `exposed` ("provavelmente obrigatória; o Cobogó não usa esse valor")
4. placeholder (`isPlaceholder`) **ou nome secreto** → `placeholder` ("provavelmente obrigatória")
5. senão → `default` ("confirme para usá-lo"; o valor padrão não secreto é guardado)

`configHint` (`rules_node.go:408-435`) traduz isso em mensagem. Se a variável é usada no código de runtime mas **não está** no exemplo → "usada no código e ausente do exemplo" (`430`); se o projeto nem tem arquivo de exemplo → "referenciada apenas" (`432`).

`isPlaceholder` (`envclass.go:77-88`): `<...>`, só asteriscos, ou contém um de 21 marcadores (`changeme`, `your-`, `xxx`, `todo`, `example`, `dummy`, `-here`...), comparação em minúsculas (`68-73`).

### Detecção de credencial exposta (`envclass.go:91-153`)

`credentialKind` devolve só o **tipo**; o valor é lido em memória e descartado (DEC-24 item 8).
- 7 formatos por regex (`91-102`): chave de acesso AWS (`AKIA|ASIA` + 16), token do GitHub (`ghp_`, `github_pat_`...), Slack (`xox[abprs]-`), Stripe live (`sk_live_`), chave do Google (`AIza` + 35), chave privada (`-----BEGIN ... PRIVATE KEY-----`), JWT (`eyJ.eyJ.`).
- Se não casa e o **nome parece secreto** (`secretLikeName`, `53-64`: contém `SECRET`, `PASSWORD`, `TOKEN`, `PRIVATE_KEY`, `API_KEY`, `ACCESS_KEY`, `CREDENTIAL`, ou termina em `_KEY`), o valor não é placeholder e `looksRandom` (`137-153`) → tipo `high_entropy_secret`.
- `looksRandom`: ≥ 20 caracteres, sem espaço, sem `://`, não começa com `/` ou `.`, e **entropia de Shannon ≥ 3,5 bits** por símbolo (`152`). O comentário diz "Heurística; erra para os dois lados".

Avisos gerados (`rules_node.go:385-400`): arquivo `.env` versionado; credencial no exemplo ("revogue ou rotacione"; `437`); nome de segredo com prefixo público (ex.: `VITE_SECRET_KEY`: "qualquer visitante pode lê-lo", DEC-15).

### Dica de endereço local (DEC-49) (`rules_node.go:378-381`)

Se o código faz `import.meta.env.VITE_API_URL || 'http://localhost:8000/api'` (`localhostDefault`, `configvars.go:42`), a variável ganha o aviso "sem valor no build, o site publicado chama esse endereço do navegador de quem o visita". Além disso, o **caminho** (`/api`) é guardado e usado depois pela plataforma (`survey.go:128-139`, `APIPaths`).

**Por quê:** é o erro nº 1 de quem publica um frontend: ele funciona na máquina local e, em produção, chama `localhost` do navegador do visitante.

---

## HEU-10 — Versão do Node e o OpenSSL do build (`nodeversion.go:42-116`, versão 0.3)

**Campos:** `node_version` e `build_legacy_openssl` (DEC-54 B). Vale **só para P-NODE e P-STATIC com npm** (`56-60`; no Bun existe a HEU-11).

**Como:**
- Padrão 26 (`61-63`, origem `default`).
- Lê `engines.node` e `.nvmrc` (`manifest.go:103-107`, `139-162`).
- `admittedVersions` (`120-136`): `.nvmrc` → major exata se estiver no catálogo; `engines` → intervalo semver do npm, via `semver.NewConstraint`.
- `admittedByRange` (`25-40`): para cada major do catálogo (22, 24, 26), amostra minors 0–99 com patches 0 e 999 e vê se **algum release** satisfaz o intervalo. A imagem de uma major acompanha o release mais recente, então "algum release" é o critério correto (`^22.12` admite 22).
- Se admite alguma versão → propõe **a maior admitida** (`105-110`), origem `declaration`.
- Se o valor é incompreensível (`lts/*`) ou nenhuma versão do catálogo o admite → **aviso**, mantém o padrão (`89-104`). Não bloqueia.
- `node-sass` nas dependências → aviso: descontinuado e nativo, não funciona nas versões do catálogo (`64-70`).

**O OpenSSL do build (DEC-54 B, `nodeversion.go:71-88`).** `react-scripts` ou `@vue/cli-service` **antes da versão 5** usam o webpack 4, que no Node 17 ou mais novo (OpenSSL 3) falha com `ERR_OSSL_EVP_UNSUPPORTED` (não há MD4). O extrator emite `build.legacy_toolchain` para essas duas ferramentas quando a major do **limite inferior da faixa declarada** (`^4.0.3`, `~3.3.0`) é menor que 5 (`manifest.go:92-101`, `declaredMajor` em `236-244`; `latest` e `*` não decidem). Se há `scripts.build` e **nenhum script do projeto já aplica** `openssl-legacy-provider` (`scriptsHandleOpenSSL`, `146-155`), a regra **pergunta** `build_legacy_openssl` (opção `true`), explicando a causa e que a imagem usa o Node 26. Respondendo `true`, fica um aviso; `false`, a pergunta não volta. O planner prefixa **só o `RUN` do build** com `NODE_OPTIONS=--openssl-legacy-provider` (`planner/planner.go:61-74`; `static.go` para o estático): **nunca é `ENV`**, então a aplicação em execução não muda.

**Por quê:** "o npm só avisa sobre `engines`, e a maioria dos projetos roda em versões mais novas". Bloquear por causa de `engines: 16` impediria deploys que funcionariam. O contorno do OpenSSL é o que o próprio webpack documenta e mantém o Node do catálogo (versões antigas do Node são a issue #32). A lista é fechada em duas ferramentas porque um `webpack` direto pode já definir `hashFunction` e funcionar; perguntar a quem funciona seria regressão.

**Limite:** é aproximação (`22-24`): um pin exato num patch incomum pode não ser reconhecido. O contorno não resolve outras incompatibilidades do webpack 4 (como o `node-sass`).

---

## HEU-11 — Versão do Bun (`bunversion.go:19-68`)

**Campo:** `bun_version`. Só quando o gerenciador é Bun (`28-30`).
- Padrão: linha mais recente da major 1 (`31-33`).
- Fixa a versão exata se `packageManager` (`bun@1.1.38`, ignorando `+sha512...`) ou `engines.bun` trazem versão exata (`bunExact`, `13`). **Faixa** não fixa nada (`48`).
- Major ≠ 1 → aviso e padrão (`50-54`).
- `bun.lock` em texto exige Bun ≥ 1.2: se a versão fixada é menor → aviso "a instalação pode falhar" (`56-59`).

---

## Quadro-resumo: o que cada regra pode devolver

| Regra | Propõe | Pergunta | Avisa | Recusa |
| --- | --- | --- | --- | --- |
| HEU-00 | duas aplicações na raiz mista (no `Survey`, DEC-53 F) | ecossistema (quando nenhuma parte é aplicação) | — | monorepo (mapeia as pastas) |
| HEU-01 | perfil, gerenciador | npm ou bun; **instalar sem lockfile** (DEC-53 H) | instalação sem lockfile (se aceita) | JSON inválido, yarn/pnpm, workspaces, lockfile só de um workspace acima (DEC-53 G) |
| HEU-02 | start, build, modo | start (ausente ou de dev; `node x.js` como opção derivada de `nodemon x.js`) | `prepare` de ganchos; arquivo `--env-file` ausente (DEC-53 D) | — |
| HEU-03 | saída, SPA | saída desconhecida | SSR (explica) | — |
| HEU-05 | porta (`PORT` lida, schema com `PORT`, ou porta escrita no código), saúde | porta (nada achado, ou duas portas escritas) | — | — |
| HEU-08 | — | — | Dockerfile/compose | — |
| HEU-09 | — (inventário) | — | `.env` versionado, credencial, segredo público | — |
| HEU-10 | versão do Node | contorno do OpenSSL no build (`react-scripts`/`@vue/cli-service` < 5) | declaração inadmissível, node-sass, contorno confirmado | — |
| HEU-11 | versão do Bun | — | major ≠ 1, bun.lock antigo | — |
