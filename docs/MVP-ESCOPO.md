# Cobogó — escopo do MVP: o que entra, o que foi cortado e por quê

> Documento de apoio para apresentar o recorte do projeto ao orientador. É uma síntese organizada do [documento mestre](PROJETO-MESTRE.md) v0.5 e das decisões em [`decisoes/`](decisoes/) — não substitui esses documentos, só reorganiza o conteúdo pra leitura rápida numa reunião. Preparado em 26 de outubro de 2026.

## 1. O que o Cobogó é, em uma frase

Uma plataforma que analisa um repositório, produz um **plano de deploy explicável** (não uma caixa-preta), gera o Dockerfile, builda e publica a aplicação — pedindo informação ao usuário quando falta evidência suficiente, em vez de adivinhar.

A pergunta de pesquisa: *em aplicações web que seguem os perfis suportados, em que medida um fluxo de deploy guiado por heurísticas reduz tempo e intervenções até o primeiro deploy funcional, comparado à configuração manual?* A contribuição não é "gerar Dockerfile e fazer deploy" isoladamente — isso já existe em trabalhos correlatos (Sampaio et al. 2024, DRAFT/Lyu et al. 2026). A contribuição é a combinação de inferência verificável + explicabilidade + abstenção quando falta dado + configuração guiada + avaliação do esforço.

## 2. A lógica do recorte: quatro camadas, não um MVP só

| Camada | O que significa | Critério pra entrar aqui |
| --- | --- | --- |
| **D0** (demonstração mínima, dezembro/2026) | O menor fluxo ponta a ponta que já prova a ideia | Só o necessário pra mostrar repositório → análise explicável → deploy real funcionando |
| **MVP do TCC** (depois da D0) | A entrega completa que será avaliada | Tudo que falta pra sustentar as perguntas de pesquisa QP1–QP4 com dados reais |
| **Extensões condicionadas a tempo** | Bom de ter, não bloqueia a defesa | Só entra se sobrar tempo depois do MVP garantido |
| **Fora do recorte inicial** | Decidido explicitamente que não é objeto deste TCC | Mudaria o problema de pesquisa (ex.: multi-tenant hostil é outro problema) |

A ideia central: **cobertura ampla cedo mata o prazo.** Cada perfil, banco ou integração nova soma trabalho de implementação *e* de avaliação (precisa de casos positivos, negativos e ambíguos, não só "funciona uma vez").

## 3. O que está em cada camada

### D0 — até dezembro de 2026

- Um repositório público Node.js/npm, HTTP, com `package.json`, lockfile e `scripts.start` coerentes.
- Análise explicável (evidências → plano → pendência quando falta dado), Dockerfile gerado, build real, contêiner, probe de readiness, URL acessível no homelab.
- Backend Go (API + worker separados), PostgreSQL interno, Docker, proxy reverso.
- Interface React enxuta, autenticação do painel.
- Três exemplos pequenos, incluindo um caso de falha/pendência explicada.

**Não exige:** Python, banco auxiliar, compatibilidade ampla, descoberta arbitrária de portas, estudo com participantes.

### MVP do TCC — depois da D0

- Perfis Node.js/npm, Python/pip e frontend estático.
- Variáveis de ambiente com descoberta guiada, overrides explícitos, histórico de planos.
- PostgreSQL auxiliar opcional por projeto, com rede e volume próprios.
- Diagnóstico de falhas, preservar release anterior até validar a candidata, recuperação após reinício do worker.
- Dashboard de saúde/recursos por projeto, stack e contêiner.
- Coleta de métricas e execução dos estudos técnico e com usuários.

A pesquisa de modelos de decisão (Jev/Laya/Julia-1) entra como **trilha experimental separada**, depois que a base heurística estiver estável — nunca como requisito pra usar o Cobogó.

### Extensões condicionadas a tempo

Go como aplicação publicada; Django; mais gerenciadores de dependência; Redis auxiliar; webhooks; repositórios privados; deploy de múltiplos serviços; importação limitada de Dockerfile/Compose.

### Fora do recorte inicial — decidido, não é esquecimento

Kubernetes, múltiplos hosts, escalonamento automático, alta disponibilidade, cobrança, marketplace, execução pública de código arbitrário, isolamento forte entre clientes não confiáveis, provisionamento de nuvem, garantia de zero downtime, geração livre de Dockerfiles por LLM, reparo automático de código.

## 4. Decisões que definiram esse recorte (e por que foram cortadas as alternativas)

| Decisão | O que ficou | O que foi cortado/adiado | Motivo |
| --- | --- | --- | --- |
| DEC-01 | Backend inteiro em Go | Reaproveitar o protótipo Node/TypeScript | Era a intenção original do autor; protótipo preservado na tag `prototype-node`, removido do `main` em 25/10/2026 depois que a primeira fatia Go funcionou (DEC-22) |
| DEC-02 | Monólito modular, API e worker como processos separados | Microserviços | Microserviços aumentam operação sem necessidade comprovada neste porte |
| DEC-03/DEC-21 | PostgreSQL único, com roles separados (API, worker, collector) | Redis para fila (herdado do protótipo); SQLite | Redis não é obrigação herdada; SQLite carece de modelo de autorização e facilita erro de backup. `SKIP LOCKED` resolve fila sem outro serviço |
| DEC-06 | D0 só Node/npm; Python e estático entram no MVP, Go é extensão | Suporte amplo desde o início | Cobertura ampla cedo ameaça a entrega de dezembro e dificulta avaliar cada perfil isoladamente |
| DEC-07 | Heurísticas determinísticas e explicáveis como base operacional | Modelo de decisão no caminho crítico | Modelo é comparador opcional (trilha experimental); a base não pode depender de algo ainda não validado |
| DEC-09 | PostgreSQL auxiliar isolado por projeto | Instância compartilhada entre projetos | Isolado é mais simples de entender/limpar; custo maior de memória é aceito pelo porte do homelab |
| DEC-10 | Logs em arquivo com metadados no banco, consulta por polling | Banco para todo log; WebSockets obrigatórios | Banco cresceria sem controle; SSE/WebSocket é evolução se o polling não bastar |
| DEC-12/DEC-20 | Operador confiável, corpus selecionado (nível A de tenancy); projetar pra nível B, não entregar agora | PaaS público pra qualquer usuário desconhecido (nível C) | Multi-tenant hostil é outro problema de pesquisa — exigiria isolamento forte (microVM/gVisor), política de abuso e faturamento |
| DEC-19 | Containers falando com o daemon do host por proxies filtrados (DOD) | Docker-in-Docker | DinD quebra a descoberta do Traefik (containers ficam invisíveis), duplica cache/disco e exige `--privileged` |
| DEC-11 | Preservar release anterior até validar a candidata | Zero downtime garantido | Zero downtime não é uma promessa sustentável neste porte; preservar a anterior já cobre o caso comum |
| DEC-14/DEC-15 | Segredos cifrados em repouso, alcance criptográfico declarado explicitamente | "Criptografia ponta a ponta" sem servidor ter acesso | A API recebe o valor pra poder usá-lo; prometer E2EE sem esse desenho seria enganoso |

## 5. Status real em 26 de outubro de 2026

| Entrega | Status |
| --- | --- |
| M0 — documentação, escopo D0 acordado | Feito; **inventário da VM e escolha formal dos 3 repositórios D0 ainda pendentes** |
| M1 — primeira fatia vertical em Go | **Concluído em 25/10/2026.** HEU-01 (package.json + lockfile), HEU-02 (scripts.start) e HEU-05 (contrato de porta) implementadas; testado ponta a ponta com repositório real: clone → SHA → análise → Dockerfile → build real → contêiner → probe HTTP confirmado |
| HEU-09 (descoberta de variáveis de configuração) | Implementada parcialmente: só a camada de evidência (`.env.example` + `process.env.NOME`), sem classificação de obrigatoriedade/exposição — isso é trabalho de interface (TEL-05), ainda não construída |
| Protótipo Node/TypeScript | Removido do `main`; preservado na tag `prototype-node` |
| M2 — frontend React, autenticação, D0 completa | Não iniciado |

## 6. Perguntas em aberto que ainda afetam o escopo

- Data formal da defesa, disponibilidade semanal e exigências do orientador (hoje sem resposta — ver `CRONOGRAMA.md`).
- CPU/RAM/disco da VM do homelab ainda não inventariados; isso pode mudar os limites de build simultâneo (RNF-05).
- Domínio exato e forma de entrada HTTPS (Cloudflare Tunnel vs. DNS direto) ainda não decidido.
- Fronteira criptográfica dos segredos ("quem pode descriptografar") ainda em aberto — ver §17.1 do mestre.
- Se o orientador validar o recorte experimental de modelos de decisão (§19.4), isso roda em paralelo, sem bloquear o MVP.
