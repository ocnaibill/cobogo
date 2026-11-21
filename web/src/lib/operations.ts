// Operações de ciclo de vida (DEC-38): rótulos e resumo do que cada uma fez.
import type { Schemas } from "../api/client";

export type Operation = Schemas["Operation"];

export const OP_LABEL: Record<Operation["kind"], string> = {
  retire_releases: "Aposentar releases antigas",
  delete_project: "Excluir projeto",
  apply_limits: "Aplicar limites",
  sweep: "Faxina de recursos órfãos",
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
