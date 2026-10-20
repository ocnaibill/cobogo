# DEC-19 — Topologia de execução e fronteira de confiança

> **Estado:** aceita em 20/10/2026.
>
> **Escopo:** como o Cobogó é executado na VM do homelab, como ele acessa o Docker e
> qual modelo de tenancy o D0/MVP assume.
>
> **Relaciona-se com:** DEC-02 (API e worker separados), DEC-04 (VM Linux no Proxmox),
> DEC-12 (operador confiável), DEC-20 (níveis de tenancy), DEC-21 (PostgreSQL),
> DEC-22 (destino do protótipo), RNF-03, RNF-05, §17.2 do documento mestre.

## Contexto

O produto precisa criar containers e publicá-los por nome de host. O Traefik descobre
serviços pelo provider Docker do host e roteia por label. O worker executa builds, que
são a etapa mais exposta a código de terceiros. A API não precisa de Docker para
nenhuma de suas funções.

O protótipo Node já opera no modelo DOD: conecta em `/var/run/docker.sock` e cria os
containers das aplicações com labels do Traefik na rede `cobogo-net`. A decisão
registrada aqui preserva essa direção e corrige a rede compartilhada.

A pergunta de pesquisa é sobre o esforço até o primeiro deploy funcional. Nenhuma
hipótese depende de isolamento entre inquilinos desconhecidos.

## Decisão

### 1. Containers com acesso ao daemon do host; DinD proibido

O Cobogó roda em containers na VM, e esses containers falam com o `dockerd` **do host**
por proxies filtrados. Não existe daemon aninhado.

| Modelo | Como funciona | Consequência para o Cobogó |
| --- | --- | --- |
| DinD | Um `dockerd` dentro do container, exigindo `--privileged` | O Traefik do host **não vê** os containers criados; cache de imagens e disco duplicados; rede e `docker stats` no daemon errado |
| DOD | O container usa o socket do daemon do host | Os containers criados são **irmãos** do plano de controle no daemon do host; roteamento por label funciona; custo ≈ nativo |

Sobre a preocupação de desempenho com camadas: não é o fator decisivo. Uma única camada
de aninhamento não seria catastrophicamente lenta por si. O que inviabiliza o DinD é o
modelo de descoberta e roteamento, além do armazenamento: quando o sistema de arquivos
de trás já é overlayfs, o driver `overlay2` do daemon interno não pode ser usado (o
daemon recusa ou degrada para `vfs`), e é necessário dedicar um volume a
`/var/lib/docker` para evitar perda e piora de desempenho.

### 2. Três proxies, três níveis de privilégio

| Componente | Rede | Endpoints liberados | Justificativa |
| --- | --- | --- | --- |
| `docker-proxy-rw` | `cobogo-control` | `POST`, `BUILD`, `CONTAINERS`, `IMAGES`, `NETWORKS`, `VOLUMES`, `ALLOW_START/STOP/RESTARTS` | O worker conduz o ciclo de vida das releases |
| `docker-proxy-ro` | `cobogo-control` | `CONTAINERS`, `EVENTS`, sem `POST` | O collector só lê estado e amostras |
| `docker-proxy-discovery` | `cobogo-control` | Somente leitura, o subconjunto que o provider do Traefik exige | Descoberta restrita, conforme RNF-03 |

Nenhum proxy publica porta. `EXEC`, `SECRETS`, `AUTH` e `SWARM` ficam em zero. A API não
recebe proxy nenhum.

### 3. Três redes

| Rede | Participantes | Regra |
| --- | --- | --- |
| `cobogo-control` | API, PostgreSQL interno, worker, collector, proxies | Nenhuma aplicação publicada participa |
| `cobogo-ingress` | Traefik e releases ativas | Só roteamento; sem acesso ao banco de controle |
| `cobogo-proj-<id>` | Aplicação e seu serviço auxiliar | Uma por projeto; volume e credencial exclusivos |

### 4. Uma fronteira de confiança no D0/MVP (DEC-20)

| Nível | Quem publica | O que exige | Situação |
| --- | --- | --- | --- |
| A | Operador e corpus selecionado | DOD, redes separadas, limites, volumes próprios | **Escopo do D0 e do MVP** |
| B | Vários usuários conhecidos | Autorização por dono, quota por projeto, auditoria com ator, isolamento por projeto | Projetar agora; entregar como extensão |
| C | Desconhecidos, código hostil | Host ou daemon separado, runtime com isolamento próprio (microVM, gVisor, sysbox), política de egresso, controle de abuso, faturamento | **Fora de escopo** |

Projetar para B significa, hoje: `owner_id` explícito em toda entidade, consulta sempre
escopada pelo dono, nomes de recurso derivados do identificador do projeto, validação de
propriedade antes de qualquer efeito externo, quota por projeto e evento de auditoria com
ator. Nada disso altera a topologia de execução.

## Alternativas consideradas

| Alternativa | Por que não |
| --- | --- |
| Docker-in-Docker | Quebra a descoberta do Traefik, duplica cache e disco, exige `--privileged` e coloca métricas no daemon errado. É desenhado para CI efêmero, não para hospedar releases endereçáveis |
| Cobogó nativo via systemd | Funciona e elimina uma camada, mas perde reprodutibilidade de instalação, não cria fronteira de privilégio entre componentes e dificulta o próprio discurso de release imutável |
| BuildKit dedicado (`buildkitd`) desde o D0 | Melhor isolamento de build e é o modelo de "servidor de build" usado por plataformas maduras, mas adiciona configuração, transferência de imagem e mais um serviço antes de existir uma fatia vertical. Fica como gatilho de revisão |
| Daemon separado para workloads desde já | Reduz o impacto de um worker comprometido, mas exige segunda VM ou segundo daemon e não é necessário no nível A |

## Consequências

**Positivas**

- O roteamento por label continua funcionando sem configuração adicional.
- Desempenho praticamente nativo e um único cache de imagens.
- A separação de privilégio fica explícita: API sem Docker, worker com escrita, collector
  com leitura.
- A decisão é defensável academicamente: separa privilégio de processo, não apenas de
  código.

**Negativas e riscos residuais**

- O socket equivale a root no host. **Comprometer o worker significa comprometer a VM**,
  incluindo volumes de todos os projetos e o banco de controle.
- O proxy filtra por prefixo de URL, não inspeciona o corpo das requisições. Com `POST` e
  `CONTAINERS` liberados, um worker comprometido ainda pode criar container privilegiado
  com o sistema de arquivos do host montado. O proxy protege contra erro de
  implementação; não contém um worker comprometido.
- Montar `docker.sock` como somente leitura **não** o torna somente leitura.
- O nível A é um limite assumido, não uma garantia de isolamento entre clientes.

**Mitigações obrigatórias** (detalhadas em §17.2 do mestre)

1. Aplicações nunca na rede de controle; proxies nunca em rede de projeto ou ingress.
2. Só volumes nomeados com prefixo derivado do projeto, com propriedade validada; bind
   mount do host proibido.
3. O repositório analisado não influencia `HostConfig`.
4. Build em rede própria, sem segredos e sem rota para a rede de controle.
5. Worker sem privilégio, rootfs somente leitura, capabilities removidas, limites de
   CPU, memória, disco e duração.
6. Imagens base fixadas por digest.

## Critérios de revisão

Reabrir esta decisão quando qualquer condição ocorrer:

- necessidade de um segundo worker ou de builds paralelos (RNF-05 hoje fixa um);
- mais de um host ou mais de uma VM;
- entrada de usuários desconhecidos, mesmo que só para teste;
- necessidade de builds com segredos;
- custo de isolamento de build medido como aceitável para um `buildkitd` dedicado;
- incidente em que o worker precise ser tratado como não confiável.

## Evidência utilizada

- Docker Engine — segurança e modelo de privilégio do daemon:
  [docs.docker.com/engine/security](https://docs.docker.com/engine/security/).
- Docker Engine — modo rootless, como alternativa de redução de privilégio:
  [docs.docker.com/engine/security/rootless](https://docs.docker.com/engine/security/rootless/).
- Docker Engine — driver overlay2 e restrições de empilhamento:
  [docs.docker.com/engine/storage/drivers/overlayfs-driver](https://docs.docker.com/engine/storage/drivers/overlayfs-driver/).
- `tecnativa/docker-socket-proxy` — lista de endpoints, aviso de que a filtragem é por
  prefixo de URL e recomendação de nunca publicar a porta:
  [github.com/Tecnativa/docker-socket-proxy](https://github.com/Tecnativa/docker-socket-proxy).
- Traefik — provider Docker e considerações de segurança:
  [doc.traefik.io/traefik/reference/install-configuration/providers/docker](https://doc.traefik.io/traefik/reference/install-configuration/providers/docker/).
- Coolify — modelo de build no servidor de deploy e possibilidade de servidor de build
  dedicado entregando por registry:
  [coolify.io/docs/core/build-deployment-model](https://coolify.io/docs/core/build-deployment-model).

Essas fontes descrevem mecanismos, não medem o Cobogó. Nenhum benchmark de desempenho
foi executado para esta decisão.
