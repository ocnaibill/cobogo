import { describe, expect, it } from "vitest";
import type { DeploymentEvent } from "../api/client";
import { canCancel, eventToLines, failureDetail, stageStatuses, stateTone } from "./deploy";

let seq = 0;
const ev = (stage: string, event_type: string, payload: Record<string, unknown> = {}): DeploymentEvent => ({
  id: `e${++seq}`,
  deployment_id: "d",
  sequence: seq,
  stage,
  event_type,
  occurred_at: "2026-11-20T10:00:00Z",
  payload,
});

describe("stageStatuses", () => {
  it("marca o que concluiu, a etapa atual e o que falta", () => {
    const s = stageStatuses("BUILDING", [ev("fetching", "completed"), ev("analyzing", "completed"), ev("generating", "completed")]);
    expect(s).toMatchObject({ fetching: "done", analyzing: "done", generating: "done", building: "current", starting: "pending", verifying: "pending" });
  });

  it("uma análise que pediu informação espera por você, não está concluída", () => {
    const s = stageStatuses("NEEDS_INPUT", [ev("fetching", "completed"), ev("analyzing", "completed")]);
    expect(s.analyzing).toBe("waiting");
    expect(s.generating).toBe("pending");
  });

  it("aponta a etapa que falhou", () => {
    const s = stageStatuses("FAILED", [ev("fetching", "completed"), ev("analyzing", "completed"), ev("generating", "completed"), ev("building", "completed"), ev("starting", "completed"), ev("verifying", "failed", { error: "x" })]);
    expect(s.verifying).toBe("failed");
    expect(s.starting).toBe("done");
  });

  it("provisionamento conta como início", () => {
    expect(stageStatuses("PROVISIONING", []).starting).toBe("current");
  });

  it("uma retomada recomeça: o que veio antes do último attempt não conta", () => {
    const s = stageStatuses("FETCHING", [ev("pipeline", "attempt", { number: 1 }), ev("fetching", "completed"), ev("analyzing", "completed"), ev("pipeline", "attempt", { number: 2, resumed: true })]);
    expect(s.fetching).toBe("current");
    expect(s.analyzing).toBe("pending");
  });

  it("sucesso marca todas as etapas", () => {
    expect(Object.values(stageStatuses("SUCCEEDED", [])).every((v) => v === "done")).toBe(true);
  });
});

describe("eventToLines", () => {
  it("mostra a saída do build como está", () => {
    expect(eventToLines(ev("building", "log", { line: "Step 1/9 : FROM node:26" }))[0]).toMatchObject({ kind: "output", text: "Step 1/9 : FROM node:26" });
  });
  it("resume eventos estruturados em português", () => {
    expect(eventToLines(ev("fetching", "completed", { commit_sha: "d33649d9999" }))[0]?.text).toBe("✓ código: concluído (commit d33649d)");
    expect(eventToLines(ev("verifying", "succeeded", { url: "https://app.cobogo.dev/" }))[0]).toMatchObject({ kind: "success", text: "✓ No ar em https://app.cobogo.dev/" });
    expect(eventToLines(ev("pipeline", "attempt", { number: 2, resumed: true }))[0]?.kind).toBe("warning");
  });
  it("só conta as variáveis entregues, nunca mostra nomes ou valores", () => {
    const line = eventToLines(ev("starting", "completed", { env: [{ name: "DB_PASSWORD", version: 1 }] }))[0]!.text;
    expect(line).toContain("1 variável");
    expect(line).not.toContain("DB_PASSWORD");
  });
});

describe("failureDetail", () => {
  it("pega o último evento de falha com log, código e diagnóstico", () => {
    const d = failureDetail([
      ev("verifying", "probe", { status: 500 }),
      ev("verifying", "failed", { error: "saiu", exit_code: 1, log_tail: "Error: X", diagnosis: { confidence: "confirmed", message: "provável causa: `X`", variables: ["X"] } }),
    ]);
    expect(d).toMatchObject({ stage: "verificação", exitCode: 1, logTail: "Error: X" });
    expect(d?.diagnosis?.variables).toEqual(["X"]);
  });
  it("reconhece a morte por memória", () => {
    expect(failureDetail([ev("verifying", "failed", { error: "x", oom_killed: true })])?.oomKilled).toBe(true);
  });
  it("sem falha, nada", () => {
    expect(failureDetail([ev("fetching", "completed")])).toBeNull();
  });
});

describe("estados", () => {
  it("só cancela o que ainda não executou", () => {
    expect(canCancel("QUEUED")).toBe(true);
    expect(canCancel("NEEDS_INPUT")).toBe(true);
    expect(canCancel("BUILDING")).toBe(false);
    expect(canCancel("SUCCEEDED")).toBe(false);
  });
  it("tons", () => {
    expect(stateTone("SUCCEEDED")).toBe("success");
    expect(stateTone("FAILED")).toBe("danger");
    expect(stateTone("NEEDS_INPUT")).toBe("warning");
  });
});

describe("profileLabel", () => {
  it("names the profiles regardless of case", async () => {
    const { profileLabel } = await import("./deploy");
    expect(profileLabel("P-NODE")).toBe("Node.js");
    expect(profileLabel("P-BUN")).toBe("Bun");
    expect(profileLabel("P-PY")).toBe("PY");
    expect(profileLabel(undefined)).toBeUndefined();
  });
});
