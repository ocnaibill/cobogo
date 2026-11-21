# DEC-39 — Variáveis e segredos guiados: importar, sugerir, exigir, auditar e rotacionar

> **Estado:** decidida com o autor em 20/11/2026; **implementada**. Última parte da primeira leva do MVP (DEC-35 a DEC-39).
>
> **Escopo:** como o usuário traz, revisa e exige variáveis de ambiente; o que fica registrado sobre elas; como a chave de cifra gira; e que testes provam a proteção dos valores.
>
> **Relaciona-se com:** DEC-14, DEC-15, DEC-21, DEC-24 (variáveis e geração), DEC-33 (a API não lê o texto cifrado), DEC-37 (variáveis públicas de build), RF-10, RF-13, RF-22, RF-23, RF-29, RN-11, RN-18, RNF-08, issues #7 e #21.

## Contexto

A DEC-24 entregou descoberta, classes, cifra AES-256-GCM presa ao projeto, ao nome e à versão, geração de segredos e o diagnóstico de variável ausente. Faltava o resto do ciclo: trazer várias variáveis de uma vez sem digitar uma a uma, ajudar com valores que o Cobogó já conhece, deixar o usuário exigir uma variável, saber quem mudou o quê, e poder trocar a chave. A rotação, em particular, **era impossível**: a API e o worker criavam a cifra com a chave única na versão 1, e trocar `COBOGO_SECRET_KEY` deixaria todas as variáveis ilegíveis.

## Decisão

1. **Importar `.env` colado, com revisão** (`POST /projects/{id}/env/import`, modos `preview` e `apply`).
   - O texto é analisado **sem executar nem expandir nada**: `NOME=valor`, `export NOME=valor`, comentários, aspas simples (literais) e duplas (só `\n`, `\"` e `\\`). `$VAR`, `${VAR}`, `$(...)` e crases ficam como texto literal, com aviso; valores de várias linhas e sintaxe que não é atribuição viram diagnóstico, nunca valor.
   - A **prévia nunca devolve valores**. Por nome, diz a ação (`create`, `replace`, `blocked`), a classe e os avisos: nome inválido, duplicado (vale a última), variável da plataforma, valor com cara de placeholder, valor em formato de credencial conhecida (só o tipo), valor vazio, valor grande demais, expansão não feita.
   - O **aplicar** recebe o texto e os `names` escolhidos; só grava esses. Por padrão a interface deixa **desmarcadas** as que substituiriam um valor existente e as que parecem placeholder ou vazias. A API não lê o texto cifrado (DEC-33), então não consegue dizer se um valor colado é igual ao guardado: por isso não existe a ação "sem mudança", e uma variável que já existe aparece como `replace`. Um valor vazio ou da plataforma fica `blocked` e nunca é gravado. Até 200 variáveis e 256 KiB por pedido. Cada gravação segue as mesmas regras de `PUT /env/{name}` (versão nova, cifra, auditoria).
2. **Sugestões da plataforma, oferecidas e nunca aplicadas sozinhas** (`GET /projects/{id}/env/suggestions`). Lista fechada: `<SLUG>_URL` (o `slug` em maiúsculas, com `_`), `APP_URL`, `SITE_URL`, `BASE_URL`, `PUBLIC_URL`, `NEXT_PUBLIC_SITE_URL`. Só aparece para um nome que a análise **encontrou** no código ou no exemplo e que o projeto ainda não definiu, e só quando há domínio das aplicações. O valor é a URL pública do projeto **sem a barra final**; o painel mostra "Usar o endereço do projeto" e a pessoa confirma. O nome decide o que se oferece, não o que se grava (issue #7).
3. **Configuração pública de build:** já entregue na DEC-37. Aqui, só o aviso no painel de que mudar uma variável pública exige novo build.
4. **Variável obrigatória que bloqueia** (`PUT /projects/{id}/required-env`, lista de nomes). Quem decide é o usuário; o painel oferece "marcar as prováveis obrigatórias" a partir da análise (`likely_required`). O worker confere logo depois da análise, **antes do plano e do build**: se faltar valor para uma obrigatória, o deploy **falha cedo** com a lista, o diagnóstico `confirmed` com `variables` e o link "Preencher variáveis". Falhar cedo (e não uma pendência `NEEDS_INPUT`) reaproveita a tela de falha e poupa o tempo do build; a pendência hoje só responde campos do plano.
5. **Histórico por auditoria, sem guardar valores antigos.** A auditoria ganha um campo `detail` (JSON) com `version` e `source` (`user`, `generated`, `imported`), e passa a registrar `env.import` e `env.required`. `GET /projects/{id}/env-history` devolve os eventos de variáveis do projeto (nome, ação, versão, origem, quem, quando), e o painel mostra "v3 · digitada por admin há 2 h". O evento `starting` de cada release já diz qual versão ela recebeu. Não há restaurar versão antiga: valor errado se corrige digitando de novo.
6. **Rotação de chave.** `COBOGO_SECRET_KEYS="1:<chave>,2:<chave>"` e `COBOGO_SECRET_KEY_CURRENT=2` formam o **chaveiro**; só `COBOGO_SECRET_KEY` continua valendo (versão 1), então nada muda para quem não rotaciona. A leitura abre cada variável com a chave da `key_version` dela; a gravação usa a atual. O comando de operador `cobogo-api rotate-secrets` (credencial do dono do banco, no molde do `migrate`; a API e o worker continuam sem ler nem reescrever texto cifrado fora do uso normal) **recifra** cada variável com a chave atual **sem mudar a versão da variável** (o valor é o mesmo: não pede novo deploy), tem `--check` que conta por versão de chave, e termina dizendo se a chave antiga já pode sair. **Rotação de chave não é rotação de credencial:** trocar a senha de um serviço externo é digitar o valor novo, que gera versão nova e pede novo deploy.
7. **Testes de proteção com valor-canário.** O valor não aparece em resposta da API, log da API e do worker, evento, análise, plano, histórico nem camadas da imagem; no `pg_dump` só existe o texto cifrado; um byte alterado no texto cifrado, ou o texto cifrado trocado entre projetos, nomes ou versões, faz a leitura falhar; depois da rotação tudo segue legível e a chave antiga não abre mais nada.

## Alternativas consideradas

- **Guardar as versões antigas cifradas** (histórico restaurável): amplia o que fica exposto e o que a chave protege, sem demanda do corpus; a auditoria responde "quem mudou o quê e quando".
- **Variável obrigatória como pendência `NEEDS_INPUT`:** ficaria mais "guiada", mas exigiria um tipo novo de resposta e o build só pararia depois de gasto.
- **Sugerir valor por qualquer nome terminado em `_URL`:** `DATABASE_URL` receberia a URL do site. A lista fechada erra para o lado seguro.
- **Rotacionar pela API:** exigiria dar à API o privilégio de ler o texto cifrado, que a DEC-33 tirou de propósito.

## Consequências e limites (exposições residuais, ditas e não escondidas)

- O valor vai ao contêiner como variável de ambiente: quem tem acesso ao Docker o lê com `docker inspect`. Quem tem a chave decifra (RN-18); **não há E2EE**.
- Um `.env` colado passa pelo navegador e pela API (HTTPS), mas não é gravado em lugar nenhum além da variável cifrada; a prévia e a resposta nunca o devolvem.
- A lista de sugestões, de placeholders e de formatos de credencial é curta e fechada; cresce com o corpus.
- O vínculo de variáveis com serviços (por exemplo, `DATABASE_URL` preenchida por um PostgreSQL auxiliar) fica para o M5.
- Uma variável obrigatória que o projeto passa a não usar mais continua bloqueando até alguém a desmarcar.

## Verificação

- **Testes:** o leitor de `.env` (aspas, `export`, comentários, `$VAR` literal, valores de várias linhas, BOM, último valor vale, diagnóstico que nunca repete o conteúdo da linha); a cifra e o chaveiro (abrir com a chave da versão, recusar versão desconhecida, nenhuma mensagem repete uma chave); com PostgreSQL real, o histórico lido da auditoria (sem valores), as variáveis obrigatórias (escopo do dono, ordenadas e sem repetição), **a rotação** (tudo segue legível sem mudar a versão da variável, tudo-ou-nada, o erro nomeia a variável e nunca o valor), o texto cifrado adulterado ou trocado entre projetos, nomes e versões recusado, e o valor-canário **ausente do banco**; a API (prévia sem valores e com os avisos certos, aplicar só os nomes escolhidos, bloqueadas nunca gravadas, limites, sugestões só da lista fechada e sem gravar, obrigatórias e histórico) e o worker (a lista do que falta, sem ler nenhum valor).
- **Em execução** (API e worker locais, banco novo, Docker de verdade, deploy de um repositório público com valores-canário):
  - **Importar e exigir:** prévia, aplicar e gerar funcionaram; com `DATABASE_URL` exigida e sem valor, o deploy falhou em `ANALYZING`, sem build, com o diagnóstico `confirmed` e a variável na lista; definida a variável, o deploy foi a `SUCCEEDED`.
  - **Canário:** três valores (digitado, importado e dentro de uma URL de conexão) e o segredo gerado foram procurados em **todas as respostas da API** (projeto, variáveis, histórico, obrigatórias, sugestões, deploys, eventos, análise, plano, limites, operações, configurações e a prévia da importação), nos **logs da API e do worker**, num **`pg_dump` do banco inteiro** e na **imagem do projeto** (`docker save`): nenhum aparece. O valor **está** no ambiente do contêiner (controle positivo, e a exposição residual dita acima).
  - **Rotação:** `--check` contou as 5 variáveis na chave 1; a rotação as passou para a chave 2 sem mudar a versão de nenhuma; API e worker reiniciados **só com a chave 2** gravaram uma variável nova com a chave 2, e um deploy depois da rotação entregou os valores corretos ao contêiner. Com só a chave antiga, a leitura falha dizendo que a versão da chave não está no chaveiro.
  - **Painel:** importar com prévia e caixas, sugestão com um clique, "exigir" por variável e "marcar as prováveis", histórico e o aviso de variável pública no navegador.

## Critérios de revisão

- Medir no corpus quantas variáveis o `.env` importado cobre e quantas sugestões são aceitas.
- Rever a lista de sugestões se aparecerem nomes de URL pública fora dela mais de uma vez.
