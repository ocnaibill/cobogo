# DEC-33 — Roles do PostgreSQL e migrations em passo próprio

> **Estado:** decidida com o autor em 20/11/2026; **implementada** (itens 1 a 8). Parte 5d2.
>
> **Escopo:** quem conecta ao PostgreSQL e com que privilégios, como as migrations deixam de rodar dentro da API e do worker, e como os roles são criados. Concretiza a DEC-21 para a D0 (o role do coletor entra com o coletor).
>
> **Relaciona-se com:** DEC-21 (roles separados), DEC-13 (migrations aditivas), DEC-24 (variáveis cifradas), DEC-27, DEC-32, RNF-03.

## Contexto

Hoje a API e o worker conectam ao banco com o mesmo usuário, que é o dono dos objetos e roda as migrations. Uma falha de injeção de SQL na API daria então DDL e leitura de tudo, inclusive do texto cifrado das variáveis dos projetos, que só o worker precisa ler. A DEC-21 já previa roles separados; falta implementá-los.

## Decisão

**Identidades**

1. Quatro identidades, com um uso cada:
   - o **superusuário** do contêiner (`POSTGRES_USER`), usado **só** pelo script de criação inicial dos roles; a aplicação nunca conecta com ele;
   - `cobogo_owner`, dono do banco e do esquema, que roda as migrations e a concessão de privilégios e **só** é usado no passo de migração;
   - `cobogo_api` e `cobogo_worker`, que fazem apenas DML nas tabelas que cada um usa. Nenhum dos dois cria, altera ou apaga objetos.

**Matriz de privilégios**

2. A matriz fica no código (`internal/store/grants.go`), não em SQL solto:

| Tabela | `cobogo_api` | `cobogo_worker` |
| --- | --- | --- |
| `users` | SELECT, INSERT, UPDATE | nada |
| `sessions` | SELECT, INSERT, UPDATE, DELETE | nada |
| `audit_events` | SELECT, INSERT | nada |
| `projects` | SELECT, INSERT, UPDATE | SELECT |
| `settings` | SELECT, INSERT, UPDATE, DELETE | SELECT |
| `deployments`, `jobs` | SELECT, INSERT, UPDATE | SELECT, UPDATE |
| `deployment_events` | SELECT, INSERT | SELECT, INSERT |
| `analyses` | SELECT | SELECT, INSERT |
| `releases` | SELECT | SELECT, INSERT, UPDATE |
| `env_variables` | INSERT, UPDATE e DELETE; SELECT **só dos metadados** (sem `ciphertext`) | SELECT (todas as colunas) |
| `schema_migrations` | SELECT | SELECT |

3. **A API escreve o valor cifrado mas não o lê de volta.** O privilégio de SELECT sobre `env_variables` é por coluna e exclui `ciphertext`. A API tem a chave de cifra no ambiente e não tem o texto cifrado; só o worker tem os dois, porque precisa decifrar para entregar o valor ao contêiner (DEC-24).
4. A leitura de `schema_migrations` (só nomes e datas) existe para a API e o worker **conferirem o esquema ao subir** (item 6); nenhum dos dois a escreve. Toda tabela do esquema precisa estar na matriz: uma tabela nova sem entrada **falha o passo de migração**, em vez de ficar com um privilégio padrão que ninguém decidiu.

**Migrations em passo próprio**

5. O subcomando `cobogo-api migrate` conecta com o `cobogo_owner`, aplica as migrations e **reaplica a matriz** (revoga tudo dos dois roles e concede o que a matriz diz, então é idempotente e remove privilégios que sobraram). Não exige a chave de cifra. Se os roles `cobogo_api` e `cobogo_worker` não existirem (desenvolvimento com um usuário só), a concessão é pulada e isso é dito na saída.
6. Com `COBOGO_AUTO_MIGRATE=0` (o `compose` de produção), a API e o worker **não migram**: só conferem que o esquema aplicado é exatamente o que este binário espera e **falham ao subir** com uma mensagem clara se não for (esquema atrasado ou mais novo). Sem a variável o comportamento é o de hoje, migração automática, para o desenvolvimento.
7. No `compose`, `migrate` é um serviço de uma execução só; a API e o worker esperam que ele termine com sucesso.

**Criação dos roles**

8. Um script idempotente (`deploy/postgres/10-roles.sh`) roda na primeira inicialização do contêiner do PostgreSQL (e pode ser rodado de novo em um volume existente). Cria os três roles com as senhas do ambiente, passa o banco e o esquema ao `cobogo_owner`, retira de `PUBLIC` o acesso ao banco e o `CREATE` no esquema, e dá `CONNECT` aos dois roles de aplicação. Senhas em hexadecimal (`openssl rand -hex 24`), para entrarem na URL de conexão sem escape.

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| Dois roles, com a API dona dos objetos | Uma falha na API daria DDL e leitura de tudo |
| Migrar dentro da API com o role dono | Mantém o dono dentro do processo exposto, que é o que se quer evitar |
| Privilégios em um arquivo SQL fixo | Fica desatualizado quando uma migration cria tabela; a matriz no código é conferida contra o esquema |
| `ALTER DEFAULT PRIVILEGES` | Concede por padrão o que a matriz precisa decidir tabela a tabela |
| Segurança por linha (RLS) | O isolamento por dono já é feito nas consultas (DEC-27); RLS não protege uma API que roda como um único usuário do banco |

## Consequências e limites conhecidos

- Os privilégios são **por tabela**, não por linha: uma falha na API ainda lê e altera qualquer linha das tabelas que ela usa (usuários, sessões, projetos, deploys). Reduz-se o que ela alcança, não o que faz dentro dessas tabelas.
- O worker lê o texto cifrado de todas as variáveis e tem a chave: comprometê-lo expõe os valores. É inerente a entregá-los aos contêineres.
- O superusuário do contêiner continua existindo, com senha, na rede de controle; o PostgreSQL não publica porta. Se esse risco importar, a senha pode ser trocada por uma aleatória descartada depois da criação dos roles.
- Os roles são do cluster, não do banco: o script cria os nomes fixos `cobogo_*`. Um segundo banco no mesmo cluster os compartilharia.
- O coletor (RF-24 a RF-26) terá o seu role, com escrita só em `metric_samples`, quando existir.

- **Verificado em execução** (a pilha em contêineres, PostgreSQL 16, banco novo): o `migrate` rodou com o role dono, aplicou as migrations e os privilégios e saiu com 0; a API e o worker subiram conectados como `cobogo_api` e `cobogo_worker`; dois deploys do repositório de exemplo da Heroku concluíram pelo Traefik (76 pedidos durante o segundo, nenhum com erro), e uma variável cifrada pela API foi decifrada pelo worker e chegou ao contêiner, inclusive depois de uma troca de valor. Direto no banco, como cada role: a API recebeu `permission denied` ao ler `ciphertext`, ao criar tabela e ao apagar `users`; o worker, ao ler `users` e `sessions` e ao criar tabela. Pela rede de controle, uma senha errada é recusada (`scram-sha-256`).
- **Dois achados da execução real, já corrigidos:** (1) `INSERT ... ON CONFLICT DO UPDATE` lê o texto cifrado pela linha `EXCLUDED`, o que exigiria à API o SELECT sobre ele; a gravação virou `UPDATE` ou `INSERT`, dentro da mesma serialização que já existia. (2) A conferência do esquema ao subir precisa ler `schema_migrations`, que a matriz inicial negava aos dois roles: a API e o worker entravam em ciclo de falha. Os testes que conferiam a matriz não rodavam essa conferência como o role; passou a haver um que roda.
- **O superusuário e o `docker exec`.** Dentro do contêiner do PostgreSQL, a conexão local (`127.0.0.1`) é de confiança, como no padrão da imagem oficial: quem entra no contêiner já tem acesso ao host. Pela rede, todo login exige senha.
- **Testes que o provam** (cada um falha quando o código correspondente é removido): a matriz coluna a coluna (privilégio de coluna virar de tabela), o fluxo completo da API e do worker só com o concedido, a revogação de privilégios que sobraram, a recusa de tabela fora da matriz, a conferência do esquema (atrasado e mais novo) e o `Prepare` que só migra quando pedido.

## Critérios de revisão

Rever quando o coletor entrar, se aparecer uma tabela que os dois processos precisem escrever, ou se a matriz ficar difícil de manter à mão.
