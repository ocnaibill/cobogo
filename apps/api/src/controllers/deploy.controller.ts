import { Request, Response } from "express";
import { PrismaClient } from "@prisma/client";
import { apiResponse } from "../utils/response";
import { buildQueue } from "../lib/queue";

const prisma = new PrismaClient();

export class DeployController {
  static async trigger(req: Request, res: Response) {
    const { projectId } = req.params;

    try {
      // 1. Busca o projeto
      const project = await prisma.project.findUnique({
        where: { id: projectId },
      });

      if (!project) {
        return apiResponse(res, 404, "PROJECT_NOT_FOUND");
      }

      // 2. Cria o registro do Deployment no Banco (Status: QUEUED)
      const deployment = await prisma.deployment.create({
        data: {
          projectId: project.id,
          status: "QUEUED",
        },
      });

      // 3. Adiciona o trabalho na Fila do Redis (O Worker vai pegar depois)
      await buildQueue.add("deploy-job", {
        deploymentId: deployment.id,
        repositoryUrl: project.repositoryUrl,
        branch: project.branch,
        projectName: project.name,
      });

      // 4. Retorna sucesso imediato para o usuário não ficar esperando
      return apiResponse(res, 202, "DEPLOYMENT_QUEUED", {
        deploymentId: deployment.id,
        status: "QUEUED",
      });
    } catch (error) {
      console.error(error);
      return apiResponse(res, 500, "INTERNAL_SERVER_ERROR");
    }
  }
}
