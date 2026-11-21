# DEC-37 — Perfil estático

> **Estado:** decidida com o autor em 20/11/2026; **implementada**. Primeira leva do MVP (DEC-35 a DEC-39).
>
> **Escopo:** como o Cobogó reconhece um site estático, decide entre estático e servidor, constrói os arquivos e os serve; e como a configuração pública de build chega ao bundle.
>
> **Relaciona-se com:** mestre §7 (P-STATIC: "build declarado, diretório de saída conhecido, artefatos servidos por servidor HTTP"; "React não implica servidor Node.js; Next.js não equivale a SPA estática"), HEU-03, HEU-09, DEC-15 (configuração pública de frontend nunca é segredo protegido), DEC-24 (exclusão de `.env*`), DEC-35 (porta da plataforma), DEC-36 (Bun), issue #30.

## Contexto

Os perfis P-NODE e P-BUN publicam um processo que escuta numa porta. Um site estático não tem processo próprio: o build gera arquivos e alguém precisa servi-los. Até aqui o Cobogó perguntava um comando de início para esses projetos (o `afn-simulator` do autor, por exemplo), e nenhuma resposta era boa: `vite preview` é ferramenta de desenvolvimento e não deveria ir para produção.

O desenho foi conferido contra repositórios reais: dois sites Vite do autor (um com `BrowserRouter`), um app Vue CLI com `vue-router`, um site Astro estático, um modelo Astro com adaptador do Cloudflare, um app Next.js e um portfólio HTML puro.

## Decisão

1. **Perfil P-STATIC, em duas formas.**
   - **Com build:** `package.json` com lockfile (npm ou Bun, como nos outros perfis) e um `scripts.build` cujo comando usa uma ferramenta conhecida de site estático. Tabela inicial, com a saída padrão: Vite → `dist`; Create React App (`react-scripts build`) → `build`; Vue CLI (`vue-cli-service build`) → `dist`; Astro → `dist`; Parcel → `dist` (ou `--dist-dir`); `bun build` com entrada `.html` → o `--outdir`; Next.js com `output: 'export'` → `out`. Um diretório declarado como texto literal na configuração (`outDir` no `vite.config` ou no `astro.config`) prevalece sobre o padrão.
   - **Sem build:** `index.html` na raiz e nenhum `scripts.build` (com ou sem `package.json`). O diretório é servido como está, menos os arquivos de infraestrutura (item 8); `.git` e `.env*` já ficam fora do contexto. Sem build não há instalação, então lockfile não é exigido.
2. **Estático ou servidor.** Com ferramenta estática no build, o projeto é P-STATIC quando o `scripts.start` falta ou é ferramenta de desenvolvimento ou de pré-visualização (`vite`, `vite preview`, `serve`, `http-server`, `live-server`, `react-scripts start`, `astro dev`/`preview`, `vue-cli-service serve`). Com um servidor de verdade no `start` (`node server.js`), segue P-NODE/P-BUN como antes: é o servidor que entrega o build. Sinais de renderização no servidor tiram o projeto do estático: Next.js sem `output: 'export'`, Astro com `output: 'server'` ou com `adapter`, `@sveltejs/kit`, `nuxt` e `@remix-run`.
3. **Decide sozinho quando a evidência é forte; explica sempre.** O plano mostra as duas etapas (build e servir), a ferramenta e a origem do diretório de saída. O perfil aceita override (`P-STATIC`, ou o perfil de servidor do gerenciador) para quem quiser o contrário.
4. **Servidor: `nginxinc/nginx-unprivileged:stable-alpine`**, sem root (uid 101), na porta 8080, que é da plataforma. O Cobogó gera a configuração: `index.html`, gzip e `X-Content-Type-Options: nosniff`. **Fallback de SPA** (rota desconhecida devolve `index.html`) ligado por padrão para Vite, CRA, Vue CLI, Parcel e `bun build`; desligado para Astro e Next export, que servem o `404.html` quando existe. Campo `spa_fallback`, com override.
5. **Build:** a etapa de build do gerenciador (npm ou Bun, com dependências de desenvolvimento, HEU-10/HEU-11 escolhendo a versão), e só o diretório de saída vai para a imagem do nginx. Se o build não gerar o diretório, o build falha dizendo isso e pedindo o diretório certo (campo `output_dir`, com override).
6. **Configuração pública de build (DEC-15).** Variáveis com prefixo público (`VITE_`, `REACT_APP_`, `VUE_APP_`, `PUBLIC_`, `NEXT_PUBLIC_`) ganham a classe **`public`**: o painel diz que elas vão para o JavaScript entregue ao navegador e não são segredo. No P-STATIC, as variáveis do projeto com esses nomes que a análise encontrou entram no build como argumentos (`ARG`); as outras não vão a lugar nenhum, porque não há processo rodando. O contêiner do nginx não recebe variáveis do projeto. A HEU-09 passa a ler `import.meta.env.X` (sem os nomes embutidos do Vite: `MODE`, `DEV`, `PROD`, `SSR`, `BASE_URL`). Incluir um `.env.production` commitado continua fora, para a DEC-39.

7. **Aviso para segredo em variável pública.** Uma variável com prefixo público e nome de segredo (`VITE_SPOTIFY_TOKEN`) gera aviso na análise: o valor vai para o navegador de qualquer visitante (achado num repositório real do corpus).
8. **O link do Cobogó não expõe arquivos de infraestrutura.** O repositório pode ser público, mas o site publicado não precisa mostrar o que não é do site. Duas camadas: (a) **servidor, em todo site estático:** o nginx responde 404 a qualquer caminho com um componente que começa com ponto (`.git`, `.env`, `.github`...), exceto `/.well-known/`; a regra vem antes do fallback de SPA, para um dotfile não cair no `index.html`; (b) **modo sem build:** uma etapa à parte do Dockerfile copia o repositório e apaga `Dockerfile*`, `docker-compose*`, `compose.y*ml`, `package.json`, `package-lock.json`, `npm-shrinkwrap.json`, `bun.lock`, `bun.lockb`, `yarn.lock`, `pnpm-lock.yaml`, `node_modules` e tudo que começa com ponto (menos `.well-known`), de modo que esses arquivos nem existam na imagem final. É uma **lista de exclusão, não de permissão**, de propósito: um site pode servir qualquer tipo de dado (`.csv`, `.md`, `.geojson`, `.glb`...) e uma lista de tipos permitidos o quebraria em silêncio. `README` e `.md` continuam servidos.
9. **Dockerfile e compose do repositório (HEU-08).** O Cobogó não os usa nem os executa: gera o próprio plano, para explicar cada decisão e manter os limites e o isolamento. A análise avisa quando os encontra, inclusive em repositórios que não suporta. Usá-los como pista (`EXPOSE`, imagem base, volumes do compose) é trabalho futuro, e usar o Dockerfile deles como modo explícito para stacks sem perfil fica depois do MVP.
10. **Aviso antes do build para `node-sass`.** Dependência descontinuada que compila código nativo e não funciona nas versões de Node do catálogo (achado no `Vue-CLI-Boilerplate`): a análise avisa na hora, sugerindo o `sass`, em vez de o usuário descobrir no fim de um build condenado. Versões antigas do Node são a issue #32.

## Alternativas consideradas

- **Caddy:** a imagem oficial roda como root.
- **Pacote `serve` do npm:** baixa dependências em todo deploy e é mais um processo Node em produção.
- **Servidor estático próprio do Cobogó em Go:** imagem menor, mas é código a manter e auditar.
- **Sempre perguntar estático ou servidor:** pergunta sem informação nova nos casos claros, que são a maioria; a pergunta fica para o conflito, e o override cobre o resto.

## Consequências e limites

- Angular, Gatsby, Eleventy, Hugo e outros geradores ficam para depois; a tabela de ferramentas é o ponto de extensão. A saída do Angular depende do `angular.json`.
- A lista de exclusão do modo sem build é por nome de arquivo; um segredo com outro nome dentro do repositório (`credenciais.json`) continua sendo servido. O aviso da DEC-24 cobre os arquivos de ambiente, mas não adivinha nomes arbitrários; isso é problema do repositório, e a proteção do Cobogó é a de não somar infraestrutura ao que o autor quis publicar.
- O diretório de saída declarado só é lido quando é texto literal; configuração calculada em código cai no padrão da ferramenta e, se errar, no erro claro do item 5.
- SSR e frameworks híbridos seguem pelos perfis de servidor, que exigem um `start` de produção.
- Um site com rotas no cliente e fallback desligado responde 404 nas rotas profundas; o override de `spa_fallback` resolve.

## Verificação

- Testes do analisador (Vite com e sem `outDir`, CRA, Astro, `bun build` com `.html`, HTML puro, servidor de verdade no `start`, Next com e sem export, Astro com adaptador, estático forçado com e sem diretório, conflito de perfil, variáveis públicas e o aviso de segredo) e do planner (npm e Bun, com e sem fallback, sem build, recusas).
- **Em execução** (API e worker locais, repositórios públicos reais):
  - `ocnaibill/afn-simulator` e `ocnaibill/frontend-planetario`: no ar sem perguntas; no planetario, uma rota profunda (`/agendamento/visita/123`) responde o `index.html` com 200 (fallback de SPA).
  - `MaeWolff/astro-portfolio-template`: no ar; rota inexistente responde 404.
  - `iamspruce/Simple-HTML-CSS-Portfolio` (sem `package.json`): no ar sem instalar nada; `/Dockerfile.cobogo` responde 404.
  - `Prakasa28/spotify-widget`: com `VITE_SPOTIFY_TOKEN` definida no projeto, o valor aparece no bundle servido e **não** aparece nas variáveis do contêiner nem no histórico da imagem final.
  - Imagem sem build construída com um repositório de teste cheio de arquivos sensíveis: `.env`, `.env.production`, `.gitignore`, `.github/ci.yml`, `package.json`, `package-lock.json`, `Dockerfile`, `Dockerfile.cobogo`, `docker-compose.yml`, `compose.yaml` e `node_modules/` **nem existem na imagem** e respondem 404; `README.md`, `docs/nota.md`, `data/dados.json`, `data/tabela.csv` e `/.well-known/security.txt` respondem 200. Com fallback de SPA ligado, um dotfile continua em 404.
  - O contêiner roda como uid 101 e não recebe variáveis do projeto; o nginx responde `X-Content-Type-Options: nosniff`.
  - `kennethtomagan/Vue-CLI-Boilerplate` e `protomorph/astro-zero` foram detectados como estáticos, mas o build falhou por causa do próprio repositório (`node-sass` exige python2; lockfile com dependências incompatíveis), com o motivo visível no log.
  - O modelo Astro com adaptador do Cloudflare e um app Next.js seguiram pelo fluxo de servidor, com o aviso da HEU-03.

## Critérios de revisão

- Medir no corpus estático quantos projetos publicam sem perguntas e quantos precisaram de override de `output_dir` ou `spa_fallback`.
- Rever a tabela quando um gerador fora dela aparecer no corpus mais de uma vez.
