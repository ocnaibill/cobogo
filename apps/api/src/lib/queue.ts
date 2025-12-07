import { Queue } from "bullmq";
import { connection } from "./redis";

// Criamos uma fila chamada 'build-queue'
export const buildQueue = new Queue("build-queue", { connection });

// Tipagem do que vai dentro do pedido de build
export interface BuildJobData {
  deploymentId: string;
  repositoryUrl: string;
  branch: string;
  projectName: string;
}
