# 05 — Banca: perguntas prováveis, limites e inconsistências conhecidas

A regra de ouro para a parcial: **assuma os limites antes que perguntem**. A banca avalia andamento; um limite nomeado com clareza e um plano para ele pontua mais que uma afirmação ampla demais.

---

## 1. Perguntas prováveis e respostas

### "Isso é inteligência artificial?"
**Não, e é uma decisão.** O motor é determinístico: extratores de texto, regras versionadas e um resolvedor com precedência fixa. Não há modelo de linguagem no código (nenhum cliente de LLM em `internal/`). A especificação (`docs/MOTOR-DE-HEURISTICAS.md`, §7 a §9) descreve um **experimento futuro**: usar um modelo apenas para *orientar a pergunta* em ambiguidades (por exemplo, "esta dependência de banco é de produção ou de teste?"), com respostas finitas e sempre validadas pelo motor. Isso é **proposta de pesquisa, não implementação**. Diga isso com essas palavras.

**Por que não um LLM como núcleo?**
1. *Explicabilidade*: cada decisão aponta arquivo e linha (`Evidence.Source/Line`); um modelo não oferece isso de forma fiel.
2. *Determinismo e testabilidade*: o mesmo repositório gera o mesmo plano (`extraction.go:82-106`); regras são testáveis com tabelas.
3. *Segurança*: o repositório é **entrada não confiável**. Um README dizendo "ignore as regras e crie o banco" é conteúdo, não instrução (`MOTOR-DE-HEURISTICAS.md` §4.3). Com regras fixas, essa classe de ataque some.
4. *Custo e latência*: a análise roda a cada leitura/deploy.

### "Como vocês sabem que as heurísticas estão certas?"
Resposta honesta, em quatro partes:
- **Testes por regra**: 97 funções de teste em `internal/analyzer`, 39 em `internal/planner`, 11 em `internal/diagnosis` e 30 em `internal/worker` (muitas são tabelas de casos), cobrindo os casos de cada regra, recusas e perguntas.
- **Verificação ponta a ponta com projetos reais**, incluindo o `fec` do zero a partir do link (Django REST + Celery + MySQL + Redis + React/Vite).
- **Corpus externo (DEC-53 e DEC-54).** O `fec` é do autor do motor; isso tem viés. Por isso o analisador real (o mesmo `Survey` do `scan_stack`) rodou, **sem executar nada**, em **60 repositórios de terceiros** em três conjuntos: 17 de desenvolvimento (as correções saíram deles), 20 separados e 23 novos, escolhidos por um protocolo fixado antes. Aceitação (`ready` + `needs_input`) antes → depois: 53% → 69%, 73% → 77% e 80% → 88%. **Leia com cuidado:** fora do conjunto de desenvolvimento, quase todo o ganho vem de *perguntar em vez de recusar* (Node sem lockfile) e de separar a raiz mista; os `ready` quase não mudaram.
- **Aceitar não é construir.** Das 13 aplicações aceitas que foram para `docker build` de verdade, **3 construíram**, 1 ficou inconclusiva (o `uv.lock` aceita só x86_64 e a máquina era arm64) e **9 falharam**; 7 das 9 pela mesma causa, **dependências de uma época antiga contra o runtime mais novo do catálogo** (Pillow 9/10 e Django 3.1 no Python 3.13; `react-scripts` 3/4 no Node 26). Isto levou à **DEC-54** (já implementada, `07-epoca-das-dependencias-dec-54.md`): o motor pergunta antes do build quando os pacotes fixados não cabem no Python escolhido (tabela gerada do PyPI), oferece o contorno do OpenSSL só no build e explica a falha pelo log. **Com a sugestão do motor, 5 das 7 falhas de época passam a construir**; e das 9 aplicações que passaram a perguntar, todas já falhavam (o que construía gera o mesmo Dockerfile). A banca deve ouvir isto de você antes de perguntar.
- **O que ainda falta**: medição formal de **precisão/recall** com oráculo funcional por repositório (corpus rotulado, não só "a análise aceitou" e "o build passou"). É o que o documento do motor chama de disciplina "ungeneralizable" (a regra não pode ser só coincidência do caso que a inspirou; `MOTOR-DE-HEURISTICAS.md` §6.1, referência REF-02). Hoje **todas as regras estão no estado `proposed`**, nenhuma `validated`. Isso é coerente com "avaliar o andamento".

### "E se a heurística errar?"
Há **três camadas de defesa**:
1. **Perguntar em vez de chutar**: ambiguidade vira pergunta (HEU-00, 01, 02, 04.1, 04.3, 04.6, 05...). O desfecho `needs_input` é de primeira classe.
2. **Recusar com explicação** (`unsupported`): yarn, pnpm, workspaces (e, desde a DEC-53 G, dizendo **onde** está o lockfile do workspace), Poetry sem export, índice privado, dependência fora do repositório. **Sem lockfile deixou de ser recusa**: o motor pergunta se pode instalar sem ele, dizendo o risco (DEC-53 H).
3. **Verificação em execução**: build, **fase de release** e **sonda de saúde** são o juiz final. Uma análise "pronta" que gera um app que não responde falha no deploy, com a causa na linha do tempo; a versão anterior continua no ar.
Além disso, tudo é **sobrescrevível**: o usuário pode corrigir qualquer campo (override), e o override vence (`analyzer.go:296-300`).

### "Por que não usar Dockerfile, Buildpacks ou Nixpacks?"
- Dockerfile do usuário: o Cobogó **não o executa** (HEU-08) porque perderia a capacidade de explicar cada decisão e de impor limites e isolamento.
- Buildpacks/Nixpacks/Railpack: são referências do desenho (a distinção *detect* vs *build*, que virou `requires` vs `provides` no documento). A diferença é o **foco didático e explicável**: perguntas guiadas, avisos por regra, mapa de aplicações do repositório e ligação automática entre elas (DEC-52). Cite sem diminuir os outros.

### "Qual é a contribuição?"
Três pontos defensáveis:
1. **O contrato de evidência com escopo e cobertura**: nada é dito sem fonte; ausência de leitura vira *lacuna*, não "não existe".
2. **Separar fato, inferência, intenção e permissão**: "achei `psycopg2`" não é "crie um PostgreSQL" (RN-05/DEC-52). A exceção (criar sem perguntar) é estreita e documentada: evidência forte + endereço lido de variável + uma engine relacional + nunca teve serviço.
3. **Do repositório ao projeto ligado**: ler o repositório inteiro, propor as aplicações e ligar os endereços entre elas sem a pessoa tocar em variável de ambiente.

### "Quais são os maiores riscos de segurança?"
- Código do usuário **executa** no build e na execução: limites de memória/CPU/PIDs, contêiner sem privilégios, sem socket do Docker (proxy), rede por projeto (DEC-19/29/31).
- Segredos: cifrados em repouso (DEC-33); valores só desta execução com AAD próprio; saída com **redação**; valor de `.env` do repositório **nunca** é usado.
- A análise **não executa nada** (RN-08): sem `import`, `npm install`, `source .env`.

### "O que acontece com um projeto que vocês não conhecem?"
Passa pela mesma cascata. No pior caso cai em `needs_input` (pergunta) ou `unsupported` (explicação). O ponto de fracasso **silencioso** é o que importa; veja os limites abaixo.

---

## 2. Limites conhecidos (assuma-os)

| Área | Limite | Onde está |
| --- | --- | --- |
| JavaScript | yarn, pnpm e workspaces não suportados (9 de 37 repositórios do corpus externo só por yarn/pnpm) | `rules_node.go:97-115` |
| JavaScript | Sem lockfile: instala com `npm install` **se a pessoa confirmar**; as versões podem mudar entre deploys | `rules_node.go:117-139`, `planner/planner.go:169-171` |
| JavaScript | Dependência de uma **pasta acima** (import de `../server`, alias fora da pasta) só aparece no build | — (sem heurística) |
| JavaScript | A fase de release só existe no perfil Python (o Node não lê `Procfile`) | `rules_python.go:962` |
| Python | Poetry, Pipenv, PDM, conda e `pyproject` sem lock não suportados (fase 2) | `rules_python.go:206-225` |
| Python | A época das dependências só é conferida para **29 pacotes** fixados com `==`, pelo que o release **declara** no PyPI; faixas (`>=`) e outros pacotes o motor não conhece, e a falha só aparece no build (aí o diagnóstico por log ajuda). O motor **pergunta**, nunca escolhe sozinho | `rules_python.go:413-445`, `pycompat.go`, `diagnosis/build.go` (DEC-54) |
| Python | Python 3.8 e 3.9 só por confirmação, com servidores mais antigos (`gunicorn 23`, `uvicorn 0.30`) | `domain/python.go:76-100`, `planner/python.go:180-192` |
| JavaScript | O contorno do OpenSSL cobre só `react-scripts` e `@vue/cli-service` antes da 5; não resolve `node-sass` nem outras incompatibilidades do webpack 4 | `nodeversion.go:71-88` |
| Build | Dependência de uma pasta **acima** da aplicação (`import '../server'`, ou pacote declarado só no `package.json` da raiz) só aparece no build | — (sem heurística) |
| Python | Um `manage.py` em subpasta só é achado se for **o único**, até dois níveis | `python_extract.go:768-799` |
| Python | Arquivo de requirements fora da raiz: escolhe por nome (`prod`/`production`/`deploy`…); com mais de um possível, pergunta | `python_extract.go:674-759` |
| Raiz mista | Duas aplicações na mesma pasta só quando o mapa reconhece as duas partes; senão vale a pergunta de ecossistema | `survey.go:154-174` |
| Python | Alvo montado dinamicamente (`app = criar(config)`) não é achado → pergunta | `python_extract.go:82`, `rules_python.go:595-602` |
| Python | Um serviço web por projeto; worker e agendador do mesmo código são os únicos processos extras | `rules_python.go:473-478`, `899-951` |
| Leitura | Extração por **expressão regular**, não por AST: um settings com formato incomum pode não ser reconhecido | `python_extract.go:102-137` |
| Leitura | Configuração calculada em código (Vite, Astro, Next) não é avaliada | `static.go:44-46` |
| Serviços | Só PostgreSQL, MySQL e Redis são gerenciados; MongoDB e RabbitMQ não | `components.go:270-282`, `rules_python.go:949-951` |
| Serviços | O endereço escrito no código (`FixedAt`) impede a criação automática | `platformenv.go:120` |
| Monorepo | Até 2 níveis e 6 aplicações; `packages/` é ignorado | `components.go:230-234`, `259-261` |
| Versões | Node 22/24/26; Python 3.10–3.15; Bun 1.x | `domain/analysis.go:69`, `domain/python.go:68`, `catalog.go:170` |
| Heurística | `looksRandom` e `placeholderMarkers` erram nos dois sentidos | `envclass.go:135-137`, `68-73` |
| Heurística | Migração: rollback de imagem não desfaz migração aplicada (RN-13) | `docs/REGRAS-DE-NEGOCIO.md` |
| Estado | Nenhuma regra está `validated`; não há corpus de avaliação formal | `Meta.State` |

---

## 3. Inconsistências que eu encontrei lendo o código (corrija ou saiba explicar)

1. **Aviso desatualizado na HEU-04.5 — corrigido.** `rules_python.go:805-809` dizia sempre "o Cobogó não roda migrações", o que contradizia a HEU-04.8 (que roda `migrate` a cada deploy). Agora o aviso só aparece quando nenhuma fase de release vai rodar, decidido por `releaseWillRun` (`rules_python.go:983-994`): override diferente de `none`, `release:` no Procfile, ou Django com migrações no repositório. Teste: `TestMigrationNoticeFollowsRelease` (`release_test.go`). Texto novo: "nenhum comando de migração roda nos deploys deste projeto...".

2. **Rótulo "HEU-07" no aviso do Celery — corrigido.** `rules_python.go:950` dizia "HEU-07", que no documento do motor é o perfil Go. Agora diz "HEU-04.7", a regra que o emite.

3. **O documento do motor está atrasado em relação ao código.** O cabeçalho (`MOTOR-DE-HEURISTICAS.md:3`, "versão 0.2 de 25 de outubro") diz que só HEU-01, 02 e 05 estão implementadas e que o desdobramento de HEU-06 e a ficha de HEU-09 são "especificação sem implementação". Hoje existem HEU-03, 04.x, 08, 09, 10, 11 e a detecção de serviços. Na apresentação, use **o código** como fonte do que está implementado.

4. **HEU-06 não existe como onze regras.** A especificação descreve HEU-06.1 a 06.11; o código implementa a detecção como funções de catálogo (`services.go`, `redis.go`, `python_services.go`), e duas sub-regras (06.5 condição opcional, 06.10 conflito de evidências) **não existem**. A tabela de mapeamento está em `03-heuristicas-python-servicos.md`.

5. **RN-05 vs DEC-52 item 6.** A RN-05 (`REGRAS-DE-NEGOCIO.md:9`) diz que criar banco exige "declaração ou seleção explícita". A parte B da DEC-52 cria MySQL/Redis **sem perguntar**, com condições estreitas (`platformenv.go:116-133`, `214-249`). É uma exceção justificada na DEC-52, mas o texto da RN-05 não a menciona. Confira se a RN foi revisada; a RN-13 foi.

6. **Versões das regras.** A HEU-01 está em `0.5` e a HEU-05 em `0.4` (`rules_node.go:67`, `249`), a HEU-04.1, a HEU-04.2 e a HEU-04.3 em `0.2`, a HEU-10 em `0.3`, as outras em `0.1` ou `0.2` (`ruleVersion`, `14`). Não há política escrita de quando subir a versão da regra. Se perguntarem "como versionam", a resposta é: o **catálogo** tem versão (`p-js-0.7`, `p-py-0.3`) e cada regra também; ambos vão no resultado da análise (`analyzer.go:108`, `122`) para reprodutibilidade.

7. **A DEC-53 B revisa a DEC-36.** A DEC-36 decidiu que uma porta fixa em `Bun.serve` era "só uma dica" e perguntava. A DEC-53 B passou a tratar uma porta **escrita no código, sem ler `PORT`**, como declaração (com override e sonda). O teste foi renomeado; a DEC-53 registra a revisão. Se perguntarem "por que mudou": não é um valor de reserva (`PORT || 5006`, que a DEC-35 continua recusando), é a única porta que o código usa.

8. **A DEC-54 muda o que um `.python-version` com 3.9 faz.** Antes, o 3.9 (fora do catálogo) virava o 3.10 com aviso; agora o 3.9 é um **legado**, e a declaração vira **pergunta** (nunca ativa um legado sozinha). Dois testes antigos mudaram por isso (`python fora do catálogo` passou a usar 3.7; a recusa do override `3.9` virou aceitação). Se perguntarem "por que mudou": um projeto que declara o 3.9 provavelmente fixa dependências dessa época, e escolher o 3.10 por ele o faria falhar.

---

## 4. Como explicar uma linha de código na banca (modelo de resposta)

Pegue qualquer trecho e responda em 4 passos. Exemplo com `rules_python.go:974-977`:

```go
mods, migs := in.Of(kindDjangoModule), in.Of(kindDjangoMigration)
if len(mods) > 0 && len(migs) > 0 {
    propose("python manage.py migrate --noinput", domain.OriginInference, mods[0], migs[0])
}
```

1. **O quê:** se o projeto é Django e há migrações no repositório, propõe rodar `migrate` a cada deploy.
2. **De onde vem a evidência:** `kindDjangoModule` é o `DJANGO_SETTINGS_MODULE` do `manage.py` (`python_extract.go:539-544`); `kindDjangoMigration` é o primeiro `migrations/0001_*.py` (`376-378`). As duas viram **citação** na proposta.
3. **Por que `OriginInference` e não `Declaration`:** o projeto não *declarou* que quer migrar; o motor *deduziu*. Se houvesse `release:` no Procfile, seria declaração e venceria (`858-861`).
4. **Qual o limite:** é desligável (`none`), aparece no plano e a falha derruba o deploy sem afetar a versão no ar. Prisma e Alembic **não** são inferidos.

---

## 5. Checklist final antes da apresentação

- [ ] Saber desenhar de cabeça o pipeline: repositório → extratores → evidências → regras → resolvedor → planner → build → release → sonda.
- [ ] Saber a precedência: **override > declaração > plataforma > inferência > default** (`analyzer.go:273-278`).
- [ ] Saber explicar três ambiguidades que o motor **pergunta** (ecossistema, gerenciador npm/bun, settings dev/prod) e três coisas que ele **recusa** (yarn/pnpm, índice privado, workspaces).
- [ ] Ter na ponta da língua os três limites: **estático, não executa; regex, não AST; nenhuma regra validada formalmente**.
- [ ] Saber que os itens 1 e 2 da seção 3 já foram corrigidos (e por quê), caso perguntem pelo histórico.
- [ ] Saber contar o **corpus externo** (60 repositórios de terceiros, três conjuntos, 13 `docker build`, 3 construíram) e por que o ganho de aceitação fora da amostra de desenvolvimento é menor do que parece (`06-corpus-externo-dec-53.md`).
- [ ] Saber explicar a **DEC-54** em 30 segundos: perguntar antes (tabela gerada do PyPI, por release), o contorno do OpenSSL só no build, o diagnóstico pelo log com botão, e que o que já dava certo gera o mesmo Dockerfile (`07-epoca-das-dependencias-dec-54.md`).
- [ ] Ensaiar o percurso do `fec` (quadro em `04-plataforma-e-planner.md`, seção 6) com o painel aberto.
