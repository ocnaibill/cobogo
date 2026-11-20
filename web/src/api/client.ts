// Cliente da API do Cobogó, tipado a partir de docs/api/openapi.yaml (DEC-28).
// Os tipos vêm de `npm run gen:api`; nada aqui é escrito à mão sobre o formato
// das respostas.
import createClient, { type Middleware } from "openapi-fetch";
import type { components, paths } from "./schema";

export type Schemas = components["schemas"];
export type DeploymentState = Schemas["DeploymentState"];
export type Deployment = Schemas["Deployment"];
export type DeploymentEvent = Schemas["DeploymentEvent"];
export type Release = Schemas["Release"];
export type Project = Schemas["Project"];
export type ProjectSummary = Schemas["ProjectSummary"];
export type Analysis = Schemas["Analysis"];
export type Finding = Schemas["Finding"];
export type Plan = Schemas["Plan"];
export type EnvVariable = Schemas["EnvVariable"];
export type ConfigVariable = Schemas["ConfigVariable"];
export type User = Schemas["User"];

/** Falha da API no formato único `{"error": {"code", "message"}}` (DEC-27). */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

/** Avisa a aplicação de que a sessão acabou, para mostrar a revalidação (TEL-01). */
export const SESSION_EXPIRED_EVENT = "cobogo:sessao-expirada";

// Rotas em que um 401 é resposta esperada, não sessão vencida.
const AUTH_ROUTES = ["/auth/login", "/auth/me", "/setup", "/auth/password"];

const sessionWatcher: Middleware = {
  onResponse({ response, request }) {
    if (response.status === 401) {
      const path = new URL(request.url).pathname.replace(/^\/api\/v1/, "");
      if (!AUTH_ROUTES.some((r) => path === r)) {
        window.dispatchEvent(new Event(SESSION_EXPIRED_EVENT));
      }
    }
    return response;
  },
};

export const api = createClient<paths>({
  baseUrl: "/api/v1",
  credentials: "same-origin",
});
api.use(sessionWatcher);

type Result<T> = { data?: T; error?: unknown; response: Response };

/** Devolve o corpo de sucesso ou lança ApiError com código e mensagem. */
export async function unwrap<T>(request: Promise<Result<T>>): Promise<T> {
  let res: Result<T>;
  try {
    res = await request;
  } catch {
    throw new ApiError(0, "network", "Sem resposta do servidor. Confira a conexão.");
  }
  if (res.response.ok) return res.data as T;
  const body = res.error as { error?: { code?: string; message?: string } } | undefined;
  throw new ApiError(
    res.response.status,
    body?.error?.code ?? "internal",
    body?.error?.message ?? `Erro ${res.response.status}`,
  );
}

/** Chave única para criar deploys sem duplicar ao repetir um clique (RN-07). */
export function idempotencyKey(): string {
  return `painel-${crypto.randomUUID()}`;
}
