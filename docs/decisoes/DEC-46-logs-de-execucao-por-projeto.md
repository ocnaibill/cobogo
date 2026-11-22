# DEC-46 — Logs de execução da aplicação e do banco, por projeto

> **Estado:** aprovada pelo autor em 22/11/2026, com as três respostas ao fim, e **implementada** (a verificação está no fim); ainda não está na VM.
>
> **Escopo:** ver, no painel, o que a aplicação (e o PostgreSQL gerenciado) imprimem **depois** que o deploy terminou: erros em produção, o que aconteceu antes de um reinício, o resultado de um pedido. O log do **build e do deploy** já existe (eventos do deploy) e não muda.
>
> **Relaciona-se com:** issue #12 (item "acesso a logs de runtime"), DEC-26 e DEC-35 (diagnóstico de falha no deploy), DEC-33 (quem decifra o quê), DEC-39 (segredos), DEC-42/44 (banco), DEC-43 (coletor), DEC-45 (comandos), RN-11, RF-07.

## Contexto

Hoje o painel mostra o log de um deploy (as etapas e a cauda do log quando falha), mas **nada do que a aplicação imprime enquanto roda**. Quando o caderno devolve um 500, ou o banco recusa uma conexão, a única saída é entrar na VM e rodar `docker logs`. Isso também reforça um erro de leitura comum: a pessoa vê "no ar" e acha que está tudo bem, sem ter onde olhar.

O Docker já guarda o stdout e o stderr de cada contêiner (`json-file`, 10 MiB × 3), mas o arquivo **some junto com o contêiner**: quando um deploy novo aposenta a release anterior, o log dela vai embora, justamente o que explicaria uma queda.

## O que limita o desenho

- **A API não tem acesso ao Docker** (DEC-33); só o worker e o coletor têm, cada um pelo seu proxy. Então o painel não pode "abrir um `docker logs`": precisa ler de um lugar que a API alcança, o banco.
- **Só o worker decifra as variáveis** (DEC-33). A redação dos valores secretos (RN-11) precisa dos valores; portanto a captura dos logs tem de ser **do worker**, não do coletor nem da API.
- O proxy do worker já libera a leitura de logs (`CONTAINERS=1`, a mesma rota que o diagnóstico de falha usa); **nenhuma permissão nova** no Docker.

## Decisão

1. **O worker captura os logs dos contêineres do Cobogó e os guarda, redigidos, no banco.** Um laço próprio (como o dos comandos da DEC-45), fora do laço dos deploys: a cada poucos segundos, lista os contêineres gerenciados com projeto (aplicações e bancos gerenciados; **não** os passageiros de comando, posse de volume e certificado), segue o log de cada um (`follow`, com carimbo de hora, a partir da última linha que já tem) e grava em lotes. Se o contêiner para e volta, o seguimento recomeça de onde parou; se o worker reinicia, recomeça do que o banco já tem. Um contêiner que ainda não tinha log guardado entra com as últimas 500 linhas dele.
2. **Cada linha vira uma linha da tabela `runtime_logs`:** projeto, o contêiner (e, nas aplicações, o deploy), a origem (`app` ou `postgres`), o fluxo (`stdout` ou `stderr`), a hora que o Docker registrou e o texto. Uma linha passa por `redact.Clean` (sem sequências ANSI nem caracteres de controle, UTF-8 válido) e **por `redact.Values` com os valores das variáveis do projeto**; o conjunto de valores é relido a cada minuto, então uma variável nova passa a ser redigida logo. Linha com mais de 8 KiB é cortada com um aviso. A redação **só cobre valores que o Cobogó conhece**: um segredo que a aplicação monte e imprima, ou que codifique antes, passa. O painel diz isso.
3. **Limites, para o log de um não encher o banco de todos.** Por contêiner, um teto de taxa (até 200 linhas por segundo, com rajada de 2 000); o que passa é descartado e **uma linha de aviso** diz quantas foram (`[N linhas descartadas pelo limite de taxa]`). Por projeto, a retenção guarda **as últimas 24 horas e no máximo 20 000 linhas** (o que vier primeiro), e uma varredura a cada minuto apaga o que passou. Um projeto excluído perde os logs na próxima varredura. O que o painel mostra diz quando houve corte ou expiração.
4. **A API lê, com paginação por cursor, e só o dono.** `GET /projects/{id}/logs` devolve até 500 linhas, em ordem, com `origin` (`app`, `postgres` ou todas), e dois cursores: `after=<id>` traz o que chegou depois (é o que o painel usa para acompanhar) e `before=<id>` traz as anteriores (para voltar no tempo). Sem cursor, as últimas 300. A resposta diz se há linhas mais antigas guardadas e o intervalo que a retenção cobre. **Consulta incremental por polling** a cada 2 s, como pede a issue #12: SSE e WebSockets ficam como evolução se o polling não bastar.
5. **O painel ganha a aba "Logs"**, ao lado de "Recursos": as linhas em fonte monoespaçada, `stderr` destacado, a hora local, o filtro por origem (aplicação, banco), uma busca de texto no que está carregado, "seguir o fim" (liga e desliga sozinho ao rolar) e um botão de copiar a seleção. Sem o painel aberto na aba, nada consulta.
6. **Para quem serve o dado.** Os logs são **o que a aplicação imprimiu**, só do dono do projeto, e **ficam guardados no banco do Cobogó**: o painel avisa que não se deve imprimir segredos nem dados pessoais, e que a redação é de melhor esforço. Nada disso entra em auditoria nem em métricas.
7. **O que fica de fora de propósito:** os logs de build (já existem), o log do Traefik e da plataforma (são do operador, não do projeto), exportar e baixar (entra na issue #28, junto do backup), alertas por padrão de texto e um visualizador com filtros avançados. O `docker logs` na VM segue valendo, e continua guardando o que o limite de taxa descartou.

## Alternativas consideradas

- **Ler o log do Docker sob demanda, sem guardar:** simples, mas a API não alcança o Docker (DEC-33); seria preciso um pedido e uma espera pelo worker a cada leitura, e o log sumiria com a release aposentada.
- **Capturar no coletor (DEC-43):** o coletor já fala com o Docker e tem o seu role, mas **não decifra variáveis**, então não consegue redigir segredos; dar a ele a chave desfaria a separação da DEC-33.
- **Redigir na leitura, pela API:** a API também não tem os valores. Guardar o texto bruto e esconder depois deixaria o segredo no banco.
- **Um agente de logs (Loki, Vector):** resolve volume e busca, mas é um serviço novo para operar numa VM de 6 GiB, para um dono e nove contêineres. Reabrir se o volume crescer.
- **Guardar arquivos em disco, só metadados no banco (DEC-10 proposta):** serve para volume grande; aqui o limite é de 20 000 linhas por projeto, que o PostgreSQL guarda sem esforço, e o ganho da consulta por cursor é grande. A DEC-10 era uma proposta; esta a substitui para logs de execução, e o limite acima é o gatilho para rever.

## Consequências e limites

- **Custo no banco:** com os 9 contêineres de hoje, o teto é de 20 000 linhas por projeto (na ordem de poucos MiB cada). O pior caso é uma aplicação muito falante, limitada por contêiner (taxa) e por projeto (linhas).
- **O que já foi impresso antes de a captura existir** só vem nas últimas 500 linhas que o Docker ainda guarda.
- Um log perdido por queda do worker **durante** uma rajada é recuperado do Docker na volta (a captura recomeça de onde parou), a menos que o Docker já tenha rodado o próprio arquivo.
- A hora é a do Docker, não a da aplicação; linhas com hora igual mantêm a ordem em que chegaram (o `id` desempata).
- Um `docker restart` mistura, na mesma aplicação, o fim de uma execução e o começo da outra; o painel marca a mudança de contêiner com uma linha divisória.

## Critérios de revisão

- Medir o volume real por projeto e o custo de gravação; se passar de alguns MiB por dia, rever os limites ou o armazenamento (arquivo ou agente).
- Ver quantas vezes o limite de taxa descarta linhas e se o painel avisa de modo claro.
- Reavaliar SSE se o polling a cada 2 s parecer lento para quem acompanha um deploy ou um comando.

## Decisões do autor (22/11/2026)

1. **Logs do PostgreSQL gerenciado:** incluídos (filtro "banco").
2. **Retenção:** 24 horas e 20 000 linhas por projeto, para ajustar pelo que medirmos.
3. **Polling de 2 s** na primeira versão. O autor perguntou por que não SSE já: o dado vem do banco e o worker grava em lotes de ~1 s, então o SSE ganharia menos de 1 s; custa soltar o `WriteTimeout` de 60 s da API só nessa rota, reconferir a sessão no meio do fluxo, sinal de vida contra o corte do túnel e um teto de conexões. O cursor `after` já é o `Last-Event-ID` do SSE, então a troca depois é um endpoint novo sobre o mesmo esquema.

## Verificação (22/11/2026)

- **Store:** o cursor por `id` (últimas, `before`, `after` com "há mais"), o filtro de origem, só o dono lê, e a retenção por idade, por quantidade por projeto e por projeto excluído.
- **Docker (contêiner de verdade):** o seguimento lê stdout e stderr com a hora do Docker (UTC, nanossegundos), retoma a partir de uma hora sem repetir, lê as últimas linhas, e recusa contêiner que não é de um projeto no Cobogó.
- **Worker (captura fim a fim):** um contêiner com `stderr`, um valor de variável impresso e uma rajada de 3 000 linhas chegou ao banco com a origem, o nome do contêiner e o deploy certos, o valor **redigido**, o `stderr` marcado, **o limite de taxa descartou e avisou quantas** (`[N linhas descartadas pelo limite de taxa]`), e religar o laço **não repetiu nenhuma linha**. Limpeza de ANSI, controle e UTF-8, e o corte de linha longa em fronteira de caractere, em teste de unidade.
- **Roles de verdade (DEC-33):** a pilha completa achou o que o teste com um usuário só não via: o worker não podia inserir porque faltava `USAGE` na sequência da chave (`runtime_logs_id_seq`). A matriz de privilégios ganhou as sequências, e um teste com os roles reais (worker grava e apaga, API só lê, API não grava nem apaga, a API marca a janela de senha da DEC-45 e o worker não mexe em sessão) passa a pegar isso; ele falha sem a correção.
- **Pilha completa em contêineres, com um deploy real** (`heroku/node-js-getting-started` com banco gerenciado): as linhas da aplicação e as do PostgreSQL (a inicialização, `stderr` do `LOG:`) apareceram com a origem certa; os contêineres de outros projetos da máquina foram capturados com as últimas 500 linhas, como previsto.
- **Painel:** a aba "Logs" trouxe as últimas linhas, acompanhou por polling (cinco pedidos à aplicação viraram cinco linhas novas em até 2 s, com "seguir o fim" ligado), o filtro "Banco" trocou a lista e escondeu o selo de origem.

## Limites encontrados

- Uma linha com mais de 16 KiB chega do Docker em pedaços, cada um com a sua hora; o texto pode trazer a hora do pedaço seguinte no meio. Como a linha é cortada em 8 KiB, não passa disso.
- Os logs de contêineres que o Cobogó não conhece como de um projeto no banco (por exemplo, de um ambiente de teste com outro banco) também são guardados, pelo `project_id` do label, e só saem pela retenção.
