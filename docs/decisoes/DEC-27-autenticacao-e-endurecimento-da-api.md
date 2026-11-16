# DEC-27 — Autenticação do administrador e endurecimento da API

> **Estado:** proposta de 16/11/2026, aguardando revisão do autor.
>
> **Escopo:** parte 4 do backend. **4a:** autenticação, sessão, proteção de requisições de alteração, limite de tentativas, validação de entrada (`root_directory`, `repository_url`) e limites do servidor. **4b:** princípios do contrato da API para o React; o OpenAPI e os endpoints novos são implementados depois, sob estas regras.
>
> **Relaciona-se com:** RF-01, RF-09, RNF-03, RNF-04, RNF-08, DEC-20, DEC-21, TEL-01, §13, §17 e §17.2 do mestre, issue #19.

## Contexto

Hoje toda rota da API é pública: qualquer pessoa que a alcance cria projetos, dispara deploys, lê eventos e define variáveis. O RF-09 exige que chamadas sem sessão válida não alterem projetos nem leiam logs, e o mestre manda proteger o painel antes de qualquer exposição externa. A leitura do código que já existe achou também:

- `root_directory` não é validado: o worker faz `filepath.Join(workspace, root_directory)`, então `../../..` leria e empacotaria diretórios do host.
- `repository_url` aceita qualquer host `https://`, o que permite ao worker clonar de servidores internos da rede (RNF-04 pede URL pública permitida).
- O servidor HTTP não tem timeouts (conexões lentas o seguram para sempre) nem limite de corpo geral.
- Nada no código lê cabeçalhos de proxy; o mestre (§13) pede aceitá-los só de origens confiáveis, e o Cobogó ficará atrás de um Cloudflare Tunnel, em que todo pedido chega do `cloudflared` e o IP real do cliente vem em cabeçalho.

## Decisão

**Identidade e senha**

1. **Um administrador, sem cadastro público** (nível A do DEC-20). Tabela `users` (`id`, `login` único, `password_hash`, `created_at`). Projetos ganham `owner_id`; toda consulta é filtrada pelo usuário da sessão. Com um só administrador a diferença é pequena, mas é o que prepara o nível B sem reescrever a API. Projetos sem dono (dados de desenvolvimento anteriores) são adotados pelo primeiro administrador criado.
2. **Senha com argon2id** (m = 64 MiB, t = 3, p = 2, salt de 16 bytes, formato PHC), comparada em tempo constante. Mínimo de 12 e máximo de 128 caracteres, sem regras de composição (NIST SP 800-63B); a senha não pode ser igual ao login. O máximo evita que uma senha enorme vire ataque de recurso.

**Criação do administrador (TEL-01)**

3. Sem nenhum usuário cadastrado, a API gera na inicialização um **token de configuração de uso único**, imprime no log uma vez e guarda só o hash, em memória (reiniciar gera outro). `POST /api/v1/setup` recebe `{token, login, password}`, cria o administrador na mesma transação que confere que ainda não existe usuário e **fecha a rota** (`409` depois). `GET /api/v1/setup/status` (`{needs_setup}`) é público para a tela saber que fluxo mostrar. A rota de setup tem o mesmo limite de tentativas do login. Recuperar uma senha perdida é manual, por um subcomando de terminal da API que redefine a senha de um login existente (acesso ao host é acesso de operador); não há recuperação por e-mail.

**Sessão**

4. **Sessão no servidor, no PostgreSQL.** Tabela `sessions` (`user_id`, `token_hash`, `created_at`, `last_seen_at`, `expires_at`, `ip`, `user_agent`). O token tem 32 bytes aleatórios e o banco guarda só o SHA-256 dele; um vazamento do banco não entrega sessões válidas.
5. **Cookie** `__Host-cobogo_session` com `Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/` e **sem `Domain`**. O prefixo `__Host-` obriga esses atributos e prende o cookie ao hostname de gestão, e uma aplicação publicada em outro hostname não o recebe (RNF-08). Em desenvolvimento por HTTP, `COBOGO_COOKIE_INSECURE=1` troca por `cobogo_session` sem `Secure`, com aviso no log; nunca em produção.
6. **Duração:** **2 h sem atividade e 12 h no máximo.** `last_seen_at` é atualizado no máximo uma vez por minuto. O login sempre cria uma sessão nova; logout a apaga; trocar a senha apaga as outras sessões do usuário. Sessões vencidas são removidas periodicamente.

**Proteção de requisições de alteração**

7. **Sem CORS: a API só serve a mesma origem** (a interface é entregue no mesmo hostname, com o proxy encaminhando `/api`). Em `POST`, `PUT`, `PATCH` e `DELETE`: o `Origin` deve ser igual à URL pública configurada (`COBOGO_PUBLIC_URL`); sem `Origin`, vale `Sec-Fetch-Site: same-origin`; sem nenhum dos dois, a requisição é recusada. Corpos exigem `Content-Type: application/json`. Somado ao `SameSite=Strict`, dispensa token CSRF. Clientes de linha de comando enviam o `Origin` explicitamente (o README mostra).
8. **Rotas públicas, e só elas:** `GET /healthz`, `GET /api/v1/setup/status`, `POST /api/v1/setup` e `POST /api/v1/auth/login`. O resto responde `401` sem sessão válida.

**Limite de tentativas e auditoria**

9. **Login:** 5 falhas em 15 min para o par (IP, login) bloqueiam esse par por 15 min (`429` com `Retry-After`). Um limite global por login (30 falhas em 15 min) cobre atacantes que trocam de IP. O bloqueio nunca derruba uma sessão já aberta, e como os contadores ficam em memória, **reiniciar a API os zera**: é a saída do operador se um ataque trancar o painel por 15 min. A resposta é idêntica para login inexistente e senha errada, e o tempo também (um hash fictício é calculado quando o login não existe). Como cada verificação de argon2id usa 64 MiB, no máximo **2 verificações correm ao mesmo tempo**; as demais esperam, e uma enxurrada de logins não esgota a memória.
10. **Auditoria.** Tabela `audit_events` (`at`, `actor_id`, `action`, `target`, `ip`, `outcome`) para setup, login (sucesso e falha), logout, troca de senha, criação de projeto, deploy, resposta, cancelamento e alteração de variáveis (só o nome, nunca o valor). Nunca recebe senha, token ou valor de variável.

**IP de cliente atrás de proxy**

11. **IP do cliente atrás de proxy.** `COBOGO_TRUSTED_PROXIES` (lista de CIDRs, vazia por padrão = nenhum cabeçalho de proxy é aceito). Só quando o endereço da conexão pertence a essa lista a API usa `CF-Connecting-IP`; na falta dele, percorre `X-Forwarded-For` da direita para a esquerda e fica com o primeiro endereço não confiável. De qualquer outra origem esses cabeçalhos são ignorados (falsificáveis). O hostname original é preservado pelo túnel e conferido contra `COBOGO_PUBLIC_URL`.
    - **Atrás do túnel, a conexão que chega à API é a do Traefik** (rede `cobogo-edge`), não a do `cloudflared`; o CIDR a confiar é o dessa rede. O Traefik repassa o `CF-Connecting-IP` sem alterá-lo.
    - **Se a lista não estiver configurada, todo cliente parece ter o mesmo IP** e um atacante e o administrador passam a dividir o mesmo contador. Para isso não passar em silêncio, a API **avisa no log**, uma vez por hora, quando recebe `CF-Connecting-IP` ou `X-Forwarded-For` de uma conexão que não é confiável ("recebi cabeçalho de proxy de uma origem não confiável; se há um proxy na frente, configure `COBOGO_TRUSTED_PROXIES`"). O limite continua protegendo o painel nesse caso, mas pode atrasar o administrador durante um ataque; a saída é reiniciar a API (item 9).
    - **A confiança em `CF-Connecting-IP` só vale se o Traefik aceita tráfego somente do `cloudflared`.** O túnel faz conexões de saída, então basta não publicar as portas do Traefik na rede local nem na Internet; quem alcançasse o Traefik direto poderia forjar o cabeçalho e escolher o IP que a API enxerga.

**Validação de entrada**

12. **`root_directory`.** Normalizado antes de gravar: `""`, `.`, `./a/b`, `a/b/` e `/a/b` viram `""` ou `a/b`. Recusados: segmento `..`, barra invertida, NUL ou caractere de controle, mais de 255 bytes e `.git` como primeiro segmento. **O worker repete a checagem:** depois do clone, resolve links simbólicos do caminho e do workspace e confirma que o caminho continua dentro do workspace (um link do repositório para fora é recusado; um link para outro diretório do próprio repositório vale). Diretório inexistente naquele commit é erro claro ("o diretório X não existe neste commit"), não "package.json ausente".
13. **`repository_url`.** `https` apenas, sem credenciais na URL, sem porta (ou `443`), host exato da lista permitida, sem IP literal e sem `localhost`, caminho `dono/repositório` (dois segmentos ou mais), até 2048 bytes, sem consulta nem fragmento. **Hosts padrão:** `github.com`, `gitlab.com`, `codeberg.org`, `bitbucket.org` e `git.sr.ht`; `COBOGO_EXTRA_GIT_HOSTS` acrescenta outros. A API valida na criação do projeto e o worker valida de novo antes de clonar (defesa em profundidade). Repositório privado ou inexistente falha sem pedir credenciais, com a mensagem "repositório inexistente ou privado: o Cobogó só clona repositórios públicos".

**Limites do servidor**

14. `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `WriteTimeout` 60 s, `IdleTimeout` 120 s, `MaxHeaderBytes` 32 KiB, corpo máximo de 1 MiB (variáveis de ambiente mantêm o limite próprio). JSON estrito: um único documento, sem campos desconhecidos. Respostas com `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer` e `X-Frame-Options: DENY`. Pânico em um manipulador vira `500` sem detalhe. Cada requisição recebe um `X-Request-Id`; o log de acesso não registra corpo nem cookies.

**Contrato para o React (4b, princípios)**

15. Prefixo `/api/v1`. Erros num formato único, `{"error": {"code": "...", "message": "..."}}`, com códigos estáveis e mensagens em português. Datas em UTC (RFC 3339), IDs UUID, listas com `limit` e cursor. **OpenAPI** em `docs/api/openapi.yaml`, mantido junto do código, com um teste que confere que as rotas registradas e as documentadas são as mesmas. Endpoints a acrescentar: listar projetos e deploys, análise estruturada de um deploy (campos, pendências com opções, variáveis, avisos), plano e Dockerfile, eventos incrementais (`?after=<seq>&limit=`, por polling, DEC-10), cancelamento (só em `QUEUED` e `NEEDS_INPUT`) e URL da release. O detalhe de um deploy deixa de embutir todos os eventos.

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| JWT sem estado | Não dá para revogar no logout nem ao trocar a senha sem manter estado no servidor de qualquer forma |
| bcrypt | Limita a senha a 72 bytes e é menos resistente a hardware paralelo; argon2id é a recomendação atual da OWASP |
| Token CSRF por formulário | `SameSite=Strict` mais conferência de `Origin` cobre a API de mesma origem com menos peças |
| Criar o administrador por variável de ambiente | A senha ficaria em arquivo de configuração e no ambiente do processo |
| Só comando de terminal para criar o administrador | Sem a "configuração inicial assistida" do TEL-01 |
| Aceitar `X-Forwarded-For` sempre | Qualquer cliente falsifica o IP e burla o limite de tentativas |
| Permitir qualquer host `https` | O worker clonaria de servidores internos da rede (RNF-04) |
| Bloquear a conta após N falhas, sem limite por IP | Qualquer pessoa trancaria o administrador para fora |

## Consequências e limites conhecidos

- Os contadores de tentativas ficam em memória e reiniciam com o processo; com mais de uma instância da API seria preciso um armazenamento compartilhado.
- Em produção o cookie `__Host-` exige HTTPS; o túnel o fornece. Em desenvolvimento por HTTP use `COBOGO_COOKIE_INSECURE=1`.
- Sem autenticação de dois fatores e sem recuperação de senha por e-mail; a recuperação é manual, por quem tem acesso ao host.
- Quem tem acesso ao banco e à chave de cifra de variáveis (DEC-24) continua podendo ler os valores (RN-18); a separação das roles do PostgreSQL entre API e worker (DEC-21) e dos proxies do Docker (DEC-19) fica para a parte 5, porque depende da topologia de implantação.
- O IP do cliente só é tão confiável quanto o caminho até a API: se o Traefik for exposto fora do túnel, o `CF-Connecting-IP` passa a ser falsificável e o limite por IP perde o sentido (o limite global por login continua valendo).
- Um redirecionamento do próprio host permitido (por exemplo, repositório renomeado) é seguido pelo `git`; um host permitido que redirecionasse para a rede interna seria um risco, mas os hosts da lista são serviços públicos conhecidos.
- Repositórios privados não são suportados (sem credenciais de clonagem).
- O limite de 12 h de sessão obriga a entrar de novo todo dia; é intencional para um painel de operador.

## Critérios de revisão

Rever se o limite de tentativas bloquear o administrador legítimo com frequência (afrouxar), se o corpus mostrar `root_directory` legítimo recusado pela normalização, ou se surgir a necessidade de mais de um usuário (nível B: papéis e quotas).
