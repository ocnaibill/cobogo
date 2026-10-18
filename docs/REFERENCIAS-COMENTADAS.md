# Cobogó — matriz de referências e trabalhos relacionados

> Versão 0.1 · 17 de outubro de 2026 · Triagem inicial de dez PDFs fornecidos pelo autor.
>
> Complementa o [documento mestre](PROJETO-MESTRE.md). Não é uma revisão sistemática nem prova de originalidade. As avaliações de relevância são interpretações para o recorte atual do Cobogó.

Para os artigos metodológicos acrescentados posteriormente e as fontes sobre detecção/vínculos de serviços, consultar [Pesquisa sobre heurísticas, evidências e decisão assistida](pesquisa/HEURISTICAS-REFERENCIAS.md). Esta matriz mantém o recorte dos dez PDFs originais.

## 1. Como foi feita a triagem

Foram identificados os dez arquivos e consultadas suas seções de problema, abordagem, avaliação/resultados e limitações pertinentes. O texto de Wu et al. (2020) está rasterizado no arquivo recebido: foi necessário OCR, com conferência visual da página de resultados de tempo de correção. Não foram alterados os PDFs nem copiados seus conteúdos integrais para o repositório.

Os números de página abaixo são **páginas físicas do PDF**, começando em 1. Podem diferir da numeração editorial: por exemplo, REF-05 p. 3 corresponde à página 78 do artigo, e REF-08 tem uma capa HAL adicional. Os nomes dos arquivos identificam as cópias consultadas, que não são distribuídas neste repositório. DOIs e links de publicação, quando disponíveis, permitem localizar os trabalhos; não se presume acesso aberto ao destino.

**Critério de classificação:** proximidade do problema e do mecanismo com o Cobogó, natureza da evidência e utilidade para o protocolo. Artigo de implementação demonstra viabilidade no cenário apresentado; não comprova sozinho ganho generalizado de produtividade. Estudo observacional mostra associações, não o efeito causal de uma ferramenta nova.

## 2. Matriz rápida

| ID | Trabalho | Papel no TCC | Prioridade e cuidado principal |
| --- | --- | --- | --- |
| REF-01 | Sampaio, Picoli e Sampaio (2024) — Automatização de Deploy de Aplicações em Contêineres Docker em Nuvem | Comparação direta com o produto: interpretação, Dockerfile e deploy | Alta: já cobre parte central da ideia; discussão reconhece ausência de testes com usuários e soluções reais |
| REF-02 | Henkel et al. (2020) — Learning from, Understanding, and Supporting DevOps Artifacts for Docker | Fundamento para análise e regras de qualidade dos Dockerfiles | Alta: qualidade/análise de artefato não equivale a deploy funcional |
| REF-03 | Wu et al. (2025) — Towards Understanding Docker Build Faults in Practice: Symptoms, Root Causes, and Fix Patterns | Taxonomia de falhas e referência para diagnóstico | Alta: distinguir sintoma, causa e reparo; assistência baseada em conhecimento tem avaliação limitada |
| REF-04 | Lyu et al. (2026) — Automatic Dockerfile Generation with Large Language Models | Comparação direta com geração automática e validação de build | Alta: DRAFT já gera/repara Dockerfiles; LLM gerador difere de modelo de decisão |
| REF-05 | Wu et al. (2020) — An Empirical Study of Build Failures in the Docker Context | Motivação empírica e operacionalização de falha/tempo | Alta: tempo entre builds é proxy, não tempo ativo medido de desenvolvedor |
| REF-06 | Noll e Pretto (2020) — Implementação de Infraestrutura como Código para Provisionamento e Deploy de Aplicações | Automação da infraestrutura, aplicação/banco e recuperação | Apoio: ambiente e aplicação preparados; tempo de provisionamento não é tempo até deploy de repo arbitrário |
| REF-07 | Martinez, Silva e Soares (2019) — Implantação automatizada de sítios institucionais com uso de Docker, Git e Jenkins | Exemplo concreto de aplicação + banco + proxy + verificação HTTP | Apoio próximo da integração: plataforma padronizada Joomla!, sem inferência multistack |
| REF-08 | Leite et al. (2013) — Um Middleware para Encenação Automatizada de Coreografias de Serviços Web em Ambientes de Computação em Nuvem | Orquestração, composição e separação das etapas de medição | Contexto: sistema distribuído e múltiplas VMs ampliam o recorte além do MVP |
| REF-09 | Zhang et al. (2018) — One Size Does Not Fit All: An Empirical Study of Containerized Continuous Deployment Workflows | Necessidades de desenvolvedores, trade-offs e avaliação mista | Alta para método e público: dados históricos e associações não demonstram superioridade atual de provedor |
| REF-10 | Henkel et al. (2021) — Shipwright: A Human-in-the-Loop System for Dockerfile Repair | Falhas, regras de reparo e limite da automação | Comparação direta na etapa de diagnóstico/reparo, não geração inicial ou deploy completo |

## 3. Fichamentos orientados ao projeto

### REF-01 — Automatização de Deploy de Aplicações em Contêineres Docker em Nuvem

**Identificação:** Caio Sampaio; Guilherme Picoli; Gustavo Scalabrini Sampaio. Universidade Presbiteriana Mackenzie, 2024. Trabalho acadêmico em formato de artigo; natureza institucional/catalogação formal a confirmar antes de fechar a bibliografia.

**Abordagem e evidência:** sistema com frontend e microsserviços em .NET/C#, incluindo obtenção de repositório GitHub, interpretação, geração de Dockerfile, criação de imagem e entrega. A seção de interpretação descreve Python 3.9 e C#/.NET; a demonstração usa uma API .NET de exemplo no DigitalOcean. A pessoa configura portas, redes e ambiente, podendo editar o Dockerfile.

**Onde conferir:** PDF pp. 6–9, requisitos e interpretação; pp. 10–14, demonstração; pp. 14–15, discussão e conclusão. A discussão reconhece falta de avaliação com usuários e soluções reais. Não adotar como evidência comprovada a afirmação de facilidade de uso derivada apenas da quantidade de passos.

**Uso recomendado:** comparação explícita de responsabilidades de configuração e do caminho repositório → execução. Para o Cobogó, investigar se descoberta de requisitos, abstenção, vínculos de serviços e avaliação com pessoas oferecem contribuição incremental mensurável. Isso é posicionamento proposto, não lacuna de literatura já demonstrada.

**Arquivo consultado:** `24-2024.2 -Caio Sampaio.pdf`.

### REF-02 — Learning from, Understanding, and Supporting DevOps Artifacts for Docker

**Identificação:** Jordan Henkel; Christian Bird; Shuvendu K. Lahiri; Thomas Reps. ICSE 2020. DOI: [10.1145/3377811.3380406](https://doi.org/10.1145/3377811.3380406).

**Abordagem e evidência:** binnacle, ferramenta de parsing em fases, mineração e aplicação de regras sobre Dockerfiles. Usa grande corpus de repositórios/Dockerfiles e um conjunto de referência escrito por especialistas. As regras incluem aspectos sintáticos e semânticos; o artigo também identifica regras que não generalizam.

**Onde conferir:** PDF pp. 1–2, objetivo e corpus; pp. 8–10, avaliação de parsing, mineração e aplicação das regras. A frequência de violações é específica às regras e à amostra: não traduzir o resultado em “essa proporção de aplicações falha no deploy”. Evitar misturar números de versões web/preprint com a edição PDF usada.

**Uso recomendado:** fundamentar catálogo versionado de heurísticas, evidências, qualidade dos templates e limites de generalização. O Cobogó infere como construir/executar um projeto; binnacle analisa artefatos Docker. Há relação metodológica, mas tarefas e métricas diferem.

**Arquivo consultado:** `3377811.3380406.pdf`.

### REF-03 — Towards Understanding Docker Build Faults in Practice: Symptoms, Root Causes, and Fix Patterns

**Identificação:** Yiwen Wu; Yang Zhang; Tao Wang; Bo Ding; Huaimin Wang. *Proceedings of the ACM on Software Engineering*, v. 2, FSE, artigo FSE040, julho de 2025. DOI: [10.1145/3715757](https://doi.org/10.1145/3715757).

**Abordagem e evidência:** análise manual de 474 ocorrências: 255 issues GitHub e 219 posts de Stack Overflow/Docker Forum. A classificação separa 15 sintomas, 23 causas e 35 padrões de correção, incluindo Dockerfile e Compose. O texto apresenta DBF-Assistant com base de conhecimento e modelos para auxiliar identificação.

**Onde conferir:** PDF pp. 5–6, seleção/anotação; pp. 6–16, taxonomias; pp. 18–19, avaliação do assistente e ameaças. A avaliação de identificação usa 14 posts adicionais e comparação humana com sete participantes; não interpretar seus resultados como taxa geral de diagnóstico ou de reparo em produção.

**Uso recomendado:** inspirar e mapear as categorias de erro do Cobogó, mantendo “causa desconhecida” quando só houver sintoma. Se o piloto de modelos focar diagnóstico, esta é uma referência direta para rótulos e desenho de comparação. O DBF-Assistant usa recuperação/geração, não o mesmo mecanismo de escolha finita de Jev/Laya/Julia-1.

**Arquivo consultado:** `3715757.pdf`.

### REF-04 — Automatic Dockerfile Generation with Large Language Models

**Identificação:** Jun Lyu; He Zhang; Yusong Yuan; Lanxin Yang; Yue Li; Manuel Rigger. ICSE 2026. DOI: [10.1145/3744916.3773162](https://doi.org/10.1145/3744916.3773162).

**Abordagem e evidência:** DRAFT extrai contexto, gera Dockerfiles com LLM e os refina iterativamente a partir de documentação e erros; também investiga otimização. A avaliação principal usa 35 projetos em cinco linguagens e relata 28 gerações bem-sucedidas. O texto verifica artefatos com testes simples, além da construção da imagem.

**Onde conferir:** PDF pp. 1 e 3–5, proposta; pp. 6–9, seleção, métricas e resultados; pp. 10–11, ameaças e conclusão. A seleção exclui projetos que não puderam ser compilados/executados manualmente no ambiente de avaliação. Hardware, corpus, ferramentas e orçamento de reparo diferem do Cobogó; 80% nesse conjunto não é uma meta transferível automaticamente.

**Uso recomendado:** tratar como trabalho diretamente relacionado, discutir preparação do contexto e distinguir build, teste do artefato e aplicação web funcional. Não afirmar que a contribuição é simplesmente “gerar Dockerfiles automaticamente”. A escolha finita de um perfil pelo experimento do Cobogó também não equivale a gerar/reparar Dockerfiles por LLM.

**Arquivo consultado:** `3744916.3773162.pdf`.

### REF-05 — An Empirical Study of Build Failures in the Docker Context

**Identificação:** Yiwen Wu; Yang Zhang; Tao Wang; Huaimin Wang. MSR 2020. DOI: [10.1145/3379597.3387483](https://doi.org/10.1145/3379597.3387483).

**Abordagem e evidência:** estudo de 857.086 builds em 3.828 projetos públicos com dados de automated builds no Docker Hub. Relata 17,8% de builds falhos nesse conjunto. O tempo de correção é inferido por pares falha–sucesso; após filtrar intervalos superiores a 12 horas, a mediana apresentada é 44,2 minutos.

**Onde conferir:** PDF p. 2, seleção e frequência; p. 3 (editorial 78), definição do intervalo e resultados; p. 4, ameaças. O tempo mede o intervalo entre fim do build falho e início do build bem-sucedido considerado. Não é observação de 44,2 minutos de programação ativa. Dados de projetos que já usam Docker não descrevem diretamente a primeira conteinerização de iniciantes.

**Uso recomendado:** sustentar a relevância empírica das falhas e justificar métricas separadas de tempo total, espera, correção ativa e tentativas. Não calcular economia do Cobogó subtraindo seus tempos dessa mediana histórica.

**Arquivo consultado:** `An Empirical Study of Build Failures in the Docker Context.pdf`.

### REF-06 — Implementação de Infraestrutura como Código para Provisionamento e Deploy de Aplicações

**Identificação:** Jones Luis Noll; Fabrício Pretto. *Revista Destaques Acadêmicos*, v. 12, n. 4, pp. 296–315, 2020. DOI: [10.22410/issn.2176-3070.v12i4a2020.2760](https://doi.org/10.22410/issn.2176-3070.v12i4a2020.2760).

**Abordagem e evidência:** provisionamento com Terraform/Ansible e fluxo DevOps envolvendo VMs, Kubernetes, aplicação Django e PostgreSQL. A aplicação de notas é usada para validar a infraestrutura. São apresentadas dez sessões de execução, com média de 18 minutos e 32 segundos para criar a estrutura proposta.

**Onde conferir:** PDF pp. 16–18, aplicação, testes e início das considerações. A medição é sobre a infraestrutura definida pelos autores, sem comparação experimental equivalente de tempo ativo manual. Repetir scripts dez vezes não corresponde a dez projetos diferentes.

**Uso recomendado:** fundamentar infraestrutura como código, provisionamento, integração com banco e recuperação. O Cobogó parte de um host preparado e infere configuração por repositório; não inclui todo esse provisionamento no tempo principal. Estratégias operacionais do artigo, inclusive backup, devem ser avaliadas com o modelo de segurança atual antes de qualquer adoção.

**Arquivo consultado:** `destaques,+Gerente+da+revista,+2760.pdf`.

### REF-07 — Implantação automatizada de sítios institucionais com uso de Docker, Git e Jenkins

**Identificação:** João G. A. Martinez; Gérson B. da Silva; Diogo Soares. WTICIFES, capítulo XXI, pp. 128–132, junho de 2019; UFAM.

**Abordagem e evidência:** relato de implantação padronizada de sites Joomla!, com repositório, job Jenkins, criação/configuração de banco, DNS e Traefik. O fluxo de teste inicia contêineres da aplicação e MySQL, faz requisição HTTP e então publica a imagem em produção.

**Onde conferir:** PDF pp. 1–2, cenário e método; pp. 3–4, pipeline e roteamento; pp. 4–5, benefícios e conclusão. Trata de uma stack preparada e padronizada, com benefícios relatados, sem estudo controlado de produtividade ou inferência de múltiplas stacks.

**Uso recomendado:** exemplo particularmente próximo de interligar aplicação, banco, proxy e verificação HTTP. Pode apoiar a justificativa da integração do Cobogó; não sustenta que dependência de banco possa ser inferida corretamente em qualquer repositório. Não copiar configuração legada como recomendação atual de segurança.

**Arquivo consultado:** `Ebook-WTICIFES-pages-128-132.pdf`.

### REF-08 — Um Middleware para Encenação Automatizada de Coreografias de Serviços Web em Ambientes de Computação em Nuvem

**Identificação:** Leonardo Leite; Nelson Lago; Marco A. Gerosa; Fabio Kon. SBRC 2013. Versão depositada em [HAL hal-00861046v1](https://inria.hal.science/hal-00861046v1).

**Abordagem e evidência:** CHOReOS Enactment Engine, middleware extensível para implantar composições de serviços em nuvem. Avalia tempo de encenação, separando criação dos nós, implantação e invocação e variando carga/recursos.

**Onde conferir:** PDF p. 2, proposta; pp. 11–14, experimento, tabelas e conclusão. O texto distingue variação de provisionamento externo e efeitos da carga; não se trata de inferir Dockerfile ou de avaliar painel para desenvolvedor individual.

**Uso recomendado:** contexto de orquestração e inspiração metodológica para tempos por etapa, medianas/variabilidade e atribuição de falha à infraestrutura. Não é necessário trazer sua arquitetura distribuída para o MVP de um único host. Relacionar pela responsabilidade de integração, não apenas pela palavra “deploy”.

**Arquivo consultado:** `ee_sbrc.pdf`.

### REF-09 — One Size Does Not Fit All: An Empirical Study of Containerized Continuous Deployment Workflows

**Identificação:** Yang Zhang; Bogdan Vasilescu; Huaimin Wang; Vladimir Filkov. ESEC/FSE 2018. DOI: [10.1145/3236024.3236033](https://doi.org/10.1145/3236024.3236033).

**Abordagem e evidência:** estudo de métodos mistos sobre fluxos de continuous deployment com contêineres: survey e análise quantitativa de projetos/builds. A etapa de survey relata 168 respostas e explora motivações, barreiras e necessidades; o texto discute trade-offs entre configuração, simplicidade, manutenção, experiência e desempenho.

**Onde conferir:** PDF pp. 1–3, questões e método; pp. 8–10, resultados, implicações e ameaças. As diferenças observadas entre workflows são associações em uma amostra histórica; não são experimento randomizado nem avaliação dos serviços atuais.

**Uso recomendado:** sustentar os dois perfis de público e a escolha de combinar eficácia técnica com percepção de uso. Aproveitar perguntas e categorias como insumo, sem importar um instrumento como se já estivesse validado para o Cobogó. Reforça que uma única solução pode não servir igualmente a todos os projetos.

**Arquivo consultado:** `fse18cd.pdf`.

### REF-10 — Shipwright: A Human-in-the-Loop System for Dockerfile Repair

**Identificação:** Jordan Henkel; Denini Silva; Leopoldo Teixeira; Marcelo d’Amorim; Thomas Reps. ICSE 2021. O PDF fornecido é uma versão de manuscrito; conferir paginação/DOI da edição final ao fechar a bibliografia.

**Abordagem e evidência:** usa embeddings BERT de logs e clustering para organizar falhas; supervisão humana produz padrões de reparo e o sistema aplica reparos/sugestões. Avalia Dockerfiles, cenários históricos e submissões de correção a projetos reais.

**Onde conferir:** PDF pp. 1–3, proposta, causas e preparação; pp. 8–10, avaliação de reparos/sugestões e generalização. Sugestão aplicável, reparo automático, patch equivalente ao humano e PR aceito são resultados diferentes; não somar suas taxas nem usar uma como sucesso de deploy.

**Uso recomendado:** diagnóstico e abstenção do Cobogó; justificativa para preservar intervenção humana e registrar falhas de dependências externas. O uso de BERT para representação/agrupamento não é o mesmo que delegar a decisão de deploy a Jev. O Cobogó não precisa implementar reparo automático para dialogar com esse trabalho.

**Arquivo consultado:** `Henkel_ETAL_ICSE21.pdf`.

## 4. Como distribuir as referências na escrita

| Parte do TCC | Referências recomendadas | O que argumentar com elas |
| --- | --- | --- |
| Problema e motivação | REF-05, REF-03, REF-09 | Falhas e necessidades existem em contextos documentados; precisam de avaliação no público escolhido |
| Análise e qualidade de Dockerfiles | REF-02, REF-10 | Regras, evidências, falhas externas, intervenção e limites da automação |
| Trabalhos diretamente relacionados | REF-01, REF-04, REF-10 | Comparar entradas exigidas, inferência, geração/reparo, execução e avaliação |
| Integração e infraestrutura | REF-06, REF-07, REF-08 | Compor app/banco/rede, automatizar fases e separar custos de infraestrutura |
| Metodologia e métricas | REF-04, REF-05, REF-09, REF-03 | Build versus execução, esforço ativo versus intervalo, estudo misto e taxonomia de diagnóstico |
| Modelos de decisão | Artigo Jev fornecido + cartões/documentação dos modelos, no documento mestre | Escolha finita e consequências; não confundir com geração livre de código de REF-04 |

## 5. Posicionamento que os trabalhos sugerem

Não sustentar a novidade apenas em automatizar deploy, gerar Dockerfile, usar um painel ou interligar contêineres: a seleção contém exemplos anteriores dessas responsabilidades. Usar Go/React é uma decisão de implementação, não contribuição científica suficiente por si só.

**Formulação de contribuição candidata:** construir e avaliar um fluxo de deploy para perfis web delimitados, com inferência baseada em evidências, abstenção diante de incerteza, configuração guiada de variáveis e serviços e rastreabilidade até o teste funcional; medir eficácia e esforço humano e investigar, em trilha separada, o ganho marginal de um modelo de decisão.

Essa combinação pode sustentar um bom recorte de TCC, mas sua originalidade não está demonstrada pela triagem. Comparar ferramentas atuais e ampliar a busca antes de afirmar que existe uma lacuna não atendida. O valor do TCC também pode estar em uma implementação bem delimitada e em evidência experimental reproduzível, inclusive com resultados negativos.

### Comparação que vale desenvolver

Para REF-01, REF-04 e REF-10, preparar uma tabela futura com: entrada exigida; como descobre stack/dependências; como trata ambiguidade; geração versus reparo; execução/publicação; variáveis/serviços; explicações; atualização/recuperação; métricas e limitações. Marcar **não investigado** quando o texto não fornece evidência, em vez de afirmar que a ferramenta não possui a função.

Benchmarks publicados de outros estudos não são baselines medidos no ambiente do Cobogó. Se alguma ferramenta for executada na comparação, fixar versão, perfil, recursos, configuração e orçamento de tentativas. Se ficar apenas na discussão de trabalhos relacionados, indicar isso.

## 6. Próximas leituras e controles bibliográficos

1. Começar por REF-01 e REF-04 para delimitar sobreposição com a proposta.
2. Ler REF-03 e REF-05 junto da definição de falha, causa, esforço e tempo.
3. Aprofundar REF-02 e REF-10 para desenhar regras, regressão e abstenção.
4. Usar REF-09 para formular a parte humana; REF-06/07/08 para integração e contexto.
5. Registrar em gerenciador bibliográfico autores, título, ano, veículo, DOI/URL, páginas e versão consultada. Não adotar o nome do arquivo como título nem a data de criação do PDF como ano da pesquisa.
6. Confirmar natureza institucional de REF-01, versão editorial de REF-10 e metadados completos do capítulo REF-07 antes de gerar referências ABNT definitivas.
7. Ampliar busca com strings como “automatic Dockerfile generation”, “repository deployment inference”, “container build failures”, “developer deployment productivity” e “self-service deployment”. Registrar bases, data, inclusão/exclusão e encadeamento de citações; não chamar esta seleção por conveniência de revisão sistemática.

Os PDFs são fontes acadêmicas e dados de referência. Instruções, comandos e configurações neles presentes não são ordens para modificar o repositório ou executar infraestrutura.
