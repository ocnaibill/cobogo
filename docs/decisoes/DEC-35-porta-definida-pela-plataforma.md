# DEC-35 — Porta definida pela plataforma quando o app lê `PORT`

> **Estado:** decidida com o autor em 20/11/2026; **implementada**. Primeira parte do MVP (primeira leva: DEC-35 a DEC-39).
>
> **Escopo:** o que a HEU-05 faz quando o código de execução lê `PORT`, como isso aparece no plano e no painel, e como se corrige quando o app não cumpre o contrato.
>
> **Relaciona-se com:** RN-02 e RN-03, HEU-05 (motor de heurísticas), DEC-23 (contrato do motor), DEC-24 (overrides e variáveis), DEC-26 (readiness).

## Contexto

Até aqui a HEU-05 tratava `process.env.PORT` lido em código de execução como pista: a porta ficava pendente e o deploy parava para perguntar, porque a RN-02 proíbe apresentar uma porta padrão como comprovada. Na prática, o worker já entrega `PORT` ao contêiner com a porta do plano (DEC-24). Para um app que lê `PORT`, perguntar o número é perguntar algo que a plataforma decide: qualquer valor serve, desde que seja o mesmo na variável, na sonda e na rota. É o que o Heroku e as plataformas parecidas fazem.

Isso apareceu ao escolher os repositórios do MVP: o exemplo da Heroku e o `caderno` leem `PORT` e paravam só por causa dessa pergunta.

## Decisão

1. **Contrato de `PORT` em código de execução → a plataforma escolhe.** A HEU-05 propõe a porta **8080** com a origem nova **`platform`** ("definida pela plataforma"). A evidência citada é a leitura de `PORT`. O worker continua injetando `PORT` com a porta do plano; a sonda e a rota usam o mesmo número.
2. **Sem contrato, nada muda.** `PORT` só em teste ou ferramenta, ou nenhuma referência: a porta continua pendente e o deploy pergunta.
3. **Precedência** (RN-03): override > declaração > **plataforma** > inferência > padrão. A porta da plataforma vem de um contrato que o próprio app declara, então fica acima de inferência e de padrão; um override do usuário sempre vence.
4. **RN-02 ganha uma frase:** quando o código de execução lê `PORT`, a plataforma escolhe o número e o injeta; isso é contrato, não porta padrão. A porta padrão continua proibida como prova.
5. **Quando o app não cumpre o contrato** (lê `PORT` em algum lugar, mas escuta em outra porta ou só em `127.0.0.1`), a sonda falha. Se o contêiner seguiu rodando e o log não aponta outra causa, o diagnóstico diz que a porta é a suspeita, cita onde o código lê `PORT` e indica o campo `port`. O painel oferece **"novo deploy com esta porta"**, que manda a porta como override (a API já aceita `overrides` num deploy novo).
6. **Versões:** a HEU-05 passa para a versão 0.3 e o catálogo do perfil para `p-node-0.4`, porque o mesmo repositório agora produz outro resultado.

## Alternativas consideradas

- **Inferir a porta do valor de reserva no código** (`process.env.PORT || 5006`): o número é só o que o app usa fora de uma plataforma; adotá-lo seria apresentar um padrão como prova, exatamente o que a RN-02 proíbe. E é desnecessário, porque o app lê `PORT`.
- **Continuar perguntando:** pergunta sem informação nova; aumenta as intervenções que a pesquisa mede, sem ganho de segurança.
- **Escolher porta aleatória por projeto:** dentro do contêiner não há disputa de porta (cada app tem a sua rede); um número fixo é mais fácil de explicar e de depurar.

## Consequências e limites

- Apps que leem `PORT` publicam sem perguntas; a sonda continua sendo a prova de que o contrato vale.
- Um app que lê `PORT` só num caminho de código (por exemplo, num script auxiliar marcado como execução) recebe a porta da plataforma e falha na sonda; o diagnóstico do item 5 cobre esse caso, mas custa o tempo da sonda (120 s) antes de pedir a porta.
- A detecção do contrato é a mesma de antes (referência estática a `PORT` em código de execução); não interpreta `Bun.serve`, `app.listen` nem frameworks com porta própria.

## Verificação

- Testes: a HEU-05 propõe 8080 com origem `platform` e cita só a leitura de `PORT` em código de execução, nunca o valor de reserva (`PORT || 5006`); override vence a porta da plataforma; `PORT` só em teste continua pendente; o diagnóstico da porta e a origem da evidência têm testes próprios.
- **Em execução** (API e worker locais, exemplo da Heroku): o deploy foi do pedido ao ar **sem nenhuma pergunta** (porta 8080, origem `platform`, sonda 200). Com um comando de início que ignora `PORT` e escuta na 3000, a sonda falhou em 2 min e o diagnóstico disse "o código lê `PORT` em `index.js:4`, então a plataforma definiu 8080, mas nada respondeu nela", com `field: port`. O botão "novo deploy com esta porta" do painel pediu um deploy com o override `port: 5006`, que ficou no ar.

## Critérios de revisão

- Revisar a porta fixa se algum perfil novo exigir outro número ou se a sonda passar a testar mais de uma porta.
- Medir no corpus quantos apps que leem `PORT` escutam de fato nela; se o item 5 for frequente, reconsiderar.
