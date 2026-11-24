# 06 — O mundo real: o corpus externo e a DEC-53

O `fec` passou de ponta a ponta, mas foi escrito por quem escreveu o motor. Este arquivo conta o experimento que tira esse viés (ou, pelo menos, mede quanto dele sobra), o que foi corrigido e o que **não** foi. A decisão completa está em `docs/decisoes/DEC-53-heuristicas-no-mundo-real-corpus-externo.md`.

## 1. O experimento

**Pergunta:** as heurísticas aceitam repositórios de terceiros, e o que elas aceitam funciona?

**Método (sem executar nada dos repositórios):**
1. `gh search repos` (mais de 100 estrelas, sem arquivados, sem repositórios do próprio autor) → clone raso com o SHA guardado, numa pasta isolada.
2. O **mesmo** `Survey` e `Analyze` do `scan_stack` rodam em cada pasta, e a saída (aplicações propostas, desfecho, campos, perguntas, recusas, avisos) é registrada.
3. Três conjuntos, para não ajustar a regra olhando a prova:

| Conjunto | Papel | Repositórios | Como foi escolhido |
| --- | --- | --- | --- |
| Desenvolvimento | as correções saíram dele | 17 | buscas por `django react`, `fastapi react`, `flask react`, `mern stack`, Bun |
| Separado | só mede | 20 | os demais resultados das mesmas buscas |
| Novo | só mede, uma vez, no fim | 23 | 10 buscas fixadas antes, posições 11 a 20, sem repetir os anteriores |

4. Depois da correção, um **`docker build` de verdade** do Dockerfile que o planner gera, para as aplicações recém-aceitas e para uma amostra sorteada (semente 53) das `ready` dos conjuntos separado e novo.

## 2. O que apareceu (prevalência em repositórios, nos 37 primeiros)

| Lacuna | Repositórios | Tipo |
| --- | --- | --- |
| Raiz que mistura Python e JavaScript | 9 | geral |
| yarn ou pnpm | 9 | fora do MVP |
| Node sem lockfile (de verdade, fora de workspace) | 5 | geral |
| Dependências Python fora da raiz, ou `manage.py` em subpasta | 4 | geral |
| Porta em constante, `nodemon` no `start`, `--env-file` sem o arquivo, `bunx --bun` | 1 cada | pequeno |

## 3. As correções (A a H) — o quê, onde, por quê

| Item | O que faz | Onde (código) | Teste |
| --- | --- | --- | --- |
| **A** | Normaliza o comando: `npx -y`, `bunx --bun`, `cross-env`, `dotenv --`, `node_modules/.bin/x` rodam a ferramenta de baixo | `commandnorm.go:31-107`; usada em `static.go` e `rules_node.go:541` | `commandnorm_test.go` |
| **B** | Porta escrita no código (`listen(8000)`, `const PORT = 8000`) vale como declaração; schema com `PORT` vale como leitura de `PORT` | `configvars.go:62-98` e `166-170`; `rules_node.go:289-302` | `node_layouts_test.go` |
| **C** | `nodemon x.js` → oferece `node x.js` como **opção** | `rules_node.go:206-212` e `716-727` | `node_layouts_test.go` |
| **D** | Script carrega `--env-file=x` e `x` não está no repositório → aviso | `manifest.go:172-229`; `rules_node.go:223-231` | `node_layouts_test.go` |
| **E** | `requirements/` e `deploy/requirements.txt`; `manage.py` em subpasta (`django_dir`); pasta só com requirements não é aplicação | `python_extract.go:660-799`, `rules_python.go:187-205` e `499-501`, `planner/python.go:221-225`, `264-267`, `300-303`, `330-332`, `components.go:202-205` e `359-379` | `python_layouts_test.go`, `planner/python_test.go` |
| **F** | Raiz mista → duas aplicações com ecossistema fixado | `survey.go:21-39`, `154-174`; `store/store.go:200-213`; migração `0023` | `survey_test.go`, `store/stacks_test.go`, `web/src/lib/stacks.test.ts` |
| **G** | A recusa diz onde está o lockfile do workspace | `ancestors.go`; `rules_node.go:122-126`; `rules_python.go:209-222` | `node_layouts_test.go`, `python_layouts_test.go` |
| **H** | Sem lockfile: pergunta e, se confirmado, instala com `npm install` e avisa | `manifest.go:122-131`; `rules_node.go:117-139`; `planner/planner.go:169-171` | `node_layouts_test.go`, `planner_test.go` |

**Como justificar (princípio, não caso):** cada item vem de uma convenção da ferramenta (como um executor trata suas opções, como o Django e os modelos de projeto organizam `requirements`, como um workspace guarda o lockfile), com teste sintético escrito a partir do princípio, nunca copiado do repositório que a motivou.

**Uma revisão que precisa ser dita:** a DEC-36 perguntava a porta fixa de `Bun.serve`. A DEC-53 B a trata como declaração. Não contradiz a DEC-35 (que recusa o valor de **reserva** de `PORT || 5006`), porque aqui o código **não lê `PORT`**: é a única porta que ele usa. A sonda de prontidão continua sendo a prova, e o override vence.

## 4. Os números (aceitação = `ready` + `needs_input`)

| Conjunto | Antes | Depois | Repositórios com ao menos uma aplicação aceita |
| --- | --- | --- | --- |
| Desenvolvimento (17) | 17/32 = 53% | 22/32 = 69% | 13/17 → 13/16 |
| Separado (20) | 24/33 = 73% | 27/35 = 77% | 15/19 → 16/19 |
| Novo (23) | 36/45 = 80% | 38/43 = 88% | 22/23 → 23/23 |

**Como ler sem se enganar**
- Fora do conjunto de desenvolvimento, quase todo o ganho é **perguntar em vez de recusar** (H) e **separar a raiz mista** (F). Os `ready` quase não mudaram (12→13 e 27→27).
- A, B, C, D e E apareceram **uma vez** no conjunto de desenvolvimento e não mudaram nada nos outros dois. São corretas por princípio, mas **não têm ganho medido fora da amostra que as originou**. Diga isto.
- O primeiro conjunto perdeu um repositório (17→16): um modelo de gerador (`cookiecutter`) deixou de ser proposto como aplicação, de propósito.
- Todas as aplicações cujo resultado mudou foram lidas à mão: nenhuma virou `ready` de forma errada; uma passou de pergunta para recusa mais precisa (um projeto Poetry, fase 2 da DEC-48).

## 5. Aceitar não é construir

`docker build` do Dockerfile gerado, numa máquina arm64:

| Grupo | Construíram | Falharam | Inconclusivo |
| --- | --- | --- | --- |
| 5 aplicações recém-aceitas (desenvolvimento) | 1 | 4 | 0 |
| 8 `ready` sorteadas (separado e novo) | 2 (uma só na segunda tentativa) | 5 | 1 (`uv.lock` só para x86_64) |

Das 9 falhas, **7 têm a mesma causa**: dependências de uma época antiga contra o runtime mais novo do catálogo.
- Pillow 9 e 10 e Django 3.1 no **Python 3.13** (Pillow compila do zero; o Django 3.1 importa `distutils`, que saiu do Python 3.12).
- `react-scripts` 3 e 4 no **Node 26** (`ERR_OSSL_EVP_UNSUPPORTED`).
- um `uvicorn==0.14.0` que o pip não resolve no Python escolhido.

As outras duas: um frontend que importa de uma pasta **acima** (`../server`, fora do diretório da aplicação) e um build de Shopify que exige variáveis. **Nenhuma falha vem dos itens A a H.**

## 6. O que ficou para depois da DEC-53

A maior causa (7 de 9 falhas de build, dependências de uma época antiga contra o runtime novo) virou a **DEC-54**, já implementada: veja [07-epoca-das-dependencias-dec-54.md](07-epoca-das-dependencias-dec-54.md). O que segue em aberto:
1. Dependência **fora da pasta da aplicação** (`import` de `../server`, ou um pacote declarado só no `package.json` da pasta acima, como o `axios` do `devconnector`).
2. yarn, pnpm e workspaces (9 de 37 repositórios).
3. Lockfile que só vale para uma arquitetura.
4. `npm ci` que falha por lockfile antigo (issue #35).

## 7. Como contar isto na banca (30 segundos)

> "O `fec` foi escrito por mim, então tem viés. Rodei o analisador, sem executar nada, em 60 repositórios de terceiros, em três conjuntos, e só ajustei regras olhando o primeiro. A aceitação subiu de 53% para 69% nele e de 80% para 88% no conjunto novo, mas o ganho fora da amostra vem de perguntar em vez de recusar. Depois construí as imagens de verdade: aceitar não é construir. A maior causa de falha (7 de 9) é dependência antiga contra runtime novo, e essa é a próxima decisão."
