# DEC-53 — Heurísticas no mundo real: o que um corpus externo mostrou que faltava

> **Estado:** proposta discutida e **aprovada pelo autor em 24/11/2026** (itens A a H); **implementada**. A medição e a auditoria estão no fim.
>
> **Escopo:** repositórios que o autor não escreveu mostraram onde as heurísticas do Cobogó recusam ou perguntam sem necessidade. Esta decisão corrige as lacunas que são gerais, por princípio, e mede o efeito em repositórios que **não** orientaram a correção.
>
> **Relaciona-se com:** DEC-23 (contrato do motor), DEC-25 (planner Node), DEC-35 (porta da plataforma), DEC-36 (Bun), DEC-37 (estático), DEC-48 (Python), DEC-49 e DEC-52 (repositório com mais de uma aplicação), RN-02, RN-08.

## Contexto

O `fec`, que a pessoa que desenhou o motor escreveu, passou de ponta a ponta. Isso tem viés: o autor do motor e o autor do exemplo são a mesma pessoa. Foram buscados repositórios públicos de terceiros (`gh search repos`, com mais de 100 estrelas, sem arquivados) e analisados **sem executar nada deles**, com o `Survey` e a `Analyze` reais do `scan_stack`, em três conjuntos, cada um com o SHA guardado:

| Conjunto | Papel | Repositórios | Como foi escolhido |
| --- | --- | --- | --- |
| Desenvolvimento | viu-se o resultado e saíram as correções | 17 | busca por `django react`, `fastapi react`, `flask react`, `mern stack`, Bun |
| Separado | só mede; nada foi ajustado olhando para ele | 20 | os demais resultados das mesmas buscas |
| Novo | só mede, uma vez, no fim | 23 | 10 buscas fixadas antes, posições 11 a 20 de cada, sem repetir os anteriores |

As recusas e perguntas foram agrupadas por **prevalência em repositórios** (não em aplicações) nos 37 primeiros:

| Lacuna | Repositórios |
| --- | --- |
| Raiz que mistura Python e JavaScript | 9 |
| yarn ou pnpm | 9 (fora do MVP) |
| Node sem lockfile (de verdade, fora de workspace) | 5 |
| Dependências Python fora da raiz, ou `manage.py` em subpasta | 4 |
| Porta em constante, `nodemon` no `start`, `--env-file` sem o arquivo, `bunx --bun` | 1 cada |

Os quatro últimos aparecem em um repositório cada: entram por serem pequenos e seguirem convenções conhecidas, não por frequência medida.

## Decisão

Cada regra nova vem de uma convenção documentada da ferramenta, não do repositório em que ela apareceu. Os testes são casos sintéticos escritos a partir do princípio.

**A. Normalizar o comando antes de reconhecer a ferramenta** (`commandnorm.go`). `npx -y`, `bunx --bun`, `npm exec --`, `pnpm exec`, `bun x`, `cross-env`, `env`, `dotenv -e … --`, `env-cmd -f`, `dotenvx run` e um caminho `node_modules/.bin/x` (ou `node ./node_modules/<pacote>/…`) rodam a mesma ferramenta que o nome limpo. O reconhecimento de build estático, de servidor de desenvolvimento e de servidor estático passa a usar `commandTokens`. A forma curta `-w` de `--watch` vale para `webpack`, `tsc` e `rollup`.

**B. Porta e variáveis.** (1) `app.listen(8000)`, `app.listen(PORT)` com `const PORT = 8000` no mesmo arquivo, `listen({ port: 8000 })` e `serve({ port: 8000 })` em código de execução, **sem ler `PORT`**, declaram a porta (origem `declaration`, citando o arquivo e a linha); duas portas diferentes continuam a pergunta; arquivos de teste não contam. (2) Um schema que recebe o ambiente inteiro (`schema.parse(process.env)`) e tem a chave `PORT` conta como leitura de `PORT`: é o contrato da DEC-35. *Isto revisa a DEC-36*, que tratava a porta fixa em `Bun.serve` como "só uma dica" e perguntava. Não contradiz a DEC-35: ela recusa adotar o **valor de reserva** de `process.env.PORT || 5006`, que é um padrão; uma porta que o código usa sem ler `PORT` é a única que ele usa. A sonda de prontidão continua sendo a prova, e o override vence.

**C. `nodemon arquivo.js` no `scripts.start`.** A pergunta passa a oferecer `node arquivo.js` como opção, derivada só do caso simples (a ferramenta e um arquivo JavaScript, sem opções). Nunca é escolhida sozinha.

**D. Arquivo de variáveis que o script carrega e que não veio no repositório.** `node --env-file=x`, `dotenv -e x`, `env-cmd -f x` e `dotenvx run -f x` em `start` ou `build`, com `x` ausente, geram um aviso que cita o arquivo e o exemplo vizinho (`.example`, `.sample`, `.template`), e manda cadastrar as variáveis na aba Variáveis. `--env-file-if-exists` não avisa.

**E. Layouts de Python.** (1) Sem `requirements.txt` na raiz, o pip usa o arquivo escolhido: o único de produção (`prod`, `production`, `deploy`, `release`), ou o único que sobra tirando os de desenvolvimento (`dev`, `local`, `test`, `ci`…), em `requirements/*.txt`, `requirements-*.txt` na raiz ou numa subpasta de primeiro nível que não é outro projeto (`deploy/requirements.txt`, `backend/requirements/…`). Mais de um possível vira a pergunta `requirements_file`, com a lista. O arquivo escolhido vai ao plano (`pip install -r <arquivo>`, com `-r` seguido como sempre). (2) Um `manage.py` numa subpasta (até dois níveis, e só se houver um) é lido de lá; o plano guarda `django_dir`, o diretório de trabalho do build (`collectstatic`), da execução, da fase de release e dos comandos passa a ser essa pasta, e o módulo de hosts da plataforma é gravado nela. (3) Uma pasta só com `requirements` (`deploy/`, `docker/`) não é uma aplicação irmã: tem de haver código Python nela. Nomes com `{{ }}`, `%` ou `$` (modelos de gerador) não são pastas reais.

**F. Raiz que mistura Python e JavaScript.** O `Survey` propõe uma aplicação por ecossistema que o mapa reconhece como aplicação (um `package.json` só de ferramentas, como Tailwind, não vira aplicação). Cada uma **fixa o ecossistema** (`pinned_ecosystem`): o projeto guarda `ecosystem` (migração 0023) e todo deploy dele leva o override `ecosystem`, sem a pergunta de HEU-00 a cada vez. Duas aplicações dividem a pasta quando cada uma fixa um ecossistema diferente; a identidade passa a ser pasta mais ecossistema. A API aceita `ecosystem` no pedido de criação e o devolve na aplicação; o painel usa a mesma chave.

**G. Dizer onde está o lockfile de um workspace.** O `Analyze` recebe, opcionalmente, a raiz do repositório e o diretório da aplicação (`Input.Repo`, `Input.Dir`). Se a pasta não tem lockfile e há um no caminho acima (`bun.lock`, `package-lock.json`, `uv.lock`…), a recusa diz qual é e que é de um workspace, em vez de "nenhum lockfile". A aceitação não muda: workspaces seguem fora.

**H. Node sem lockfile pergunta em vez de recusar.** A HEU-01 faz a pergunta `allow_unlocked` (opção `true`): instalar com `npm install` (ou `bun install`, se o `package.json` declara `bun@`) e dizer que as versões podem mudar entre deploys. Sem resposta, as demais regras seguem, para todas as perguntas virem juntas; responder `true` deixa um aviso na análise; responder `false` recusa, com a causa dita. É a política que o Python já tem para `requirements.txt` sem versões fixadas. Se o lockfile existe acima da pasta (G), é recusa, não pergunta. Versões: HEU-01 0.5, HEU-04.1 0.2, HEU-04.3 0.2, HEU-05 0.4; catálogos `p-js-0.6` e `p-py-0.2`.

## Alternativas consideradas

- **Aceitar yarn, pnpm e workspaces.** Seria a maior alavanca restante (9 repositórios em 37 só por yarn ou pnpm), mas cada um é um perfil novo, e a imagem do build depende do gerenciador. Fica para outra DEC.
- **Poetry e Pipenv.** Seguem na fase 2 da DEC-48.
- **Ajustar cada regra até o caso que a motivou passar.** É exatamente o viés que se quer evitar; por isso cada item cita a convenção e a medição usa conjuntos que não participaram.
- **Adotar o valor de reserva da porta** (`PORT || 5000`): continua rejeitado, pela DEC-35.

## Consequências e limites

- Aceitação não é correção: uma análise pronta ainda pode gerar uma aplicação que não responde. A sonda de prontidão e o diagnóstico seguem sendo o juiz final, e um conjunto de aplicações aceitas foi **construído de verdade** (abaixo).
- B (porta fixa) pode errar quando a porta está num ramo que não roda em produção; o deploy falha na sonda, com a causa, e o override corrige.
- H admite instalações não reprodutíveis: o aviso fica na análise e no histórico, e a recusa continua disponível.
- F cria duas aplicações com a mesma pasta e o mesmo repositório; o build de cada uma copia a pasta inteira. As duas compartilham o contexto, mas não a imagem.
- Em Python, só um `manage.py` por projeto é achado nas subpastas; com dois, o motor não escolhe.
- `Survey` de um repositório de modelo (cookiecutter) agora não propõe aplicação nenhuma, o que é o certo: ainda não é um projeto.

## Verificação

- **Testes:** `commandnorm_test.go` (A), `node_layouts_test.go` (B, C, D, G, H), `python_layouts_test.go` (E, G), `survey_test.go` (F), `planner_test.go` e `python_test.go` (H, E), `store/stacks_test.go` (F, com PostgreSQL de verdade) e `web/src/lib/stacks.test.ts` (F). Três testes antigos mudaram porque o comportamento mudou de propósito: a porta fixa em `Bun.serve` (B), o `nodemon` com opção derivada (C) e o Node sem lockfile (H).
- **Medição** (o mesmo `Survey` e a mesma `Analyze` do `scan_stack`, antes e depois, em cada conjunto; `READY` + `NEEDS_INPUT` é "aceita"; `UNSUPPORTED` é recusa):

| Conjunto | Antes (aplicações aceitas) | Depois | Repositórios com ao menos uma aplicação aceita |
| --- | --- | --- | --- |
| Desenvolvimento (17) | 17/32 = 53% | 22/32 = 69% | 13/17 → 13/16 |
| Separado (20, 19 com aplicação) | 24/33 = 73% | 27/35 = 77% | 15/19 → 16/19 |
| Novo (23) | 36/45 = 80% | 38/43 = 88% | 22/23 → 23/23 |

  Duas leituras que a tabela esconde. **(1)** Fora do conjunto de desenvolvimento, quase todo o ganho vem de H (perguntar em vez de recusar) e de F; os `READY` quase não mudaram (12→13 e 27→27). Os itens A, B, C, D e E apareceram uma vez cada no conjunto de desenvolvimento e não mudaram nada nos outros dois: são correções corretas por princípio, **sem ganho medido fora da amostra que as originou**. **(2)** A queda de repositórios no primeiro conjunto (17→16) é um modelo de gerador (`cookiecutter`) que deixou de ser proposto como aplicação, de propósito.
- **Auditoria:** cada aplicação cujo resultado mudou foi lida. Nenhuma virou `READY` de forma errada. Uma foi de pergunta para recusa e a recusa é mais precisa (`marcelovicentegc`: a pergunta de ecossistema levava a um projeto Poetry, que a fase 2 da DEC-48 ainda não instala).
- **Build de verdade** (o Dockerfile que o planner gera, `docker build`, numa máquina arm64; o que a análise aceitou, não o que ela promete): das 5 aplicações recém-aceitas do conjunto de desenvolvimento, **1 construiu** (Node sem lockfile, `danielstern/express-react-fullstack`); das 8 `READY` sorteadas nos conjuntos separado e novo (semente 53), **2 construíram** (uma só na segunda tentativa; a primeira falhou no `npm ci`, sem causa que o repositório explique), 1 ficou inconclusivo (o `uv.lock` aceita só x86_64) e **5 falharam**. Das 9 falhas, 7 têm a mesma causa: **dependências de uma época antiga contra o runtime mais novo do catálogo** (Pillow 9 e 10 e Django 3.1 no Python 3.13, `react-scripts` 3 e 4 no Node 26 com `ERR_OSSL_EVP_UNSUPPORTED`, um `uvicorn==0.14.0` que o pip não resolve no Python escolhido). As outras: um frontend que importa de uma pasta acima (`../server`, fora do diretório da aplicação) e um build de Shopify que precisa de variáveis. Em nenhum caso a causa é o item A a H.

## O que isto deixa para a próxima decisão

Em ordem do que a evidência mostra, não do que parece mais fácil:

1. **Época das dependências contra o runtime padrão.** O Python 3.13 e o Node 26 são o padrão do catálogo; um projeto que fixa Pillow 9, Django 3.1 ou `react-scripts` 4 não constrói neles, e o motor diz "pronto". É o que a issue #32 já descrevia para Node, agora com evidência para Python. Um caminho: escolher a versão do runtime a partir do que o próprio repositório fixa (tabela pequena e fechada de pacotes que quebram, no molde do `node-sass` da DEC-37), avisar na análise e pedir confirmação.
2. **Dependência fora da pasta da aplicação** (`import` de `../server`, alias para uma pasta acima): hoje só o build revela.
3. **yarn, pnpm e workspaces** (9 repositórios em 37).
4. **Lockfile que só vale para uma arquitetura** (`uv.lock` com `supported-environments`).
