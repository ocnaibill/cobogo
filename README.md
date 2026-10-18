# Project Cobogó

> **Status:** Atlas (v0.1) | Licença: AGPLv3

Uma plataforma open-source de **_Platform as a Service_ (PaaS)** para orquestração simplificada de aplicações Node.js. O Cobogó abstrai a complexidade do Docker e Proxy Reverso, permitindo que desenvolvedores façam deploy de seus repositórios Git com um clique.

**Foco:** Developer Experience (DX), Auto-hospedagem e Soberania de dados.

## Documentação do projeto

O código atual corresponde ao protótipo Node.js descrito abaixo. A documentação do TCC apresenta a evolução proposta para Go/React, com requisitos, decisões e critérios de avaliação ainda sujeitos a revisão.

- [Documento mestre: escopo, requisitos, arquitetura, telas e roadmap](docs/PROJETO-MESTRE.md)
- [Motor de heurísticas e apoio por modelos de decisão](docs/MOTOR-DE-HEURISTICAS.md)
- [Referências comentadas e trabalhos relacionados](docs/REFERENCIAS-COMENTADAS.md)
- [Pesquisa sobre heurísticas, evidências e decisão assistida](docs/pesquisa/HEURISTICAS-REFERENCIAS.md)

Os documentos incluem diagramas Mermaid e links para suas prévias SVG.

## 💡 A Identidade

O nome **Cobogó** faz referência ao elemento arquitetônico modernista, ícone de Brasília. Assim como o cobogó é um bloco modular que permite a permeabilidade da luz e ventilação sem comprometer a estrutura, esta plataforma atua como uma estrutura modular para containers, gerenciando o fluxo de tráfego e dados enquanto garante o isolamento e segurança de cada serviço.

## 🛠️ Tech Stack

O projeto utiliza uma arquitetura de Monorepo gerenciada por NPM Workspaces.

- **Backend:** Node.js, Express, TypeScript

- **Validação & ORM:** Zod, Prisma

- **Banco de Dados:** PostgreSQL

- **Filas & Cache:** Redis, BullMQ

- **Infraestrutura:** Docker, Dockerode (Docker API)

- **Proxy Reverso:** Traefik v2

## **🚀 Como Rodar Localmente (Development)**

Siga os passos abaixo para iniciar o ambiente de desenvolvimento completo.

### **1\. Pré-requisitos**

Certifique-se de ter instalado:

- **Node.js** (lts/krypton ou superior)
- **Docker Desktop** (Deve estar rodando)
- **Git**

### **2\. Instalação**

Clone o repositório e instale as dependências. Como é um monorepo, o comando na raiz instala tudo.

```
git clone https://github.com/ocnaibill/cobogo.git
cd cobogo
npm install
```

### **3\. Configuração de Variáveis (.env)**

Configure as variáveis de ambiente da API. Um arquivo de exemplo já está configurado para o ambiente Docker padrão.

```
cd apps/api
cp .env.example .env
```

**Nota:** Verifique se o arquivo .env gerado contém REDIS_HOST=localhost e DATABASE_URL apontando para a porta 5432\.

### **4\. Subindo a Infraestrutura (Docker)**

Volte para a raiz do projeto e inicie os containers de suporte (Postgres, Redis e Traefik):

##### Na raiz do projeto (cobogo/)

```
docker compose \-f infra/docker-compose.dev.yml up \-d
```

### **5\. Configurando o Banco de Dados**

Com os containers rodando, execute as migrações do Prisma para criar as tabelas no PostgreSQL:

##### Na raiz do projeto

```
npm run db:migrate
```

### **6\. Iniciando a Aplicação**

O sistema é composto por dois processos principais que devem rodar simultaneamente. Abra **dois terminais**:

```
Terminal 1: API Server
Responsável por receber requisições HTTP e gerenciar o banco de dados.

Na raiz

npm run dev:api
A API rodará em http://localhost:3001
```

```

Terminal 2: Deploy Worker
Responsável por processar a fila, clonar repositórios e construir imagens Docker.
cd apps/api
npm run worker
```

## **🧪 Como Testar (Fluxo de Deploy)**

Como o Frontend ainda está em desenvolvimento, utilize o **Postman** ou **Insomnia**.

### **1\. Criar um Projeto**

Envie uma requisição POST para registrar o projeto e validar o repositório Git.

- **URL:** POST http://localhost:3001/projects
- **Body (JSON):**

```
{
  "name": "meu-app-teste",
  "repositoryUrl": "https://github.com/jatins/express-hello-world.git",
  "branch": "main"
}
```

### **2\. Disparar o Deploy**

Copie o id retornado no passo anterior e dispare o processo de build.

- **URL:** POST http://localhost:3001/projects/:id/deploy

### **3\. Acessar a Aplicação**

Aguarde o Worker finalizar o processo (acompanhe os logs no Terminal 2). Quando finalizar, acesse no seu navegador:

👉 **http://meu-app-teste.localhost**

## Licença

Este projeto é licenciado sob a [AGPLv3](LICENSE).
