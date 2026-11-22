// Backups da plataforma (DEC-47): textos e regras do painel.
import type { Schemas } from "../api/client";

export type BackupsView = Schemas["BackupsView"];
export type BackupFile = Schemas["BackupFile"];

export type BackupHealth = "ok" | "stale" | "never" | "error" | "unavailable";

/** Em que pé está o backup: sem volume, nunca rodou, atrasado, com erro na última tentativa, ou em dia. */
export function backupHealth(v: BackupsView): BackupHealth {
  if (!v.configured) return "unavailable";
  if (!v.status.last_ok_at && v.backups.length === 0) return "never";
  if (v.stale) return "stale";
  if (v.status.last_error) return "error";
  return "ok";
}

export const HEALTH_TEXT: Record<BackupHealth, { tone: "success" | "warning" | "danger" | "neutral"; label: string }> = {
  ok: { tone: "success", label: "em dia" },
  error: { tone: "warning", label: "a última tentativa falhou" },
  stale: { tone: "danger", label: "atrasado" },
  never: { tone: "danger", label: "nenhum backup ainda" },
  unavailable: { tone: "neutral", label: "volume de backups não montado" },
};

/** Backups feitos pelo agendamento (sem rótulo) e os pedidos antes de uma atualização. */
export const isScheduled = (f: BackupFile) => f.label === "";

/** O endereço de download de um backup. */
export const downloadUrl = (name: string) => `/api/v1/backups/${encodeURIComponent(name)}/download`;

/** Valor de uma configuração numérica pelo painel (ou o padrão). */
export function numberSetting(settings: Schemas["Setting"][] | undefined, key: string): number | undefined {
  const v = settings?.find((s) => s.key === key)?.value;
  return typeof v === "number" ? v : undefined;
}
