import express from 'express';
import cors from 'cors';
import helmet from 'helmet';
import { PrismaClient } from '@prisma/client';
import { apiResponse } from './utils/response';

const app = express();
const prisma = new PrismaClient();

// Middlewares de Segurança e Utilidade
app.use(helmet());
app.use(cors());
app.use(express.json());

// Rota Raiz
app.get('/', (req, res) => {
  return apiResponse(res, 200, 'COBOGO_API_RUNNING', {
    version: '0.0.1',
    status: 'active'
  });
});

// Rota de Teste do Banco (Lista Usuários)
app.get('/users', async (req, res) => {
  try {
    const users = await prisma.user.findMany();
    return apiResponse(res, 200, 'USERS_FETCHED', users);
  } catch (error) {
    return apiResponse(res, 500, 'DATABASE_ERROR', error);
  }
});

const PORT = process.env.PORT || 3001;

app.listen(PORT, () => {
  console.log(`🚀 Cobogó API running on http://localhost:${PORT}`);
});
