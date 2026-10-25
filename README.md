# Project Cobogó

> **Status:** M1 em Go (fatia vertical inicial) | Licença: AGPLv3

Uma plataforma open-source de **_Platform as a Service_ (PaaS)** guiada por heurísticas: analisa um repositório, produz um plano de deploy explicável, gera o Dockerfile e conduz a aplicação até um contêiner verificado.

**Foco:** Developer Experience (DX), explicabilidade das decisões e auto-hospedagem.

## Documentação do projeto

O protótipo original em Node.js/TypeScript foi preservado na tag [`prototype-node`](https://github.com/ocnaibill/cobogo/tree/prototype-node) e removido do `main` após a primeira fatia vertical em Go entrar em funcionamento (DEC-22). O código atual implementa o recorte M1 do documento mestre: um perfil (Node.js/npm), análise explicável, geração de Dockerfile, build e contêiner reais.

- [Documento mestre: escopo, requisitos, arquitetura, telas e roadmap](docs/PROJETO-MESTRE.md)
- [Motor de heurísticas e apoio por modelos de decisão](docs/MOTOR-DE-HEURISTICAS.md)
- [Referências comentadas e trabalhos relacionados](docs/REFERENCIAS-COMENTADAS.md)
- [Pesquisa sobre heurísticas, evidências e decisão assistida](docs/pesquisa/HEURISTICAS-REFERENCIAS.md)

Os documentos incluem diagramas Mermaid e links para suas prévias SVG.

## 💡 A identidade

O nome **Cobogó** faz referência ao elemento arquitetônico modernista, ícone de Brasília. Assim como o cobogó é um bloco modular que permite a permeabilidade da luz e ventilação sem comprometer a estrutura, esta plataforma atua como uma estrutura modular para containers, gerenciando o fluxo de tráfego e dados enquanto garante o isolamento e segurança de cada serviço.

## 🛠️ Tech stack

- **Backend:** Go, API e worker como processos separados (DEC-01, DEC-02)
- **Banco de dados:** PostgreSQL, com fila de jobs via `SKIP LOCKED` (DEC-03, §12.3)
- **Build e execução:** Docker Engine SDK oficial (`github.com/docker/docker`)
- **Frontend:** React (ainda não iniciado; chega em M2)

Ver [§12 do mestre](docs/PROJETO-MESTRE.md#12-arquitetura-proposta) para a arquitetura completa e [§17.2](docs/PROJETO-MESTRE.md#172-execução-dod-e-isolamento-entre-projetos) para a topologia de execução prevista no homelab (DEC-19). Em M1, o worker fala direto com o daemon Docker local; os três proxies filtrados entram ao implantar na VM do Proxmox.

## **🚀 Como rodar localmente (desenvolvimento)**

### **1. Pré-requisitos**

- **Go** 1.25 ou superior
- **Docker** (rodando e acessível localmente)
- **PostgreSQL** (pode ser o do `infra/docker-compose.dev.yml`)
- **Git**

### **2. Banco de dados**

```bash
docker compose -f infra/docker-compose.dev.yml up -d postgres
```

As migrations são aplicadas automaticamente pela API/worker na inicialização.

### **3. Rodando API e worker**

Em dois terminais, na raiz do projeto:

```bash
# Terminal 1 — API
DATABASE_URL="postgres://cobogo:devpassword@localhost:5432/cobogo_core?sslmode=disable" go run ./cmd/api
```

```bash
# Terminal 2 — Worker
DATABASE_URL="postgres://cobogo:devpassword@localhost:5432/cobogo_core?sslmode=disable" go run ./cmd/worker
```

A API sobe em `http://localhost:8080` por padrão (`API_ADDR` para trocar).

## **🧪 Como testar (fluxo de deploy)**

Ainda sem frontend (chega em M2); use `curl` ou um cliente HTTP.

### **1. Registrar um projeto**

```bash
curl -X POST localhost:8080/projects -H 'content-type: application/json' -d '{
  "slug": "meu-app-teste",
  "repository_url": "https://github.com/exemplo/repositorio",
  "root_directory": ""
}'
```

### **2. Disparar o deploy**

```bash
curl -X POST localhost:8080/projects/<id>/deployments -H 'content-type: application/json' -d '{
  "idempotency_key": "tentativa-1",
  "port": 3000
}'
```

`port` é o override explícito (RN-03); se a análise não encontrar um contrato de porta inferível, o deploy para em `NEEDS_INPUT` em vez de inventar um valor (RN-02).

### **3. Acompanhar o plano e os eventos**

```bash
curl localhost:8080/deployments/<id>
```

Ao suceder, a resposta inclui a `release` com a porta publicada no host para acesso direto ao contêiner.

## Licença

Este projeto é licenciado sob a [AGPLv3](LICENSE).
