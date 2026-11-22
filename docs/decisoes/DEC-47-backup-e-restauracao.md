# DEC-47 — Backup e restauração: da plataforma e dos dados de cada projeto

> **Estado:** respostas do autor registradas em 22/11/2026 (ao fim). **Parte A implementada e na VM**; **parte B implementada** (verificação no fim; ainda não está na VM); C segue depois, com a sua subida na VM.
>
> **Escopo:** (A) cópia automática e restauração do banco interno do Cobogó, com a chave tratada à parte; (B) cópia, download e exclusão dos **dados de um projeto** (volumes e PostgreSQL gerenciado) pelo painel; (C) restauração dos dados de um projeto a partir de uma cópia que está na VM.
>
> **Relaciona-se com:** issue #28 (e #6, #7, #9, #16), DEC-33 (roles), DEC-39 (chave e `rotate-secrets`), DEC-41 (volumes), DEC-42 e 44 (banco gerenciado), DEC-45 (comandos e a senha de novo), RNF-11, RNF-14, RN-10, RN-13.

## Contexto

Hoje a única proteção dos dados é um `pg_dump` que eu rodo **à mão** antes de cada atualização, e que fica na própria VM. Os dados das aplicações (volumes, bancos gerenciados) **não têm cópia nenhuma**: o painel avisa "sem backup (issue #28), perder o disco perde os dados". E o disco é um só: 40 GiB, 24 GiB livres, dentro de uma VM.

Os números cabem folgados: o banco do Cobogó tem 27 MB, e os volumes de aplicações de estudo são de MiB. O desafio aqui não é espaço nem desempenho, e sim **consistência** (copiar o arquivo de um PostgreSQL ou de um SQLite em uso pode gerar uma cópia que não abre), **a chave** (o dump tem os segredos cifrados; sem a chave eles não voltam, e **junto** da chave deixariam de ser protegidos) e **não apagar nada sem querer** ao restaurar.

## Decisão

### A. Banco interno e a chave

1. **Um serviço `backup` na pilha** (a imagem do próprio PostgreSQL, que traz `pg_dump`), que **todo dia às 03:00** gera um dump consistente do banco do Cobogó (`pg_dump` em formato custom, uma transação só, sem travar ninguém) para o volume `cobogo_backups`, com a soma SHA-256 ao lado. Conecta com um **role próprio, `cobogo_backup`, só de leitura** (`pg_read_all_data`; não cria, não altera, não apaga). O dono do esquema não é usado, para manter a separação da DEC-33. Retenção: por padrão os **últimos 7 dias e as 4 últimas semanas** (domingos), **configurável** nas configurações do Cobogó (`backup_keep_daily`, `backup_keep_weekly`, e `backup_project_keep` para os projetos; o serviço lê os valores do banco a cada execução, pelo mesmo role de leitura); o que passa disso o serviço apaga, e **só** o que ele mesmo criou, com o padrão do nome.
2. **`deploy/update.sh`** substitui o meu ritual manual: faz um backup nomeado (`pre-<commit>`), puxa o código e sobe a pilha; se o backup falhar, não atualiza.
3. **A chave nunca vai no mesmo arquivo, e só uma coisa do `.env` precisa ser guardada.** O dump leva os segredos **cifrados**. O `deploy/.env` tem dois tipos de valor: os que **se refazem** (as senhas dos roles do banco, o token das rotas do Traefik, a senha do PostgreSQL: nenhum dado depende deles, porque o dump não leva senha de role e o script de roles as define de novo; o token do túnel se pega de novo na Cloudflare; a URL e o domínio são configuração) e **a `COBOGO_SECRET_KEY`** (e as de rotação), que cifra as variáveis dos projetos e **não se refaz**: uma chave nova deixa as variáveis do dump ilegíveis (as pessoas teriam de digitá-las de novo). Por isso o `restore.sh` **gera um `.env` novo, com todas as senhas e tokens recriados**, e só pede a chave de antes; o roteiro manda guardar **só essa linha** (ou as duas, se há rotação), em um lugar à parte e fora da VM, nunca junto do dump. Sem a chave de antes, o `restore.sh` avisa, restaura do mesmo jeito com uma chave nova, e o `rotate-secrets --verify` lista quais variáveis ficaram ilegíveis, para a pessoa as digitar de novo. O `--verify` **abre toda variável cifrada** com a chave carregada e sai com código 3 se alguma não abrir: é a prova de que uma restauração serve. (O `--check` que já existia só **conta** variáveis por versão de chave e não decifra nada, então não serve de prova; foi por isso que o `--verify` nasceu.)
4. **Restaurar** é um script, `deploy/restore.sh <dump>`, para uma VM limpa ou a existente: sobe só o PostgreSQL, restaura, roda o `migrate` (que traz um dump antigo até o esquema atual; com um dump de esquema **mais novo** que o código, a **API e o worker recusam subir** com "o banco é mais novo que este binário") e confere com `rotate-secrets --verify`. **Nunca apaga volumes de aplicações**: eles são reconciliados pelos labels, como na DEC-41.
5. **O painel diz a verdade sobre o backup.** Um cartão na visão geral mostra o último backup da plataforma (quando, tamanho, quantos há) e **avisa se o último tem mais de 36 horas** ou se o serviço não está respondendo. O estado vem de um arquivo de status que o serviço escreve; a API o lê.
6. **A cópia fora da VM é o download pelo navegador** (decisão do autor): o administrador **baixa os backups da plataforma pelo painel**, com a senha digitada de novo (item 9). O roteiro cita também `rsync`/`scp` para outra máquina da casa, e o Cobogó **não empurra** nada sozinho por ora. **Não promete** proteção contra a perda da VM ou do disco: uma cópia na mesma VM protege de erro, não de desastre. Isso é dito no painel e no roteiro.

### B. Dados de um projeto (volumes e banco gerenciado)

7. **"Fazer backup agora", por projeto**, na aba "Dados". O pedido vira uma operação do worker, que **não copia arquivos do volume em uso**:
   - **PostgreSQL gerenciado:** um contêiner descartável, na rede do projeto, roda `pg_dump` (formato custom) com a senha entregue por ambiente e grava no volume de backups. É consistente por construção.
   - **Volumes de aplicação:** um contêiner descartável monta o volume **somente leitura** e grava um `tar.gz`. Como um SQLite em uso pode estar no meio de uma escrita, há a escolha **"pausar a aplicação durante o backup"**, a mesma da DEC-45: **desligada por padrão**, com o aviso de que sem ela a cópia de um arquivo em uso pode não abrir.
   - Cada backup é uma pasta `projects/<projeto>/<data>/` com os arquivos, as somas SHA-256 e um `manifest.json` (projeto, caminhos, tamanhos, versão do esquema, se a aplicação foi pausada). **Não leva variáveis de ambiente**: elas estão no banco interno (A), cifradas.
8. **Limites.** No máximo **3 backups por projeto** por padrão, configurável (`backup_project_keep`; o mais antigo sai quando entra um além do limite) e **um backup por vez** na máquina. O pedido é **recusado** se, depois dele, sobrariam menos de 5 GiB livres no disco. O tamanho esperado vem do que a varredura já mede (DEC-41).
9. **Baixar, e quem baixa o quê.** **Cada dono baixa os backups dos seus projetos**: a API confere o dono em toda rota de backup, como em todo o resto, então, quando houver mais de um usuário, cada um vê e baixa só o que é seu. **Os backups da plataforma** (o banco interno, que tem os segredos cifrados de todos) são **só do administrador**, e também se baixam pelo painel, com a senha de novo. `GET /projects/{id}/backups/{bid}/download` entrega o backup como um `.tar` montado na hora, em fluxo, a partir do volume de backups que a API monta **somente para leitura**; a API não ganha acesso ao Docker nem ao volume dos projetos. Exige **a senha digitada de novo** (DEC-45), porque os dados de um projeto não são cifrados e podem ter dado de gente. A soma SHA-256 vai no cabeçalho. Subir arquivo **não** existe nesta versão (o túnel da Cloudflare limita o envio a 100 MB, e esse é o caso do autor): para levar um backup a uma VM nova, copia-se o arquivo para a pasta de backups. **Pendência combinada com o autor:** estudar depois como subir backups maiores que o limite (envio em partes, ou pela rede local).
10. **Antes de apagar.** Na exclusão do projeto (DEC-38) e na do banco gerenciado (DEC-42), o painel oferece **"fazer backup antes de apagar"** e só habilita a exclusão depois que um backup bom existe ou a pessoa escolhe apagar sem. A pergunta que o autor fez na DEC-41. **Excluir o projeto apaga também os backups dele** (o worker os recolhe): dado de um projeto que se foi não fica para trás sem dono, e o painel avisa na exclusão que se baixe antes.

### C. Restaurar os dados de um projeto

11. **"Restaurar", só de um backup que está na VM**, com a confirmação por **digitar o nome do projeto** e a senha de novo. Antes de tocar em qualquer coisa, o worker faz **um backup automático do estado atual** (`antes-de-restaurar`), que conta nos 3 e não é apagado por ele. Depois: pausa a aplicação, restaura cada volume e o banco, inicia a aplicação e espera a prontidão. Se qualquer passo falhar, **nada é apagado**: o backup de segurança existe e a aplicação volta como estava (ou continua pausada, dizendo por quê).
12. **Restaurar não apaga volumes nem cria o que não existe por conta própria.** O volume restaurado é o **que o projeto já declara**; um caminho do backup que o projeto não declara mais é listado e **ignorado**, com o aviso. O banco gerenciado é restaurado com `pg_restore --clean --if-exists` **só se o projeto tem um banco gerenciado** (senão, o painel diz que há um dump e deixa a escolha de criar o banco).
13. **Compatibilidade.** Um backup guarda a versão do esquema do Cobogó e a do PostgreSQL gerenciado (16). Um dump de **outra versão maior** do PostgreSQL é recusado; o do banco interno segue a regra do item 4.

## Alternativas consideradas

- **Snapshot da VM no Proxmox:** protege de desastre, mas o snapshot de uma VM com PostgreSQL ligado **não prova consistência** (a própria issue diz); fica como complemento do dono, não como o backup do Cobogó.
- **Copiar o diretório de dados do PostgreSQL (`rsync` do volume):** rápido e **inconsistente** com o banco no ar. Descartado.
- **Backup contínuo (WAL, `pgBackRest`, `wal-g`):** é a resposta certa para banco grande e para perder segundos de dado; aqui é um serviço a mais e muita operação para 27 MB. Reabrir se o volume ou a exigência de perda crescerem.
- **Ferramenta pronta (restic, Borg):** boa para a cópia **fora** da VM, com criptografia e deduplicação. Entra como o jeito recomendado do dono guardar fora (o roteiro cita), não como parte do Cobogó.
- **Incluir a chave no backup, cifrada com uma senha:** reintroduz o problema de uma senha a guardar e deixa chave e dados no mesmo lugar. Descartado: a separação é o ponto.
- **Subir backups pelo painel:** esbarra no limite de 100 MB do túnel e abre um caminho de arquivo grande para a API. Descartado nesta versão.

## O que isto **não** resolve (dito, não escondido)

- **Perda do disco ou da VM** sem uma cópia fora dela: as cópias locais somem junto. Só a cópia que o dono leva para fora protege (itens 6 e 9).
- **Perda de dados entre dois backups** (até 24 horas no banco interno): não há recuperação ponto a ponto.
- **A cópia de um volume com a aplicação no ar** pode não ser consistente (item 7); pausar resolve, com o custo de o projeto sair do ar.
- **A chave perdida** não tem recuperação: os segredos cifrados ficam ilegíveis (as variáveis têm de ser digitadas de novo).
- **Backups com dados de pessoas** ficam na VM sem cifra; quem tem acesso à VM os lê. Cifrar em repouso é uma evolução (junto da cópia fora da VM).

## Ordem de implementação

1. **A** (serviço, role, retenção, `update.sh`, `restore.sh`, cartão de estado, restauração provada numa pilha limpa, com a chave certa e com a errada);
2. **B** (operação do worker, volume de backups, API de listar, criar, baixar e apagar, aba, "antes de apagar");
3. **C** (restaurar), cada uma comitada e posta na VM separadamente.

## Critérios de revisão

- Medir o tempo e o tamanho reais dos backups na VM depois de uma semana; ajustar a retenção.
- Rever a perda aceitável (24 h) e a cópia fora da VM quando houver dado que dói perder.
- Reavaliar cifra em repouso e subir backup quando o limite do túnel deixar de ser problema.

## Decisões do autor (22/11/2026)

1. **Cópia fora da VM:** o download pelo navegador (backups da plataforma pelo administrador, e os de cada projeto pelo dono). Sem empurrar sozinho por ora.
2. **Restaurar pelo painel (parte C):** sim, nesta rodada, por último na ordem.
3. **Pausar a aplicação durante o backup:** desligado por padrão, com o aviso.
4. **Retenção:** 7 dias e 4 semanas na plataforma e 3 por projeto, **configurável**.
5. **Chaves do `.env`:** recriar tudo o que se refaz (o `restore.sh` gera um `.env` novo) e guardar à parte só a chave dos segredos (item 3).
6. **Upload de backup grande pelo túnel da Cloudflare:** olhar depois (pendência no item 9).

## Verificação da parte A (22/11/2026)

- **Pacote `backup`:** nome e rótulo do arquivo (e a recusa de qualquer caminho ou outro arquivo), a retenção (o mais novo de cada dia nos últimos N, de cada semana nas últimas M, os 10 rótulos mais novos, e **o mais novo de todos nunca sai**), o estado e o aviso de atraso (36 h), e a execução: grava em arquivo temporário, confere que abre, calcula a soma, põe no lugar de uma vez e só **depois** aplica a retenção; um dump que falha, sai vazio ou não abre não deixa nada, nem temporário, e a retenção só apaga nome de backup válido.
- **Serviço, em contêiner de verdade:** o `pg_dump` com o role `cobogo_backup` (`pg_read_all_data`) fez o dump, o `pg_restore --list` o aceitou (21 tabelas) e a soma bateu; ao subir sem backup recente o serviço fez um na hora; `run --rm backup once <rótulo>` fez o pedido manual; a retenção **mudada nas configurações** (`backup_keep_daily`) foi lida na execução seguinte; valor fora da faixa é recusado pela API.
- **API:** o estado, os arquivos e o aviso de atraso; o download só com a senha digitada de novo (403 sem ela, e a janela é da sessão), com `Content-Length` e a soma em `X-Content-SHA256` iguais às do arquivo baixado; nome que não é de backup, soma, `..` e **link simbólico** dão 404. O prazo de escrita da API (60 s) é renovado a cada pedaço, para um arquivo grande por um enlace lento não ser cortado.
- **Painel:** a página "Backups" (estado, arquivos, senha de novo, retenção editável), o ponto vermelho na navegação e o aviso na visão geral quando o último backup bom passa de 36 horas.
- **Restauração fim a fim, numa pilha limpa** (volumes e `.env` apagados): com a chave certa, o `restore.sh` gerou um `.env` novo com **todas as senhas e tokens recriados**, restaurou, subiu a pilha e o `rotate-secrets --verify` abriu as 2 variáveis; o administrador entrou com a **mesma senha** de antes, o projeto e as variáveis estavam lá e o serviço de backup já fez o primeiro backup da pilha nova. **Sem a chave**, o script avisou, restaurou com uma chave nova e saiu com código 3 listando `restaura-me/MEU_SEGREDO` e `restaura-me/OUTRA_CHAVE` como ilegíveis. Com o banco já com dados, **recusou** sem `--force`; um arquivo com a soma errada foi recusado antes de tocar em qualquer coisa; um banco com migração de esquema futuro faz a API recusar subir.

## Limites da parte A

- O backup é **diário**: até 24 horas de dados do banco interno se perdem entre dois.
- As cópias ficam **no mesmo disco**; só o download pelo painel (ou um `scp`) as leva para fora.
- O `update.sh` fica por provar na VM: aqui só se verificou a sintaxe e o `backup once`, que ele chama; o `git pull` não foi executado na máquina de desenvolvimento.
- Numa VM que já existe, o role `cobogo_backup` e a senha dele precisam ser criados uma vez à mão (o script de roles não roda sozinho de novo), como foi com o coletor; está no roteiro.
- O painel só tem um usuário (o administrador); quando houver mais de um, a rota dos backups da plataforma passa a exigir o papel de administrador.

## Verificação da parte B (22/11/2026)

- **Arquivos e retenção (unidade):** os nomes dos arquivos de volume derivam do caminho (com um sufixo de soma contra colisão) e só os nomes fechados entram no `.tar`; a retenção por projeto tira os bons mais antigos, **nunca o `before_restore`** (conta no limite, não sai), e não conta falhos, em andamento nem os já marcados; o `.tar` de download sai em fluxo, com o `manifest.json` primeiro, **sem** arquivo estranho nem link simbólico, e o `Content-Length` é exato.
- **Store e privilégios:** a API só cria o pedido e marca `delete_requested`; o worker conduz o estado; um por vez por projeto (índice parcial no banco); só o dono; os backups de um projeto excluído viram "a apagar".
- **Docker (contêiner de verdade):** o `tar` lê o volume **somente leitura** (a fonte fica intocada), entra arquivo de outro dono com permissão fechada (`CAP_DAC_READ_SEARCH`, e só ela), o contêiner só enxerga a **subpasta** do próprio backup (a vizinha não aparece), o prazo curto mata a cópia, e uma falha devolve o motivo sem deixar contêiner. **Um volume que não existe é recusado** (o Docker o criaria vazio e o backup pareceria bom), e o de **outro projeto** também.
- **Worker (fim a fim, no worker de verdade):** um volume com arquivo fechado e um PostgreSQL gerenciado com dados saíram numa pasta do volume de backups, com `manifest.json` e somas que **batem**; o dump abre (`pg_restore --list`) e tem as linhas; um caminho declarado sem volume é dito no manifest e **não derruba**; a retenção marcou o mais antigo e o worker apagou os arquivos; com o **banco parado, o backup inteiro falhou e não deixou pasta** (nenhum backup incompleto parecendo bom).
- **Pilha completa em contêineres, com um deploy real:** um backup **com pausa** deixou o projeto `stopped` durante a cópia e `running` depois; o download (403 sem a senha de novo; com ela, um `.tar` de 6 656 bytes, igual ao `Content-Length`) trouxe o `manifest.json`, `sha256sums.txt` (que o `shasum -c` conferiu: **OK**), o `.tar.gz` do volume (`notas.txt` e `sub/a.txt`) e o `database.dump` com as 3 linhas; **excluir o projeto levou os backups dele** e deixou o backup da plataforma.
- **O que a pilha real pegou:** o worker tentava `chmod` na pasta `projects`, que é do serviço de backup (outro usuário): "operation not permitted". Agora ele só abre o que criou, e um teste cobre isso.
- **Painel:** a aba "Dados" tem o cartão de backups (fazer, pausar, lista com o que guardou, baixar com a senha de novo, apagar com confirmação), e a exclusão do projeto e a do banco mostram a trava: sem backup bom, "Fazer backup agora" ou "Apagar sem backup"; a trava vira verde quando o backup termina.

## Limites da parte B

- Dois contêineres no mesmo volume (a aplicação e o backup) é a escolha padrão do autor: o `tar` de um arquivo em uso (SQLite) pode não abrir; a pausa resolve.
- Os dados dos backups **não são cifrados** na VM.
- Backup de projeto não leva as variáveis (estão no backup da plataforma) nem o histórico de deploys.
- O backup copia os volumes **declarados**; um caminho que a aplicação grava e que não está declarado não tem volume e não entra.
