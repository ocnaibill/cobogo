# DEC-41 — Volumes persistentes para dados da aplicação

> **Estado:** proposta discutida com o autor em 21/11/2026; **aprovada pelo autor em 21/11/2026; implementação a seguir**.
>
> **Escopo:** como o usuário declara caminhos a persistir, como o volume nasce e é usado, o que acontece na troca de release, e o que acontece com os dados quando o projeto é excluído.
>
> **Relaciona-se com:** RF-12, RF-14, RF-18, RN-10 (ajustada aqui), DEC-11, DEC-29, DEC-32, DEC-38, DEC-40, issues #34, #28 e #6.

## Contexto

O caderno (Bun) grava o SQLite em `data/` e as folhas em `content/`. Sem volume, cada deploy apaga comentários, assinantes e o que foi escrito pela interface. O mestre diz que o repositório não influencia o `HostConfig` do contêiner: volumes vêm do plano e da política da plataforma, nunca do que o código declara.

## Decisão

1. **Quem declara é o usuário.** O projeto ganha o campo **"Dados persistentes"**: uma lista de caminhos absolutos dentro do contêiner (`/app/data`, `/app/content`), com validação (caminho absoluto, sem `..`, sem a raiz, sem caminhos do sistema, no máximo 8). A análise só **sugere** ("achei SQLite em `data/`"), e o usuário confirma, no mesmo modelo das sugestões da DEC-39. Nada é inferido e aplicado sozinho.
2. **Um volume nomeado por projeto e caminho**, com labels do Cobogó e do projeto (nome derivado do identificador do projeto, nunca do texto do repositório), criado uma vez e reutilizado por retry, reinício e novo deploy.
3. **Conteúdo inicial vem da imagem, uma vez.** Um volume nomeado **vazio** montado sobre um caminho que a imagem já tem recebe o conteúdo da imagem na primeira montagem (as folhas do repositório em `content/`). Depois disso o **volume prevalece** sobre a imagem, e o painel diz isso ao lado do campo ("mudanças nesses arquivos no repositório não chegam mais ao ar").
4. **Uma release escreve no volume por vez.** Em projeto com volume, a release **ativa é parada antes de a candidata subir** (aceita alguns segundos fora do ar, DEC-11; dois processos com SQLite em WAL no mesmo arquivo é o risco evitado). Se a candidata falhar, a anterior é **iniciada de novo** (DEC-40), e o deploy falha sem deixar o projeto fora do ar. O período de espera da DEC-38 passa a valer só para projetos sem volume.
5. **Excluir o projeto e os dados (ajuste da RN-10).** O Cobogó nunca apaga dados sem uma ação explícita, mas essa ação pode ser a própria confirmação da exclusão: o diálogo lista os volumes com o tamanho, oferece **baixar os dados antes** (#28) e exige marcar **"apagar também os dados (irreversível)"**. Com a marca, o worker remove os volumes junto da limpeza da DEC-38 e registra o que apagou. **Sem** a marca, os volumes ficam e passam a aparecer numa lista de **"volumes sem projeto"**, com ação de apagar depois, confirmada e registrada; sem isso eles ficariam invisíveis ocupando disco.
6. **Privilégio.** O proxy do Docker passa a liberar `VOLUMES`, e o código do worker só cria, inspeciona, monta e (quando pedido) remove volumes com os labels do Cobogó e do projeto. Um volume sem esses labels nunca é tocado.
7. **Auditoria e visibilidade.** Declarar, mudar e apagar dados têm auditoria com ator. O painel mostra, por projeto, os volumes com o caminho e o tamanho.

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

## Critérios de revisão

- Medir o tempo fora do ar na troca de release de um projeto com volume.
- Rever a sugestão por análise com o corpus (quantos SQLite e `content/` são detectados e aceitos).
