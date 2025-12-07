import { Worker } from "bullmq";
import { connection } from "./lib/redis";
import { PrismaClient } from "@prisma/client";
import { BuildJobData } from "./lib/queue";
import { BuildService } from "./services/build.service";
import { ContainerService } from "./services/container.service";

const prisma = new PrismaClient();

console.log("👷 Worker de Deploy iniciado e aguardando jobs...");

/**
 * Este worker escuta a fila 'build-queue'.
 * Assim que chegar algo, ele executa a função anonima abaixo.
 */

const worker = new Worker<BuildJobData>(
  "build-queue",
  async (job) => {
    const { deploymentId, repositoryUrl, branch, projectName } = job.data;

    console.log(`[${projectName}] Iniciando Job #${job.id}`);

    console.log(`[${projectName}] Iniciando deploy #${deploymentId}...`);

    try {
      // 1. Atualiza status para BUILDING
      await prisma.deployment.update({
        where: { id: deploymentId },
        data: { status: "BUILDING" },
      });

      // 2. CHAMADA REAL DO DOCKER
      const imageName = await BuildService.buildImage(
        repositoryUrl,
        branch,
        projectName
      );

      // 3. NOVO: RODAR O CONTAINER
      // Assumimos porta 3000 por enquanto (padrão Node)
      const url = await ContainerService.startContainer(
        projectName,
        imageName,
        3000
      );

      // 4. Status: LIVE (Sucesso)
      await prisma.deployment.update({
        where: { id: deploymentId },
        data: {
          status: "LIVE",
          finishedAt: new Date(),
          // Vamos salvar a URL gerada no log ou num campo futuro (por enquanto console)
        },
      });

      console.log(
        `[${projectName}] Deploy Finalizado! Acesse em: http://${url}`
      );
    } catch (error) {
      console.error(`[${projectName}] Falha no deploy:`, error);

      // Atualiza status para FAILED
      await prisma.deployment.update({
        where: { id: deploymentId },
        data: { status: "FAILED", finishedAt: new Date() },
      });
    }
  },
  { connection }
);

// Tratamento de erros do próprio worker (ex: perdeu conexão com Redis)
worker.on("error", (err) => {
  console.error("Erro no Worker:", err);
});
