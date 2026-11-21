import { describe, expect, it } from "vitest";
import { isSettled, summarize, type Operation } from "./operations";

const op = (over: Partial<Operation>): Operation => ({
  id: "1", kind: "retire_releases", state: "DONE", params: {}, result: {}, attempts: 1,
  not_before: "2026-11-20T10:00:00Z", created_at: "2026-11-20T10:00:00Z", updated_at: "2026-11-20T10:00:00Z", ...over,
});

describe("summarize", () => {
  it("names what was retired", () => {
    const text = summarize(op({ result: { containers_removed: [{ name: "cobogo-app-1", reason: "x" }], images_removed: ["cobogo/app:abc"] } }));
    expect(text).toContain("1 contêiner(es) removido(s)");
    expect(text).toContain("1 imagem(ns) removida(s)");
    expect(text).toContain("cobogo-app-1");
  });
  it("shows before and after limits, or says nothing changed", () => {
    const before = { memory_mb: 1024, cpus: 1, pids: 256 };
    const after = { memory_mb: 512, cpus: 1, pids: 256 };
    expect(summarize(op({ kind: "apply_limits", result: { before, after, changed: true } }))).toBe("De 1024 MiB, 1 CPU, 256 processos para 512 MiB, 1 CPU, 256 processos.");
    expect(summarize(op({ kind: "apply_limits", result: { before, after: before, changed: false } }))).toContain("Nada mudou");
  });
  it("explains a refusal in the operation's own words", () => {
    expect(summarize(op({ kind: "apply_limits", state: "FAILED", error: "a aplicação usa 300 MiB agora" }))).toBe("a aplicação usa 300 MiB agora");
    expect(summarize(op({ state: "FAILED" }))).toContain("sem detalhe");
  });
  it("says it will retry, and is quiet while pending", () => {
    expect(summarize(op({ state: "PENDING", attempts: 1, error: "docker fora do ar" }))).toContain("Tentando de novo");
    expect(summarize(op({ state: "PENDING", attempts: 0 }))).toBe("");
  });
  it("knows when an operation is over", () => {
    expect(isSettled(op({ state: "DONE" }))).toBe(true);
    expect(isSettled(op({ state: "FAILED" }))).toBe(true);
    expect(isSettled(op({ state: "RUNNING" }))).toBe(false);
  });
});
