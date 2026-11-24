# Material de estudo — as heurísticas do Cobogó

Este material foi escrito **a partir do código** (23/11/2026, commit `3c56ee8`; atualizado em 24/11/2026 para o código com a **DEC-53** e a **DEC-54**, commits `5245683` e `c255941`), não dos documentos de especificação. Onde os dois divergem, vale o código, e a divergência está anotada em [05-banca-e-limites.md](05-banca-e-limites.md).

Convenção das referências: `arquivo:linha` ou `arquivo:inicio-fim`. Os caminhos são relativos a `internal/analyzer/`, salvo quando o prefixo (`internal/planner/`, `internal/httpapi/`...) aparece. Os números de linha valem para o código desta atualização (commit `c255941`); se você mexer nos arquivos, eles andam.

## Como estudar (ordem sugerida)

| Ordem | Arquivo | O que você sai sabendo |
| --- | --- | --- |
| 1 | este README | O pitch, os números e o vocabulário |
| 2 | [01-motor.md](01-motor.md) | Como o motor funciona por dentro: evidência, regra, resolvedor, precedência |
| 3 | [02-heuristicas-javascript.md](02-heuristicas-javascript.md) | HEU-00, 01, 02, 03, 05, 08, 09, 10, 11 (Node, Bun, estático) |
| 4 | [03-heuristicas-python-servicos.md](03-heuristicas-python-servicos.md) | HEU-04.1 a 04.8 (Python/Django), HEU-06 (banco, Redis), persistência, mapa do repositório |
| 5 | [04-plataforma-e-planner.md](04-plataforma-e-planner.md) | O que a plataforma decide sozinha (DEC-52), o planner, a fase de release, o percurso completo do `fec` |
| 6 | [05-banca-e-limites.md](05-banca-e-limites.md) | Perguntas prováveis, respostas honestas, fraquezas conhecidas |
| 7 | [06-corpus-externo-dec-53.md](06-corpus-externo-dec-53.md) | O experimento com 60 repositórios de terceiros, as correções A a H, os números e o que falhou ao construir |
| 8 | [07-epoca-das-dependencias-dec-54.md](07-epoca-das-dependencias-dec-54.md) | A época das dependências: perguntar antes (tabela gerada do PyPI), o contorno do OpenSSL no build, o diagnóstico pelo log, e o que já dava certo continuar dando |

## O pitch em 60 segundos

> O Cobogó é uma PaaS própria: você cola o link de um repositório e ele descobre sozinho **o que é** a aplicação, **como construir e rodar**, **de que serviços ela precisa** e **como ligar tudo**.
>
> Isso não é adivinhação nem execução do código do usuário. É um **motor de heurísticas baseado em evidências**: extratores leem arquivos como texto (`package.json`, `requirements.txt`, `settings.py`...) e produzem *evidências* com arquivo e linha; regras versionadas transformam evidências em propostas; um resolvedor aplica uma precedência fixa. Quando as evidências não bastam ou se contradizem, **o motor pergunta**, em vez de chutar.
>
> Cada decisão é explicável: "escolhi Python 3.13 porque o `requires-python` diz `>=3.10` e o padrão do catálogo satisfaz; evidência: `pyproject.toml:12`".

Três ideias para repetir na banca:

1. **Evidência, não palpite.** Toda conclusão aponta para um arquivo e uma linha.
2. **Estático, nunca execução.** O código do repositório não roda durante a análise (RN-08): nada de `import`, `npm install` ou `source .env`.
3. **Perguntar é um resultado válido.** O desfecho de uma análise é `ready`, `needs_input` ou `unsupported`. O motor não é obrigado a responder tudo.

## Os números (para citar sem errar)

| Item | Valor | Onde está no código |
| --- | --- | --- |
| Catálogo JavaScript | `p-js-0.7`, 8 regras: HEU-01, 02, 03, 05, 08, 09, 10, 11 | `catalog.go:105`, `catalog.go:116` |
| Catálogo Python | `p-py-0.3`, 10 regras: HEU-04.1 a 04.8, 09, 08 | `rules_python.go:16`, `rules_python.go:27` |
| Perfis de execução | P-NODE (npm), P-BUN, P-STATIC, P-PY | `domain.Profile*`, `planner/planner.go:78-146` |
| Despacho de ecossistema | HEU-00 (JS, Python, ambos, monorepo) | `ecosystem.go` |
| Node suportado | 22, 24, 26 (padrão 26) | `domain/analysis.go:69-71` |
| Python suportado | 3.10 a 3.15 (padrão 3.13; 3.10 com aviso de fim de suporte); **3.8 e 3.9 só por confirmação** (DEC-54) | `domain/python.go:68-74` |
| Bun | major 1 | `catalog.go:170`, `bunversion.go` |
| Engines de dados gerenciadas | PostgreSQL, MySQL, Redis | `services.go`, `redis.go` |
| Formatos de credencial reconhecidos | 7 (AWS, GitHub, Slack, Stripe, Google, chave privada, JWT) + entropia | `envclass.go:91-102`, `envclass.go:152` |
| Segredos que o Cobogó gera | 11 nomes, lista **fechada** | `envclass.go:19-23` |
| Prefixos de variável pública de build | `VITE_`, `REACT_APP_`, `VUE_APP_`, `PUBLIC_`, `NEXT_PUBLIC_` | `envclass.go:31` |
| Limite de leitura por arquivo | 1 MiB | `extraction.go:55` |
| Estado de todas as regras | `proposed` (nenhuma está "validated" ainda) | `State: RuleProposed` em cada regra |
| Repositórios de terceiros analisados (DEC-53) | 60, em três conjuntos (17 de desenvolvimento, 20 separados, 23 novos); 13 aplicações construídas de verdade (3 construíram); com a DEC-54, 5 das 7 falhas de época passam a construir | `06-corpus-externo-dec-53.md` |

## Vocabulário mínimo

- **Evidência** (`domain.Evidence`): um fato extraído, com `Kind`, `Name`, `Value`, `Source` (arquivo), `Line` e `Scope`. Tem um ID estável calculado por hash (`extraction.go:108-112`).
- **Escopo** (`Scope`): `runtime`, `build`, `test` ou `unknown`. Uma referência em arquivo de teste não vira requisito de produção.
- **Regra** (`Rule`): função pura `Apply(RuleInput) RuleOutput` com metadados (ID, versão, estado, pré-condições, **campos que ela tem permissão de escrever**).
- **Proposta** (`Proposal`): "o campo X deve valer V", com uma *origem* (`declaration`, `platform`, `inference`, `default`) e as evidências que a sustentam.
- **Pendência** (`Pending`): a regra não conseguiu fechar um campo; vira pergunta ao usuário, com opções quando dá.
- **Conflito** (`Conflict`): duas propostas de mesma autoridade com valores diferentes. Nunca "o último vence".
- **Não suportado** (`Unsupported`): o repositório está fora do que o catálogo cobre; explica e para.
- **Override**: resposta do usuário a uma pendência; vence tudo.
- **Ecossistema fixado** (`pinned_ecosystem`, DEC-53 F): quando uma pasta mistura Python e JavaScript e as duas partes são aplicações, cada uma guarda qual ecossistema é, e todo deploy dela leva o override `ecosystem`.
- **Versão legada** (DEC-54 A): Python 3.8 ou 3.9, fora de suporte; o motor só a usa se a pessoa confirmar (override), nunca por declaração do projeto.
- **Sugestão do diagnóstico** (`diagnosis.suggestion`, DEC-54 C): uma correção que o motor conhece (`overrides` + rótulo), oferecida como "Novo deploy com …" quando o log do build mostra a causa; nada é aplicado sem o clique.
- **Perfil**: a "forma" de rodar (P-NODE, P-BUN, P-STATIC, P-PY), que escolhe o template de Dockerfile.
- **Stack / projeto**: o repositório inteiro; **aplicações** são as partes que viram deploy (DEC-52).
