import Docker from "dockerode";
import simpleGit from "simple-git";
import fs from "fs-extra";
import path from "path";

const docker = new Docker({ socketPath: "/var/run/docker.sock" });

export class BuildService {
  /**
   * O coração do sistema: Clona, Cria Dockerfile e Builda
   */
  static async buildImage(
    repositoryUrl: string,
    branch: string,
    projectName: string
  ): Promise<string> {
    const imageName = `cobogo-${projectName}:latest`; // Ex: cobogo-meu-app:latest
    // Cria uma pasta temporária única para esse build
    const tempDir = path.join(
      __dirname,
      "../../temp",
      `${projectName}-${Date.now()}`
    );

    try {
      // 1. Clonar o Repositório
      console.log(`[Build] Clonando ${repositoryUrl} em ${tempDir}...`);
      await fs.ensureDir(tempDir);
      await simpleGit().clone(repositoryUrl, tempDir, [
        "--branch",
        branch,
        "--depth",
        "1",
      ]);

      // 2. Auto-Detectar Dockerfile
      const dockerfilePath = path.join(tempDir, "Dockerfile");
      if (!fs.existsSync(dockerfilePath)) {
        console.log(
          `[Build] Dockerfile não encontrado. Gerando um automático para Node.js...`
        );
        await this.createDefaultDockerfile(tempDir);
      }

      // 3. Mandar o Docker construir a imagem
      console.log(`[Build] Iniciando Docker Build para ${imageName}...`);

      // O dockerode espera um arquivo TAR ou uma pasta.
      const stream = await docker.buildImage(
        {
          context: tempDir,
          src: ["."], // Manda tudo que está na pasta
        },
        {
          t: imageName, // Tag da imagem
        }
      );

      // O Docker retorna um "Stream" de logs. Precisamos esperar ele terminar.
      await new Promise((resolve, reject) => {
        docker.modem.followProgress(
          stream,
          (err, res) => {
            if (err) return reject(err);
            resolve(res);
          },
          (event) => {
            // Captura de logs em tempo real
            if (event.stream) {
              process.stdout.write(event.stream); // Joga no console por enquanto
            }
          }
        );
      });

      console.log(`[Build] Imagem ${imageName} criada com sucesso!`);
      return imageName;
    } catch (error) {
      throw error;
    } finally {
      // 4. Limpeza: Apagar a pasta temporária para não encher o disco
      await fs.remove(tempDir);
    }
  }

  /**
   * Cria um Dockerfile genérico para Node.js se o usuário não tiver um
   */
  private static async createDefaultDockerfile(dir: string) {
    const content = `
      FROM node:18-alpine
      WORKDIR /app
      
      # Otimização de Cache: Copia só package.json primeiro
      COPY package*.json ./
      RUN npm install --production
      
      # Copia o resto do código
      COPY . .
      
      # Build (se houver script de build no package.json)
      # RUN npm run build --if-present
      
      # Porta padrão
      EXPOSE 3000
      
      # Comando de Start
      CMD ["npm", "start"]
    `;

    await fs.writeFile(path.join(dir, "Dockerfile"), content);
  }
}
