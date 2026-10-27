# Cobogó — requisitos não funcionais

> Extraído de [PROJETO-MESTRE.md §10](PROJETO-MESTRE.md#10-requisitos-não-funcionais). Fase: D0 = demonstração de dezembro/2026, MVP = entrega completa, EXP = trilha experimental. Valores numéricos são parâmetros iniciais propostos, a validar no piloto.

- **RNF-01** (D0): Na base heurística, mesmos arquivos, configurações e versões de regras/templates produzem o mesmo plano normalizado; timestamps e IDs não entram na comparação. Variação de modelos é registrada separadamente no experimento.
- **RNF-02** (D0): 100% dos campos decisórios do plano têm origem rastreável; cada bloqueio informa dado faltante ou conflito.
- **RNF-03** (D0): Somente componentes operacionais autorizados acessam a API Docker; API pública e aplicações não recebem socket, modo privilegiado ou montagens arbitrárias do host.
- **RNF-04** (D0): Repositórios de D0 são previamente selecionados; clone com timeout e limite de tamanho; apenas HTTPS em host permitido, sem submódulos/LFS automáticos ou destinos privados por redirecionamento.
- **RNF-05** (D0): Aplicar limites de CPU, memória, duração e logs; ponto inicial: um build simultâneo, timeout de build de 10 min e readiness de 120 s. Limites efetivos do builder devem ser comprovados.
- **RNF-06** (MVP): Operações comuns de API devem ter p95 até 500 ms no piloto com cinco projetos, excluídos clone/build/streaming; publicar hardware, carga e método usados.
- **RNF-07** (MVP): Jobs usam persistência e lease; após expiração, reconciliar em até dois ciclos do worker. Proposta: lease de 60 s com heartbeat de 15 s.
- **RNF-08** (MVP): Segredos são cifrados em repouso com chave fora do banco; sessões usam proteção apropriada e cookies restritos ao hostname de gestão; verificar ausência do segredo de teste nos artefatos.
- **RNF-09** (D0): Todo deploy tem correlação entre SHA, plano, eventos, logs, imagem e release; tempos usam UTC e durações usam relógio adequado.
- **RNF-10** (MVP): Fluxo principal funciona por teclado, apresenta foco e rótulos e comunica estado sem depender apenas de cor.
- **RNF-11** (MVP): Instalação e recuperação em VM Linux limpa são documentadas; restauração do banco interno e de um volume auxiliar é demonstrada antes da avaliação.
- **RNF-12** (MVP): Regras e templates são testáveis sem Docker; testes com Docker cobrem build, readiness, persistência, falha e retomada. Mudança de perfil exige caso positivo e caso de abstenção.
- **RNF-13** (MVP): Proposta de coleta a cada 15 s e histórico bruto de 24 h; marcar como antiga uma série sem atualização por 30 s e registrar lacunas. Congelar parâmetros após medir custo no homelab.
- **RNF-14** (MVP): Usar cifragem autenticada e chaves versionadas fora do banco; adulteração do ciphertext e tentativa de usar segredo de outro projeto devem falhar. Provar restauração e troca de chave antes da avaliação.
- **RNF-15** (EXP): O adaptador experimental recebe apenas contexto permitido e sanitizado; testes com segredo-canário e conteúdo adversarial não podem produzir saída de segredo nem efeito operacional.
- **RNF-16** (MVP/EXP): Medir sobrecarga de coleta e inferência separadamente de build/runtime; limitar duração, concorrência, memória e retenção. Indisponibilidade experimental não bloqueia a base heurística.
