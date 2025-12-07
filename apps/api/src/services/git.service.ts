import { exec } from "child_process";
import { promisify } from "util";

const execAsync = promisify(exec);

export class GitService {
  /**
   * Verifica se o repositório E a branch específica existem.
   * Usa 'git ls-remote --heads' para ser leve e buscar apenas o que importa.
   */
  static async validateRepository(
    url: string,
    branch: string = "main"
  ): Promise<boolean> {
    try {
      // O comando final será algo como:
      // git ls-remote --heads https://github.com/facebook/react.git main
      const { stdout } = await execAsync(
        `git ls-remote --heads ${url} ${branch}`
      );

      // Se o stdout vier vazio, o comando rodou, mas a branch não existe.
      // Se vier texto, a branch existe.
      return stdout.trim().length > 0;
    } catch (error) {
      // Importante: Logar o erro para debug
      console.error(`[GitService] Erro ao validar repo:`, error);
      return false;
    }
  }
}
