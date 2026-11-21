// Operações de ciclo de vida (DEC-38): rótulos e resumo do que cada uma fez.
import type { Schemas } from "../api/client";

export type Operation = Schemas["Operation"];

export const OP_LABEL: Record<Operation["kind"], string> = {
  retire_releases: "Aposentar releases antigas",
  delete_project: "Excluir projeto",
  apply_limits: "Aplicar limites",
  sweep: "Faxina de recursos órfãos",
  stop_app: "Parar a aplicação",
  start_app: "Iniciar a aplicação",
  restart_app: "Reiniciar a aplicação",
};

export const OP_STATE_LABEL: Record<Operation["state"], string> = {
  PENDING: "Agendada",
  RUNNING: "Em execução",
  DONE: "Concluída",
  FAILED: "Falhou",
};

export const isSettled = (op: Pick<Operation, "state">) => op.state === "DONE" || op.state === "FAILED";

const list = (v: unknown): string[] =>
  Array.isArray(v) ? v.map((x) => (typeof x === "string" ? x : typeof x === "object" && x && "name" in x ? String((x as { name: unknown }).name) : String(x))) : [];

type Res = { memory_mb?: number; cpus?: number; pids?: number };
const res = (r: unknown): string => {
  const x = (r ?? {}) as Res;
  return `${x.memory_mb ?? "?"} MiB, ${x.cpus ?? "?"} CPU, ${x.pids ?? "?"} processos`;
};

/** Uma frase sobre o que a operação fez (ou por que falhou). */
export function summarize(op: Operation): string {
  const r = op.result ?? {};
  if (op.state === "FAILED") return op.error || "Falhou sem detalhe.";
  if (op.state === "PENDING" && op.attempts > 0 && op.error) return `Tentando de novo: ${op.error}`;
  if (op.state !== "DONE") return "";
  switch (op.kind) {
    case "apply_limits":
      return r.changed === false ? `Nada mudou: o contêiner já tinha ${res(r.after)}.` : `De ${res(r.before)} para ${res(r.after)}.`;
    case "stop_app":
      return "A aplicação foi parada; o endereço mostra a página de projeto inativo.";
    case "start_app":
    case "restart_app":
      return "A aplicação voltou ao ar e passou na verificação.";
    case "retire_releases":
    case "delete_project":
    case "sweep": {
      const c = list(r.containers_removed);
      const i = list(r.images_removed);
      const parts = [`${c.length} contêiner(es) removido(s)`];
      if (op.kind !== "sweep") parts.push(`${i.length} imagem(ns) removida(s)`);
      return parts.join(", ") + (c.length ? `: ${c.join(", ")}` : "") + ".";
    }
  }
  return "";
}

export type AppTransition = "stopping" | "starting" | "restarting";

const TRANSITION_OF: Partial<Record<Operation["kind"], AppTransition>> = { stop_app: "stopping", start_app: "starting", restart_app: "restarting" };

/** A aplicação está mudando de estado agora? Vem das operações do projeto, então
 * continua valendo depois de recarregar a página ou de sair e voltar. */
export function appTransition(ops: Pick<Operation, "kind" | "state">[] | undefined): AppTransition | null {
  for (const op of ops ?? []) {
    const t = TRANSITION_OF[op.kind];
    if (t && (op.state === "PENDING" || op.state === "RUNNING")) return t;
  }
  return null;
}

export const TRANSITION_LABEL: Record<AppTransition, string> = {
  stopping: "Parando…",
  starting: "Iniciando…",
  restarting: "Reiniciando…",
};

export const TRANSITION_NOTICE: Record<AppTransition, string> = {
  stopping: "Parando a aplicação… o endereço já mostra a página de projeto inativo.",
  starting: "Iniciando a aplicação e esperando ela responder…",
  restarting: "Reiniciando a aplicação…",
};
