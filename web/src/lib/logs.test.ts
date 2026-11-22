import { describe, expect, it } from "vitest";
import { appendLines, asText, filterLines, prependLines, startsNewContainer, type LogLine } from "./logs";

const line = (id: number, text = `linha ${id}`, extra: Partial<LogLine> = {}): LogLine => ({
  id, container_id: "c1", container: "app-1", deployment_id: null, origin: "app", stream: "stdout", at: "2026-11-22T10:00:00.000Z", line: text, ...extra,
});

describe("logs de execução", () => {
  it("acrescenta só o que é novo e não repete", () => {
    const a = [line(1), line(2)];
    expect(appendLines(a, [line(2), line(3)]).map((l) => l.id)).toEqual([1, 2, 3]);
    expect(appendLines(a, [])).toBe(a);
    expect(appendLines(a, [line(1)])).toBe(a);
  });

  it("corta o começo quando passa do limite em memória", () => {
    const got = appendLines([line(1), line(2), line(3)], [line(4), line(5)], 4);
    expect(got.map((l) => l.id)).toEqual([2, 3, 4, 5]);
  });

  it("põe as anteriores no começo, sem repetir", () => {
    expect(prependLines([line(5), line(6)], [line(3), line(4), line(5)]).map((l) => l.id)).toEqual([3, 4, 5, 6]);
    expect(prependLines([], [line(1)]).map((l) => l.id)).toEqual([1]);
  });

  it("filtra por texto sem diferenciar maiúsculas", () => {
    const all = [line(1, "Erro de conexão"), line(2, "tudo certo"), line(3, "ERRO fatal")];
    expect(filterLines(all, "erro").map((l) => l.id)).toEqual([1, 3]);
    expect(filterLines(all, "  ")).toBe(all);
  });

  it("marca o começo de um contêiner novo na mesma origem, mas não a troca de origem", () => {
    expect(startsNewContainer(undefined, line(1))).toBe(false);
    expect(startsNewContainer(line(1), line(2))).toBe(false);
    expect(startsNewContainer(line(1), line(2, "x", { container_id: "c2" }))).toBe(true);
    expect(startsNewContainer(line(1), line(2, "x", { container_id: "pg", origin: "postgres" }))).toBe(false);
  });

  it("monta o texto para copiar", () => {
    expect(asText([line(1, "a"), line(2, "b", { stream: "stderr" })])).toBe("2026-11-22T10:00:00.000Z OUT a\n2026-11-22T10:00:00.000Z ERR b");
  });
});
