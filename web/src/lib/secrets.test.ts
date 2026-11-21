import { describe, expect, it } from "vitest";
import { chosen, defaultSelection, describeChange, missingLikely, toggle, withRequired, type ImportPreview } from "./secrets";

const preview: ImportPreview = {
  variables: [
    { name: "A", line: 1, action: "create", class: "user", default_selected: true, warnings: [] },
    { name: "B", line: 2, action: "replace", class: "user", default_selected: false, warnings: [{ code: "replaces_existing", message: "x" }] },
    { name: "PORT", line: 3, action: "blocked", class: "platform", default_selected: false, warnings: [{ code: "platform", message: "x" }] },
  ],
  diagnostics: [],
};

describe("importação", () => {
  it("começa só com o que a prévia marca", () => {
    expect([...defaultSelection(preview)]).toEqual(["A"]);
  });
  it("liga e desliga sem mexer no original", () => {
    const base = defaultSelection(preview);
    const next = toggle(base, "B");
    expect([...next].sort()).toEqual(["A", "B"]);
    expect([...base]).toEqual(["A"]);
    expect([...toggle(next, "A")]).toEqual(["B"]);
  });
  it("nunca envia uma bloqueada, mesmo marcada", () => {
    expect(chosen(preview, new Set(["A", "B", "PORT", "FORA"]))).toEqual(["A", "B"]);
  });
});

describe("histórico", () => {
  const at = "2026-11-20T10:00:00Z";
  it("descreve versão, origem e quem", () => {
    expect(describeChange({ action: "env.set", name: "X", version: 3, source: "user", actor: "admin", at })).toBe("v3 · digitada por admin");
    expect(describeChange({ action: "env.set", name: "X", version: 2, source: "imported", actor: "admin", at })).toBe("v2 · importada por admin");
    expect(describeChange({ action: "env.generate", name: "X", version: 1, source: "generated", at })).toBe("v1 · gerada");
    expect(describeChange({ action: "env.delete", name: "X", actor: "admin", at })).toBe("removida por admin");
    expect(describeChange({ action: "env.import", names: ["A", "B"], at })).toBe("importou 2 variável(is)");
    expect(describeChange({ action: "env.required", names: ["A", "B"], at })).toBe("exigiu A, B");
    expect(describeChange({ action: "env.required", names: [], at })).toBe("deixou de exigir variáveis");
  });
});

describe("obrigatórias", () => {
  it("adiciona e remove em ordem estável e sem repetir", () => {
    expect(withRequired(["B"], "A", true)).toEqual(["A", "B"]);
    expect(withRequired(["A", "B"], "A", true)).toEqual(["A", "B"]);
    expect(withRequired(["A", "B"], "A", false)).toEqual(["B"]);
  });
  it("lista só as prováveis ainda não exigidas", () => {
    expect(missingLikely(["A"], ["A", "B"])).toEqual(["B"]);
  });
});
