import { Worker } from "bullmq";
import { connection } from "./lib/redis";
import { PrismaClient } from "@prisma/client";
import { BuildJobData } from "./lib/queue";

const prisma = new PrismaClient();

console.log("👷 Worker de Deploy iniciado e aguardando jobs...");

/**
 * Este worker escuta a fila 'build-queue'.
 * Assim que chegar algo, ele executa a função anonima abaixo.
 */
const worker = new Worker<BuildJobData>(
  "build-queue",
  async (job) => {
    const { deploymentId, projectName } = job.data;

    console.log(`[${projectName}] Iniciando deploy #${deploymentId}...`);

    try {
      // 1. Atualiza status para BUILDING
      await prisma.deployment.update({
        where: { id: deploymentId },
        data: { status: "BUILDING" },
      });

      // 2. SIMULAÇÃO DO DOCKER (5 segundos)
      // Aqui vai entrar a lógica real do Docker depois
      await new Promise((resolve) => setTimeout(resolve, 5000));

      // Simula um log
      console.log(`[${projectName}] Construindo imagem Docker... (Fake)`);

      // 3. Atualiza status para LIVE (Sucesso)
      await prisma.deployment.update({
        where: { id: deploymentId },
        data: {
          status: "LIVE",
          finishedAt: new Date(),
        },
      });

      console.log(`[${projectName}] Deploy finalizado com sucesso! 🚀`);
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
