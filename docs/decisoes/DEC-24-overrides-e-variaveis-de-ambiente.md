# DEC-24 — Overrides do pipeline e variáveis de ambiente do projeto

> **Estado:** proposta de 16/11/2026, aguardando revisão do autor.
>
> **Escopo:** como o usuário responde pendências do plano (3a), como o Cobogó descobre, classifica, guarda e entrega variáveis de ambiente (3b) e como explica a falha causada por variável ausente (3c). Segunda e terceira partes do backend depois do contrato do motor (DEC-23).
>
> **Relaciona-se com:** DEC-14, DEC-15, DEC-23, RN-02, RN-03, RN-11, RN-15, RN-18, RF-08, RF-10, RF-22, RF-23, RNF-08, HEU-09 (§6.2 do [motor](../MOTOR-DE-HEURISTICAS.md)), issues #12, #20, #21 e #22.

## Contexto

O deploy só guarda `port_override`. Não há como responder a pendência de `start_command` (RF-08 incompleta) e não há onde guardar variáveis de ambiente. O HEU-09 já inventaria nomes (do `.env.example` e de `process.env.X` no código), mas só como lista; nada indica ao usuário o que provavelmente é obrigatório, o que o Cobogó pode gerar, ou o que o repositório já vazou. Três riscos orientam a decisão:

- Um valor digitado ou sugerido sem critério ("exemplo", `changeme`) viraria configuração real.
- Um valor vindo do repositório (inclusive um `.env` commitado) poderia ser usado em silêncio, e o `COPY . .` do Dockerfile gerado hoje o levaria para dentro da imagem.
- Gerar um segredo para uma credencial de terceiro entrega um app quebrado, sem erro claro.

## Decisão

**Dois mecanismos, com ciclos de vida diferentes.**

1. **Overrides do pipeline (3a)** são `campo → valor` para `port`, `start_command` e `build_command`. Não são sensíveis, pertencem a um deploy e a um SHA, e entram na entrada do analisador (DEC-23 §2). O armazenamento passa de `port_override` para um mapa de overrides no deploy. A resposta a uma pendência grava o override, registra o evento e reenfileira a análise **no mesmo SHA**; um novo clone não pode mudar o commit já fixado.
2. **Variáveis de ambiente (3b)** pertencem ao **projeto**, podem ser segredos e persistem entre deploys. Ficam fora do analisador, do plano, dos eventos e da carga dos jobs, que carregam só referências.

**Entrada do usuário.**

3. Validação estrutural antes de aceitar (porta numérica, comando de uma linha). Para `start_command`, as pendências oferecem como opções os scripts do `package.json` (`npm run <nome>`), com texto livre como alternativa. Cada opção é classificada como `production` ou `development` **pelo comando** (ferramenta de desenvolvimento conhecida, `--watch`); o nome `dev`/`watch` só reforça e, quando é o único motivo, a nota diz isso ("nome não basta", HEU-02). Opções de desenvolvimento são oferecidas, não escondidas: quem quer testar nesse modo pode escolhê-lo. Ficam fora da lista, **mas voltam em `omitted` com o nome do script** para a interface mostrar tudo: scripts que não executam o projeto em modo nenhum (`test`, `lint`, `format`, `typecheck`, `clean`), `build` (até haver suporte a build como forma de execução), ganchos `pre`/`post` de outro script e nomes que não são seguros num `sh -c`. Placeholder nunca vira valor: aparece como dica e o campo começa vazio. A validação semântica é o build, a execução e a sonda; a falha diz que o valor foi informado pelo usuário e mostra a saída.

**Classificação das variáveis (extensão do HEU-09).**

4. Cada variável do inventário recebe uma **classe**:
   - `platform`: fornecida pela plataforma, reservada. No D0 só `PORT`; `DATABASE_URL` entra quando houver vínculo com banco provisionado (issue #22).
   - `generable`: segredo que o próprio app inventa. A lista é **fechada e conservadora** (`JWT_SECRET`, `SESSION_SECRET`, `COOKIE_SECRET`, `CSRF_SECRET`, `SECRET_KEY`, `SECRET_KEY_BASE`, `APP_SECRET`, `APP_KEY`, `AUTH_SECRET`, `NEXTAUTH_SECRET`, `ENCRYPTION_KEY`). Nunca por padrão genérico (`*_KEY`, `*_TOKEN`, `*_PASSWORD`): falso negativo vira entrada manual; falso positivo geraria credencial de terceiro inválida.
   - `user`: o resto.
5. Cada variável recebe também o **estado no exemplo**: `empty`, `placeholder` (`changeme`, `your-…`, `<…>`, `xxx`, `TODO`, `exemplo`, `replace-me`, lista fechada), `default` (valor real não secreto, ex.: `NODE_ENV=production`), `commented` (`# NAME=…` com `NAME` em MAIÚSCULAS e `_`; prosa em comentário não conta) ou `absent` (referenciada no código, ausente do exemplo).
6. **Nada disso bloqueia o deploy.** A tela mostra avisos: `placeholder` → "provavelmente obrigatória"; `empty` → "o exemplo estava vazio, provavelmente é opcional; preencha agora se achar que o app quebra sem ela"; `commented` → "comentada no exemplo, provavelmente opcional ou alternativa"; `absent` com referência de runtime → "usada no código, ausente do exemplo"; `default` → valor pré-preenchido para confirmação. Só as pendências do pipeline bloqueiam.

**Valores que vêm do repositório.**

7. O Cobogó **nunca usa um valor que vem do repositório sem confirmação explícita**. A única exceção é o pré-preenchimento do item 6: somente de arquivos `.env.example`/`.env.sample`/`.env.template`, somente estado `default`, nome não secreto e valor que não casa com formato de credencial; o usuário revisa e confirma (RF-22). Nunca de `.env` real. O Dockerfile deixa de levar `.env` real para a imagem: o planner gera `.dockerignore` excluindo `.env` e `.env.*` (exceto os exemplos), para que o app não carregue por conta própria um valor que o Cobogó prometeu não usar.
8. **Detecção de exposição.** O analisador emite um aviso de segurança, não bloqueante, quando: (a) existe um arquivo de ambiente real (`.env`, `.env.local`, `.env.production`…) no clone — em um clone todo arquivo está versionado; ou (b) um valor em arquivo de ambiente casa com formato conhecido de credencial (AWS `AKIA`/`ASIA`, GitHub `ghp_`/`github_pat_`, Slack `xox`, Stripe `sk_live_`/`rk_live_`, Google `AIza`, cabeçalho de chave privada PEM, JWT de três segmentos) ou é valor de alta entropia em nome secreto. O aviso diz **o que fazer**: revogar/rotacionar a credencial (removê-la do histórico do Git não basta) e que o Cobogó não a usa. O achado guarda arquivo, linha, nome e tipo — **nunca o valor nem fragmento dele**. Isso exige ler valores em memória para classificar, exceção deliberada ao "só nomes" do HEU-09. O risco é de vazamento na saída, não de leitura (o worker já tem o clone inteiro em disco), e é contido **pela estrutura**: o detector é uma função própria que devolve só `{arquivo, linha, nome, tipo}`; o valor nunca é gravado em `Evidence`, que tem campo de valor. Testes com valores-canário verificam que ele não aparece em evidência, evento, log nem resposta da API (RN-15, RNF-08).

**Geração de segredo.**

9. É uma **ação da plataforma disparada pelo usuário**, não uma heurística (RN-15: heurísticas não inventam valores secretos). Disponível só para `generable`; 32 bytes de `crypto/rand` em base64url. O valor é guardado e **reutilizado** em todo deploy (regenerar invalidaria sessões); trocá-lo é ação explícita que indica necessidade de novo deploy (RF-23).

**Armazenamento e entrega.**

10. Tabela `env_variables` do mestre (§14.2): AES-256-GCM, chave fora do banco (variável de ambiente do servidor), versão de chave e AAD = projeto + nome + versão da variável. A API grava e nunca devolve valor (só "definida / não definida" e metadados); só o worker decifra (DEC-21) e entrega o valor como variável de ambiente na criação do contêiner, nunca no Dockerfile. A interface declara o alcance real: o servidor consegue decifrar; não se anuncia E2EE (RN-18, DEC-14). Rotação de chave e política completa ficam na issue #21.

**Diagnóstico de falha (3c, depois da 3b).**

11. Quando o contêiner cai ou a sonda não passa, o worker guarda as últimas linhas do log (redigidas, RN-11) e cruza com o inventário HEU-09, **incluindo** variáveis referenciadas no código e ausentes do exemplo. Saída: `confirmada` (nome de variável sem valor aparece no log, ou padrão fechado como "X is not defined" / "missing required environment variable X"), `possível` (lista das variáveis sem valor, sem afirmar) ou `desconhecida`. Nunca se afirma causa sem o nome no log (issue #12: sintoma ≠ diagnóstico). Exige capturar o log do contêiner na falha; entra com a adaptação Docker reforçada (parte 5).

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| Pendência bloqueante para toda variável vazia no exemplo | Quase todo app real ficaria em NEEDS_INPUT; variável vazia é, em geral, opcional |
| Gerar qualquer variável com nome de segredo | Credenciais de terceiro geradas produzem app quebrado sem erro claro |
| Geração automática sem ação do usuário | Troca silenciosa de configuração; viola RN-02 |
| Usar valores do `.env` do repositório | Perpetua o vazamento e contraria a promessa de não usar valores do repositório |
| Detectar segredos só pelo nome do arquivo | Perde chaves reais em `.env.example` |
| Tratar override e variável como o mesmo registro | Ciclos de vida e proteção diferentes (deploy/SHA vs. projeto/segredo) |

## Consequências e limites conhecidos

- A detecção de exposição cobre arquivos de ambiente do SHA analisado; não varre credenciais embutidas no código-fonte nem o histórico do Git (chave removida de um commit antigo não é detectada).
- `.dockerignore` quebra apps que dependem de um `.env` commitado; o diagnóstico (item 11) deve apontar variáveis sem valor, e o aviso do item 8 explica por quê.
- A exclusão vale para o perfil P-NODE do D0 (servidor com `npm start`). Perfis de frontend leem `.env.production` no build para configuração pública (DEC-15); quando o perfil estático entrar, a exclusão ganha uma opção explícita, por projeto e por arquivo, de incluir configuração pública não secreta. Nunca por padrão.
- Escolher uma opção `development` hoje tende a falhar: o planner instala com `npm ci --omit=dev` (ferramentas como `nodemon` e `vite` não existem na imagem) e servidores de desenvolvimento costumam escutar só em `127.0.0.1`, onde a sonda não os alcança. Um campo de **modo de execução** (`production` por padrão; `development` instala dependências completas, não faz `prune` e usa `NODE_ENV=development`) é decisão da parte 2 (planner). Até lá, a interface avisa que a opção pode falhar.
- A lista de geráveis, de placeholders e de formatos de credencial é curta e fechada; precisa crescer com o corpus.
- Não se distingue variável de build de variável de runtime para fins de entrega; só runtime é entregue em M1 (RF-10 em configuração pública de frontend, DEC-15, fica para depois).
- Heurísticas por padrão de log (item 11) dependem do formato das mensagens de cada runtime; são medidas no corpus antes de se confiar nelas.
- O deploy continua sem `analysis_id` por variável; a lista de avisos faz parte do `AnalysisResult`.

## Critérios de revisão

Rever se, no corpus, a regra "vazia no exemplo = provavelmente opcional" errar com frequência relevante (apps que quebram sem a variável), se a lista de segredos geráveis gerar credenciais inválidas, ou se o `.dockerignore` quebrar uma fração significativa dos repositórios.
