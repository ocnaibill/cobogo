import { Router } from "express";
import { ProjectController } from "../controllers/project.controller";

const router = Router();

// Rotas de Projetos
router.post("/projects", ProjectController.create);
router.get("/projects", ProjectController.list);

// --- ROTA RAIZ ---
router.get("/", (req, res) => {
  res.json({
    success: true,
    system: "Cobogó API",
    version: "0.0.1",
  });
});

export { router };
