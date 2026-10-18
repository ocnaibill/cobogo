# Cobogó — pesquisa sobre heurísticas, evidências e decisão assistida

> Versão 0.1 · Consulta em 17 de outubro de 2026.
>
> Acompanha a [especificação do motor](../MOTOR-DE-HEURISTICAS.md) e amplia a [matriz dos dez PDFs](../REFERENCIAS-COMENTADAS.md). Reunião de referências com leitura orientada; não é revisão sistemática nem levantamento exaustivo. Links para textos completos não significam leitura integral de cada artigo.

## 1. Perguntas que orientam a pesquisa

| Questão | O que procurar | Entrega para o Cobogó |
| --- | --- | --- |
| Como converter arquivos em requisitos de execução? | Análise estática de artefatos DevOps, detection, providers e planos intermediários | Catálogo de extratores e limites por formato |
| Como combinar sinais incompletos ou contraditórios? | Sistemas baseados em regras, fontes de supervisão fraca, proveniência e conflitos | Registro de evidências e resolução sem somas arbitrárias |
| Quando o sistema deve abster-se? | Selective classification, reject option, risco e cobertura | Estados de insuficiência/conflito e métricas |
| Quando confiar em um escore? | Calibração, mudança de domínio e autoavaliação | Protocolo de calibração e critérios de uso |
| Como pedir uma informação que não está no código? | Interação humano-IA, desambiguação e controle do usuário | Perguntas curtas, justificadas e corrigíveis |
| Como ligar aplicação e banco? | Service binding e separação entre provisionamento e conexão | Contrato de vínculo independente da detecção |
| O modelo agrega algo às regras? | Avaliação de decisões estruturadas e efeitos downstream | Comparação H/D/H+D com referências independentes |

**Hipótese a investigar:** o apoio semântico pode ser útil quando o papel de uma dependência está descrito em linguagem natural ou disperso entre evidências. Falta de informação sobre a preferência do usuário continua exigindo pergunta. Nenhuma fonte desta seleção demonstra que Jev, Laya ou Julia-1 resolve esse caso no Cobogó.

## 2. Artigos já fornecidos: como reaproveitar

Os fichamentos completos, páginas físicas consultadas e limitações estão na matriz anterior. Não repetir a mesma leitura sem uma pergunta específica.

| Referência | Pergunta para a releitura | Aplicação proposta |
| --- | --- | --- |
| REF-02 — Henkel et al., *Learning from, Understanding, and Supporting DevOps Artifacts for Docker* (ICSE 2020) | Como representar estrutura e regras sem tratar todo arquivo como texto indiferenciado? | Fichas de heurísticas, formatos reconhecidos e proveniência. [DOI](https://doi.org/10.1145/3377811.3380406) |
| REF-10 — Henkel et al., *Shipwright: A Human-in-the-Loop System for Dockerfile Repair* (ICSE 2021) | Quais falhas exigem entendimento humano e como avaliar reparo/sugestão? | Separar interpretação, intervenção e sucesso. [Preprint dos autores](https://arxiv.org/abs/2103.02591) |
| REF-04 — Lyu et al., *Automatic Dockerfile Generation with Large Language Models* (ICSE 2026) | Qual contexto e qual verificação sustentam a geração/refinamento? | Comparação com seleção finita de alternativas; não copiar taxas entre corpora. [DOI](https://doi.org/10.1145/3744916.3773162) |
| REF-03 — Wu et al., *Towards Understanding Docker Build Faults in Practice: Symptoms, Root Causes, and Fix Patterns* (2025) | O sintoma observado permite inferir causa? | Taxonomia de falhas e possível experimento alternativo de diagnóstico. [DOI](https://doi.org/10.1145/3715757) |
| REF-09 — Zhang et al., *One Size Does Not Fit All* (ESEC/FSE 2018) | Quais necessidades e trade-offs variam entre usuários? | Avaliar perguntas e esforço, além de acurácia técnica. [DOI](https://doi.org/10.1145/3236024.3236033) |
| REF-01 — Sampaio, Picoli e Sampaio (2024) | O que já é automatizado e o que a pessoa informa? | Delimitar contribuição incremental de evidência/abstenção/vínculos |
| REF-07 — Martinez, Silva e Soares (2019) | Como aplicação e banco são encadeados no fluxo preparado? | Comparar integração previamente definida com descoberta e escolha de destino |

REF-05 permanece útil para falhas e definição de tempo; REF-06/08 para infraestrutura e orquestração. Não há necessidade de citar todos em cada capítulo.

## 3. Artigos metodológicos acrescentados

As aplicações ao Cobogó abaixo são interpretações de projeto, não conclusões dos autores sobre deploy.

### P-H01 — Selective Classification for Deep Neural Networks

**Autores/veículo:** Yonatan Geifman e Ran El-Yaniv. NeurIPS/NIPS 2017. **Prioridade:** alta.

**Consultado:** página oficial dos anais e resumo do preprint. O trabalho estuda classificadores que rejeitam parte dos casos para controlar o compromisso entre erro e cobertura, com avaliação em classificação de imagens.

**Aplicação:** medir decisões aceitas, erros entre elas e abstenção. **Limite:** garantias e resultados daquele método não se transferem automaticamente a um classificador de configuração, especialmente com mudança de domínio e pequena amostra.

**Leitura a aprofundar:** definição de risco seletivo, procedimento de seleção e pressupostos das garantias; comparar com o protocolo proposto sem alegar implementação equivalente.

**Fontes:** [anais oficiais](https://proceedings.neurips.cc/paper/2017/hash/4a8423d5e91fda00bb7e46540e2b0cf1-Abstract.html), [preprint e acesso ao texto](https://arxiv.org/abs/1705.08500).

### P-H02 — On Calibration of Modern Neural Networks

**Autores/veículo:** Chuan Guo, Geoff Pleiss, Yu Sun e Kilian Q. Weinberger. ICML 2017, PMLR 70, pp. 1321–1330. **Prioridade:** alta.

**Consultado:** resumo e metadados oficiais. Investiga descalibração em redes e métodos de pós-processamento, incluindo temperature scaling, nos conjuntos avaliados.

**Aplicação:** não apresentar confiança como probabilidade de acerto sem verificar; reservar dados para calibração e reportar métricas. **Limite:** não fornece um limiar universal nem demonstra calibração de Jev/Laya/Julia-1 em repositórios. Um método que usa logits exige que o adaptador realmente os disponibilize.

**Leitura a aprofundar:** métricas, diagramas de confiabilidade, requisitos dos métodos e efeitos de amostras pequenas.

**Fontes:** [publicação e BibTeX](https://proceedings.mlr.press/v70/guo17a.html), [texto completo](https://proceedings.mlr.press/v70/guo17a/guo17a.pdf).

### P-H03 — Snorkel

**Título:** *Snorkel: Rapid Training Data Creation with Weak Supervision*. **Autores:** Alexander Ratner, Stephen H. Bach, Henry Ehrenberg, Jason Fries, Sen Wu e Christopher Ré. PVLDB 11(3), 269–282, 2017. DOI 10.14778/3157794.3157797. **Prioridade:** média.

**Consultado:** resumo do preprint e registro/trechos da publicação. Combina fontes de supervisão fraca para criação de dados de treinamento; fontes heurísticas podem ser ruidosas e dependentes.

**Aplicação:** organizar regras que podem discordar ou não cobrir um caso; registrar proveniência e evitar interpretar indícios repetidos como evidências independentes. **Limite:** o motor inicial não será Snorkel nem precisa aprender pesos de regras. Não usar regras que serão avaliadas como única referência correta do próprio teste.

**Leitura a aprofundar:** funções de rotulação, correlações e separação entre rótulo fraco e referência humana. A versão ampliada de periódico tem outra identificação; não misturar suas datas/metadados.

**Fontes:** [preprint](https://arxiv.org/abs/1711.10160), [texto da edição PVLDB](https://www.vldb.org/pvldb/vol11/p269-ratner.pdf), [DOI](https://doi.org/10.14778/3157794.3157797).

### P-H04 — Guidelines for Human-AI Interaction

**Autores/veículo:** Saleema Amershi et al. CHI 2019. DOI 10.1145/3290605.3300233. **Prioridade:** alta para o fluxo de perguntas.

**Consultado:** página da publicação e apresentação das diretrizes pelos autores. Propõe e avalia 18 diretrizes para sistemas com IA, contemplando limites, correção, desambiguação e controle da interação.

**Aplicação:** exibir capacidade e limite, permitir corrigir inferência e explicar por que uma pergunta é necessária. O painel deve ajudar a escolher um destino de banco sem pressupor conhecimento de infraestrutura. **Limite:** diretriz de interface não demonstra redução de esforço no Cobogó; esse efeito exige avaliação própria.

**Leitura a aprofundar:** diretrizes de correção, incerteza e explicação, selecionando exemplos aplicáveis a TEL-04/05/09.

**Fontes:** [publicação](https://www.microsoft.com/en-us/research/publication/guidelines-for-human-ai-interaction/), [apresentação pelos autores](https://www.microsoft.com/en-us/research/blog/guidelines-for-human-ai-interaction-design/), [texto completo](https://www.microsoft.com/en-us/research/wp-content/uploads/2019/01/Guidelines-for-Human-AI-Interaction-camera-ready.pdf).

### P-H05 — Language Models (Mostly) Know What They Know

**Autores/identificação:** Saurav Kadavath et al. Preprint arXiv:2207.05221, 2022. **Prioridade:** complementar.

**Consultado:** resumo e metadados do preprint. Estuda autoavaliação e previsões de correção/conhecimento em modelos de linguagem; relata limites de calibração em novas tarefas.

**Aplicação:** motivar teste da confiabilidade de um escore, em vez de confiar no nome do campo. **Limite:** modelos, tarefas e procedimentos diferem dos classificadores de decisão candidatos. Não prova que perguntar “você tem certeza?” corrige o problema nem constitui avaliação de Jev, Laya ou Julia-1.

**Leitura a aprofundar:** distinção entre julgar uma resposta e prever que se conhece uma resposta; transferência de calibração.

**Fonte:** [preprint e acesso ao texto](https://arxiv.org/abs/2207.05221).

### P-H06 — Jev for Scientific Decisions: Evaluating Semantic Choices and Their Consequences

**Identificação:** Deng et al., arXiv:2609.24965v3, 2026; artigo fornecido anteriormente pelo autor do Cobogó. **Prioridade:** alta para desenho experimental.

**Consultado:** texto HTML, em especial discussão e limitações. Separa escolhas semânticas e consequências calculadas; um resultado final pode ocultar erro em decisão intermediária. O conjunto preparado e recorrente não estabelece transferência para deploy e não cobre adequadamente respostas de insuficiência/conflito nas decisões principais.

**Aplicação:** avaliar interpretação, plano e execução separadamente; acrescentar casos ambíguos e de abstenção. **Limite:** o artigo não demonstra que o modelo pode deduzir intenção operacional ausente no contexto.

**Fonte:** [versão consultada](https://arxiv.org/html/2609.24965v3). Preservar a versão ao citar; conferir metadados completos antes da bibliografia final.

## 4. Fontes técnicas primárias para consultar

São mecanismos e contratos de software, distintos dos artigos empíricos. Foram consultadas as páginas abaixo; não foi executada comparação entre ferramentas.

| ID | Fonte | Ideia aproveitável e limite |
| --- | --- | --- |
| T-H01 | Cloud Native Buildpacks: [detect](https://buildpacks.io/docs/for-platform-operators/concepts/lifecycle/detect/) e [build plan](https://buildpacks.io/docs/for-buildpack-authors/concepts/build-plan/) | Separar detecção e plano de dependências requeridas/providas. Dependências de build não equivalem à intenção de criar serviço de dados; o Cobogó não herda automaticamente a execução de scripts de detecção |
| T-H02 | Railpack: [arquitetura](https://railpack.com/architecture/overview/) | Análise por providers e plano intermediário separado da geração/execução. Comparar extração, overrides e fronteira do plano; não afirmar ganho sem executar baseline equivalente |
| T-H03 | [Service Binding for Kubernetes 1.0.0](https://servicebinding.io/spec/core/1.0.0/) | Distinguir serviço, informação de conexão e workload consumidor. Inspiração conceitual para vínculo explícito; não implica adotar Kubernetes ou declarar conformidade com a especificação |

A pergunta de leitura é sempre “qual responsabilidade este mecanismo resolve?”. Detector de build, vínculo de conexão e provisionador têm responsabilidades diferentes. O motor do Cobogó deverá compô-las mantendo intenção e permissão explícitas.

## 5. Modelos candidatos

| ID | Fonte primária consultada | Consequência para o experimento |
| --- | --- | --- |
| M-H01 | Jev: [documentação TypeSafe](https://docs.typesafe.ai/introduction) | Interface com estado e perguntas tipadas permite candidato de escolha finita. Registrar versão/semântica do retorno; acesso externo depende de orçamento e política de dados |
| M-H02 | [Laya — cartão do modelo](https://huggingface.co/convaiinnovations/laya) | Variantes e idiomas devem ser fixados. O próprio cartão relata limitações zero-shot e sobreconfiança; resultado ajustado em outro benchmark não é resultado em deploy |
| M-H03 | [Julia-1 — cartão do modelo](https://huggingface.co/SupersonicLabs/Julia-1) | Escolha nativa entre 2–20 opções e possibilidade de CPU tornam viável investigar o ensaio de seis classes. Medir memória/latência/acerto no hardware real; não presumir certeza ou equivalência entre runtimes |

Cartões são documentação dos produtores, não validação independente. Preservar revisão, runtime, idioma, tamanho de contexto e política de truncamento. Não transformar uma tabela comercial entre modelos em evidência de superioridade no Cobogó.

## 6. Estratégia de busca reproduzível

### 6.1 Registro da busca inicial realizada

Busca web dirigida em 17/10/2026, seguida de consulta a fontes primárias. Além dos links conhecidos fornecidos pelo usuário, foram usadas consultas como:

- `site.proceedings.mlr.press selective classification deep neural networks Geifman El Yaniv 2017 reject option`
- `site.proceedings.mlr.press On Calibration of Modern Neural Networks Guo 2017`
- `site.arxiv.org Language Models Mostly Know What They Know Kadavath 2022`
- `site.buildpacks.io docs detection build plan requires provides`
- `site.vldb.org Snorkel Rapid Training Data Creation with Weak Supervision Ratner 2017`
- `site.microsoft.com research Guidelines for Human AI Interaction Amershi 2019`

A busca inicial é direcionada por conceitos conhecidos e tem viés de seleção. Não foi feita varredura das bases abaixo, nem contagem completa de resultados/deduplicação. P-H01 foi confirmado nos anais NeurIPS, embora a consulta inicial tivesse filtro PMLR inadequado para esse artigo. Isso reforça que o registro de busca não equivale a cobertura sistemática de uma base.

### 6.2 Próxima rodada planejada

Pesquisar em ACM Digital Library, IEEE Xplore, SpringerLink, arXiv e SBC OpenLib; usar índice acadêmico para descobrir trabalhos e consultar a publicação original para extrair resultados. Fazer encadeamento de referências de binnacle, Shipwright, DRAFT e estudos de abstenção. As strings abaixo são **propostas, ainda não executadas como busca de revisão**:

| Eixo | String inicial |
| --- | --- |
| Inferência de deploy | `("Dockerfile" OR "container build" OR "deployment configuration") AND (inference OR detection OR heuristic OR "static analysis")` |
| Dependências de serviço | `("service dependency" OR "database dependency") AND (repository OR configuration) AND (inference OR detection)` |
| Abstenção | `("selective classification" OR "reject option" OR abstention) AND (calibration OR "distribution shift")` |
| Decisão assistida | `("human-in-the-loop" OR "mixed initiative") AND (configuration OR deployment OR disambiguation)` |
| Vínculo e intenção | `("service binding" OR "service provisioning") AND (configuration OR intent)` |
| Busca nacional | `("implantação automática" OR "deploy automático") AND (heurísticas OR inferência OR contêineres)` |

Incluir estudos que descrevam mecanismo, evidência, método de avaliação ou contratos diretamente úteis. Excluir duplicatas de versão, menções superficiais a Docker e comparações sem método verificável; manter trabalhos conceituais relevantes identificados como tais. Documentação de ferramentas entra em trilha separada. Não descartar resultados negativos ou trabalhos que antecipem a ideia do projeto.

Para cada candidato, registrar: consulta/base/data; título; autores; ano/veículo; versão; DOI/URL; status de acesso/leitura; tarefa; entrada/saída; mecanismo; corpus; métricas; limitações; decisão de inclusão e motivo. Isso permite transformar esta triagem em revisão mais rigorosa sem inventar contagens retroativas.

## 7. Ordem de leitura e entregas

1. **Motor:** REF-02 + T-H01/T-H02 → completar fichas HEU-01/02/06/09 e contrato do plano.
2. **Abstenção e confiança:** P-H01/P-H02 → definir métricas, denominadores e limites de uso de scores.
3. **Interação e vínculo:** P-H04 + T-H03 → simular as perguntas para três intenções de banco.
4. **Experimento:** P-H06 + cartões → fixar uma pergunta, modelos elegíveis, partições e registro de falha.
5. **Aprofundamento opcional:** P-H03/P-H05 → investigar conflitos e calibração, se o piloto indicar necessidade.

Após a leitura, cada ideia incorporada deve apontar a uma regra, contrato, tela ou métrica. Uma referência que não ajuda a formular problema, método ou comparação pode permanecer como contexto, sem virar obrigação de implementação.
