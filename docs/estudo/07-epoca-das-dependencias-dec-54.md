# 07 — A época das dependências: a DEC-54

A DEC-53 mostrou que **aceitar não é construir**: 7 das 9 falhas de `docker build` tinham a mesma causa, dependências fixadas de uma época antiga contra o runtime mais novo do catálogo (Python 3.13, Node 26). A DEC-54 faz o motor **perguntar antes** quando os próprios pacotes fixados mostram o descompasso, e **explicar depois** quando o log do build o mostra. Decisão completa: `docs/decisoes/DEC-54-epoca-das-dependencias.md`.

## 1. O problema, com os casos reais

| Projeto | O que fixa | O que acontecia no Cobogó |
| --- | --- | --- |
| `saasitive` | Django 3.1.2 | `No module named 'distutils'` (saiu do Python 3.12) |
| `OnlineJudge` | Pillow 10.2, Django 3.2 | sem wheel para o 3.13; compila do zero e falha |
| `python_shop`, `python_book` | Pillow 9.1.1, Django 3.2 | idem |
| `mern-full-course`, `devconnector` | `react-scripts` 3 e 4 | `ERR_OSSL_EVP_UNSUPPORTED` no Node 26 |

O motor dizia "pronto" nos dois casos; o build era o primeiro a descobrir.

## 2. As três partes

### A. Perguntar antes, com uma tabela **gerada** (Python)
- **Origem do dado:** `scripts/gen-pycompat/main.go` consulta o PyPI e, para **cada release** de 29 pacotes (Django, Pillow, numpy, pandas, scipy, psycopg2, lxml, cryptography…), guarda a menor e a maior versão do Python que os **classificadores** declaram, só onde o intervalo muda (`internal/analyzer/pycompat.json`, ~17 KB). **Não vem de nenhum repositório de teste**; qualquer pessoa regenera.
- **Uso** (`rules_python.go:413-445`, `pycompat.go`): lê os pacotes fixados com `==`, descobre o maior Python que todos aceitam (respeitando o `requires-python`) e **pergunta** `python_version`, citando pacote, versão e intervalo. Quem **declarou** a versão só recebe aviso.
- **Python legado (3.8 e 3.9):** existem, mas só por **confirmação** (nunca por `.python-version` ou `requires-python`), com servidores que eles aceitam.
- **A lição de projeto:** a primeira versão da tabela guardava um intervalo por série (`2.9`). Dizia que o `psycopg2` 2.9.9 só ia até o Python 3.9 (o que o 2.9.0 declarava; o 2.9.9 vai até o 3.12). Perguntar sem necessidade a quem já dava certo seria **regressão**; por isso a tabela passou a ser **por release**.

### B. Node: o contorno documentado, só no build
- `react-scripts` ou `@vue/cli-service` **antes da 5** (webpack 4) + um `scripts.build` → pergunta `build_legacy_openssl` (`nodeversion.go:71-88`).
- Se confirmado, o `RUN` do build ganha `NODE_OPTIONS=--openssl-legacy-provider` (`planner/planner.go:61-74`). **Prefixo do comando, nunca `ENV`:** a aplicação em execução não muda.
- Se algum script do projeto **já** aplica o contorno (`scriptsHandleOpenSSL`), não pergunta.
- Versões antigas do Node continuam na issue #32; o contorno mantém o Node 26.

### C. Diagnosticar a falha do build, com a correção proposta
- O worker guarda 120 linhas do log do build (`worker.go:274-290`); `diagnosis.Build` (`diagnosis/build.go`) procura assinaturas e **cita a linha do log**.
- Assinaturas: OpenSSL (Node/estático), `distutils` ausente e `Failed building wheel` (Python). O `No matching distribution` **não sugere** um Python: a causa é ambígua e o teste real mostrou que o 3.12 não resolvia.
- A sugestão (`diagnosis.suggestion`) vira o botão **"Novo deploy com …"** no painel; nada é aplicado sem o clique, e cada correção aceita é uma intervenção (issue #35).

## 3. Quem já dava certo continua dando

Motor de antes contra o de agora, nas 53 aplicações `ready` do corpus externo e nos 6 projetos conhecidos:
- `fec` (backend e frontend), `caderno`, `afn-simulator` e o exemplo da Heroku geram o **mesmo Dockerfile, byte a byte**; o `clone-tabnews` segue pedindo resposta.
- 43 das 53 não mudam; 1 é o modelo de gerador (DEC-53); **9 passam a perguntar** (6 frontends CRA e 3 aplicações Python). O `docker build` do Dockerfile de **antes** foi feito para essas 9: **todas já falhavam**. Nenhum projeto que construía passou a perguntar.

## 4. O que as sugestões resolvem (`docker build` de verdade)

| Projeto | Pergunta do motor | Resultado |
| --- | --- | --- |
| `saasitive` (Django 3.1.2) | Python 3.8 (legado, por confirmação) | constrói |
| `OnlineJudge`, `python_shop`, `python_book` | Python 3.10 | constroem |
| `mern-full-course` (`react-scripts` 3.3) | OpenSSL no build | constrói |
| `Django-React-NotesApp`, `FARM-Stack-Course` (frontends CRA) | OpenSSL no build | constroem (antes não) |
| `devconnector` (cliente, `react-scripts` 4.0.3) | OpenSSL no build | passa do OpenSSL e cai em outra causa (`axios` só no `package.json` da pasta acima) |
| `FARM-Stack-Course` (backend, `uvicorn==0.14.0`) | nenhuma | o diagnóstico cita o log e **não sugere** (o 3.12 não resolve) |
| `deepresearch_agent_harness` | Python 3.10 | **sem conclusão**: erro do `pip` não isolado e, na segunda tentativa, `Bad Gateway` do Docker Hub |

**Das 7 falhas de época da DEC-53, 5 passam a construir** com a sugestão do motor.

## 5. O que continua aberto
- Dependência de uma pasta **acima** da aplicação.
- yarn, pnpm e workspaces.
- `npm ci` que falha por lockfile antigo (`mernProjectEcommerce` e os dois de `kunaltyagi9`): issue #35.
- A tabela cobre só pacotes **fixados com `==`**; faixas (`>=`) não dizem o release. Classificador é o que o release **declara**, não garantia de wheel: por isso é pergunta, nunca escolha sozinha.

## 6. Como contar isto na banca (30 segundos)

> "Aceitar não é construir: construí as imagens de verdade e vi que a maior causa de falha era dependência antiga contra runtime novo. O motor agora pergunta antes, usando uma tabela que eu **gero** do PyPI e não dos repositórios que falharam, e explica depois, citando a linha do log. Nada é escolhido sozinho: tudo vira pergunta ou botão. E conferi que o que já dava certo gera o mesmo Dockerfile: das 9 aplicações que passaram a perguntar, todas já falhavam. Com a sugestão do motor, 5 das 7 falhas passam a construir."
