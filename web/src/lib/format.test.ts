import { describe, expect, it } from "vitest";
import { duration, relative, repoName, shortSha } from "./format";

describe("format", () => {
  it("duração legível", () => {
    expect(duration("2026-11-20T10:00:00Z", "2026-11-20T10:00:08Z")).toBe("8 s");
    expect(duration("2026-11-20T10:00:00Z", "2026-11-20T10:01:12Z")).toBe("1 min 12 s");
    expect(duration("2026-11-20T10:00:00Z", "2026-11-20T10:02:00Z")).toBe("2 min");
  });
  it("tempo relativo", () => {
    const now = Date.parse("2026-11-20T10:00:00Z");
    expect(relative("2026-11-20T09:59:50Z", now)).toBe("agora");
    expect(relative("2026-11-20T09:55:00Z", now)).toBe("há 5 minutos");
  });
  it("nome do repositório e SHA curto", () => {
    expect(repoName("https://github.com/heroku/node-js-getting-started.git")).toBe("github.com/heroku/node-js-getting-started");
    expect(shortSha("d33649d9a1")).toBe("d33649d");
    expect(shortSha(undefined)).toBe("—");
  });
});
