# DEC-25 — Planner do perfil P-NODE: PATH, NODE_ENV e modo de execução

> **Estado:** proposta de 16/11/2026, aguardando revisão do autor.
>
> **Escopo:** como o planner transforma a análise resolvida em Dockerfile para o perfil P-NODE (parte 2 do backend).
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
6. **A versão do Node continua fixa em 20 (`node:20-alpine`).** Avisar quando `engines.node` não admite Node 20 foi considerado e **adiado**: interpretar intervalos do npm (`^`, `~`, `||`, faixas) com segurança pede uma biblioteca de semver, e adicionar dependência é decisão separada. Trocar a imagem pela versão pedida exige catálogo com mais de uma versão, fora do D0.

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| `npm run build` / `npm start` | Executa ganchos (RN-13) e diverge do comando mostrado ao usuário |
| `npx <ferramenta>` | Pode baixar pacotes em runtime; não é reproduzível |
| Inferir o modo pelo comando | O usuário precisa escolher conscientemente; a classificação é só pista |
| Trocar a imagem pelo `engines.node` | Exige validar cada versão contra o corpus; D0 tem uma só |
| Avisar sobre `engines.node` já | Pede biblioteca de semver; adiado |

## Consequências e limites conhecidos

- Aplicativos que dependem de `prestart`/`prebuild` precisam informar o comando completo.
- Uma ferramenta de runtime listada só em `devDependencies` falha no modo de produção (o `prune` a remove). É o comportamento correto, mas a mensagem de falha ainda não diz isso (diagnóstico 3c).
- A versão do Node é fixa e nada avisa se `engines.node` a contradiz; pinagem por digest segue como limite do M1.
- `HEALTHCHECK`/readiness e o caminho sondado continuam em `/` (parte 3).

## Critérios de revisão

Rever se, no corpus, a falta de ganchos `pre`/`post` quebrar uma fração relevante dos repositórios, ou se `NODE_ENV=production` na imagem mudar o comportamento de aplicações que dependem do valor padrão.
