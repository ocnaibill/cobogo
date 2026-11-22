import { describe, expect, it } from "vitest";
import { backupHealth, downloadUrl, isScheduled, numberSetting, type BackupsView } from "./backups";

const view = (over: Partial<BackupsView> = {}): BackupsView => ({
  configured: true,
  status: { last_run_at: null, last_ok_at: "2026-11-22T03:00:00Z", last_file: "x.dump", last_error: "", next_run_at: null, keep_daily: 7, keep_weekly: 4 },
  backups: [{ name: "x.dump", size_bytes: 10, created_at: "2026-11-22T03:00:00Z", label: "", sha256: "a" }],
  stale: false,
  stale_after: "36h0m0s",
  total_bytes: 10,
  ...over,
});

describe("backups da plataforma", () => {
  it("diz em que pé está o backup", () => {
    expect(backupHealth(view())).toBe("ok");
    expect(backupHealth(view({ stale: true }))).toBe("stale");
    expect(backupHealth(view({ configured: false }))).toBe("unavailable");
    expect(backupHealth(view({ backups: [], status: { ...view().status, last_ok_at: null } }))).toBe("never");
    expect(backupHealth(view({ status: { ...view().status, last_error: "pg_dump: sem conexão" } }))).toBe("error");
    // atrasado vale mais que o erro da última tentativa
    expect(backupHealth(view({ stale: true, status: { ...view().status, last_error: "x" } }))).toBe("stale");
  });

  it("separa os agendados dos pedidos com rótulo", () => {
    expect(isScheduled({ name: "a", size_bytes: 1, created_at: "", label: "", sha256: "" })).toBe(true);
    expect(isScheduled({ name: "a", size_bytes: 1, created_at: "", label: "pre-abc", sha256: "" })).toBe(false);
  });

  it("monta o endereço de download com o nome codificado", () => {
    expect(downloadUrl("cobogo-core-20261122-030000-pre-abc.dump")).toBe("/api/v1/backups/cobogo-core-20261122-030000-pre-abc.dump/download");
    expect(downloadUrl("a b")).toBe("/api/v1/backups/a%20b/download");
  });

  it("lê uma configuração numérica, ou nada", () => {
    const s = [{ key: "backup_keep_daily", kind: "integer", value: 9, source: "database", default: 7 }] as never;
    expect(numberSetting(s, "backup_keep_daily")).toBe(9);
    expect(numberSetting(s, "outra")).toBeUndefined();
    expect(numberSetting(undefined, "x")).toBeUndefined();
  });
});
