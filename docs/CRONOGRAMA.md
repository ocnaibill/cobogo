# Cobogó — cronograma e prazos

> Documento de apoio para apresentar o calendário ao orientador. Preparado em 26 de outubro de 2026. Revisão de 26/10/2026: janela geral comprimida para terminar no **primeiro semestre de 2027** (até 30/06/2027), com os prazos reais do TCC2 informados pelo autor. Isso substitui a proposta anterior deste documento, que seguia as janelas mais longas do [documento mestre](PROJETO-MESTRE.md#21-roadmap-e-critérios-de-saída) v0.5 §21 (iam até abril/2028). O mestre ainda não foi atualizado com essa compressão — fazer isso é um dos próximos passos, não só deste arquivo.
>
> **Sobre a natureza destas datas:** os três marcos institucionais da seção 1 são **reais**, informados pelo autor. As datas de 2 e 3 (marcos de engenharia e de pesquisa) são uma **proposta de encaixe** para caber na janela pedida — ainda não validada pelo orientador. A seção 4 lista o que essa compressão aperta de verdade; não deve ser lida como um plano já resolvido.

## 1. Marcos institucionais do curso (datas reais)

| Data | Entrega | Status |
| --- | --- | --- |
| 01/10/2026 | Apresentação de tema e objetivos do TCC | Concluído |
| **27/10/2026** | Entrega do 2º relatório do TCC2 | **Amanhã** — ver §2 pra o que esse relatório pode reportar com o que já está pronto |
| 26/11/2026 | Apresentação de resultados parciais | A ~4 semanas; M2 (demonstração D0) precisa estar pronta antes disso pra ter resultado de verdade pra mostrar |
| A confirmar | Defesa final | Alvo redefinido para dentro do 1º semestre de 2027 (até 30/06/2027); data oficial ainda não informada pela secretaria/orientador |

## 2. O que o relatório de 27/10 já pode reportar

Com base no que está realmente pronto hoje (não é projeção):

- M1 concluído em 25/10/2026: fatia vertical completa em Go — análise explicável (HEU-01, HEU-02, HEU-05, camada de evidência de HEU-09), geração de Dockerfile, build real, contêiner, probe de readiness. Testado ponta a ponta com um repositório público real.
- Protótipo Node/TypeScript removido do `main`, preservado na tag `prototype-node` (DEC-22).
- Fichas completas de HEU-06 (decisão de banco de dados) e HEU-09 formalizadas no catálogo de heurísticas, com leitura de Cloud Native Buildpacks, Railpack e do paper binnacle (Henkel et al. 2020) fundamentando o formato.
- Documentação do projeto (mestre v0.5, motor de heurísticas, matriz de referências, pesquisa complementar) publicada e versionada.
- Em aberto, a reportar como próximo passo: inventário de hardware da VM e escolha formal dos 3 repositórios D0 (parte de M0 ainda não fechada).

## 3. Marcos de engenharia e pesquisa redefinidos (proposta para caber até 30/06/2027)

| Marco | Entrega | Janela original (mestre v0.5) | Janela redefinida | Status em 26/10/2026 |
| --- | --- | --- | --- | --- |
| M0 | Base de projeto | 2ª quinzena de outubro/2026 | até 31/10/2026 | Parcial — falta inventariar a VM e escolher os 3 repositórios D0 |
| M1 | Primeira fatia vertical em Go | Final de outubro a 1ª metade de novembro/2026 | — | **Concluído em 25/10/2026** |
| M2 | Demonstração D0 (React, autenticação, URL no homelab) | 2ª metade de novembro/2026 | até 20/11/2026 | Não iniciado — precisa terminar antes da apresentação de 26/11 |
| M3 | Entrega mínima (vídeo, limitações, evidências guardadas) | Início de dezembro/2026 | até 10/12/2026 | Não iniciado |
| M4 | MVP por perfis (Python, estático, variáveis guiadas, segredos) | Dezembro/2026 a março/2027 (4 meses) | dezembro/2026 a fevereiro/2027 (3 meses) | Não iniciado |
| M5 | Serviços e robustez (banco auxiliar, dashboard, atualização segura) | Abril a junho/2027 (3 meses) | fevereiro a março/2027 (6 semanas) | Não iniciado |
| M6 | Piloto e protocolo de avaliação | Julho a setembro/2027 (3 meses) | março a abril/2027 (6 semanas) | Não iniciado |
| M7 | Avaliação (técnica + estudo com participantes) | Outubro a dezembro/2027 (3 meses) | abril a maio/2027 (6 semanas) | Não iniciado |
| M8 | Análise e escrita final | Janeiro a março/2028 (3 meses) | maio a junho/2027 (6 semanas) | Não iniciado |
| M9 | Reserva e defesa | Abril/2028 | junho/2027, sem reserva sobrando | A confirmar |

A compressão não é proporcional em todas as fases: M4 (MVP por perfis) manteve a maior fatia porque é onde está o volume de código ainda não escrito; M5–M8, que eram 3 meses cada no plano de 18 meses, viraram ~6 semanas cada.

## 4. O que essa compressão aperta de verdade

Isto não é uma lista de problemas pra ignorar — é o que precisa de decisão consciente, de preferência com o orientador, antes de tratar a tabela acima como compromisso:

- **Estudo com participantes (M7).** O mestre já propunha 6–10 participantes com consentimento, anonimização e regras da instituição (§19.2) — processos que em geral têm prazo próprio de aprovação, difícil de encaixar numa janela de 6 semanas que também inclui rodar o estudo. Se o comitê de ética/CEP da instituição tiver prazo mínimo, isso pode furar a janela de M6–M7 sozinho. Vale perguntar ao orientador se dá pra iniciar os trâmites já em paralelo a M4/M5, não esperar M6.
- **Corpus de avaliação (§19.1).** A proposta original é 18 repositórios elegíveis (9 dev/calibração + 9 teste) mais 6 casos negativos. Com M6 em 6 semanas, dá pra manter o número ou é melhor reduzir e relatar isso como limitação explícita? O mestre já trata os números como "propostas a discutir no piloto", então reduzir é compatível com o que já está escrito — só precisa de decisão.
- **Escrita (M8).** Seis semanas pra analisar resultados e escrever a versão final é pouco se a escrita só começar ali. O mestre já recomenda não deixar a escrita pro fim (§21: "a escrita não começa no fim") — isso fica mais crítico ainda nesta janela comprimida. Vale tratar cada marco (M2 a M7) como gerando um trecho escrito da monografia, não só código/dados.
- **Sem reserva em M9.** O plano original tinha um mês de reserva antes da defesa; a compressão pra junho/2027 elimina essa folga. Qualquer atraso em M4–M7 empurra direto pra cima da defesa.
- **Experimento de modelos de decisão (§19.4).** Já era trilha experimental separada, sem bloquear o MVP. Com a janela apertada, é a candidata mais natural a virar resultado negativo documentado ("não deu tempo de validar", com a base heurística sólida) em vez de comparação H+D completa — o que o próprio mestre já trata como desfecho aceitável (§9.5 do motor: "se heurísticas resolverem... manter o resultado negativo como contribuição").

## 5. Ordem de corte se um marco ficar em risco

Direto do §21 do mestre, continua válido com a janela nova:

1. Primeiro cortar: polimento visual e atualização de releases.
2. Manter: um perfil só (Node), hostnames preparados manualmente.
3. **Nunca cortar:** análise explicável, build real, readiness, logs e uma URL funcional, verificação de sucesso, proteção do plano de controle.
4. Python e bancos auxiliares já pertencem a M4 — adiar não é corte, é o que já estava planejado pra depois.

## 6. O que precisa ser confirmado com o orientador

- Se o alvo de 30/06/2027 pra defesa é realista pra ele, ou se "primeiro semestre de 2027" tem alguma flexibilidade que não está clara aqui.
- Prazo do processo de ética/CEP pra estudo com participantes (M7), e se pode começar antes de M6.
- Se cabe reduzir o corpus de avaliação (§19.1) e o desenho do estudo com participantes (§19.2) pra caber na janela, mantendo os critérios de validade já documentados.
- Se o experimento de modelos de decisão (Jev/Laya/Julia-1) deve ser tentado nesta janela ou documentado como trabalho futuro.
- Disponibilidade semanal real do autor nos próximos 8 meses — é o número que mais muda se essa tabela é factível.
