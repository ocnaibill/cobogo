# DEC-41 — Volumes persistentes para dados da aplicação

> **Estado:** proposta discutida com o autor em 21/11/2026; **aprovada pelo autor em 21/11/2026 e implementada**, exceto a sugestão pela análise (item 1), que fica para depois.
>
> **Escopo:** como o usuário declara caminhos a persistir, como o volume nasce e é usado, o que acontece na troca de release, e o que acontece com os dados quando o projeto é excluído.
>
> **Relaciona-se com:** RF-12, RF-14, RF-18, RN-10 (ajustada aqui), DEC-11, DEC-29, DEC-32, DEC-38, DEC-40, issues #34, #28 e #6.

## Contexto

O caderno (Bun) grava o SQLite em `data/` e as folhas em `content/`. Sem volume, cada deploy apaga comentários, assinantes e o que foi escrito pela interface. O mestre diz que o repositório não influencia o `HostConfig` do contêiner: volumes vêm do plano e da política da plataforma, nunca do que o código declara.

## Decisão

1. **Quem declara é o usuário.** O projeto ganha o campo **"Dados persistentes"**: uma lista de caminhos absolutos dentro do contêiner (`/app/data`, `/app/content`), com validação (caminho absoluto, sem `..`, sem a raiz, sem caminhos do sistema, no máximo 8). A análise **sugerir** ("achei SQLite em `data/`"), com o usuário confirmando, no mesmo modelo das sugestões da DEC-39, é o desenho; **ainda não foi implementada**. Hoje só vale o que o usuário declara, e nada é inferido nem aplicado sozinho.
2. **Um volume nomeado por projeto e caminho**, com labels do Cobogó e do projeto (nome derivado do identificador do projeto, nunca do texto do repositório), criado uma vez e reutilizado por retry, reinício e novo deploy.
3. **Conteúdo inicial vem da imagem, uma vez.** Um volume nomeado **vazio** montado sobre um caminho que a imagem já tem recebe o conteúdo da imagem na primeira montagem (as folhas do repositório em `content/`). Depois disso o **volume prevalece** sobre a imagem, e o painel diz isso ao lado do campo ("mudanças nesses arquivos no repositório não chegam mais ao ar").
4. **Uma release escreve no volume por vez.** Em projeto com volume, a release **ativa é parada antes de a candidata subir** (aceita alguns segundos fora do ar, DEC-11; dois processos com SQLite em WAL no mesmo arquivo é o risco evitado). Se a candidata falhar, a anterior é **iniciada de novo** (DEC-40), e o deploy falha sem deixar o projeto fora do ar. O período de espera da DEC-38 não muda: a release anterior já está parada, e sai depois dele. Parar a anterior leva até 10 s se a aplicação ignora o sinal de término; esse tempo entra na janela fora do ar.
5. **Excluir o projeto e os dados (ajuste da RN-10).** O Cobogó nunca apaga dados sem uma ação explícita, mas essa ação pode ser a própria confirmação da exclusão: o diálogo lista os volumes com o tamanho, oferece **baixar os dados antes** (#28) e exige marcar **"apagar também os dados (irreversível)"**. Com a marca, o worker remove os volumes junto da limpeza da DEC-38 e registra o que apagou. **Sem** a marca, os volumes ficam e passam a aparecer numa lista de **"volumes sem projeto"**, com ação de apagar depois, confirmada e registrada; sem isso eles ficariam invisíveis ocupando disco.
6. **Privilégio.** O proxy do Docker passa a liberar `VOLUMES` (e `SYSTEM`, só para `GET /system/df`, que mede o tamanho). O proxy filtra por método e caminho, não por label: quem garante que só se toca um volume com os labels do Cobogó e do projeto é o código do worker, e um worker comprometido continua sendo o limite assumido da DEC-19. Um volume sem esses labels nunca é tocado.
7. **Posse dos volumes novos.** Um caminho que a imagem não tinha vira um ponto de montagem do root, onde a aplicação (que não é root) não escreve. Num volume recém-criado, o worker roda um contêiner descartável da própria imagem do projeto, sem rede e só com as capacidades de posse, com um comando fixo da plataforma, que entrega o conteúdo ao usuário da imagem. Se falhar (por exemplo, imagem sem `chown`), é um aviso, não uma falha do deploy.
8. **Auditoria e visibilidade.** Declarar, mudar e apagar dados têm auditoria com ator. O painel mostra, por projeto, os volumes com o caminho e o tamanho (medido pela faxina, a cada 5 minutos, e por isso aproximado), numa aba "Dados". Um volume só pode ser apagado se estiver **sem projeto**; um em uso é recusado, e o Docker recusa o que ainda está montado por um contêiner.

## Alternativas consideradas

- **Declaração no repositório (arquivo do Cobogó):** o repositório passaria a decidir montagens do contêiner, contra o princípio do mestre. Pode voltar mais tarde como sugestão, nunca como ordem.
- **Duas releases em paralelo com o mesmo volume:** zero downtime parcial, mas corrompe SQLite em WAL; descartada.
- **Nunca apagar volumes na exclusão:** protege dados, mas deixa lixo invisível e foi descartada pelo autor em favor da escolha explícita.
- **Bind mounts no host:** acoplam o Cobogó ao caminho da VM e a permissões do host; volumes nomeados são geridos pelo Docker.

## Consequências e limites

- Um volume é local à VM: sem backup (#28) a perda do disco perde os dados. O painel diz isso.
- O usuário que declara um caminho errado cria um volume vazio por cima do conteúdo da imagem; apagar o volume desfaz.
- Mudança de dono ou de permissão dentro do volume entre versões da imagem não é tratada.
- Trocar o caminho declarado cria um volume novo; o antigo vira "volume sem projeto" até ser apagado.

## Verificação

- **Testes:** validação dos caminhos (absolutos, limpos, fora do sistema, sem repetição nem um dentro do outro, até 8); com PostgreSQL real, caminhos por dono, volumes registrados com tamanho, "sem projeto" (projeto excluído ou caminho tirado da lista), exclusão só do que está sem projeto e com o nome como confirmação, a escolha de apagar dados chegando ao worker e a matriz de privilégios; a API (declarar, listar, 400 em caminho inválido, 409 `volume_in_use`, 400 em confirmação errada, 401); com Docker real, o nome derivado e estável, os labels, a recusa de um volume que não é do Cobogó, **a posse** (sem ela a aplicação não escreve no ponto de montagem do root; com ela escreve), o conteúdo inicial vindo da imagem, o tamanho só dos volumes do Cobogó e a remoção idempotente; e o worker apagando os dados com a exclusão só quando marcado.
- **Em execução** (API e worker locais, com o worker atrás de um `docker-socket-proxy` com `VOLUMES=1` e `SYSTEM=1`, o caminho da VM; deploy real do caderno): sem volume o deploy funcionou; com `/app/data` e `/app/content` declarados os dois volumes nasceram, a aplicação rodou como o usuário `bun`, o conteúdo da imagem (as folhas e o SQLite) apareceu nos volumes e a release anterior foi parada antes de a nova subir; um arquivo gravado pela aplicação **sobreviveu a um novo deploy**; um deploy com a candidata quebrada **reiniciou a release anterior** (a aplicação seguiu no ar, com o arquivo) e o deploy falhou; tirar um caminho da lista deixou o volume "sem projeto", e apagá-lo com a aplicação ainda montando-o foi **recusado pelo Docker** (a operação falhou com a explicação), enquanto depois de um deploy sem o caminho foi apagado; excluir o projeto **sem** marcar dados manteve o volume, que apareceu como sem projeto com o tamanho medido. Os tamanhos (1,5 KiB e 157 KiB) foram medidos pela faxina. No painel: a aba "Dados", a lista de volumes sem projeto na visão geral e a exclusão do projeto com a escolha de apagar os dados.

## Critérios de revisão

- Medir o tempo fora do ar na troca de release de um projeto com volume.
- Rever a sugestão por análise com o corpus (quantos SQLite e `content/` são detectados e aceitos).
