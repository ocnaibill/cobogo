import express from "express";
import cors from "cors";
import helmet from "helmet";
import { router } from "./routes";
import { PrismaClient } from "@prisma/client";
import { apiResponse } from "./utils/response";

const app = express();
const prisma = new PrismaClient();

// Middlewares de Segurança e Utilidade
app.use(helmet());
app.use(cors());
app.use(express.json());

// Montador de Rotas
app.use(router);

const PORT = process.env.PORT || 3001;

app.listen(PORT, () => {
  console.log(`🚀 Cobogó API running on http://localhost:${PORT}`);
});
