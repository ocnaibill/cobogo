import { describe, expect, it } from "vitest";
import type { ProjectSummary, Stack } from "../api/client";
import { appTabLabel, dataApp, pendingProposals, stackTone } from "./stacks";

const app = (over: Partial<ProjectSummary>): ProjectSummary =>
  ({
    id: Math.random().toString(),
    slug: "x",
    repository_url: "https://github.com/o/r",
    root_directory: "",
    limits: {},
    created_at: "",
    app_state: "none",
    persistent_paths: [],
    stack_id: "s",
    role: "",
    last_deployment: null,
    ...over,
  }) as ProjectSummary;

const stack = (apps: ProjectSummary[], over: Partial<Stack> = {}): Stack =>
  ({ id: "s", slug: "fec", repository_url: "", created_at: "", scan_state: "done", scan: null, scanned_at: null, apps, ...over }) as Stack;

describe("stacks", () => {
  it("nomeia as abas pelo papel, com a pasta só quando repete", () => {
    const front = app({ role: "frontend", root_directory: "frontend" });
    const back = app({ role: "backend" });
    expect(appTabLabel(front, [front, back])).toBe("Frontend");
    const two = app({ role: "frontend", root_directory: "admin" });
    expect(appTabLabel(two, [front, two])).toBe("Frontend · admin");
  });

  it("o banco fica com o backend", () => {
    const front = app({ role: "frontend" });
    const back = app({ role: "backend" });
    expect(dataApp(stack([front, back]))).toBe(back);
    expect(dataApp(stack([front]))).toBeUndefined();
  });

  it("o tom do projeto é o pior das aplicações", () => {
    const ok = app({ last_deployment: { id: "1", state: "SUCCEEDED", created_at: "" } });
    const bad = app({ last_deployment: { id: "2", state: "FAILED", created_at: "" } });
    expect(stackTone(stack([ok]))).toBe("success");
    expect(stackTone(stack([ok, bad]))).toBe("danger");
    expect(stackTone(stack([], { scan_state: "failed" }))).toBe("danger");
  });

  it("a proposta só traz as pastas que ainda não são aplicação", () => {
    const back = app({ role: "backend", root_directory: "" });
    const scan = {
      commit_sha: "a",
      apps: [
        { root_directory: "", role: "backend", label: "", slug: "fec-api", evidence: [], components: [], questions: [], warnings: 0 },
        { root_directory: "frontend", role: "frontend", label: "", slug: "fec", evidence: [], components: [], questions: [], warnings: 0 },
      ],
    };
    expect(pendingProposals(stack([back], { scan })).map((p) => p.root_directory)).toEqual(["frontend"]);
  });
});
