import { describe, expect, it } from "vitest";
import { clock, isActive, reauthSecondsLeft } from "./commands";

describe("comandos pontuais", () => {
  it("só na fila ou rodando ocupa a vaga do projeto", () => {
    expect(isActive({ state: "queued" })).toBe(true);
    expect(isActive({ state: "running" })).toBe(true);
    for (const state of ["succeeded", "failed", "timed_out", "canceled", "interrupted"] as const) expect(isActive({ state })).toBe(false);
  });

  it("conta o que falta da janela da senha, sem passar de zero", () => {
    const now = Date.parse("2026-11-22T10:00:00Z");
    expect(reauthSecondsLeft("2026-11-22T10:10:00Z", now)).toBe(600);
    expect(reauthSecondsLeft("2026-11-22T09:59:00Z", now)).toBe(0);
    expect(reauthSecondsLeft(null, now)).toBe(0);
    expect(reauthSecondsLeft(undefined, now)).toBe(0);
  });

  it("mostra a contagem como mm:ss", () => {
    expect(clock(600)).toBe("10:00");
    expect(clock(65)).toBe("1:05");
    expect(clock(0)).toBe("0:00");
  });
});
