import { describe, expect, it } from "vitest";
import { normalizeDir, siblingSlug } from "./repomap";

describe("siblingSlug", () => {
  it("joins the project and the folder name", () => {
    expect(siblingSlug("fec", "frontend")).toBe("fec-frontend");
    expect(siblingSlug("fec", "apps/Web_App")).toBe("fec-web-app");
  });
  it("stays within what the API accepts", () => {
    const slug = siblingSlug("a".repeat(60), "frontend");
    expect(slug.length).toBeLessThanOrEqual(63);
    expect(slug).toMatch(/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/);
  });
});

describe("normalizeDir", () => {
  it("treats the root in every spelling the same", () => {
    for (const d of ["", ".", "./", "/"]) expect(normalizeDir(d)).toBe("");
    expect(normalizeDir("./frontend/")).toBe("frontend");
  });
});
