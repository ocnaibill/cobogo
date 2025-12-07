import { Request, Response } from "express";
import { PrismaClient } from "@prisma/client";
import { z } from "zod";
import { apiResponse } from "../utils/response";
import { GitService } from "../services/git.service";

const prisma = new PrismaClient();

// Esquema de Validação
const createProjectSchema = z.object({
  name: z
    .string()
    .min(3)
    .regex(
      /^[a-z0-9-]+$/,
      "O nome deve conter apenas letras minúsculas e hífens"
    ),
  repositoryUrl: z.string().url("URL do repositório inválida"),
  branch: z.string().default("main"),
  nodeVersion: z.string().default("18"),
});

export class ProjectController {
  static async create(req: Request, res: Response) {
    try {
      // 1. Validação dos dados de entrada
      const data = createProjectSchema.parse(req.body);

      // 2. Verificar se já existe um projeto com esse nome (Slug único)
      const existingProject = await prisma.project.findUnique({
        where: { name: data.name },
      });

      if (existingProject) {
        return apiResponse(res, 400, "PROJECT_NAME_TAKEN", {
          message: "Este nome já está em uso.",
        });
      }

      // 3. Validar se o Git existe
      const isGitValid = await GitService.validateRepository(
        data.repositoryUrl,
        data.branch
      );
      if (!isGitValid) {
        return apiResponse(res, 400, "INVALID_REPOSITORY", {
          message: `O repositório não existe ou a branch '${data.branch}' não foi encontrada.`,
        });
      }

      // 4. Salvar no Banco (Temporariamente hardcoded userId até termos login)
      // Vamos pegar o primeiro usuário do banco ou criar um fake se não tiver
      let user = await prisma.user.findFirst();
      if (!user) {
        user = await prisma.user.create({
          data: {
            name: "Admin",
            email: "admin@cobogo.dev",
            passwordHash: "temp",
          },
        });
      }

      const newProject = await prisma.project.create({
        data: {
          name: data.name,
          repositoryUrl: data.repositoryUrl,
          branch: data.branch,
          nodeVersion: data.nodeVersion,
          userId: user.id,
        },
      });

      return apiResponse(res, 201, "PROJECT_CREATED", newProject);
    } catch (error: any) {
      // Se for erro do Zod (validação)
      if (error instanceof z.ZodError) {
        return apiResponse(res, 400, "VALIDATION_ERROR", error.issues);
      }
      console.error(error);
      return apiResponse(res, 500, "INTERNAL_SERVER_ERROR");
    }
  }

  static async list(req: Request, res: Response) {
    const projects = await prisma.project.findMany({
      orderBy: { createdAt: "desc" },
    });
    return apiResponse(res, 200, "PROJECTS_LISTED", projects);
  }
}
