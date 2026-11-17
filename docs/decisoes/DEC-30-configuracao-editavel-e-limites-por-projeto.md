# DEC-30 — Configuração editável pelo painel, limites por projeto e falta de memória

> **Estado:** decidida com o autor em 17/11/2026; **implementada** (itens 1 a 11). Parte 5a2.
>
> **Escopo:** quais configurações o administrador muda pelo painel sem reiniciar nada, como o valor efetivo é escolhido, os limites de recursos por projeto e a mensagem quando uma aplicação ou um build morre por falta de memória.
>
> **Relaciona-se com:** DEC-29 (limites), DEC-27 (administrador e auditoria), DEC-28 (contrato), DEC-24 (diagnóstico de falha).

## Contexto

Na DEC-29 os limites de recursos e os hosts de repositório extras são variáveis de ambiente do processo. Mudá-los exige editar o `.env` ou o `compose` e reiniciar, e o painel não tem como mostrá-los. Há ainda dois pontos abertos: o limite por projeto (adiado na DEC-29) e o fato de uma morte por falta de memória aparecer só como "código 137".

Os padrões (512 MiB para a aplicação e 2 GiB para o build) foram escolhidos às cegas. A VM do homelab tem 16 GiB, e `next start`, NestJS com Prisma e o modo de desenvolvimento passam facilmente de 512 MiB; `next build` ou um monorepo médio pedem de 2 a 4 GiB.

## Decisão

**Duas classes de configuração**

1. **Fundação e segurança ficam só no ambiente do processo:** `DATABASE_URL`, `COBOGO_SECRET_KEY`, `COBOGO_PUBLIC_URL`, `COBOGO_TRUSTED_PROXIES`, `COBOGO_COOKIE_INSECURE`, `API_ADDR`. O banco e a chave precisam existir antes de qualquer painel, e os outros protegem a própria sessão: uma sessão roubada não deve poder trocá-los.
2. **Política operacional vive no banco e o administrador a edita pelo painel:** `app_memory_mb`, `app_cpus`, `app_pids`, `build_memory_mb`, `build_cpus` e `extra_git_hosts`. O domínio das aplicações (parte 5b) entra aqui quando existir.

**Valor efetivo**

3. **Precedência por campo:** projeto, depois banco, depois ambiente, depois o padrão de fábrica. As variáveis `COBOGO_APP_*`, `COBOGO_BUILD_*` e `COBOGO_EXTRA_GIT_HOSTS` continuam valendo como **padrão inicial** da instalação; remover um valor do banco devolve o campo ao ambiente (ou ao padrão). A resposta da API diz de onde veio cada valor (`source`: `project`, `database`, `env` ou `default`).
4. **Novos padrões de fábrica**, para uma VM de 16 GiB: aplicação **1024 MiB**, 1 CPU, 256 processos; build **4096 MiB** e 2 CPUs. O limite não reserva memória: subir o teto não custa nada até a aplicação usá-lo.
5. **Faixas aceitas** (validadas na API e no ambiente pela mesma definição): memória da aplicação de 64 a 262144 MiB, memória do build de 256 a 262144 MiB, CPUs de 0,1 a 256, processos de 16 a 65536. O teto serve para barrar um erro de digitação, não para impor política: com um único administrador, não há outra camada.

**Quando vale**

6. **O worker lê a configuração a cada deploy**, ao começar o pipeline, e a usa no build e na criação do contêiner. A mudança vale **a partir do próximo deploy**; um contêiner já em execução mantém os limites com que subiu. Aplicar a mudança a um contêiner vivo (o Docker permite para memória, CPU e processos) fica para depois. Os hosts extras são lidos a cada validação, na API e no worker, então valem na hora.

**Limites por projeto**

7. Um projeto tem um objeto `limits` com os mesmos cinco campos de recursos, todos opcionais. Campo ausente herda do nível de cima. A API devolve o que foi definido (`overrides`) e o valor efetivo com a origem (`effective`).

**API** (acrescentada ao contrato, DEC-28)

8. `GET /settings` lista cada chave com tipo, valor efetivo, origem, padrão e faixa, para o painel montar o formulário. `PUT /settings/{key}` grava e `DELETE /settings/{key}` devolve a chave ao ambiente ou ao padrão. `GET` e `PUT /projects/{id}/limits` leem e substituem os limites do projeto (`{}` limpa todos). O objeto `Project` ganha `limits`.
9. Toda alteração entra na auditoria com ator e **chave** (`setting.update`, `setting.reset`, `project.limits`). Os valores não são segredos, mas a trilha segue a regra de guardar identificadores e não conteúdo.
10. `extra_git_hosts` é uma lista de nomes de host, em minúsculas, sem esquema, porta, caminho nem IP, com no máximo 20 itens. Um host extra só amplia de onde o Cobogó clona, e a validação da URL do repositório continua a mesma (HTTPS, sem credenciais).

**Falta de memória**

11. Para a aplicação, o Docker informa com exatidão se o kernel a matou por memória (`OOMKilled`). Nesse caso a falha traz um diagnóstico `confirmed`: "a aplicação foi encerrada por exceder o limite de memória (N MiB)", com o limite que estava valendo. Para o build, um passo que termina com código 137 ganha no erro a dica de que provavelmente excedeu o limite de memória do build (N MiB). Em ambos o texto aponta o ajuste no painel.

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| Tudo editável no painel, inclusive `COBOGO_PUBLIC_URL` e proxies confiáveis | Uma sessão roubada trocaria a origem aceita ou o IP confiável; e o banco e a chave precisam existir antes do painel |
| Só variáveis de ambiente | Reiniciar o processo para ajustar um limite não é amigável e o painel não saberia mostrá-los |
| Limite apenas global | Um projeto pesado forçaria o teto de todos; o limite por projeto é o ponto de ajuste real |
| Aplicar a mudança aos contêineres em execução já | Reaplicar pelo `update` do Docker funciona, mas muda o comportamento de aplicações no ar; fica como ação explícita futura |
| Detectar falta de memória só pelo código 137 | Um `kill -9` manual também dá 137; `OOMKilled` do daemon é exato |

## Consequências e limites conhecidos

- A soma dos limites das aplicações no ar pode passar da RAM da VM (o limite é teto, não reserva); nesse caso o próprio kernel decide quem morre. O Cobogó não confere essa soma; quem a vigia é o administrador, e o collector (RNF) é quem terá a visão do uso real.
- `source` fala do banco ou do ambiente **do processo que respondeu**: se a API e o worker rodarem com ambientes diferentes, o worker usa o seu. Em instalação normal os dois leem o mesmo `compose`.
- O limite por projeto e o global são lidos no início do pipeline; um deploy retomado relê, então vale o valor atual.
- Não há histórico de versões das configurações além da trilha de auditoria (que registra a chave, não o valor anterior).

- **Verificado em execução:** com a API e o worker reais, um limite de projeto (700 MiB) e uma configuração global (300 processos) chegaram ao contêiner exatamente (`734003200` bytes e `300`), a origem de cada valor veio certa em `effective`, e o evento `generating` registra os limites usados no deploy. Um segundo deploy, depois de baixar o projeto para 64 MiB, usou o novo valor sem reiniciar nenhum processo. A morte por memória foi provada com Docker real (`OOMKilled` do daemon, diagnóstico `confirmed` citando o limite), não no deploy de exemplo, que cabe em 64 MiB.
- **Testes que o provam** (cada um falha quando o código correspondente é removido): a precedência projeto, banco, ambiente e padrão, a rejeição de IP como host, o filtro por dono na troca de limites e a detecção da morte por memória.

## Critérios de revisão

Rever se aparecer configuração que o administrador queira mudar e não está na lista, se a soma dos limites estourar a RAM na prática (conferir antes de aceitar), ou se mudar limite só para o próximo deploy confundir (então vem o "aplicar agora").
