// Comandos pontuais (DEC-45): textos e regras do painel.
import type { Schemas } from "../api/client";

export type CommandRun = Schemas["Command"];
export type CommandState = CommandRun["state"];

export const COMMAND_STATE_LABEL: Record<CommandState, string> = {
  queued: "na fila",
  running: "rodando",
  succeeded: "concluído",
  failed: "falhou",
  timed_out: "passou do prazo",
  canceled: "cancelado",
  interrupted: "interrompido",
};

export type Tone = "success" | "warning" | "danger" | "info" | "neutral";

export const COMMAND_STATE_TONE: Record<CommandState, Tone> = {
  queued: "neutral",
  running: "info",
  succeeded: "success",
  failed: "danger",
  timed_out: "warning",
  canceled: "neutral",
  interrupted: "warning",
};

/** Ainda ocupa a vaga do projeto (um por vez). */
export const isActive = (c: Pick<CommandRun, "state">) => c.state === "queued" || c.state === "running";

/** Quanto falta para a janela de senha fechar, em segundos (0 se já fechou ou não abriu). */
export function reauthSecondsLeft(until: string | null | undefined, now = Date.now()): number {
  if (!until) return 0;
  return Math.max(0, Math.floor((new Date(until).getTime() - now) / 1000));
}

/** "mm:ss" de uma contagem regressiva. */
export function clock(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${String(s).padStart(2, "0")}`;
}
