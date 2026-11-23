// Backup dos dados de um projeto (DEC-47 B): textos e regras do painel.
import type { Schemas } from "../api/client";

export type ProjectBackup = Schemas["ProjectBackup"];
export type ProjectBackupsView = Schemas["ProjectBackupsView"];
export type ProjectRestore = Schemas["ProjectRestore"];

export const BACKUP_STATE_LABEL: Record<ProjectBackup["state"], string> = {
  queued: "na fila",
  running: "copiando",
  succeeded: "concluído",
  failed: "falhou",
};

export const BACKUP_STATE_TONE: Record<ProjectBackup["state"], "success" | "danger" | "info" | "neutral"> = {
  queued: "neutral",
  running: "info",
  succeeded: "success",
  failed: "danger",
};

export const REASON_LABEL: Record<ProjectBackup["reason"], string> = {
  manual: "pedido por você",
  before_delete: "antes de apagar",
  before_restore: "antes de restaurar",
};

/** Ainda ocupa a vaga do projeto (um por vez). */
export const isBackupActive = (b: Pick<ProjectBackup, "state">) => b.state === "queued" || b.state === "running";

/** Um backup bom que ainda não foi marcado para apagar: o que vale para "fazer backup antes de apagar". */
export const isUsable = (b: ProjectBackup) => b.state === "succeeded" && !b.delete_requested;

export const hasUsableBackup = (list: ProjectBackup[]) => list.some(isUsable);

/** Um resumo do que o backup guardou: "volume /app/data (1,2 MiB) · banco (40 KiB)". */
export function itemsSummary(b: ProjectBackup, fmt: (n: number) => string): string {
  const parts = b.manifest.items.map((it) => (it.kind === "postgres" || it.kind === "mysql" ? `banco (${fmt(it.size_bytes)})` : `volume ${it.path ?? ""} (${fmt(it.size_bytes)})`));
  return parts.join(" · ");
}

/** O endereço de download de um backup. */
export const projectBackupUrl = (projectId: string, backupId: string) => `/api/v1/projects/${projectId}/backups/${backupId}/download`;

/**
 * Pode excluir (o projeto ou o banco)? Sem nada a perder, sim; com dados, só com um backup
 * bom na mão ou com a escolha explícita de apagar sem.
 */
export function canDelete(hasData: boolean, list: ProjectBackup[], skip: boolean): boolean {
  return !hasData || hasUsableBackup(list) || skip;
}

export const RESTORE_STATE_LABEL: Record<ProjectRestore["state"], string> = {
  queued: "esperando o backup de segurança",
  running: "restaurando",
  succeeded: "restaurado",
  failed: "falhou",
};

export const RESTORE_STATE_TONE: Record<ProjectRestore["state"], "success" | "danger" | "info" | "neutral"> = {
  queued: "neutral",
  running: "info",
  succeeded: "success",
  failed: "danger",
};

/** A restauração ainda ocupa a vaga do projeto. */
export const isRestoreActive = (r: Pick<ProjectRestore, "state">) => r.state === "queued" || r.state === "running";

/**
 * O que a restauração de um backup faria hoje: os volumes do backup que o projeto ainda declara e o
 * banco (só se o projeto tem banco gerenciado); o resto é ignorado, com o motivo. É o que o painel
 * mostra antes de pedir, e é a mesma conta que a API e o worker fazem.
 */
export function restorePlan(backup: ProjectBackup, declaredPaths: string[], hasManagedDb: boolean): { restore: string[]; skipped: string[] } {
  const restore: string[] = [];
  const skipped: string[] = [];
  for (const it of backup.manifest.items) {
    if (it.kind === "volume") {
      if (declaredPaths.includes(it.path ?? "")) restore.push(`volume ${it.path}`);
      else skipped.push(`${it.path}: o projeto não declara mais este caminho`);
    } else if (hasManagedDb) {
      restore.push("banco do Cobogó");
    } else {
      skipped.push("banco: o projeto não tem banco gerenciado (crie-o na aba Banco e restaure de novo)");
    }
  }
  return { restore, skipped };
}
