# DEC-38 — Ciclo de vida: aposentar releases, excluir projeto e aplicar limites

> **Estado:** decidida com o autor em 20/11/2026; **implementada**. Primeira leva do MVP (DEC-35 a DEC-39).
>
> **Escopo:** o que acontece com contêineres, redes e imagens depois de uma release nova entrar no ar (parte 5c), como excluir um projeto sem perder o histórico, como aplicar limites a um contêiner que já roda (issue #33) e a faxina de recursos órfãos.
>
> **Relaciona-se com:** RF-14, RF-15, RF-18, RN-07, RN-12, DEC-11, DEC-27, DEC-29, DEC-30, DEC-31 (item 10 e o "rebaixar e retirar" adiado), DEC-32, DEC-33 (roles e matriz de privilégios), issues #16, #33, #34, #36 e #37.

## Contexto

Depois da DEC-31, uma release nova vira a ativa e a anterior continua rodando para sempre: sem rota, mas ocupando memória e disco. Não existe exclusão de projeto, e um limite alterado só vale no próximo deploy. Nesta sessão, os testes deixaram uma dúzia de contêineres vivos no Mac. A API não tem acesso ao Docker (DEC-32): tudo o que mexe em contêiner precisa passar pelo worker.

## Decisão

1. **Fila de operações.** Nova tabela `operations` (projeto, tipo, estado, parâmetros, resultado, erro, tentativas, `not_before`, quem pediu). A API só insere e consulta; o worker as executa no mesmo laço dos jobs, com posse por prazo (`lease_until`, como nos jobs) e tentativas limitadas (RN-09). Tipos: `retire_releases`, `delete_project`, `apply_limits` e `sweep`. A matriz de privilégios (DEC-33) ganha só esta tabela; a API **não** ganha `DELETE`.
2. **Aposentar releases (5c).** Ao promover uma release, o worker agenda `retire_releases` para o projeto, com `not_before` = agora + **período de espera** (`retire_grace_seconds`, padrão 30 s, editável pela API de configurações da DEC-30; o painel ainda não tem tela para configurações; o Traefik consulta a cada 2 s). A operação não prende o pipeline de deploy nem o worker durante a espera. Ao executar: para cada contêiner gerenciado do projeto que não é o da release ativa e cujo deploy já terminou, tira do ingress, para e remove; a release vira `retired`. Se um deploy novo estiver em curso, a candidata dele nunca é tocada.
3. **Retenção de imagens.** Ficam as imagens da release ativa e da anterior (`release_images_kept`, padrão 2, editável). As mais antigas do projeto são removidas, só as de tag `cobogo/<slug>:*` e só se nenhum contêiner as usa. Isso deixa a imagem da versão anterior pronta para um rollback futuro, que **ainda não existe como botão** e continua dito como limite.
4. **Faxina de órfãos, ligada por padrão.** O worker, de tempos em tempos (e ao subir), lista os contêineres **com os labels do Cobogó** e remove os elegíveis: o deploy deles terminou (sucesso, falha, cancelado ou não suportado), não são a release ativa de um projeto e passou o período de espera; ou o projeto foi excluído. **Nunca** toca em contêiner sem os labels (o banco do Cobogó, outras aplicações da máquina), em release ativa nem em candidata de deploy em curso. Cada faxina que removeu algo grava uma operação `sweep` concluída, com a lista do que saiu (RF-18).
5. **Excluir projeto: `DELETE /projects/{id}`.** O corpo traz `confirm_slug`, que precisa ser igual ao nome do projeto. Responde 202 com a operação.
   - **Recusa com 409** (`deployment_in_progress`) se há deploy em andamento (de buscar o código até verificar). Deploys na fila ou aguardando resposta são cancelados.
   - **Na hora, numa transação:** o projeto recebe `deleted_at`, as variáveis cifradas são apagadas e uma operação `delete_project` é criada. A rota sai do Traefik em até 2 s (a consulta de rotas ignora projetos excluídos) e o projeto some das listas e responde 404.
   - **Depois, pelo worker:** remove os contêineres do projeto, a rede `cobogo-proj-<id>` e as imagens do projeto, e marca as releases como `removed`. Se falhar, repete com limite e a falha fica visível na operação.
   - **Preservado: o histórico** (deploys, eventos e log, análises, releases e auditoria). É evidência de avaliação e de rastreabilidade (RF-14). Só as variáveis cifradas são apagadas, e **volumes nunca são tocados** (issue #34). Apagar o histórico de vez é uma operação de operador, separada e fora da API: issue #36.
   - O `slug` fica livre: a restrição de unicidade vira um índice único parcial, só sobre projetos não excluídos.
6. **Aplicar limites ao vivo: `POST /projects/{id}/limits/apply` (#33).** Cria a operação `apply_limits`; o worker atualiza o contêiner da release ativa (`ContainerUpdate`; memória e swap juntos, mesmas regras da DEC-29) com os limites efetivos de hoje. Só mexe em contêiner com os labels do Cobogó e do projeto. **Recusa reduzir a memória abaixo do uso atual** (uso do contêiner menos o cache), em vez de matar a aplicação em silêncio. O resultado (de quanto para quanto) fica na operação.
7. **Superfície.** `GET /operations/{id}` e `GET /projects/{id}/operations` (as recentes). Toda ação tem auditoria com ator e projeto. No painel: "zona de perigo" na página do projeto, com confirmação digitando o nome; "Aplicar agora" na aba de limites, com o resultado; e um aviso quando há limpeza pendente ou falha.
8. **Configurações novas** (DEC-30), sem sobreposição por projeto: `retire_grace_seconds` (5 a 3600, padrão 30) e `release_images_kept` (1 a 20, padrão 2).

## Alternativas consideradas

- **Apagar o histórico ao excluir:** exigiria `DELETE` em cinco tabelas para a API ou o worker e perderia dado de avaliação; foi descartado por decisão do autor, com o comando de operador (issue #36) para o dia em que um participante do estudo precisar sair.
- **Aposentar dentro do pipeline de deploy, esperando o período:** prenderia o worker e a posse do deploy por 30 s sem necessidade.
- **Aposentar só por faxina periódica:** funciona, mas deixa a release antiga ocupando recursos por minutos e sem um registro claro de "foi aposentada por causa deste deploy".
- **Excluir projeto direto na API:** a API não tem Docker, de propósito.

## Consequências e limites

- Não há garantia de zero downtime (DEC-11): a troca é de uma vez, a antiga fica no ar até o período de espera acabar, e depois é removida.
- Parar, iniciar e reiniciar a aplicação ficam para a issue #37. Rollback por botão e a reconciliação completa (#24) também ficam de fora.
- A faxina só enxerga contêineres com os labels do Cobogó; o que for criado fora dele não é gerenciado.
- A recusa de reduzir memória usa uma leitura instantânea de uso; um pico logo depois ainda pode estourar o novo limite, e a falha seria diagnosticada como falta de memória (DEC-30).
- Um projeto excluído mantém o `slug` na linha antiga; um novo projeto com o mesmo nome é outro projeto, com outro id.
- **Imagens anteriores a esta DEC não têm o label do projeto** (as novas passam a tê-lo, em `BuildWithArgs`), então a retenção e a exclusão não as enxergam: ficam até alguém as remover. É de propósito: sem o label, o nome da tag (`cobogo/<slug>:*`) não prova de quem é a imagem, e um projeto novo com o mesmo nome poderia perder a dela.
- Um contêiner do Cobogó de um deploy que o banco **não conhece** nunca é tocado pela faxina: pode ser de outra instalação do Cobogó no mesmo Docker.
- A API não desfaz uma exclusão; não há "restaurar". O histórico fica, mas o projeto, as variáveis e a release não voltam.

## Verificação

- **Testes:** armazenamento com PostgreSQL real (exclusão lógica, histórico preservado, rotas e listas, `slug` reutilizável, recusa com deploy rodando, cancelamento do que espera, fila de operações com tomada de posse e tentativas limitadas, coalescência da aposentadoria, release ativa nunca aposentada), o fluxo inteiro **só com os privilégios concedidos a cada role** (a API não apaga histórico nem conclui operação; o worker não toca em `projects`), a regra de elegibilidade da remoção (9 casos), a API (confirmação por nome, 409, 404, auditoria, sessão) e o adaptador Docker com contêineres, redes e imagens reais (só mexe no que tem os labels do Cobogó, recusa reduzir memória abaixo do uso, mantém a aplicação rodando).
- **Em execução** (API e worker locais, com o worker atrás de um `docker-socket-proxy` com a **mesma configuração do compose**, que é o caminho da VM):
  - **Faxina:** ao subir, removeu exatamente os 2 contêineres de releases substituídas que o banco indicava e deixou os 9 de release ativa.
  - **Aposentadoria:** dois deploys seguidos; a release antiga **continuou no ar durante a espera** (as duas coexistiram, inclusive 2 s depois do sucesso) e saiu depois dela; a imagem antiga foi podada com retenção 1 e a nova ficou; as releases ficaram `retired` e `active`.
  - **Limites ao vivo:** 1024 → 512 MiB, 1 → 0,5 CPU e 256 → 100 processos aplicados a um contêiner no ar, sem reiniciá-lo (o mesmo `StartedAt`), com memória e swap iguais e o evento no histórico do deploy.
  - **Excluir:** na hora, `GET` do projeto passa a 404 e as variáveis somem; depois, contêiner, rede e imagem saem; deploys, eventos, análises, releases (`removed`) e auditoria **ficam**; o nome volta a poder ser usado. Com deploy na fila, ele é cancelado; com deploy construindo, a exclusão é recusada com `deployment_in_progress` e o projeto continua.
  - **Painel:** operações, "Aplicar agora" e a zona de perigo funcionam. Um teste de interface achou um defeito grave: o botão **Cancelar** da zona de perigo **enviava o formulário e excluía o projeto**, porque o `Button` não declarava `type` e, dentro de um `<form>`, o padrão do navegador é `submit`. Corrigido na raiz (todo `Button` é `type="button"` por padrão; quem envia declara `submit`), com teste de regressão que falha sem a correção.

## Critérios de revisão

- Medir quanto tempo a faxina leva e quantos órfãos aparecem em uso real; ajustar o intervalo.
- Rever a retenção de duas imagens quando o rollback por botão for desenhado.
