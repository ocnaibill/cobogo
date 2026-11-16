# DEC-23 — Contrato do motor de heurísticas

> **Estado:** aceita em 16/11/2026.
>
> **Escopo:** forma da entrada, das regras, das evidências e da saída do analisador (primeira parte do backend a ser refeita com testes depois da fatia M1).
>
> **Relaciona-se com:** DEC-07 (base heurística determinística), RN-02, RN-03, RN-08, RF-02, RF-03, RF-08, RNF-01, RNF-02, RNF-12, §4.1–4.3 e §6 do [motor de heurísticas](../MOTOR-DE-HEURISTICAS.md).

## Contexto

O analisador do M1 é uma função única (`Analyze(rootDir, portOverride)`) que aplica HEU-01, HEU-02, HEU-05 e HEU-09 em sequência fixa, devolvendo o plano junto com o Dockerfile. Não tem testes, só aceita override de porta e foi validado contra um único repositório. Três problemas já apareceram:

- O §6 do motor exige que cada regra tenha ID, versão, estado e pré-condição; no código existia só uma versão global do catálogo.
- `process.env.PORT` encontrado em `test.js` podia ser citado como contrato de porta de produção, e arquivos ignorados (grandes, `.tsx`, acesso dinâmico) davam a impressão de "não encontrado".
- A pendência de `start_command` não tinha como ser respondida; RF-08 exige corrigir o plano no mesmo SHA.

## Decisão

1. **Regra é um objeto.** ID, versão, estado (`proposed`, `validated`, `retired`), pré-condições (`Requires`), campos de que é dona (`Fields`) e a função de aplicação. Uma regra só propõe valores ou pendências para os próprios campos; o motor rejeita violações. Regras `retired` não rodam; `proposed` rodam e o estado fica registrado na saída.
2. **Overrides entram na entrada do analisador**, como mapa `campo → valor` (hoje: `port`, `start_command`, `build_command`). O override tem precedência (RN-03); a proposta que ele cobriu fica registrada em `Overrode` para ser exibida. Isso não bloqueia: o usuário já escolheu explicitamente, então a contradição com uma declaração do repositório é informada, não tratada como conflito (interpretação de RN-03 sobre o §4.3). Override inválido vira pendência e é ignorado; override de campo que não aceita override é erro de chamada.
3. **Evidência é um fato com ID derivado do conteúdo**, extraído por um extrator: tipo, nome, valor, fonte, linha e **escopo** (`runtime`, `build`, `test`, `unknown`). O escopo de código-fonte é classificado pelo caminho (`test/`, `__tests__/`, `*.test.*`, `*.spec.*` → `test`; `*.config.*` → `build`); o restante é `runtime` por convenção do perfil P-NODE. Isso é uma inferência, não um fato — o corpus decide se a classificação generaliza.
4. **Cobertura é um relatório da análise inteira**: arquivos lidos e lacunas (`too_large`, `symlink`, `unreadable`, `dynamic_env_access`). Relações entre evidências (apoio, oposição, duplicata) ficam adiadas até a HEU-06 precisar delas.
5. **Resolvedor genérico.** Por campo, a precedência é override > declaração > inferência > default. Valores diferentes no mesmo nível geram conflito, nunca "o último vence". Campo obrigatório sem valor e sem pendência vira pendência genérica. Resultado: não suportado > precisa de informação > pronto.
6. **Pré-condições.** Regra cuja pré-condição declarou "não suportado" é pulada (`skipped`), para não perguntar ao usuário coisas inúteis sobre um projeto que será recusado.
7. **O analisador termina em campos resolvidos e pendências.** Gerar o plano e o Dockerfile é papel do planner, que passa a consumir a análise.
8. **Entrada por `fs.FS`**, não por caminho: fixtures em memória nos testes (`fstest.MapFS`), o mesmo código sobre `os.DirFS` no worker. Links simbólicos não são seguidos.
9. **Corpus.** Fixtures sintéticas (em código) cobrem positivos e negativos de cada regra. Repositórios reais ficam num manifesto à parte (URL, SHA, perfil esperado), separados em desenvolvimento e reservados; os reservados são escolhidos pelo autor com critérios escritos. Promover uma regra a `validated` exige medidas no corpus (support, confidence e violation rate, como em REF-02), com limites a definir junto com o manifesto.

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| Regra como interface Go | Uma struct com função cobre o mesmo e simplifica regras sintéticas nos testes |
| Override aplicado depois, no worker | Duplica precedência e origem fora do motor e perde a explicação única por campo |
| Evidência mínima atual (`regra, descrição, fonte`) | Não distingue teste de runtime nem ausência de leitura |
| Analisador gerando o Dockerfile | Mistura decisão com geração e impede trocar o backend de build sem tocar nas regras |

## Consequências e limites conhecidos

- `build_command` é propriedade de HEU-02 nesta versão; separar em regra própria quando HEU-03 (estático) entrar.
- Catálogo único do perfil P-NODE; a seleção de perfil entra com o segundo perfil (M4).
- Coerência do lockfile é verificada só por existência do arquivo.
- Detecção de servidor de desenvolvimento em `scripts.start` é uma lista curta de ferramentas conhecidas; `npm run dev` indireto não é resolvido.
- Referências a variáveis dentro de comentários contam como referência.
- O worker ainda só guarda override de porta no deploy; generalizar o armazenamento dos overrides e o endpoint de resposta é trabalho do pipeline e da API.

## Critérios de revisão

Rever quando o corpus real mostrar classificação de escopo errada com frequência relevante, quando uma regra precisar de relações entre evidências (HEU-06) ou quando o primeiro override contradizer uma declaração e o comportamento "informar, não bloquear" se mostrar inadequado.
