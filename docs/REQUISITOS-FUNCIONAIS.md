# Cobogó — requisitos funcionais

> Extraído de [PROJETO-MESTRE.md §9](PROJETO-MESTRE.md#9-requisitos-funcionais). Fase: D0 = demonstração de dezembro/2026, MVP = entrega completa, EXT = extensão, EXP = trilha experimental.

- **RF-01** (D0): Registrar projeto com URL Git, referência e diretório raiz.
- **RF-02** (D0): Resolver referência para SHA e analisar arquivos sem executar o projeto.
- **RF-03** (D0): Produzir plano inspecionável com runtime, comandos, porta, etapas e pendências.
- **RF-04** (D0): Gerar e disponibilizar Dockerfile e política de contexto de build.
- **RF-05** (D0): Executar deploy assíncrono, persistindo estado e eventos.
- **RF-06** (D0): Iniciar aplicação, verificar readiness e retornar URL roteada.
- **RF-07** (D0): Exibir logs por etapa e erro acionável.
- **RF-08** (D0): Solicitar entrada quando faltar dado e permitir corrigir plano.
- **RF-09** (D0): Autenticar pessoa administradora e proteger operações e logs.
- **RF-10** (MVP): Gerenciar variáveis e segredos de execução.
- **RF-11** (MVP): Implementar perfis Python e estático definidos na seção 7 do mestre.
- **RF-12** (MVP): Provisionar PostgreSQL auxiliar explicitamente solicitado.
- **RF-13** (MVP): Vincular variáveis ao serviço auxiliar com mapeamento explícito.
- **RF-14** (MVP): Parar, reiniciar e remover recursos de aplicação com rastreabilidade.
- **RF-15** (MVP): Atualizar aplicação preservando release anterior até validar a candidata.
- **RF-16** (MVP): Reconciliar jobs e recursos após reinício/interrupção.
- **RF-17** (MVP): Registrar e exportar métricas de avaliação sem segredos.
- **RF-18** (MVP): Aplicar retenção e limpeza apenas a recursos geridos pelo Cobogó.
- **RF-19** (EXT): Receber webhook autenticado e iniciar deploy de commit permitido.
- **RF-20** (EXT): Publicar aplicações Go conforme perfil específico.
- **RF-21** (MVP): Descobrir nomes de variáveis e apresentar pendências com evidências.
- **RF-22** (MVP): Importar `.env` por ação explícita, com revisão e classificação por campo.
- **RF-23** (MVP): Versionar configuração, trocar segredo e indicar necessidade de novo deploy.
- **RF-24** (MVP): Exibir saúde e recursos por contêiner gerido.
- **RF-25** (MVP): Agregar observação por projeto, stack operacional e perfil tecnológico.
- **RF-26** (MVP): Consultar histórico de métricas e indicar dados ausentes ou antigos.
- **RF-27** (EXP): Registrar sugestões de modelos em modo observação, sem efeito no deploy.
- **RF-28** (EXP): Comparar heurísticas e combinação com modelo em ambiente experimental.
- **RF-29** (MVP): Auditar operações sobre segredos sem registrar valores.
