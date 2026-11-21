import { describe, expect, it } from "vitest";
import { parsePaths } from "../components/Data";

describe("parsePaths", () => {
  it("keeps one path per line, trimmed, without blanks or repeats", () => {
    expect(parsePaths("/app/data\n  /app/content  \n\n/app/data\n")).toEqual(["/app/data", "/app/content"]);
    expect(parsePaths("")).toEqual([]);
    expect(parsePaths("   \n  ")).toEqual([]);
  });
});
