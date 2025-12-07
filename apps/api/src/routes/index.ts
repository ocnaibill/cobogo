import { Router } from "express";
import { ProjectController } from "../controllers/project.controller";
import { DeployController } from "../controllers/deploy.controller";

const router = Router();

// Rotas de Projetos
router.post("/projects", ProjectController.create);
router.get("/projects", ProjectController.list);

// Rota para disparo de deploy
router.post("/projects/:projectId/deploy", DeployController.trigger);

// --- ROTA RAIZ ---
router.get("/", (req, res) => {
  res.json({
    success: true,
    system: "Cobogó API",
    version: "0.0.1",
  });
});

export { router };
