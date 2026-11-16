# DEC-25 — Planner do perfil P-NODE: PATH, NODE_ENV e modo de execução

> **Estado:** proposta de 16/11/2026, aguardando revisão do autor.
>
> **Escopo:** como o planner transforma a análise resolvida em Dockerfile para o perfil P-NODE (parte 2 do backend): comandos, ambiente, modo de execução, versão do Node e imagens.
>
> **Relaciona-se com:** DEC-23, DEC-24, RN-02, RN-13, RF-04, RF-08, HEU-01, HEU-02, issues #30 e #31.
>
> **Verificação:** além dos testes que leem o Dockerfile, dois testes com `COBOGO_TEST_DOCKER=1` constroem e executam imagens reais; sem a linha `ENV PATH`, o build falha com `mkdirp: not found`.

## Contexto

O HEU-02 propõe o **valor cru** de `scripts.start` e `scripts.build` (por exemplo `vite build`, `tsc`, `next start`). O planner o escrevia direto em `RUN` e `CMD`. Dentro da imagem, `node_modules/.bin` não está no `PATH`, então qualquer comando que não seja `node` falha com "not found"; o único repositório validado até aqui usava `node index.js`. Além disso a imagem não definia `NODE_ENV`, e o DEC-24 deixou em aberto o modo de execução (produção ou desenvolvimento).

## Decisão

1. **`ENV PATH=/app/node_modules/.bin:$PATH` na imagem.** O Cobogó executa exatamente o comando que a análise mostrou ao usuário. Alternativa recusada: reescrever para `npm run build`/`npm start`, que também executa `prestart`, `prebuild` e similares; um `prestart` costuma rodar migração de banco, e migrações não são inferidas nem executadas por convenção (RN-13). Consequência assumida: ganchos `pre`/`post` não rodam, o que também significa que um `prebuild` de que o projeto dependa (limpeza, geração de código) não roda; o usuário pode informá-lo como `build_command`.
2. **`NODE_ENV=production` na imagem**, no modo de produção. Como o npm omite dependências de desenvolvimento quando `NODE_ENV=production`, a instalação para build usa `npm ci --include=dev` de forma explícita. Uma variável do projeto com o mesmo nome (DEC-24) vale sobre o valor da imagem, porque entra no `docker run`.
3. **Modo de execução** é um campo do plano, `execution_mode`, `production` por padrão (origem `default`), **sobrescrevível** e nunca inferido. A tela envia `execution_mode: "development"` junto com o `start_command` quando o usuário escolhe uma opção classificada como `development` (DEC-24 item 3). No modo `development`: `NODE_ENV=development`, instalação completa (`npm ci --include=dev`), sem build e sem `prune`. O aviso de que servidores de desenvolvimento costumam escutar só em `127.0.0.1` continua sendo da interface (issue #31); o Cobogó não altera o código do usuário para corrigir isso.
4. **`CMD` em formato exec com JSON válido** (`encoding/json`), em vez do `%q` do Go. O comando de execução continua passando por `sh -c`, o que permite `&&`, variáveis e redirecionamentos.
5. **Comandos com barra invertida no fim são recusados** na validação: num Dockerfile, `RUN cmd \` continuaria na instrução seguinte e a absorveria (`npm prune`).
6. **Versão do Node por projeto.** O catálogo aceita as majors **22, 24 e 26** (as que o projeto Node.js ainda suporta; a 20 saiu de suporte em 30/04/2026) e o padrão é a **26 (LTS)**. A regra HEU-10 escolhe a maior versão suportada que `engines.node` admite (intervalos do npm, via biblioteca de semver) ou, sem `engines`, a major de `.nvmrc`. Uma major é "admitida" se algum release dela satisfaz o intervalo, porque a imagem acompanha o release mais recente. `node_version` é um campo do plano, sobrescrevível pelo usuário.
7. **Declaração que nenhuma versão admite é aviso, não bloqueio.** Se `engines.node`/`.nvmrc` pede uma versão fora do catálogo (`20.x`, `^16`, `<19`) ou algo que não se interpreta (`lts/*`), vale o padrão e a análise traz um aviso dizendo o que foi declarado e que a versão do Node pode ser o motivo de uma falha. O npm só avisa sobre `engines`, e a maioria dos projetos roda em versões mais novas; bloquear seria atrito excessivo. Versões fora de suporte só por override explícito ficam para a issue #32.
8. **Dois estágios, ambos Debian (glibc).** O build roda em `node:<versão>` (imagem completa, com compilador e `python3` para módulos nativos) e a execução em `node:<versão>-slim`, que recebe só `/app` pronto (`COPY --from=build`). Escolha de Debian no lugar de Alpine: `musl` não tem os binários pré-compilados que muitos módulos nativos esperam e a Alpine não traz compilador. As ferramentas do próprio Cobogó (`git`, cliente Docker) ficam no worker, não na imagem do app.

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| `npm run build` / `npm start` | Executa ganchos (RN-13) e diverge do comando mostrado ao usuário |
| `npx <ferramenta>` | Pode baixar pacotes em runtime; não é reproduzível |
| Inferir o modo pelo comando | O usuário precisa escolher conscientemente; a classificação é só pista |
| Uma única versão de Node para todos os projetos | Um projeto que pede uma major mais nova que a da imagem pode falhar; sem versão por projeto o usuário não tem saída |
| Alpine (`musl`) | Sem compilador e com menos binários pré-compilados; módulos nativos quebram |
| Imagem completa também na execução | Leva compilador e ferramentas para o runtime sem necessidade |
| Bloquear quando `engines` não é admitido | O npm não impõe `engines`; a maioria roda em versões mais novas |

## Consequências e limites conhecidos

- Aplicativos que dependem de `prestart`/`prebuild` precisam informar o comando completo.
- Uma ferramenta de runtime listada só em `devDependencies` falha no modo de produção (o `prune` a remove). É o comportamento correto, mas a mensagem de falha ainda não diz isso (diagnóstico 3c).
- A pinagem por digest segue como limite do M1: as tags acompanham o release mais recente da major.
- A checagem de intervalo é aproximada: amostra minors de 0 a 99 com patches 0 e 999. Um pin exato num patch incomum (`22.11.37`) pode não ser reconhecido e gera aviso.
- A Node 26 é LTS desde 28/10/2026; módulos nativos sem binário para ela podem falhar, e o usuário pode escolher 24 ou 22. O corpus mostra se o padrão deve mudar.
- O primeiro build de cada versão baixa a imagem completa (cerca de 1 GB); depois ela fica no cache do Docker. Pré-baixar as imagens do catálogo no worker é uma otimização possível, não necessária.
- Um módulo nativo que precise de bibliotecas do sistema além do compilador ainda falha; o diagnóstico 3c deve apontá-lo.
- `HEALTHCHECK`/readiness e o caminho sondado continuam em `/` (parte 3).

## Critérios de revisão

Rever se o padrão 26 falhar em módulos nativos com frequência relevante (voltar para 24), se, no corpus, a falta de ganchos `pre`/`post` quebrar uma fração relevante dos repositórios, ou se `NODE_ENV=production` na imagem mudar o comportamento de aplicações que dependem do valor padrão.
