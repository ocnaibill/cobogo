# DEC-45 — Comandos pontuais no ambiente do projeto

> **Estado:** aprovada pelo autor em 22/11/2026, com as três respostas abaixo, e **implementada** (a verificação está no fim); ainda não está na VM.
>
> **Escopo:** rodar um comando de uma vez (`bun run reindex`, `npm run migrations:up`, um script de seed) no ambiente de um projeto, a partir do painel, sem abrir uma porta para quem não deveria.
>
> **Relaciona-se com:** DEC-19 e DEC-32 (o proxy do Docker e `EXEC=0`), DEC-33 (API sem Docker, worker sem escrita de texto cifrado), DEC-38 (fila de operações), DEC-39 (segredos), DEC-41 (volumes), DEC-42/44 (banco), RF-18, issue #38.

## Contexto

O caderno precisa rodar `bun run reindex` depois de um deploy; um app com banco novo precisa rodar migrações (o Cobogó não as roda, RN-13); muitos projetos de estudo têm um `seed`. Hoje não há como: o contêiner da aplicação só roda o comando de início.

Dar um comando a quem usa o painel é, por natureza, dar execução de código. A pergunta de segurança certa não é "dá para impedir qualquer dano" (não dá, o comando é código), e sim **"o que esse comando alcança que o código do repositório já não alcance, e o que muda para quem rouba a sessão do painel?"**

## O que já é verdade (e por que ajuda)

- O código do repositório **já executa** na nossa máquina, no contêiner da aplicação: sem capacidades (`CapDrop ALL`), sem ganho de privilégio, usuário não-root da imagem (`USER node`/`bun`), limites de memória, CPU e processos, só na rede do projeto, nenhum bind mount do host, e o worker só fala com o Docker por um proxy que filtra rotas (DEC-32). Um comando pontual **no mesmo contêiner endurecido** não acrescenta nada à superfície de ataque do que o deploy já aceitou.
- A API **não tem acesso ao Docker** (DEC-33): ela só grava um pedido; quem executa é o worker.
- O proxy tem `EXEC=0`: ninguém, nem o worker, entra num contêiner em execução.

## Decisão

1. **Um comando é um contêiner novo e descartável, nunca um `exec`.** O worker cria, a partir da **imagem da release ativa** (o código já construído), um contêiner com o comando, roda até terminar e o remove. `EXEC` continua `0`: abrir `exec` no proxy daria entrada em **qualquer** contêiner da máquina, inclusive os da plataforma, e é a decisão que não se desfaz depois. Criar contêiner o worker já pode, com os mesmos limites.
2. **Mesma jaula da aplicação, só que mais apertada.** Mesma imagem e usuário, `CapDrop ALL`, `no-new-privileges`, rede **só do projeto** (alcança o banco gerenciado, não alcança o ingress nem a rede de controle), **sem porta publicada**, sem bind mount, labels do Cobogó e do projeto (`cobogo.role=command`) e limites próprios: 512 MiB (ou o limite do projeto, o menor), 1 CPU, 128 processos. As variáveis entregues são as da aplicação (inclusive o banco e a CA da DEC-44), porque o comando existe para usá-las.
3. **Prazo e saída limitados.** Tempo máximo de 10 min (padrão 5), com `SIGTERM` e depois `SIGKILL`; saída guardada limitada (cabeça e cauda, 256 KiB no total); **um comando por vez por projeto** e no máximo três na máquina; o comando roda sem TTY e sem entrada do teclado (não interativo). Um botão cancela (o worker mata o contêiner).
4. **Quem pode pedir, e como.** Só o dono do projeto. O pedido exige **reautenticação**: a senha digitada de novo, que vale por uma **janela de 10 min** (decidido pelo autor) para os comandos seguintes, na própria sessão; a janela some ao sair e não passa para outra sessão. Motivo: hoje o painel **nunca devolve um valor de segredo**, e isso protege quem tem a sessão roubada; um comando pode imprimir o ambiente, então sem o passo extra a proteção deixaria de existir. Com ele, um cookie roubado não vira execução. A checagem de origem e o limite de tentativas que já existem seguem valendo.
5. **Quem executa e o que cada papel vê (DEC-33).** A API grava o pedido na fila de operações (`run_command`, DEC-38) e uma linha em `project_commands`; o worker executa e grava o desfecho (estado, código de saída, duração, saída). A API só lê. Nada de Docker na API, nada de texto cifrado novo para o worker além do que a aplicação já recebe ao subir.
6. **Auditoria e redação.** Cada pedido entra no log de auditoria (quem, projeto, a referência do comando e se foi script ou texto livre) e a recusa por falta da janela também. O **texto do comando** e a saída ficam no registro do próprio comando (`project_commands`), que é o histórico do que foi rodado. A API **não conhece os valores das variáveis** (só o worker as decifra, DEC-33), então não pode redigir o texto no pedido: quem o redige é o worker, ao concluir, trocando os valores de variável do projeto por `[REDIGIDO]` no texto e na saída (comparação exata, como nos logs de falha; também na saída parcial que o painel mostra enquanto roda). Isso **reduz** vazamento, não o garante: um comando que codifica o valor antes de imprimir passa. Por isso o item 4. Um comando que o worker perde (reinício) fica como "interrompido" com o texto digitado, sem redação.
7. **Volumes (DEC-41).** O comando monta os mesmos volumes da aplicação, porque é para isso que ele serve (reindexar, migrar, popular). Duas escritas ao mesmo tempo no mesmo volume é o risco que a DEC-41 evitou na troca de release; aqui a pessoa escolhe: **rodar com a aplicação no ar** (padrão, decidido pelo autor, avisando que um arquivo como o SQLite pode ser disputado) ou **pausar a aplicação durante o comando** (para, roda, volta a iniciar, reaproveitando as operações da DEC-40).
8. **Sugestões, não só texto livre.** A análise já lê `package.json`. O painel lista os scripts que **não** são o comando de início nem de desenvolvimento (`reindex`, `migrations:up`, `seed`) como botões de um clique; o campo livre existe, atrás de um "Outro comando". Ambos passam pelo mesmo caminho e pelas mesmas garantias.
9. **Sem ambiente interativo agora.** Um terminal no navegador (sessão longa, WebSocket, TTY, entrada do teclado) é outra decisão, com outro risco (sessão aberta, ocioso, difícil de limitar e de auditar). Ele pode vir depois **sobre este mesmo contêiner descartável** (por `attach`, que não precisa de `EXEC`), nunca por `exec` no contêiner da aplicação.
10. **Não vale para site estático** (a imagem é de um servidor web, sem o código nem o runtime do projeto) nem para projeto sem release.
11. **Limpeza.** O pedido que o worker perde (reinício no meio) deixa um contêiner com o label de comando; a varredura (DEC-38) remove o que tem esse label e está há mais tempo que o prazo máximo, e marca o comando como interrompido.

## O que isto **não** resolve (dito, não escondido)

- **Fuga do contêiner por falha do kernel ou do runtime (issue #41).** A defesa é a que a aplicação já tem (sem capacidades, sem privilégios, perfil de seccomp padrão do Docker, processos limitados) e manter o host atualizado. Para vários donos desconfiando uns dos outros o passo seguinte seria um runtime isolado (gVisor, Sysbox) ou usuário por namespace; para um dono, o custo não se paga hoje.
- **Saída de rede (issue #42).** O comando, como a aplicação, pode abrir conexões para a internet e para a rede local. Restringir a saída é uma decisão **da plataforma inteira**, anterior a esta, e fica registrada como lacuna, não como parte do comando.
- **O dono vê o que o ambiente tem.** Com reautenticação o dono pode, de propósito, imprimir os próprios segredos. Ele já os digitou; o que se protege é a sessão roubada.
- **Código do repositório que faz mal ao rodar o comando** (um `postinstall`, um script malicioso) tem o mesmo alcance da aplicação, não maior.

## Alternativas consideradas

- **`exec` no contêiner da aplicação** (`EXEC=1`): sem criar contêiner, mas dá `exec` em todos os contêineres, inclusive o banco do Cobogó e os proxies. Descartada: é a porta que a DEC-32 fechou de propósito.
- **Comandos fixos no repositório** (um bloco `cobogo.yml` rodado a cada deploy, estilo release phase): resolve migração, não o "reindexar quando eu quiser". Pode vir depois, e usa este mesmo mecanismo.
- **Só botões de scripts do `package.json`, sem campo livre:** mais seguro na aparência, igual na realidade (o script é código do repositório e o campo livre roda no mesmo contêiner), e inutiliza o caso de uso de um comando que não está nos scripts. Fica o campo livre, com o mesmo passo de senha.
- **Terminal interativo já:** grande, e a parte que mais dá medo. Adiada (item 9).

## Decisões do autor (22/11/2026)

1. **Reautenticação:** a janela de 10 min, não a cada comando.
2. **Volumes:** rodar com a aplicação no ar por padrão, com a opção de pausá-la.
3. **Escopo do MVP:** os botões dos scripts **e** o campo de comando livre.
4. **Lacunas registradas como issues** (fora desta decisão): isolamento mais forte dos contêineres (#41) e restrição da saída de rede (#42).

## Critérios de revisão

- Medir quantos comandos pontuais são scripts do `package.json` e quantos são texto livre; se o livre quase não é usado, tirar.
- Rever quando houver um segundo dono na mesma máquina: aí o runtime isolado deixa de ser opcional.

## Verificação (22/11/2026)

- **Store e esquema:** a vaga é única por projeto (índice parcial no banco), só o dono cria e lê, o worker conduz o estado, um comando concluído não muda mais, o worker que cai deixa "interrompido"; a janela de senha vive na sessão e outra sessão não a tem. A matriz de privilégios dá à API só o `INSERT`/`SELECT` e o `UPDATE` da coluna `cancel_requested`.
- **API:** sem a janela, `403 reauth_required` antes de qualquer outra conferência (e auditado); a senha errada não abre a janela e conta no limite de tentativas; validação de script oferecido (os de início, build, desenvolvimento, teste e ciclo de vida não são oferecidos), tamanho, prazo e um por vez; sem release ou em site estático, `commands_unavailable`. O OpenAPI e o teste de paridade com as rotas passam.
- **Docker (contêiner de verdade):** o contêiner de comando sai com `CapDrop ALL`, `no-new-privileges`, sem porta, sem bind mount, só a rede do projeto, limites de memória, CPU e processos, e os labels do Cobogó; a saída enorme é cortada em cabeça e cauda; parar mata um comando que não termina; só se age em contêiner de comando (a aplicação é recusada) e os órfãos saem.
- **Worker (fim a fim, no worker de verdade):** falha com código de saída; o valor da variável sai redigido da saída **e** do texto do comando; sucesso; prazo; cancelar; pausar uma aplicação que não dá para parar não roda o comando; nenhum contêiner sobra.
- **Pilha completa em contêineres, com um deploy real** (`heroku/node-js-getting-started`; worker pelo `docker-socket-proxy`, que continua com `EXEC=0`): o comando rodou como `uid=1000(node)` com `CapEff` zerado, `PORT` e variáveis do projeto no ambiente e o valor do segredo redigido; um volume novo nasce com a posse do usuário da imagem e um segundo comando enxerga o que o primeiro escreveu; `command_active` com um comando rodando; cancelar um comando em andamento sai com 143 e a saída parcial; o prazo de 5 s encerra um `sleep 40`; 30 mil linhas ficam em 256 KiB (começo e fim); com **pausar a aplicação** o projeto ficou `stopped` durante o comando e voltou a `running`, respondendo 200, depois.
- **Painel:** a aba "Comandos" pede a senha numa sessão nova, mostra a contagem da janela, aceita o comando livre, mostra "na fila", "rodando" e o botão Cancelar, e lista o histórico com a saída redigida. Os botões dos scripts aparecem quando o `package.json` tem scripts pontuais (testado na API e em teste de unidade; o projeto do teste real não tinha nenhum).

## Limites encontrados

- Dois contêineres no mesmo volume (a aplicação no ar e o comando) é a escolha padrão do autor: um arquivo SQLite pode ser disputado. A opção de pausar existe e foi medida.
- Um deploy enquanto um comando roda não espera por ele: a release que o comando usa é a que estava ativa quando começou.
- O texto de um comando perdido por reinício do worker não é redigido (ver item 6).
