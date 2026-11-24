# DEC-54 — A época das dependências: runtime padrão novo contra projeto antigo

> **Estado:** proposta discutida com o autor em 24/11/2026; **aprovada pelo autor em 24/11/2026 (A, B e C) e implementada**. A medição está no fim.
>
> **Escopo:** o catálogo escolhe o runtime mais novo (Python 3.13, Node 26) e o motor diz "pronto" para projetos que fixam dependências de uma época em que o runtime era outro. Esta decisão faz o motor **perguntar antes do build** quando os próprios pacotes fixados mostram o descompasso, e **diagnosticar depois** quando o log do build o mostra, sempre com a correção como proposta que só vale se a pessoa confirmar.
>
> **Relaciona-se com:** DEC-35 (o botão "novo deploy com esta porta", que esta decisão generaliza), DEC-48 (Python), DEC-53 (o corpus externo que mostrou o problema), issue #32 (Node fora de suporte), issue #35 (correções propostas e consentidas), RN-02 e RN-08.

## Contexto

Na DEC-53, `docker build` de verdade nas aplicações que a análise aceitou mostrou que **7 de 9 falhas** tinham a mesma causa: dependências fixadas de uma época antiga contra o runtime mais novo do catálogo. Pillow 9 e 10, Django 3.1 e `psycopg2` no Python 3.13; `react-scripts` 3 e 4 no Node 26 (`ERR_OSSL_EVP_UNSUPPORTED`). A análise dizia "pronto", e o build era o primeiro a descobrir.

## Decisão

**A. Perguntar antes do build, com uma tabela gerada de metadados públicos** (`pycompat.go`, `pycompat.json`).
1. `scripts/gen-pycompat/main.go` consulta o PyPI e, para **cada release** de uma lista fechada de 29 pacotes (o Django e os com código nativo que mais quebram: Pillow, numpy, pandas, scipy, psycopg2, lxml, cryptography, cffi, greenlet, PyYAML, matplotlib, scikit-learn, mysqlclient, grpcio, gevent, uvloop, pydantic-core, orjson, ujson, bcrypt, pyzmq, regex, markupsafe, aiohttp, multidict, yarl, frozenlist), guarda a menor e a maior versão do Python que os **classificadores do PyPI** declaram, só nos pontos em que o intervalo muda. O dado vem do PyPI, **não de nenhum repositório de teste**; rode o script para atualizar.
2. A HEU-04.2 (0.2) lê as dependências fixadas com `==` e confere o Python escolhido. Se algum pacote fixado não declara suporte a ele, **pergunta** `python_version`: a primeira opção é o **maior** Python que todos os pacotes fixados (e o `requires-python` do projeto) admitem; a segunda, manter o do Cobogó, dizendo que o build pode falhar. A pergunta cita cada pacote, a versão e o intervalo que ele declara. Quem **declarou** a versão (`.python-version`, `runtime.txt`) só recebe um aviso: a decisão é dela.
3. **Python legado** (`3.8` e `3.9`, `domain.LegacyPythonVersions`): saíram de suporte, então só valem por **confirmação** (override, nunca por `.python-version` nem `requires-python`, no molde da issue #32). Uma declaração de 3.8 ou 3.9 vira pergunta, com a versão do catálogo mais próxima ao lado. O planner usa a imagem `python:3.8`/`3.9` e servidores que elas aceitam (`gunicorn 23.0.0`, `uvicorn 0.30.6`), porque os do catálogo exigem um Python mais novo.

**B. Node: o contorno documentado, não uma versão antiga.** Com `react-scripts` ou `@vue/cli-service` **antes da versão 5** (as que usam o webpack 4) e um script de `build`, a HEU-10 (0.3) pergunta `build_legacy_openssl`: rodar **só o comando de build** com `NODE_OPTIONS=--openssl-legacy-provider`. Vale para P-NODE e P-STATIC, é um prefixo do `RUN` do build (**nunca** um `ENV`: a imagem que executa a aplicação não muda) e só depois da confirmação. Se algum script do projeto já aplica o contorno, o motor não pergunta. Versões antigas do Node continuam na issue #32.

**C. Diagnosticar a falha do build com a correção proposta.** O worker guarda o final do log do build (120 linhas, sem valores secretos) e, se o build falha, `diagnosis.Build` procura assinaturas e devolve a causa **citando a linha do log**:
- `ERR_OSSL_EVP_UNSUPPORTED` / `error:0308010C` (Node, estático) → sugere `build_legacy_openssl=true`;
- `No module named 'distutils'` (Python) → sugere `python_version=3.11`;
- `Failed building wheel for X` / `Could not build wheels for X` (Python) → sugere o Python **logo abaixo** do atual;
- `No matching distribution found for X` → só diz o que o log diz: a causa é ambígua (release removido, índice privado, outro Python), então **não sugere** um Python.

A sugestão (`diagnosis.suggestion`: `overrides` e `label`) aparece no painel como **"Novo deploy com …"**, ao lado de "Novo deploy sem mudar"; nada é aplicado em silêncio, e cada correção aceita é uma intervenção (issue #35). A API já aceita esses overrides num novo deploy.

Versões: HEU-04.2 0.2, HEU-10 0.3, catálogos `p-js-0.7` e `p-py-0.3`; os campos novos são `build_legacy_openssl` (catálogo JavaScript) e as versões `3.8`/`3.9` de `python_version`.

## Alternativas consideradas

- **Escolher a versão sozinho, sem perguntar.** É o que a tabela permitiria, mas ela é classificador, não garantia de wheel; escolher em silêncio apresentaria um palpite como prova (RN-02). Pergunta com a evidência é o contrato do motor.
- **Só o diagnóstico depois da falha (C).** É a parte que não depende de tabela, mas custa um build condenado antes de a pessoa ver a sugestão; por isso A existe para os pacotes mais comuns.
- **Uma versão antiga do Node para o webpack 4.** Exige imagens e testes de versões fora de suporte (issue #32). O contorno do OpenSSL é o que a própria ferramenta documenta e mantém o Node do catálogo.
- **Estender a tabela para o PyPI inteiro.** Cresce sem fim e passa a decidir por pacotes que ninguém mediu; a lista é fechada e o C cobre o resto pelo log.

## Consequências e limites

- Um projeto que **antes passava como `ready`** e fixa um pacote da lista contra o Python 3.13 agora recebe uma pergunta. Foi medido: os projetos conhecidos (`fec`, `caderno`, `afn-simulator`, o exemplo da Heroku) geram o mesmo Dockerfile de antes. As perguntas novas apareceram só em aplicações que o `docker build` mostrou que falham.
- O classificador do PyPI é o que o release **declara**; pode estar atrasado (um Python novo que funciona sem estar declarado) ou adiantado. A pergunta sempre oferece manter a versão do Cobogó.
- A tabela cobre só pacotes **fixados com `==`**; uma faixa (`>=`) não diz o release.
- O contorno do OpenSSL não resolve outras incompatibilidades do webpack 4 (por exemplo, `node-sass`).
- O diagnóstico por log reconhece só as quatro assinaturas acima; o resto segue como antes.
- **Fica para depois:** dependência de uma pasta **acima** da aplicação (um `axios` declarado só no `package.json` da raiz, que apareceu ao reconstruir o `devconnector`), yarn, pnpm e workspaces, e versões antigas do Node (#32).

## Verificação

- **Testes:** `pycompat_test.go` (tabela, ordem de versões, pergunta, resposta, `requires-python`), `node_layouts_test.go` (OpenSSL), `python_test.go` (Python legado), `planner/python_test.go` e `planner/planner_test.go` (imagem legada, `RUN` do build sem `ENV`), `diagnosis/build_test.go` (assinaturas e sugestões), `worker/buildfailure_test.go` (a falha leva o log e o diagnóstico) e `web/src/lib/deploy.test.ts` (a sugestão chega ao painel). Dois testes antigos mudaram porque o comportamento mudou de propósito: a declaração do Python 3.9 (agora pergunta) e a recusa do `3.9` como override (agora legado, por confirmação).
- **Regressão (o que já dava certo continua dando).** O motor de antes (`5245683`) e o de agora, nas 53 aplicações `ready` do corpus externo e nos seis projetos conhecidos: `fec` (backend e frontend), `caderno`, `afn-simulator` e o exemplo da Heroku geram o **mesmo Dockerfile, byte a byte**; o `clone-tabnews` segue pedindo resposta. Das 53, 43 não mudam. As 10 que mudam são o modelo de gerador que deixou de ser proposto (DEC-53) e **9 que passam a perguntar**: 6 frontends com `react-scripts` 3 ou 4 e 3 aplicações Python com pacotes fixados fora do Python 3.13. Para cada uma dessas 9, o `docker build` do Dockerfile **de antes** foi feito: **as 9 já falhavam**. Nenhum projeto que construía passou a perguntar.
- **O que as sugestões resolvem** (`docker build` de verdade, com a primeira opção da pergunta):

| Projeto (falhava na DEC-53) | Pergunta do motor | Resultado |
| --- | --- | --- |
| `saasitive` (Django 3.1.2) | Python 3.8 (legado, por confirmação) | **constrói** |
| `OnlineJudge` (Pillow 10.2, Django 3.2) | Python 3.10 | **constrói** |
| `python_shop` (Pillow 9.1, Django 3.2) | Python 3.10 | **constrói** |
| `python_book` (Pillow 9.1, Django 3.2) | Python 3.10 | **constrói** |
| `mern-full-course` (`react-scripts` 3.3) | OpenSSL no build | **constrói** |
| `dennisivy/Django-React-NotesApp` e `FARM-Stack-Course` (frontends CRA) | OpenSSL no build | **constroem** (antes não) |
| `devconnector` (cliente, `react-scripts` 4.0.3) | OpenSSL no build | passa do OpenSSL e cai em outra causa: `axios` declarado só no `package.json` da pasta acima |
| `FARM-Stack-Course` (backend, `uvicorn==0.14.0`) | nenhuma (fora da lista) | diagnóstico cita o log; **não sugere** um Python, porque o Python 3.12 não o resolve |

  Das 7 falhas "de época" da DEC-53, **5 passam a construir** com a sugestão do motor. Duas ficam: uma por dependência de uma pasta acima (que ficou para depois) e outra por um `uvicorn==0.14.0` que o pip não resolve nos Pythons 3.12 e 3.13 testados.
- **Sem conclusão:** `deepresearch_agent_harness` (`aiohttp` 3.8.5, `numpy` 1.26) também falhava no Python 3.13 e passa a perguntar pelo 3.10; a primeira tentativa no 3.10 terminou no `pip` (código de saída 2), sem causa que eu tenha isolado, e a segunda não conseguiu baixar a imagem (o Docker Hub devolveu `Bad Gateway`). Não conto como resolvido.
- **Diagnóstico por log (C), com logs reais de `docker build`:** `ERR_OSSL_EVP_UNSUPPORTED` do `react-scripts` 3 → sugere o contorno; `No module named 'distutils'` do Django 3.1 no 3.13 → sugere o 3.11; `No matching distribution found for uvicorn==0.14.0` → cita a linha e **não** sugere.
- **Dois casos de CRA ainda falham** por outra causa (`npm ci` em lockfile antigo): `mernProjectEcommerce`, `kunaltyagi9` (blog e crud). É a issue #35 (diagnóstico com correção proposta), que o item C abre o caminho para estender.
