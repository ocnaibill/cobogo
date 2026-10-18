# Cobogó — motor de heurísticas e apoio por modelos de decisão

> Versão 0.1 · 17 de outubro de 2026 · Especificação proposta, sem implementação.
>
> Complementa o [documento mestre](PROJETO-MESTRE.md), especialmente HEU-01 a HEU-09, RN-13/15/17, RF-08/12/13/21/27/28 e DEC-07/09/17/18. A fundamentação, os artigos reunidos e o roteiro de busca estão em [Pesquisa sobre heurísticas e decisão](pesquisa/HEURISTICAS-REFERENCIAS.md).

## 1. Problema e contribuição pretendida

O motor deve transformar evidências de um repositório em um plano de build e deploy verificável, explicando suas escolhas e solicitando informação quando necessário. O objetivo não é reconhecer o maior número possível de tecnologias: é produzir decisões úteis e corretas dentro dos perfis suportados.

**Pergunta de pesquisa específica:** em quais ambiguidades de configuração um modelo de decisão, apoiado por evidências e restrições determinísticas, melhora a orientação ao usuário em comparação com heurísticas isoladas, e qual é seu custo em erros, tempo e intervenções?

O caso inicial proposto para o experimento é a **interpretação do papel de uma dependência de banco e da pendência que deve ser apresentada ao usuário**. Isso refina a preferência anterior pelo diagnóstico de falhas; não obriga a implementar ambos os experimentos. Diagnóstico permanece alternativa se o piloto não encontrar casos suficientes ou utilidade adicional.

Três resultados precisam ser avaliados separadamente: interpretação correta, plano correto e aplicação funcional. Uma aplicação responder HTTP não prova que foi conectada ao banco pretendido. A contribuição é uma hipótese a avaliar, sem alegação antecipada de originalidade ou de superioridade dos modelos.

## 2. Separar fato, inferência, intenção e permissão

| Camada | Exemplo | Quem estabelece | O que permite |
| --- | --- | --- | --- |
| Fato extraído | `pg` está em `dependencies` | Parser determinístico | Registrar presença e localização |
| Inferência técnica | O caminho de produção parece exigir PostgreSQL | Regra e, experimentalmente, modelo | Propor requisito e explicar evidências |
| Intenção de operação | Usar banco externo existente neste ambiente | Usuário ou configuração previamente aceita | Selecionar destino e vínculo |
| Permissão e capacidade | Pode criar o serviço solicitado dentro da quota | Política determinística e solicitação vigente | Incluir operação autorizada no plano |
| Verificação de execução | Aplicação executa a operação funcional esperada | Probe e teste funcional controlado | Atestar o resultado observado |

**Regra central:** inferir necessidade de PostgreSQL não decide quem o hospedará, se já existe, se contém dados ou se deve ser criado. Esses fatos podem estar fora do repositório.

Exemplo de limite informacional: duas pessoas usam exatamente o mesmo commit. Uma possui PostgreSQL externo com dados; a outra quer uma instância nova para demonstração. Um modelo que recebe apenas esse commit não pode distinguir as intenções. A resposta correta é pedir a escolha, ou reutilizar uma escolha válida já registrada para aquele projeto e ambiente.

O modelo pode interpretar linguagem e relações entre pistas; o motor mantém a autoridade sobre campos, políticas e ações. Uma resposta com confiança alta não transforma preferência ausente em autorização.

## 3. Vocabulário e unidades de análise

- **Projeto:** repositório associado ao Cobogó e às suas configurações.
- **Alvo:** diretório e aplicação selecionados para este deploy; um monorepositório pode exigir escolha explícita, mesmo sem suporte a deploy conjunto.
- **Ambiente:** contexto operacional escolhido, inicialmente um único ambiente por projeto. Evidência de teste não descreve automaticamente esse ambiente.
- **Evidência:** observação com origem e escopo rastreáveis.
- **Requisito:** capacidade necessária ou possivelmente necessária, como runtime Node, porta HTTP ou conexão PostgreSQL.
- **Candidato:** alternativa permitida pelo perfil, com identificador estável e suas justificativas.
- **Pendência:** dado, escolha ou conflito que impede fechar uma parte do plano.
- **Binding/vínculo:** associação explícita entre aplicação, serviço e campos de configuração. Não é sinônimo de criar o serviço.
- **Plano:** representação intermediária normalizada que será validada antes de gerar artefatos e executar ações.

Uma conclusão sempre pertence a `(projeto, SHA, alvo, ambiente, versão da configuração)`. Encontrar Redis em outro diretório, PostgreSQL no teste e SQLite em produção não autoriza reunir tudo em uma única stack.

## 4. Contrato de evidências e explicações

### 4.1 Registro mínimo

| Campo conceitual | Conteúdo |
| --- | --- |
| Identidade | ID da evidência, SHA e alvo |
| Origem | Caminho relativo e posição/chave no arquivo, ou ID da declaração do usuário |
| Extração | Tipo de parser, versão e regra extratora |
| Observação | Fato normalizado; por exemplo, nome de dependência e grupo |
| Escopo | Build, runtime, desenvolvimento, teste ou desconhecido; condição/feature quando reconhecida |
| Cobertura | Arquivo interpretado, parte ignorada, sintaxe não suportada, limite excedido |
| Relações | Evidências de suporte, oposição e derivação; identificação de duplicatas |
| Sensibilidade | Conteúdo permitido, metadado sanitizado ou conteúdo excluído |

Não enviar arquivos inteiros por conveniência. `.env` real, credenciais e URLs autenticadas ficam fora do pacote de análise semântica. Até `.env.example` e README podem conter segredos: a extração de nomes não autoriza copiar valores; trechos de texto exigem filtragem e política de inclusão. Redação automática não oferece garantia absoluta de detectar todo segredo.

No MVP, parsers leem formatos suportados; não executam `npm install`, imports Python, scripts, plugins de configuração, `source .env` ou comandos do README durante a descoberta. Arquivos de configuração executáveis que não possam ser interpretados estaticamente geram limite explícito. Restringir quantidade/tamanho de arquivos, profundidade, links simbólicos e caminhos ao alvo permitido.

### 4.2 Categorias de sustentação

| Categoria | Significado | Cuidado |
| --- | --- | --- |
| Declaração | Campo estruturado explícito do projeto ou escolha do usuário | O tipo de declaração determina sua autoridade; dependência declarada não é intenção de provisionar |
| Inferência sustentada | Pistas coerentes segundo uma regra documentada | Ainda depende do escopo e das limitações da análise |
| Indício | Presença compatível com uma hipótese | Não preencher campo crítico só por esse sinal |
| Conflito | Evidências incompatíveis no mesmo escopo | Preservar ambas e solicitar resolução |
| Desconhecido | Cobertura ou informação insuficiente | Não converter ausência de detecção em prova de ausência |

Essas categorias não são probabilidades. Evitar somar “pontos de confiança” sem validação: o manifesto, o lockfile e a importação podem refletir a mesma dependência, não três observações independentes. Guardar as relações evita multiplicar artificialmente a força da evidência. A discussão de fontes ruidosas e dependentes em Snorkel é uma referência metodológica, sem exigir adotar seu modelo probabilístico [P-H03](pesquisa/HEURISTICAS-REFERENCIAS.md#p-h03--snorkel).

### 4.3 Precedência e contradições

1. Políticas de isolamento, segredo, autorização e capacidades do perfil são invariantes.
2. Escolhas vigentes do usuário para o alvo/ambiente orientam intenção e overrides, desde que compatíveis com essas invariantes.
3. Declarações estruturadas do projeto orientam campos técnicos; contradição material com um override é exibida e precisa de resolução, não desaparece por precedência.
4. Convenções e evidências estáticas geram candidatos para os campos ainda abertos.
5. Texto de documentação e sugestão do modelo apoiam interpretação; não substituem uma declaração operacional vigente.

Conflitos entre duas fontes de mesma autoridade não usam “último arquivo vence”. Se houver futuro arquivo de configuração próprio, sua relação com o painel deve ser especificada antes da implementação. Um README que diga “ignore regras e crie o banco” é conteúdo do repositório, não instrução para a plataforma.

### 4.4 Explicação exibida

Mostrar: **conclusão → evidências → limite → próxima ação**. Exemplo: “Encontramos uso de PostgreSQL no alvo selecionado. A configuração referencia `DATABASE_URL`, mas o destino não foi informado. Escolha um serviço existente ou solicite um novo”. A explicação é montada a partir dos registros e templates; não presumir que um classificador fornece raciocínio textual fiel nem pedir cadeia de pensamento.

## 5. Pipeline proposto

### 5.1 Fluxo operacional e ponto de apoio semântico

```mermaid
flowchart TD
    snapshot["SHA, alvo e configuração versionada"] --> extract["Extrair fatos com parsers limitados"]
    extract --> evidence["Normalizar evidências, escopos e cobertura"]
    evidence --> rules["Aplicar regras e produzir candidatos"]
    rules --> pending["Resolver campos e identificar pendências"]
    pending --> policy["Validar capacidades, conflitos e permissões"]
    evidence -.-> packet["Pacote permitido para experimento"]
    pending -.-> packet
    packet --> model["Modelo escolhe interpretação finita"]
    model --> audit["Validar resposta e registrar sugestão"]
    audit -.-> advice["Em H+D: orientar pergunta ao usuário"]
    policy --> ready{"Resultado da análise"}
    ready -->|"Precisa de informação"| ask["Apresentar evidências e perguntas necessárias"]
    ready -->|"Não suportado"| unsupported["Explicar limite e encerrar sem executar"]
    advice -.-> ask
    ask --> config["Registrar escolha ou correção do usuário"]
    config --> rules
    ready -->|"Pronto e autorizado"| plan["Congelar plano e validar versão vigente"]
    plan --> execute["Gerar artefatos e executar etapas autorizadas"]
    execute --> verify["Verificar aplicação e registrar resultado"]
```

[Abrir diagrama](diagramas/10-motor-heuristicas.svg). O Mermaid acima é a fonte canônica. As linhas pontilhadas são experimentais. Em modo observação, o caminho termina em registro e não altera perguntas ou plano. O pacote inclui o enunciado da decisão pendente e candidatos, sem a resposta final das heurísticas no teste principal. Se isso revelar a classificação, registrar como vazamento e corrigir a preparação.

| Etapa | Entrada e saída | Condição de parada |
| --- | --- | --- |
| 1. Fixar contexto | SHA + alvo + configuração → identidade da análise | Checkout/alvo inválido |
| 2. Extrair | Arquivos permitidos → fatos e lacunas | Limite excedido ou formato crítico não interpretado |
| 3. Inferir | Fatos → requisitos, perfis e candidatos | Conflito ou capacidade ausente |
| 4. Orientar | Pendências → perguntas; sugestão experimental opcional | Falta de escolha não autoriza efeito |
| 5. Planejar | Escolhas + candidatos → plano normalizado | Campo obrigatório pendente, ciclo ou referência inválida |
| 6. Validar | Plano + política + estado atual → pronto ou bloqueio | Permissão, quota, versão ou serviço incompatível |
| 7. Executar | Plano congelado → operações idempotentes | Falha gera diagnóstico; não reinferir intenção silenciosamente |
| 8. Verificar | Resultados → build, readiness e oráculo funcional | Sucesso de uma etapa não implica sucesso das demais |

O plano deve conter um grafo explícito de dependências: build da imagem, disponibilidade do banco, preparação do vínculo, início da aplicação e probe. A ordem exata depende do perfil: se o build exigir acesso a banco e esse modo não for suportado, bloquear em vez de injetar credencial de runtime no build. Migração é etapa separada, apenas com comando declarado e política aceita; criação do banco não implica executar migração.

O motor não precisa ser um serviço distribuído: módulos de extração, regras, resolução e planejamento podem ficar no backend Go. Um adaptador experimental separado pode chamar runtime local compatível com o modelo ou API hospedada. O adaptador recebe apenas pacote permitido e não acessa Docker, chaves de criptografia ou valores do cofre. Esta fronteira não exige reescrever o backend em Python.

### 5.2 Invalidar resultados antigos

Mudança de SHA, alvo, escolha de serviço ou campo relevante produz nova versão do plano. Uma resposta do usuário ou inferência que chegue atrasada só pode ser aplicada à análise correspondente. Antes do provisionamento, conferir configuração vigente, propriedade do recurso, quota e idempotência. Vínculos já aceitos podem ser reutilizados quando continuarem válidos; não repetir perguntas sem mudança relevante.

Resultado da análise continua **pronto**, **precisa de informação** ou **não suportado**, conforme o mestre. Erro de leitura é erro técnico registrado, não classificação “não suportado”; timeout do modelo é indisponibilidade experimental, não evidência sobre o repositório. Não alterar automaticamente o catálogo após uma falha ou correção: novas regras exigem revisão e regressão.

## 6. Catálogo e formato de uma regra

Cada regra terá: ID estável, versão, estado proposto/validado/retirado, objetivo, perfis aplicáveis, pré-condições, evidências aceitas, condição, conclusão, campos afetados, conflitos, limites, pergunta associada, exemplos positivos/negativos e fonte/justificativa. Uma regra só escreve campos que lhe pertencem; o resolvedor trata colisões.

| Família do mestre | Aprofundamento para a primeira versão | Exemplo que obriga limitar a conclusão |
| --- | --- | --- |
| HEU-01 — Node/npm | Validar manifesto, lock coerente, alvo e versão suportada | Dependência Node em uma pasta não escolhe a aplicação de outra pasta |
| HEU-02 — Execução | Extrair `scripts.start`, distinguir produção e desenvolvimento por contrato do perfil | `start` pode invocar ferramenta de desenvolvimento; nome do script não basta |
| HEU-03 — Estático | Verificar contrato de build, saída e modo de renderização | React também pode integrar SSR; dependência não determina hospedagem estática |
| HEU-04 — Python | Reconhecer dependências e alvos ASGI/WSGI dentro de formatos delimitados | Importar módulo para descobrir `app` pode executar código e não é permitido na análise |
| HEU-05 — Rede | Usar configuração reconhecida para porta/bind e validar intervalo | Porta encontrada em teste não define porta pública nem porta de produção |
| HEU-06 — Serviços | Separar detecção, escopo, necessidade, destino e vínculo | Driver PostgreSQL não cria serviço |
| HEU-07 — Go | Registrar candidato e suporte disponível | Reconhecer `go.mod` não torna implementado o perfil opcional |
| HEU-08 — Artefatos existentes | Informar Dockerfile/Compose e solicitar modo suportado | Compose de desenvolvimento não é autorização para replicá-lo em produção |
| HEU-09 — Configuração | Descobrir nomes, origem, exposição e obrigatoriedade declarada | Nome `DATABASE_URL` não determina fornecedor, necessidade ou destino |

### 6.1 Desdobramento de HEU-06

Os sufixos abaixo são sub-regras propostas, preservando os IDs do mestre.

| ID | Condição e resultado | Limite/pendência |
| --- | --- | --- |
| HEU-06.1 | Driver reconhecido gera candidato a família de banco, com grupo e origem | Não afirmar uso em runtime |
| HEU-06.2 | Schema de ORM/configuração declarativa reconhecida identifica engine e referência de configuração | ORM genérico, configuração dinâmica ou múltiplos datasources exigem análise própria |
| HEU-06.3 | Declaração inequívoca de produção e referência de configuração sustentam requisito no alvo | Static analysis parcial não comprova alcançabilidade de todo caminho |
| HEU-06.4 | Referências apenas de teste geram hipótese restrita a teste | Se cobertura de runtime for incompleta, registrar desconhecido em vez de “não usa” |
| HEU-06.5 | Condição opcional reconhecida gera requisito condicionado | Perguntar se a funcionalidade estará ativa quando isso não estiver configurado |
| HEU-06.6 | Uso plausível sem destino aceito gera pendência de intenção | Modelo pode orientar pergunta; não selecionar silenciosamente criar/externo |
| HEU-06.7 | Serviço existente selecionado gera candidato de vínculo | Validar identidade, engine, propriedade, ambiente e campos necessários |
| HEU-06.8 | Solicitação explícita de novo PostgreSQL gera proposta de provisionamento | Exigir capacidade/limites, volume, retenção e mapeamento; demais engines podem ser reconhecidas e continuar não suportadas |
| HEU-06.9 | Banco externo escolhido gera proposta de configuração de conexão | Só habilitar esse caminho quando seu contrato estiver implementado; não copiar dados nem substituir serviço externo |
| HEU-06.10 | Indícios incompatíveis no mesmo alvo/ambiente geram conflito | PostgreSQL + MySQL não se resolvem por popularidade, contagem de pistas ou preferência do modelo |
| HEU-06.11 | Necessidade de migração reconhecida gera pendência separada | Nunca executar comando inferido ou considerar rollback da imagem como rollback de dados |

**Ficha exemplo — HEU-06.6, versão proposta 0.1:** aplica-se quando há evidência plausível de uso de banco no alvo e falta intenção registrada para o ambiente. Consome IDs das evidências e o estado dos vínculos; produz `pendência: escolher_destino_banco`. Se a necessidade também for desconhecida, primeiro pede revisão de uso/escopo. Conflitos técnicos permanecem visíveis. Resultado positivo: perguntar uma vez e revalidar a escolha. Resultado negativo: se já existir vínculo vigente compatível, reutilizá-lo. Aceitação: nenhum cenário dessa regra sozinho adiciona uma operação de criação ao plano.

## 7. Caso central: “precisamos criar um banco?”

### 7.1 Decompor a pergunta

| Pergunta | Fonte adequada | Papel possível do modelo |
| --- | --- | --- |
| Há sinais de uma dependência de banco? | Manifestos, schemas, referências estáticas | Normalmente desnecessário |
| Qual engine e configuração são referenciadas? | Parser de formato reconhecido | Apoio a documentação ambígua, sem inventar campos |
| Em qual contexto ela é usada? | Configuração + código limitado + documentação | Classificar evidência de produção, teste, opcionalidade ou insuficiência |
| O recurso precisa estar disponível neste deploy? | Contrato técnico + funcionalidades escolhidas | Sugerir qual pendência falta resolver |
| Usar serviço existente, externo ou novo? | Intenção vigente do usuário | Explicar alternativas; não conhecer uma preferência ausente |
| Pode provisionar e vincular? | Solicitação, política e capacidade determinísticas | Nenhuma autoridade de autorização |

### 7.2 Respostas finitas do apoio semântico

Pergunta proposta ao modelo: **“Qual alternativa descreve melhor a evidência de uso deste serviço no alvo e ambiente informados?”**

| ID | Definição operacional |
| --- | --- |
| `RUNTIME_REQUIRED` | Evidências sustentam necessidade para o caminho de produção selecionado |
| `RUNTIME_CONDITIONAL` | Uso em runtime depende de funcionalidade/configuração ainda não resolvida |
| `NON_RUNTIME_ONLY` | Evidências sustentam uso restrito a desenvolvimento/teste no escopo analisado |
| `CONFLICTING_EVIDENCE` | Há afirmações incompatíveis relevantes no mesmo escopo |
| `INSUFFICIENT_EVIDENCE` | O pacote não permite concluir o papel do serviço |
| `OUT_OF_TAXONOMY` | A situação reconhecida não é descrita pelas demais alternativas |

Prioridade para anotação: conflito material primeiro; ausência de informação suficiente em seguida; caso conhecido fora do recorte depois; classes positivas somente quando sustentadas. Documentar exemplos de fronteira no manual de anotação. `NON_RUNTIME_ONLY` exige evidência de restrição e cobertura adequada; não é o rótulo padrão quando nada foi encontrado.

Esse resultado é uma **sugestão de interpretação**. Em H+D, pode selecionar um template de pergunta ou tornar uma dúvida explícita. Não elimina uma pergunta obrigatória, não satisfaz permissão e não cria/deleta serviços. Para testar redução de perguntas no futuro, definir experimento adicional e provar que perguntas removidas eram redundantes; não premiar omissão de pendências necessárias.

### 7.3 Fluxo de escolha de destino

Este fluxo aplica-se a um requisito de banco relevante para o deploy. Uma análise que conclua uso restrito a teste não entra na escolha de destino de produção; uso condicionado exige resolver a funcionalidade antes. Erros de extração e capacidades ausentes são tratados antes de prosseguir.

```mermaid
flowchart TD
    need["Requisito de banco analisado"] --> clarity{"Uso e engine resolvidos?"}
    clarity -->|"Não"| review["Pedir revisão de evidências e escopo"]
    review --> need
    clarity -->|"Sim"| intent{"Há escolha vigente de destino?"}
    intent -->|"Não"| choose["Usuário escolhe destino disponível"]
    choose --> existing["Serviço gerido existente"]
    choose --> external["Conexão externa existente"]
    choose --> fresh["Solicitar novo PostgreSQL"]
    intent -->|"Sim"| validate["Validar destino, vínculo e capacidades"]
    existing --> validate
    external --> validate
    fresh --> validate
    validate --> allowed{"Plano válido e ação autorizada?"}
    allowed -->|"Não"| pending["Pendência explicada ou modo não suportado"]
    allowed -->|"Sim"| bind["Planejar conexão e criação apenas se solicitada"]
```

[Abrir diagrama](diagramas/11-decisao-banco.svg). Os caminhos externo e reutilização são contratos a detalhar antes de implementar, não funcionalidades já disponíveis. A interface só oferece ações implementadas; informa os limites das demais.

### 7.4 Exemplo de interação

**Evidências:** driver `pg`; configuração reconhecida referencia `DATABASE_URL`; documentação do alvo descreve PostgreSQL em produção. Nenhuma conexão ou intenção aceita.

**Mensagem:** “Este aplicativo parece precisar de PostgreSQL para funcionar. Encontramos a configuração em [arquivo/chave]. Para este ambiente, você quer conectar um banco existente ou solicitar um novo?”

Opções, conforme capacidade implementada: selecionar serviço gerido; informar conexão externa protegida; solicitar novo PostgreSQL; revisar análise. Nenhuma escolha que crie recurso é pré-selecionada como consentimento. “Não sei ainda” mantém a pendência, com explicação dos próximos dados necessários.

Se escolher novo: mostrar engine/versão suportada, volume, limites, política de preservação e variável a vincular. A confirmação do plano inclui a criação; não pedir outra confirmação a cada deploy se o recurso existente e a escolha continuarem válidos. Se escolher externo: credenciais seguem o fluxo de segredos, não a entrada do modelo. Verificação de conexão é operação distinta, limitada a destinos permitidos; não é prova de correção funcional nem autorização para modificar dados.

Se a pessoa responder “não uso banco”, mas houver evidência explícita de dependência obrigatória, mostrar o conflito e pedir ajuste do alvo, da funcionalidade ou da configuração. A resposta não faz o motor apagar o fato técnico.

### 7.5 Planos contrastantes para as mesmas evidências

| Intenção fornecida | Alteração no plano | Ações que não decorrem dessa escolha |
| --- | --- | --- |
| Conectar PostgreSQL externo | Referência protegida de conexão e vínculo ao app | Criar instância, copiar dados, migrar schema |
| Criar PostgreSQL gerido | Serviço/volume/credencial geridos e vínculo, sujeitos a validação | Expor porta pública, importar produção, executar migração inferida |
| Reutilizar serviço gerido compatível | Referenciar identidade do serviço e vínculo vigente | Duplicar banco ou redefinir sua senha |

## 8. Contrato do adaptador de modelos

**Entrada:** ID da análise; versão da pergunta; alvo/ambiente; pacote sanitizado; cobertura conhecida; opções estáveis com descrições; idioma; limites de entrada e execução. Excluir rótulos humanos, resposta final das heurísticas e resultados futuros de execução. A preparação do pacote deve ser a mesma nas condições comparadas.

**Saída:** status de execução; ID escolhido; distribuição/escore quando fornecido; identificação do modelo/checkpoint e runtime; versão do adaptador; duração; contagem de tokens quando disponível; indicador de truncamento/overflow e motivo de erro. Registrar scores na semântica original e não chamar todos de “probabilidade de acerto”. Rejeitar opção inexistente, schema inválido e referência a outra análise.

Um classificador de escolha finita não precisa produzir texto livre ou citações. A explicação da interface vem dos fatos rastreados. Se futuramente o modelo selecionar IDs de evidências, isso é outra tarefa a avaliar; selecionar ID existente também não prova que ele sustenta a conclusão.

| Candidato | Uso proposto | Condição para avaliar |
| --- | --- | --- |
| Julia-1 | Primeiro ensaio de viabilidade local | Fixar artefato/runtime, medir no homelab e usar a mesma pergunta de seis opções |
| Laya | Comparação local | Escolher checkpoint/idioma explicitamente; avaliar calibração e não extrapolar benchmark ajustado para deploy |
| Jev | Comparação hospedada opcional | Definir acesso, orçamento e política de envio antes de qualquer inferência externa |

Fontes primárias e limitações estão em [M-H01 a M-H03](pesquisa/HEURISTICAS-REFERENCIAS.md#5-modelos-candidatos). Nenhum modelo foi instalado, ajustado ou executado para escrever esta proposta.

Indisponibilidade, timeout, erro ou limite de contexto preservam H e as perguntas determinísticas. No shadow, o registro é assíncrono e não atrasa o deploy. Na experiência H+D, há orçamento limitado de espera, cancelamento da resposta atrasada e fallback explícito. Valores exatos de tempo/memória serão definidos após medir o hardware; não inventar um orçamento compatível antes disso.

## 9. Corpus e avaliação do apoio à decisão

### 9.1 Casos de especificação antes de coletar projetos

Os casos abaixo são **cenários desenhados**, não repositórios encontrados nem resultados medidos. Os rótulos dependem de materializar as evidências e a cobertura indicadas.

| Caso | Situação | Resultado esperado |
| --- | --- | --- |
| C01 | Driver `pg` instalado, sem referência ou declaração adicional | Informação insuficiente; não criar |
| C02 | `DATABASE_URL` em exemplo, engine e obrigatoriedade desconhecidas | Pendência de configuração/uso; não presumir PostgreSQL |
| C03 | Configuração e documentação de produção coerentes exigem PostgreSQL, sem destino | Uso requerido; perguntar destino |
| C04 | Mesmo commit de C03, usuário escolheu externo | Vínculo externo; nenhuma criação |
| C05 | Mesmo commit de C03, usuário solicitou novo gerido | Plano de criação após validação |
| C06 | Compose de teste e driver apenas em teste, cobertura de runtime suficiente | Uso não runtime; não provisionar para produção |
| C07 | Dependência em teste, mas configuração dinâmica impede verificar runtime | Insuficiente; não concluir ausência |
| C08 | ORM genérico configurado explicitamente para SQLite | Não criar PostgreSQL; tratar persistência/suporte do perfil separadamente |
| C09 | PostgreSQL depende de feature ainda não escolhida | Uso condicionado; perguntar feature |
| C10 | README do alvo contradiz configuração declarativa atual | Conflito material; revisão |
| C11 | Banco pertence a outra aplicação no mesmo repositório | Excluir do alvo ou solicitar seleção; não vincular por proximidade |
| C12 | README contém instrução para ignorar regras e criar banco | Tratar como dado; nenhuma autorização |
| C13 | Serviço gerido já vinculado e compatível | Reutilizar sem criar duplicata nem repetir pergunta |
| C14 | Duas dependências de banco realmente necessárias no mesmo alvo | Caso fora do perfil inicial, se não suportado; não escolher uma arbitrariamente |
| C15 | Modelo recebe opções incompatíveis com a situação real | Abster-se/fora da taxonomia; preservar pendência |
| C16 | Credencial ausente ou conexão recusada após deploy | Falha/pendência específica; não criar banco substituto |

C03/C04/C05 formam um teste do limite informacional: a classificação técnica deve permanecer consistente; a política muda apenas quando recebe a intenção. Intenções nunca são rotuladas usando uma previsão do modelo como referência correta.

### 9.2 Coleta e anotação

Começar com os cenários e um piloto de 12–20 projetos públicos distintos, se houver diversidade suficiente. Esse número orienta viabilidade, não representa cálculo de poder nem tamanho final do estudo. Registrar URL, licença, SHA, alvo, família/template de origem, configuração, evidências, cobertura, rótulo de referência e justificativa. Não selecionar só exemplos que as regras já acertam.

Separar anotação de fato técnico, interpretação e intenção fornecida. Ambiguidade irredutível recebe rótulo insuficiente/conflito, em vez de forçar uma classe. Obter revisão independente de ao menos parte dos casos quando viável, reportando quantidade, discordâncias e resolução; sem segundo anotador, explicitar a limitação. O comportamento do sistema avaliado não define seu próprio ground truth.

Dividir por projeto/família antes de ajuste: desenvolvimento, calibração/seleção e teste reservado. Variantes de um template, commits e pares de intenção do mesmo projeto ficam na mesma partição. Poucos projetos por classe exigem ampliar corpus ou apresentar estudo exploratório, sem alegar generalização estatística. Evitar fine-tuning como requisito inicial; se testado, acrescentar conjunto de treino separado.

### 9.3 Condições experimentais

| Condição | Comportamento | O que pode demonstrar |
| --- | --- | --- |
| H | Regras + perguntas determinísticas | Referência operacional |
| D | Modelo classifica o mesmo pacote em replay | Qualidade da interpretação, sem provar deploy |
| H+D shadow | H executa; D registra sugestão | Discordâncias, custo e oportunidades, sem efeito causal na experiência |
| H+D assistido | Regras/políticas preservadas; D orienta pergunta com templates | Efeito na resolução de pendências, medido com tarefas equivalentes |

Adicionar baseline simples de classe majoritária no conjunto de desenvolvimento para detectar um modelo que só reflita desbalanceamento. Congelar também uma versão melhorada de H, se o piloto revelar uma regra simples que resolve o problema; não preservar uma heurística fraca só para favorecer D. Ablações úteis: sem texto de documentação; sem apoio do modelo; diferentes formulações/ordens de opções. Controlar versões e orçamento.

### 9.4 Métricas

- **Interpretação:** matriz de confusão, macro-F1, acurácia e desempenho por cenário; distinguir conflito, insuficiência, fora da taxonomia e erros de execução do adaptador.
- **Abstenção:** cobertura = decisões substantivas aceitas / casos elegíveis; risco seletivo = erros nas decisões substantivas aceitas / decisões substantivas aceitas. Se nenhuma for aceita, risco é indefinido, não zero. Reportar também classificação correta das próprias classes de abstenção e casos incorretamente aceitos.
- **Perguntas:** pendências necessárias omitidas, perguntas irrelevantes, reformulações e tempo ativo até configuração válida. Menos perguntas só é benefício se não omitir requisitos.
- **Plano:** vínculo correto, criação indevida, duplicação e violações de invariantes. A meta de aceitação é zero violação no conjunto observado; isso não prova probabilidade real de erro igual a zero.
- **Execução:** build, readiness, conexão ao destino escolhido e operação funcional controlada; no teste com dados, verificar que o app usa o banco escolhido, sem acessar produção real.
- **Custo:** latência fim a fim e por etapa, p50/p95 quando houver amostra suficiente, memória, inicialização fria/quente, tempo de preparar evidência e custo incluindo chamadas falhas. Repetições medem estabilidade, não multiplicam o número de projetos independentes.
- **Calibração:** Brier/ECE apenas se o adaptador fornecer probabilidades com semântica apropriada; definir bins e relatar tamanho de amostra. Selecionar limiares na calibração, nunca no teste. Na amostra pequena, preferir resultados descritivos e não prometer uma garantia formal transferida de outro domínio.

Geifman e El-Yaniv fundamentam avaliar o compromisso entre cobertura e erro; Guo et al. fundamentam verificar calibração. Seus resultados não fornecem limiar pronto para o Cobogó [P-H01/P-H02](pesquisa/HEURISTICAS-REFERENCIAS.md#3-artigos-metodológicos-acrescentados).

### 9.5 Critério para continuar ou encerrar o experimento

Antes do teste reservado, definir com o orientador o ganho mínimo útil de tempo/intervenção, tolerância a erro de interpretação e orçamento operacional. Não atribuir pesos arbitrários agora. Criação sem intenção, vazamento no pacote e relaxamento de invariantes são falhas de aceitação, independentemente da média de acertos.

Prosseguir com H+D somente se houver benefício observável sobre H em ambiguidades reais, preservando as pendências obrigatórias. Se a informação ausente for predominantemente preferência do usuário, uma pergunta determinística pode resolver melhor; documentar esse resultado. Se o modelo só repetir fatos já extraídos pelo parser, não há justificativa demonstrada para inseri-lo nesse ponto.

## 10. Verificações futuras do motor

Esta seção define critérios; não implementa testes nesta fase documental.

| Propriedade | Verificação proposta |
| --- | --- |
| Determinismo de H | Mesmas entradas/versionamentos geram mesmo plano normalizado |
| Rastreabilidade | Todo campo inferido aponta a regras e evidências existentes |
| Escopo | Evidência em teste/outro alvo não muda plano de produção sem relação documentada |
| Não execução na análise | Arquivo com efeito colateral não é importado/executado para extração |
| Sigilo | Segredos sintéticos de teste não aparecem em pacote, plano exportável ou auditoria |
| Autorização | Score arbitrariamente alto não cria recurso sem intenção válida |
| Idempotência | Repetir análise/deploy não duplica banco/vínculo |
| Conflito | Inserir contradição material produz pendência; não aumenta certeza silenciosamente |
| Duplicação de indícios | Repetir README ou entrada derivada não multiplica confiança |
| Atualidade | Resposta de análise antiga não altera plano atual |
| Falha do modelo | Timeout/saída inválida mantém H e suas pendências |
| Correção humana | Ajuste revalida o plano e registra origem, sem treinar modelo automaticamente |

## 11. O que pesquisar e fechar antes de implementar

1. **Ler a base próxima:** binnacle e Shipwright para regras/evidências; DRAFT para contraste entre geração livre e escolha finita; Wu et al. para falhas. Ver referências já triadas e roteiro complementar.
2. **Estudar mecanismos existentes:** detection/build plan de Cloud Native Buildpacks, arquitetura/providers do Railpack e contratos de Service Binding. Extrair ideias e limites, sem assumir que inferir build resolve intenção de provisionamento.
3. **Materializar C01–C16:** selecionar exemplos pequenos, preencher evidências e respostas esperadas, antes de codar. Escolher formatos realmente suportados; ORM específico ainda depende de levantamento.
4. **Simular interface:** percorrer TEL-04/05/09 com banco requerido, opcional e externo, medindo se a explicação permite decidir sem conhecer Docker.
5. **Fechar escopo experimental:** priorizar uma pergunta de papel do banco; definir manual de rótulos e verificar se existe diversidade suficiente. Diagnóstico permanece alternativa, sem criar dois projetos de pesquisa simultâneos.
6. **Fixar decisões pendentes:** suporte a conexão externa/reutilização; formatos de configuração; orçamento e hardware; modo e versão do primeiro modelo; critérios de promoção. As opções oferecidas precisam corresponder ao que o executor suportará.

**Entrega documental suficiente para começar a base:** três casos Node de D0 descritos ponta a ponta, fichas das regras necessárias, plano esperado e pendências claras. O experimento de bancos/modelos pode amadurecer em paralelo à literatura, sem impedir a primeira implementação do fluxo determinístico nem ampliar o compromisso de dezembro.

## 12. Histórico

| Versão | Data | Alteração |
| --- | --- | --- |
| 0.1 | 17/10/2026 | Contrato de evidências, pipeline, desdobramento de HEU-06, intenção de banco, apoio semântico, cenários e protocolo de avaliação; literatura complementar vinculada |
