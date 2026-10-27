# Cobogó — regras de negócio e de operação

> Extraído de [PROJETO-MESTRE.md §8](PROJETO-MESTRE.md#8-regras-de-produto-e-operação).

- **RN-01**: "Deploy funcional" exige build concluído, contêiner em execução e probe HTTP aprovado; no experimento, exige também o teste funcional definido para o repositório.
- **RN-02**: Informação obrigatória ausente ou sinais conflitantes impedem execução automática do plano. Uma porta padrão nunca é apresentada como porta comprovada.
- **RN-03**: Overrides válidos e explícitos têm precedência; depois vêm declarações suportadas do repositório e, por último, defaults documentados. Restrições de segurança prevalecem sobre configurações.
- **RN-04**: Cada deploy usa um commit imutável e um plano final imutável. A mudança da branch depois da análise exige novo deploy.
- **RN-05**: Driver de banco em dependências é apenas pista. Criar banco exige declaração ou seleção explícita do serviço e do vínculo de configuração.
- **RN-06**: Dados e credenciais do PostgreSQL interno do Cobogó não são compartilhados com aplicações publicadas.
- **RN-07**: Um projeto pode ter no máximo uma operação de deploy efetivamente executando; pedidos repetidos com a mesma chave de idempotência não criam efeitos duplicados.
- **RN-08**: Build, migração ou início não executam no processo da API. A análise estática não executa código do repositório; builds e instalações executam código e usam ambiente controlado.
- **RN-09**: Falhas de configuração não geram tentativas infinitas. Somente falhas transitórias classificadas podem ser repetidas automaticamente, com limite.
- **RN-10**: Remover projeto ou contêiner não implica remover volumes. Exclusão de dados é uma operação separada, explícita e registrada.
- **RN-11**: Logs e exportações passam por ocultação de segredos conhecidos. O sistema não promete detectar todo segredo que uma aplicação imprima.
- **RN-12**: Sucesso histórico do deploy e saúde atual são informações distintas. Uma queda posterior altera a saúde da release, preservando o histórico da execução.
- **RN-13**: Migrações de esquema da aplicação não são inferidas nem executadas por convenção. Exigem comando declarado e política explícita; rollback de imagem não desfaz migrações.
- **RN-14**: Um Dockerfile ou Compose existente é informado ao usuário; D0 não o executa automaticamente nem o sobrescreve no Git. A pessoa deve selecionar um modo suportado ou receber indicação de incompatibilidade.
- **RN-15**: Heurísticas descobrem nomes e necessidades de configuração; não inventam valores secretos. Valores de `.env`, credenciais, tokens e conexões autenticadas não entram no contexto de modelos de decisão.
- **RN-16**: Métricas pertencem ao projeto, recurso e intervalo efetivamente medidos. Novo contêiner/release inicia nova série; ausência de coleta não é consumo zero nem aplicação saudável.
- **RN-17**: Modelo experimental só pode sugerir opções previamente permitidas. Nunca autoriza permissões, cria comandos, altera segredos, remove dados ou substitui validações determinísticas.
- **RN-18**: A interface descreve o alcance real da criptografia. Criptografia em trânsito e em repouso não é anunciada como servidor incapaz de acessar segredos.
