# DEC-42 — PostgreSQL auxiliar por projeto: necessidade, escolha, criação e vínculo

> **Estado:** proposta discutida com o autor em 21/11/2026; **aprovada pelo autor em 21/11/2026 e implementada**.
>
> **Escopo:** como o Cobogó percebe que um projeto precisa de banco, como pergunta o destino antes de gastar o build, como cria e mantém um PostgreSQL por projeto e como o liga à aplicação (`DATABASE_URL`).
>
> **Relaciona-se com:** RF-12, RF-13, RN-05, RN-06, RN-10, RN-13, DEC-09, DEC-24, DEC-33, DEC-38, DEC-40, DEC-41, HEU-06 (motor de heurísticas §6.1), issues #6, #22, #28 e #38.

## Contexto

Hoje o estágio `PROVISIONING` do pipeline existe e está vazio (`services: []`). Um projeto que só funciona com banco faz o deploy chegar ao fim e a aplicação cair ao subir, com um erro de conexão. O autor pediu o contrário: o Cobogó deve **perceber a necessidade, avisar e ajudar antes do build**, sem que dependa de a pessoa lembrar de pedir um banco. A RN-05 impõe o limite: **um driver nas dependências é só uma pista; criar banco exige escolha explícita**. Esta DEC faz as duas coisas valerem juntas: a pergunta é obrigatória quando a evidência é forte, mas a criação só acontece depois da resposta.

## Decisão

1. **Detecção (análise estática, só Node e Bun, só PostgreSQL).** A análise passa a trazer `service_needs`, com a engine, o nível, a variável de conexão provável e as evidências (arquivo e linha), seguindo a HEU-06:
   - **`required` (forte):** um arquivo de **runtime** importa um driver de PostgreSQL (`pg`, `postgres`, `pg-promise`) ou o schema do Prisma declara `provider = "postgresql"` (HEU-06.1 a 06.3). Código de teste, de configuração e de ferramentas não conta.
   - **`possible` (fraco):** a dependência está no `package.json`, mas nenhum arquivo de runtime a usa (HEU-06.5: não se presume ativa nem inativa).
   - **Variável:** a primeira, nesta ordem, que o código referencia: `DATABASE_URL`, `POSTGRES_URL`, `DB_URL`. Se nenhuma for citada, propõe `DATABASE_URL` e a pessoa pode trocar o nome.
   - Outras engines (MySQL, MongoDB, Redis) **não são gerenciadas**; ficam fora desta entrega e, quando detectadas, o Cobogó só diz que a engine não é suportada e que o banco tem de ser externo.
2. **Pergunta antes do build (HEU-06.6).** Com `required`, sem intenção registrada e **sem valor já definido** para a variável, o deploy para em `NEEDS_INPUT` **logo depois da análise**, como a pendência da porta, com três escolhas:
   - **Criar um PostgreSQL do Cobogó e conectar** (`managed`);
   - **Usar um banco que já tenho** (`external`): a URL entra pelo fluxo de variáveis e segredos (DEC-39), nunca pela resposta da pendência;
   - **Não preciso de banco** (`none`).
   Quem já digitou um valor para a variável conectou o banco por conta própria: não há pergunta. A escolha fica registrada como **intenção vigente** do projeto e não é repetida a cada deploy (C13). Com `possible`, não há bloqueio: o painel mostra uma **sugestão** com as mesmas escolhas, no modelo das sugestões da DEC-39 e da DEC-41.
3. **O serviço gerenciado é um contêiner por projeto** (DEC-09): `postgres:16-alpine` fixado por digest, nome derivado do projeto (`cobogo-pg-<12 do id>`), **só na rede do projeto**, sem porta publicada, com os labels do Cobogó e do projeto, limite fixo de **256 MiB de memória, 0,5 CPU e 128 processos**, e uma verificação de saúde feita pelo próprio Docker (o worker não usa `exec`, DEC-32). Os dados ficam num **volume nomeado** do mesmo mecanismo da DEC-41 (`/var/lib/postgresql/data`), então ele aparece na aba de dados com o tamanho, entra na escolha de apagar dados ao excluir o projeto e vira "volume sem projeto" se o projeto sair.
4. **Criação idempotente, no estágio `PROVISIONING`.** O worker garante rede, volume e contêiner (cria o que falta, inicia o que está parado) e **espera o banco ficar saudável antes de a aplicação subir**. Retry, reinício do worker e novo deploy reaproveitam o que existe; nunca criam um segundo contêiner nem um segundo volume. O serviço **não** é tocado pela aposentadoria nem pela faxina de releases (DEC-38), e parar a aplicação (DEC-40) **não** para o banco.
5. **Credencial e vínculo (RF-13).** Ao escolher `managed`, a **API** gera a senha e grava a variável de conexão (`postgres://app:<senha>@cobogo-pg-<id>:5432/app`) como variável do projeto, cifrada como as demais, com a origem nova **`linked`**; o worker só a lê para criar o banco e entregá-la à aplicação. Isso mantém a regra da DEC-33 (a API escreve segredos, o worker só lê). A senha **nunca é devolvida** e **não é a do PostgreSQL interno do Cobogó** (RN-06). O vínculo guarda a identidade (projeto, engine, variável, contêiner, volume) numa tabela própria. **Nunca sobrescreve um valor digitado**: escolher `managed` com a variável já definida pela pessoa é recusado com a explicação; o caminho é remover o valor ou escolher `external`. Uma variável `linked` não é editada por `PUT`: desvincular é uma ação própria.
6. **Superfície.** `GET /projects/{id}/database` (necessidade com evidências, intenção, estado do serviço, tamanho e a sugestão), `POST /projects/{id}/database` com `{"choice": "managed|external|none", "variable": "DATABASE_URL"}` (responder a pendência reenfileira o mesmo deploy, como as respostas da DEC-24) e `DELETE /projects/{id}/database`, que **apaga o serviço e os dados**, com o nome do projeto como confirmação e como operação do worker (`delete_service`). No painel, uma aba **"Banco"** e o aviso na página do projeto; a variável vinculada aparece como "vinculada ao PostgreSQL do projeto".
7. **O que não é feito sozinho (RN-13).** O banco nasce **vazio**. Migração de esquema **não é inferida nem executada**: aplicações que criam as tabelas ao subir funcionam; as que exigem um comando de migração (`prisma migrate deploy`) precisam dele declarado, o que depende do estudo dos comandos no projeto (#38). Trocar a senha, subir a versão do PostgreSQL e restaurar um backup são operações explícitas, separadas e fora desta entrega.
8. **Testes de proteção.** A senha do banco não aparece em resposta da API, log, evento, análise, plano nem camada de imagem; a variável `linked` segue a rotação de chave da DEC-39; o banco não é alcançável de fora da rede do projeto; nenhum banco é criado sem a escolha `managed`.

## Alternativas consideradas

- **Criar o banco sozinho quando a evidência é forte:** poupa um clique, mas contraria a RN-05 e cria dados que a pessoa não pediu, com custo de memória e de proteção.
- **Só sugerir, sem bloquear:** é o que a pessoa esquece de ler; o deploy chega ao fim e a aplicação cai. O bloqueio só ocorre com evidência forte e sem valor definido.
- **Uma instância compartilhada entre projetos:** gasta menos memória, mas mistura credenciais e complica a exclusão e a limpeza (DEC-09).
- **O worker gerar a senha:** exigiria dar a ele escrita de texto cifrado, que a DEC-33 tirou de propósito.
- **Guardar a senha numa tabela própria:** duplicaria o cofre; reaproveitar a variável cifrada herda rotação de chave, auditoria e histórico.
- **Mais de uma variável de conexão (`PGHOST`, `PGUSER`...):** muitos projetos usam uma URL só; um mapeamento múltiplo fica para quando o corpus pedir.

## Consequências e limites

- Cada projeto com banco gerenciado consome cerca de 256 MiB a mais, mesmo com a aplicação parada. O número máximo de projetos com banco depende da medição do homelab.
- Sem backup (#28), a perda do disco da VM perde os dados do banco, como nos demais volumes.
- O limite de 256 MiB é fixo; uma configuração editável pode vir depois.
- A detecção é estática e erra para os dois lados: um banco usado por um caminho que a análise não vê não gera pergunta, e uma dependência morta gera só uma sugestão.
- Só PostgreSQL 16 e uma variável de URL. Outros bancos exigem banco externo.
- Excluir o projeto remove o contêiner do banco; os dados seguem a escolha da DEC-41 (apagar junto, ou ficar como volume sem projeto).

## Verificação

- **Testes:** a detecção (importação de `pg`, `postgres`, `pg-promise` e adaptadores do Drizzle; configuração de TypeORM, Sequelize e Knex; schema do Prisma com o nome da variável; só a dependência; e o que **não** conta: teste, `scripts/`, SQLite, MySQL, citação em texto); com PostgreSQL real, a escolha gravando a variável `linked` sem sobrescrever a que a pessoa digitou, a senha que não muda ao repetir a escolha, a recusa de trocar de escolha com banco gerenciado, a exclusão tirando a variável, o volume do serviço que não é "sem projeto" e os deploys à espera da pergunta voltando à fila; a API (400, 409 `variable_defined`, `managed_exists`, `variable_linked`, importação bloqueada, exclusão confirmada pelo nome, 401) e a senha ausente de respostas, histórico e log; o worker (a pergunta só com evidência forte, sem escolha e sem valor; "não preciso" e "gerenciado" não perguntam; "externo" sem valor lembra a URL); e, **com Docker real**, o banco criado uma vez, alcançável pela rede do projeto só com a senha certa, **sem porta publicada**, com 256 MiB e os labels do Cobogó, reaproveitado com os dados intactos, reiniciado se estiver parado e apagado com o volume.
- **Em execução** (API e worker locais, o worker atrás do `docker-socket-proxy` com a mesma configuração da VM; repositório público real que usa `pg`): o deploy **parou antes do build** na pergunta (nenhuma imagem, nenhum contêiner, nenhum banco criado); escolher "criar" **não criou nada** e reenfileirou o deploy, que seguiu até o provisionamento: o banco nasceu, ficou saudável e a aplicação recebeu a variável vinculada; um **novo deploy reaproveitou** o banco (`created: false`); apagar pela API removeu contêiner, volume e variável; o deploy seguinte **voltou a perguntar**. A senha não apareceu em nenhum log, resposta da API, `pg_dump` do Cobogó nem na imagem. No painel: a pergunta no deploy parado, a aba "Banco" e o aviso na página do projeto.

## O que a verificação real mostrou (limites)

O primeiro repositório real que testei (um Next.js no estilo curso.dev) **não usa `DATABASE_URL`**: monta a conexão com `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_DB` e `POSTGRES_PASSWORD`, e em produção **exige TLS** (`ssl: true`). Duas consequências, que a DEC já listava como limites e que o corpus confirmou logo:

- **Uma URL só.** O vínculo liga uma variável de URL. Um projeto com variáveis separadas recebe o banco criado, mas a aplicação não lê a URL e não conecta. Mapear uma família de variáveis (`POSTGRES_*`, `PG*`, `DB_*`) a partir do mesmo banco é o próximo passo natural.
- **Sem TLS.** O banco gerenciado não oferece TLS: está só na rede do projeto, sem porta pública. Uma aplicação que exige TLS em produção não conecta. Oferecer TLS ou uma forma de a aplicação desligá-lo fica para depois.

Os dois estão registrados em issues.

## Critérios de revisão

- Medir no corpus quantos projetos recebem a pergunta, quantas vezes ela está certa e quantos têm banco que a análise não viu.
- Rever o limite de memória e a política de versão do PostgreSQL com o uso real.
