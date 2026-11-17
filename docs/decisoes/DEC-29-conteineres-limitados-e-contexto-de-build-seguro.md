# DEC-29 — Limites e endurecimento do contêiner da aplicação, e contexto de build seguro

> **Estado:** decidida com o autor em 17/11/2026; **implementada** (itens 1 a 10). Parte 5a. **Os padrões de 512 MiB e 2 GiB foram substituídos pela DEC-30** (1024 MiB e 4096 MiB, e limites editáveis pelo painel); o restante vale como está.
>
> **Escopo:** o que o adaptador Docker impõe a todo contêiner de aplicação e a todo build, e como o contexto de build trata links simbólicos e arquivos especiais.
>
> **Relaciona-se com:** DEC-19 (mitigações obrigatórias 3 e 5), DEC-26, DEC-24 (arquivos `.env` fora do contexto), RNF-05, §17.2 do mestre.

## Contexto

Hoje o contêiner da aplicação roda com as capacidades padrão do Docker, sem limite de memória, CPU nem processos, e com log sem limite de tamanho. Uma aplicação com vazamento de memória, ou um bug de bifurcação, derruba a VM inteira, inclusive o PostgreSQL e o próprio Cobogó. O build também roda sem limite de memória.

O contexto de build tem dois defeitos de segurança e robustez, o primeiro conhecido desde o 3e:

1. Um link simbólico no repositório gera um erro opaco (`write too long`), porque o cabeçalho do tar declara um link e o código tenta gravar o conteúdo do alvo.
2. O código abre o caminho do link com `os.Open`, que **segue** o link: um link para `/etc/passwd` ou para uma chave SSH do usuário do worker leria um arquivo de fora do repositório. Só não vaza hoje porque o passo 1 falha antes.

## Decisão

**Limites e endurecimento (contêiner da aplicação)**

1. **Limites padrão** (aprovados pelo autor): 512 MiB de memória, 1 CPU e 256 processos. A troca para disco (`swap`) fica desligada: o limite de memória é o limite real. Por instalação, `COBOGO_APP_MEMORY_MB`, `COBOGO_APP_CPUS` e `COBOGO_APP_PIDS` os alteram. Limite **por projeto** fica para depois: exige colunas, validação e um teto de política, e só faz sentido com a tela que o edita.
2. **Sem capacidades do Linux** (`cap-drop ALL`) e `no-new-privileges`: um processo da aplicação não adquire privilégio por binários setuid. O contêiner já não é privilegiado e não aceita bind mount (DEC-19).
3. **Log limitado** a 3 arquivos de 10 MiB (driver `json-file`): o log de uma aplicação barulhenta não enche o disco. `LogTail` (DEC-26) continua lendo o final.
4. **O que não entra por padrão:** sistema de arquivos somente leitura (quebra aplicações Node que gravam em `/tmp` ou no próprio diretório) e política de reinício (a supervisão de aplicações no ar pertence ao collector e ao RF-15; reiniciar sozinho esconderia a falha que a sonda e o diagnóstico precisam ver).
5. Portas abaixo de 1024 dentro do contêiner continuam servíveis por quem não é root: o Docker ajusta `net.ipv4.ip_unprivileged_port_start` do contêiner, então remover `NET_BIND_SERVICE` não quebra uma aplicação na porta 80. Há um teste com Docker real.

**Build**

6. **Limites do build:** 2048 MiB de memória (sem swap) e 2 CPUs, por `COBOGO_BUILD_MEMORY_MB` e `COBOGO_BUILD_CPUS`. Maiores que os da aplicação porque `npm ci` e o `build` do front pedem mais. O prazo de 10 min (RNF-05) já existe. O build usa o construtor clássico do daemon, que respeita esses limites.
7. **Rede do build:** continua a padrão. A rede própria do build, sem rota para a rede de controle (DEC-19, mitigação 4), depende das redes da parte 5b e 5d.

**Contexto de build**

8. **Nunca se lê o conteúdo por um link.** Um link simbólico entra no contexto **como link** (entrada de tar do tipo link, sem conteúdo) quando o alvo resolve, de forma léxica a partir do diretório do link, para dentro do repositório. O daemon resolve links apenas dentro da raiz do contexto.
9. **Links que apontam para fora** (alvo absoluto, ou relativo que escapa pela raiz) **não entram**. Em vez de silenciar, cada um gera uma linha de aviso no log do build ("link simbólico `x` aponta para fora do repositório e não foi incluído"). Se a aplicação dependia dele, o build falha com um "arquivo não encontrado" que o aviso explica.
10. **Arquivos que não são regulares nem links** (FIFO, soquete, dispositivo) são ignorados: abrir uma FIFO travaria o worker. O git não versiona esses tipos, então só aparecem se algo os criar no workspace.

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| Falhar o deploy ao achar um link para fora | Um link inofensivo (por exemplo, para um diretório de exemplo fora da raiz) derrubaria um projeto que o build nem usa |
| Seguir os links e copiar o conteúdo | Lê arquivos do host fora do repositório; é a falha que se quer evitar |
| Limite fixo único, sem variáveis | Um homelab com pouca ou muita memória precisa ajustar sem recompilar |
| Sistema de arquivos somente leitura por padrão | Quebra aplicações comuns sem que o corpus ainda tenha medido quantas |
| Reinício automático por padrão | Esconde a falha de boot; a sonda precisa ver a saída do processo |

## Consequências e limites conhecidos

- O contexto de build ainda é montado inteiro em memória antes de ir ao daemon. Um repositório de vários GiB pesaria no worker. Transmitir por `io.Pipe` e limitar o tamanho fica como evolução.
- A conferência de links é léxica: um link que passa por um diretório que é ele mesmo um link pode, em tese, resolver diferente no disco. Como o contexto vai ao daemon como tar e o daemon confina a resolução à raiz, o efeito prático é só um erro de build, não leitura de fora.
- Um limite de memória baixo demais mata a aplicação com o código 137 (falta de memória). O diagnóstico (DEC-26) hoje não reconhece esse código como causa; fica registrado para quando o corpus mostrar a frequência.
- Os limites não impedem esgotar disco por escrita em volume nomeado; limites de disco (RNF) ficam para o collector.

- **Verificado com Docker real:** o contêiner mostra, na própria inspeção do daemon, os limites aplicados (memória e swap 512 MiB, 1 CPU, 256 processos, `cap-drop ALL`, `no-new-privileges`, log de 10 MiB × 3). Um processo sem root e sem capacidades serve a porta 80 e reporta `CapEff` e `CapBnd` zerados e `NoNewPrivs 1`. Uma alocação além do limite mata a aplicação com 137; uma tempestade de `spawn` bate no limite de processos; um passo de build que passa da memória falha. A aplicação de exemplo da Heroku seguiu implantando de ponta a ponta sob esses limites.
- **Testes que o provam** (cada um falha quando o código correspondente é removido): sem `cap-drop`, sem `no-new-privileges`, sem limite de processos, sem limite de memória do build, e a conferência de links sempre verdadeira.

## Critérios de revisão

Rever se o corpus mostrar aplicações legítimas mortas pelo limite padrão (subir o padrão ou liberar o limite por projeto), builds sem memória em 2 GiB, ou aplicações que precisem escrever na raiz do contêiner.
