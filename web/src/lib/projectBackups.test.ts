import { describe, expect, it } from "vitest";
import { canDelete, hasUsableBackup, isBackupActive, itemsSummary, projectBackupUrl, type ProjectBackup } from "./projectBackups";

const backup = (over: Partial<ProjectBackup> = {}): ProjectBackup => ({
  id: "b1", project_id: "p1", reason: "manual", pause_app: false, state: "succeeded", size_bytes: 100, delete_requested: false,
  created_at: "2026-11-22T10:00:00Z", started_at: null, finished_at: null,
  manifest: { version: 1, project_id: "p1", project_slug: "x", created_at: "2026-11-22T10:00:00Z", paused_app: false, reason: "manual", items: [] },
  ...over,
});

describe("backup dos dados do projeto", () => {
  it("só na fila ou copiando ocupa a vaga", () => {
    expect(isBackupActive({ state: "queued" })).toBe(true);
    expect(isBackupActive({ state: "running" })).toBe(true);
    expect(isBackupActive({ state: "succeeded" })).toBe(false);
    expect(isBackupActive({ state: "failed" })).toBe(false);
  });

  it("um backup serve se foi bom e não está marcado para apagar", () => {
    expect(hasUsableBackup([backup()])).toBe(true);
    expect(hasUsableBackup([backup({ state: "failed" }), backup({ delete_requested: true }), backup({ state: "running" })])).toBe(false);
    expect(hasUsableBackup([])).toBe(false);
  });

  it("só libera a exclusão com dados se há um backup bom ou se a pessoa escolhe apagar sem", () => {
    expect(canDelete(false, [], false)).toBe(true); // nada a perder
    expect(canDelete(true, [], false)).toBe(false);
    expect(canDelete(true, [backup({ state: "failed" })], false)).toBe(false);
    expect(canDelete(true, [backup()], false)).toBe(true);
    expect(canDelete(true, [], true)).toBe(true);
  });

  it("resume o que o backup guardou", () => {
    const b = backup({ manifest: { ...backup().manifest, items: [
      { kind: "volume", path: "/app/data", file: "v.tar.gz", size_bytes: 2048, sha256: "a" },
      { kind: "postgres", file: "database.dump", size_bytes: 100, sha256: "b" },
    ] } });
    expect(itemsSummary(b, (n) => `${n} B`)).toBe("volume /app/data (2048 B) · banco (100 B)");
  });

  it("monta o endereço de download", () => {
    expect(projectBackupUrl("p1", "b1")).toBe("/api/v1/projects/p1/backups/b1/download");
  });
});
