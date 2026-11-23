// O mapa do repositório (DEC-49): rótulos dos papéis e o slug do projeto de uma pasta irmã.

export const ROLE_LABEL: Record<string, string> = {
  frontend: "Frontend",
  backend: "Backend",
  fullstack: "Aplicação completa",
  app: "Aplicação",
  worker: "Worker",
  database: "Banco de dados",
  cache: "Cache",
  queue: "Fila",
};

export const ROLE_TONE: Record<string, "brand" | "success" | "neutral"> = {
  frontend: "brand",
  backend: "brand",
  fullstack: "brand",
  app: "brand",
  worker: "neutral",
  database: "success",
  cache: "success",
  queue: "success",
};

/** O slug sugerido para o projeto de uma pasta irmã: `<projeto>-<pasta>`, no formato que a API aceita. */
export function siblingSlug(projectSlug: string, dir: string): string {
  const base = dir.split("/").filter(Boolean).pop() ?? "app";
  const clean = `${projectSlug}-${base}`
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, "-")
    .replace(/-+/g, "-")
    .slice(0, 63)
    .replace(/^-+|-+$/g, "");
  return clean || "projeto";
}

/** A pasta de um projeto sem `./` nem barras nas pontas; vazio para a raiz. */
export function normalizeDir(dir: string): string {
  return dir.replace(/^\.\//, "").replace(/^\/+|\/+$/g, "").replace(/^\.$/, "");
}
