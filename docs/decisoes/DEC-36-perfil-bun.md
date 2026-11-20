# DEC-36 — Perfil Bun

> **Estado:** decidida com o autor em 20/11/2026; **implementada**. Primeira leva do MVP (DEC-35 a DEC-39).
>
> **Escopo:** como o Cobogó reconhece, planeja e publica um projeto JavaScript/TypeScript que usa o Bun como gerenciador e runtime. O alvo é Bun em geral; o `caderno` do autor é um dos casos de teste, não o molde.
>
> **Relaciona-se com:** DEC-06 (perfis), DEC-23 (contrato do motor), DEC-24 (overrides e variáveis), DEC-25 (planner do perfil Node), DEC-35 (porta da plataforma), RN-02, HEU-01, HEU-02, HEU-05, HEU-09, HEU-10.

## Contexto

O D0 só aceitava npm: um `bun.lock` levava à recusa. O Bun virou escolha comum para servidores HTTP em TypeScript (Hono, Elysia, `Bun.serve`) e o autor o usa. Bun não estava no MVP ("mais gerenciadores" era extensão); o autor decidiu trazê-lo, **além** de Python e estático, não no lugar deles.

Antes de desenhar, o analisador real rodou sobre repositórios Bun públicos de formatos diferentes (modelo oficial do Hono, um proxy com Hono, uma API Hono com MongoDB, um exemplo Elysia com MikroORM, um app htmx com SQLite, além do `caderno`). Eles mostraram o que um perfil genérico precisa tratar: lockfile texto ou binário, `start` ausente, porta lida de `PORT`, de outra variável ou fixa no código, `process.env?.PORT`, `process.env["NOME"]` e `export default app` sem porta nenhuma. As imagens oficiais (`oven/bun`) foram inspecionadas: Debian 13, usuário `bun` (uid 1000), um `node` de compatibilidade, sem compilador.

## Decisão

1. **Perfil P-BUN no mesmo catálogo JavaScript** (versão `p-js-0.5`). Compartilha as regras de comando (HEU-02), porta (HEU-05) e variáveis (HEU-09) com o P-NODE. A HEU-01 escolhe o perfil pelo lockfile: `package-lock.json` → P-NODE com npm; `bun.lock` (texto, Bun 1.2+) ou `bun.lockb` (binário) → P-BUN com bun.
2. **npm e Bun juntos viram pergunta, não recusa.** O campo `package_manager` fica pendente com as duas opções, cada uma citando o seu lockfile. A resposta é um override comum (DEC-24) e a análise refeita escolhe o perfil. yarn e pnpm continuam fora; `workspaces` continua fora.
3. **As regras veem os overrides válidos.** A HEU-01 precisa saber a resposta para escolher o perfil; os overrides passam a fazer parte da entrada das regras, como a DEC-23 já previa para a análise. Um campo obrigatório não ganha pendência genérica quando a regra dona dele já perguntou outra coisa (o perfil depende do gerenciador).
4. **Comando de execução (HEU-02).** Sem `scripts.start`, a pendência oferece os scripts como `bun run <script>` no P-BUN (`npm run <script>` no P-NODE) e, no P-BUN, também `bun run <arquivo>` tirado dos campos `module` e `main` do `package.json` (é o que o `bun init` cria). É opção oferecida, nunca escolhida sozinha. `bun --watch` e `bun --hot` contam como ferramenta de desenvolvimento.
5. **Porta (HEU-05).** Vale a DEC-35: `PORT` lida em código de execução → porta da plataforma. Além disso, o Bun tem um contrato próprio documentado: `Bun.serve()` sem `port` e um `export default` com `fetch` servido pelo próprio Bun leem `BUN_PORT`, depois `PORT`, depois 3000. No P-BUN, um arquivo de execução com `Bun.serve(` sem `port`, ou o arquivo de entrada do `start` com `export default` sem `port`, conta como contrato de `PORT` do runtime, com evidência própria. Porta fixa no código (`port: 3000`, `.listen(3000)`) continua sendo pista e vira pergunta (RN-02). Se o reconhecimento errar, a sonda e o diagnóstico da DEC-35 mostram.
6. **Versão do Bun (nova HEU-11).** `packageManager: "bun@X.Y.Z"` ou `engines.bun` com versão exata fixam a imagem (`oven/bun:X.Y.Z`); uma faixa (`>=1.0.0`) ou nada usam `oven/bun:1`, a linha mais recente da major 1. Campo `bun_version`, com override validado (`1`, `1.Y` ou `1.Y.Z`). Um `bun.lock` com versão fixada abaixo de 1.2 gera aviso: esse formato exige 1.2+.
7. **Plano e Dockerfile.** Build em `oven/bun:<versão>`, execução em `oven/bun:<versão>-slim`. Instala com `bun install --frozen-lockfile`, que falha com mensagem clara se o lockfile estiver desatualizado; com `--production` quando não há build. Com build, roda o comando como está e reinstala só as dependências de produção antes da etapa final. `NODE_ENV=production` (verificado: não afeta o que o `bun install` instala; só `--production` afeta). Execução como usuário `bun`, comando do `start` como está. TypeScript roda direto, sem `tsc`. Projetos que usam o Bun só para instalar e iniciam com `node` funcionam pelo `node` de compatibilidade da imagem.
8. **Variáveis (HEU-09), para os dois perfis.** O inventário passa a ler `process.env?.X`, `process.env["X"]` (nome literal, que antes virava lacuna de cobertura) e `Bun.env.X`, além de `process.env.X`. O Bun carrega `.env` sozinho, mas a DEC-24 já tira `.env*` do contexto de build, então um `.env` commitado nunca chega ao contêiner. `import.meta.env` fica para a DEC-37, porque no Vite ele é configuração pública de build, não variável de execução.

9. **O app pode escrever no próprio diretório (os dois perfis).** O `WORKDIR` criava `/app` como root na imagem de execução, e um app que cria `data/` para um SQLite falhava com `EACCES` (achado no deploy real do `caderno`). A imagem final passa a entregar `/app` ao usuário do app (`node` ou `bun`). O disco continua efêmero: some no próximo deploy (persistência é a issue #34).
10. **`prepare` que só instala ganchos do Git é removido do build (os dois perfis).** `"prepare": "husky"` roda no install, mas o husky é dependência de desenvolvimento e no contêiner não há repositório Git; o build falhava com `husky: command not found` (achado no deploy real de uma API Hono, e o `npm ci --omit=dev` tem o mesmo problema). Quando cada trecho do `prepare` é uma ferramenta de ganchos (husky, lefthook, simple-git-hooks, is-ci), o build o tira do `package.json` da imagem (`npm pkg delete` / `bun pm pkg delete`) antes de instalar, e a análise avisa. Um `prepare` misto (`husky && npm run build`) não é tocado.

## Alternativas consideradas

- **Bun como gerenciador do perfil Node, rodando em `node`:** quebra quem usa APIs do Bun (`Bun.serve`, `bun:sqlite`), que é a maioria dos projetos com `bun.lock`.
- **Recusar npm e Bun juntos:** o repositório costuma carregar os dois por migração incompleta; perguntar custa uma resposta e resolve.
- **Compilar módulos nativos instalando compilador na imagem de build:** mais lento em todo build para atender poucos casos; fica como limite declarado.

## Consequências e limites

- O Bun só roda scripts de instalação de dependências confiáveis (`trustedDependencies`); pacotes que compilam código nativo falham no build por falta de compilador. O log do build mostra o erro; uma imagem de build com compilador fica para depois.
- `workspaces` (monorepos) seguem fora, como no npm.
- O servidor fullstack do Bun (`bun index.html`) e sites estáticos com Bun entram com o perfil estático (DEC-37).
- Next.js e frameworks pesados rodando no Bun podem funcionar, sem garantia.
- O contrato implícito do item 5 depende de achar `Bun.serve(` ou o `export default` do arquivo de entrada; servidores montados de outro jeito caem na pergunta da porta.

## Verificação

- Testes do analisador (perfil escolhido pelo lockfile, pergunta npm/Bun e as duas respostas, porta pelo `Bun.serve` sem `port` e pelo `export default` do arquivo de entrada, porta fixa vira pergunta, versões e avisos, opções `bun run`, leitura de `process.env?.X`, `process.env["X"]` e `Bun.env.X`, validação dos overrides) e do planner (Dockerfiles com e sem build, versão fixada, `bun.lockb`, modo desenvolvimento, remoção do `prepare` e `/app` gravável nos dois perfis).
- **Em execução** (API e worker locais, repositórios públicos reais):
  - `ocnaibill/caderno`: no ar **sem nenhuma pergunta** (Bun, porta da plataforma, sonda 200).
  - `mikro-orm/elysia-bun-example-app` (`bun.lockb`, `.listen(3000)`): perguntou a porta; com 3000, no ar.
  - `cdbrw/htmx-bun-hono` (`bun.lockb`, só `dev` com `--hot`): perguntou o comando, oferecendo `bun run dev` como desenvolvimento; com `bun run src/index.tsx`, no ar.
  - `ProMehedi/bun-hono-api-starter` (`process.env?.PORT`, `prepare: husky`): o build passou com o `prepare` removido; falhou ao subir com diagnóstico **confirmado** de `JWT_SECRET`; com o valor gerado pelo Cobogó, o diagnóstico seguinte apontou `MONGO_URI`, um banco externo que o Cobogó ainda não provisiona.
  - `honojs/starter` (modelo Bun, sem lockfile): recusado com o motivo.

## Critérios de revisão

- Medir no corpus quantos projetos Bun publicam sem perguntas e quantos falham por módulo nativo; se forem muitos, rever o item de compilador.
- Rever o item 5 se aparecer falso positivo frequente (arquivo de entrada com `export default` que não é servidor).
