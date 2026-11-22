# DEC-44 — Conexão do PostgreSQL gerenciado: variáveis separadas e TLS

> **Estado:** aprovada pelo autor em 22/11/2026 e **implementada** (a verificação está no fim); não está na VM até a próxima atualização.
>
> **Escopo:** como o banco gerenciado (DEC-42) é entregue a aplicações que não leem uma URL só, e como atende as que exigem TLS em produção.
>
> **Relaciona-se com:** DEC-42 (o banco gerenciado e o vínculo), DEC-39 (variáveis e segredos), DEC-41 (volumes), RF-13, RN-05, issue #40.

## Contexto

A DEC-42 liga o banco a **uma** variável de URL e não oferece TLS. O primeiro repositório real testado (um Next.js no estilo curso.dev) mostrou os dois limites de uma vez: monta a conexão com `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_DB` e `POSTGRES_PASSWORD`, e em produção usa `ssl: true` (com `POSTGRES_CA` opcional). O banco foi criado e ficou saudável, mas a aplicação não conectou. Em projetos de estudo esse padrão é comum.

## Decisão

1. **O vínculo passa a ser um conjunto de variáveis, não uma.** Um estilo de conexão é uma de quatro formas fechadas: **URL** (`DATABASE_URL`, `POSTGRES_URL`, `DB_URL`) ou uma **família de partes** de prefixo `POSTGRES_`, `PG`, `DB_` ou `DATABASE_`, com os papéis `host`, `port`, `user`, `password`, `database` e, opcionalmente, `ca`. O banco entrega os mesmos cinco valores (host, porta, usuário, senha e banco) em qualquer estilo, e a **senha é uma só**.
2. **A análise detecta o estilo pelo que o código referencia.** Se o código lê ao menos o host e a senha de uma família (pelos sufixos `HOST`/`HOSTNAME`, `PASSWORD`/`PASS`/`PWD`, `USER`/`USERNAME`, `PORT`, `DB`/`DATABASE`/`NAME`), o estilo é essa família, e o Cobogó grava **exatamente as variáveis que o código referencia**, nem mais, nem menos. Sem família reconhecida, vale a URL, como hoje. A pergunta antes do build (HEU-06.6) mostra o estilo detectado e **cada variável que seria criada, com o nome editável**: se o código lê `MYAPP_DB_HOST`, a pessoa escreve esse nome ao lado do papel "host" e o Cobogó grava essa variável. Também deixa trocar de estilo (URL ou outra família).
3. **Mesmas garantias da DEC-42.** Todas as variáveis do vínculo têm a origem `linked`, cifradas como as demais, **não se editam, removem nem importam** enquanto vinculadas, e saem juntas quando o banco é apagado. Nenhum valor digitado pela pessoa é sobrescrito: se **qualquer** variável do conjunto já tem valor digitado, a escolha "criar" é recusada e diz qual. A senha continua sem aparecer em lugar nenhum; as demais (host, porta, usuário e banco) não são segredos, mas seguem o mesmo caminho para não haver duas regras.
4. **O banco gerenciado passa a oferecer TLS**, ligado por padrão nos bancos **novos**. O certificado é autoassinado, com o nome do contêiner no `SAN`, gerado na criação **dentro do volume dos dados**, fora do diretório do PostgreSQL (`PGDATA` passa a ser uma subpasta do volume). O worker gera o certificado em Go (a imagem do PostgreSQL não traz `openssl`) e o grava no volume por um contêiner descartável que **nunca inicia**, sem rede e sem capacidades, como a posse dos volumes da DEC-41; a chave fica com o usuário `postgres`, modo 0600. Assim o certificado e a chave sobrevivem a recriar o contêiner. O banco continua **aceitando conexão sem TLS**: ligar TLS não obriga ninguém.
5. **A aplicação passa a confiar na CA do banco, sem mudar o código.** O certificado do banco é público. Ao subir uma aplicação de um projeto com banco gerenciado, o worker **injeta o certificado num arquivo do contêiner** (`/etc/cobogo/db-ca.pem`, lido do contêiner do banco e copiado antes de iniciar, sem guardar nada em outro lugar) e entrega duas variáveis da plataforma que apontam para ele: `NODE_EXTRA_CA_CERTS` (o Node acrescenta a CA às raízes em que já confia) e `PGSSLROOTCERT` (os clientes baseados em libpq). Com isso, uma aplicação que faz `ssl: true`, ou que usa `sslmode=require` num cliente que verifica de verdade, **valida o servidor e o nome** contra o nosso certificado e conecta, sem tocar no código. Se o código lê uma variável de CA própria (`POSTGRES_CA`, `DB_CA`, `DATABASE_CA`, `DATABASE_SSL_CA`, `PG_CA`), ela também recebe o certificado, **em tempo de execução**, sem ser gravada: a aba "Banco" diz que ela é entregue na execução, e a lista de variáveis não a mostra, porque não há valor guardado. A URL não ganha `sslmode`: clientes que não verificam não precisam dele, e os que verificam já confiam na CA. O que continua de fora: clientes que não usam nenhum desses mecanismos (por exemplo, o JDBC com verificação) o que não funcionar é dito na interface, não prometido. O Bun honra `NODE_EXTRA_CA_CERTS` (medido, ver abaixo).
6. **TLS aqui é compatibilidade, não a barreira de segurança.** O banco só existe na rede privada do projeto, sem porta publicada; o que o protege é isso e a senha. A interface não vende TLS como o que torna o banco seguro.
7. **Bancos já criados** (pela DEC-42, sem TLS e com o diretório de dados na raiz do volume) **não são migrados** nem mudam: continuam como estão, e passar a TLS é recriar o banco (apagar e escolher de novo, o que apaga os dados). Isso é dito na aba "Banco" desses projetos. Nenhum banco gerenciado existe em produção hoje.
8. **O que não muda:** o banco nasce vazio, o Cobogó não roda migrações (RN-13), PostgreSQL 16, 256 MiB, só na rede do projeto.

## Alternativas consideradas

- **Gravar a URL e todas as partes ao mesmo tempo:** serviria a qualquer aplicação sem detectar nada, mas cria variáveis que o código não lê e polui a lista; a regra "só o que o código referencia" mantém o vínculo explicável.
- **Deixar a pessoa mapear as variáveis uma a uma:** é flexível e resolve tudo, mas devolve ao usuário o trabalho que a análise sabe fazer. Fica como escape (trocar de estilo na pergunta), não como padrão.
- **TLS obrigatório no banco:** quebraria as aplicações que conectam sem TLS, que são a maioria.
- **Certificado de uma CA própria da plataforma:** valeria para todos os projetos, mas obriga a guardar uma chave de CA e a distribuí-la; o autoassinado por projeto, com a chave no volume, é mais simples de limpar.
- **Guardar a chave privada no banco do Cobogó:** exigiria dar ao worker escrita de texto cifrado, que a DEC-33 tirou de propósito.

## Consequências e limites

- Uma aplicação que **verifica TLS** por um caminho que não usa `NODE_EXTRA_CA_CERTS`, `PGSSLROOTCERT` nem uma variável de CA que ela leia não conecta ao banco gerenciado. A saída é conexão sem TLS, se o código deixar, ou um banco externo.
- A detecção de família é estática e fechada: nomes fora dessas quatro famílias (`MYAPP_DB_HOST`, por exemplo) não são reconhecidos como família e caem na URL; a pessoa então escreve, na pergunta, o nome de cada variável que o código lê.
- Papéis que o código não referencia (por exemplo a porta) não são gravados; valem os padrões da biblioteca (5432).
- O certificado é autoassinado, com validade longa; renovar é uma operação futura, junto da troca de senha.

## Critérios de revisão

- Medir no corpus quantos projetos usam URL, quantos usam cada família e quantos exigem TLS sem aceitar uma CA.
- Rever se o padrão de nomes cobre o que aparece; acrescentar famílias com evidência, nunca por palpite.

## Verificação (22/11/2026)

- **Análise:** testes da detecção de família (`POSTGRES_`, `PG`, `DB_` e variações de sufixo), de que a URL vence, de que sem host e senha cai na URL e de que a CA é reconhecida. No clone-tabnews real, a análise devolveu a família `POSTGRES` com `POSTGRES_CA`.
- **Store e API:** escolha em partes grava só as variáveis dos papéis informados (a CA nunca é gravada), recusa e **não grava nada** se qualquer uma já tem valor digitado, e apagar o serviço leva todas as vinculadas; a API valida papéis, nomes duplicados, nomes da plataforma e estilos misturados, e a senha não aparece em resposta, histórico nem log.
- **Docker (banco de verdade):** o banco com TLS aceita `sslmode=verify-full` pelo nome do contêiner com a CA entregue, recusa `verify-full` sem ela e continua aceitando conexão sem TLS; o arquivo da CA entra no contêiner antes de iniciar, a leitura recusa contêiner de outro projeto, e o que o usuário definiu em `NODE_EXTRA_CA_CERTS` não é trocado.
- **Pilha completa em contêineres** (worker pelo `docker-socket-proxy`, que deixa passar a cópia de arquivos com `POST=1`): o **clone-tabnews** real, com `POSTGRES_*` e `ssl`, subiu e respondeu `/api/v1/status` consultando o banco gerenciado; no banco, as conexões da aplicação aparecem como **TLSv1.3**.
- **Bun:** `Bun.SQL` com verificação ligada conecta com `NODE_EXTRA_CA_CERTS` e falha com `self signed certificate` sem ele. Observação: com `tls: true` o Bun **não verifica** o certificado, então conecta nos dois casos.
- **Painel:** a pergunta mostra o estilo detectado com o nome editável de cada variável; a aba "Banco" mostra as variáveis vinculadas, o TLS e, nos bancos antigos, que não têm TLS.

## Limites encontrados

- Um deploy do clone-tabnews exige respostas que não são do banco (comando de início e porta; não há `scripts.start` nem `scripts.build`); isso é da análise de comandos, não desta decisão.
