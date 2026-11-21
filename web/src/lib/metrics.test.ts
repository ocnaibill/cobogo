import { describe, expect, it } from "vitest";
import { age, cores, healthOf, percent, percentOf, roleLabel, segments } from "./metrics";

const pt = (t: number, v: number | null) => ({ t: t * 1000, v });

describe("segments", () => {
  it("cuts the line at a null value and never turns it into zero", () => {
    const s = segments([pt(0, 1), pt(5, 2), pt(10, null), pt(15, 3), pt(20, 4)], 5000);
    expect(s).toHaveLength(2);
    expect(s[0]?.map((p) => p.v)).toEqual([1, 2]);
    expect(s[1]?.map((p) => p.v)).toEqual([3, 4]);
    expect(s.flat().some((p) => p.v === 0)).toBe(false);
  });
  it("cuts the line at a gap of missing samples", () => {
    const s = segments([pt(0, 1), pt(5, 1), pt(60, 1), pt(65, 1)], 5000);
    expect(s).toHaveLength(2);
  });
  it("keeps a continuous series whole and handles empty ones", () => {
    expect(segments([pt(0, 1), pt(5, 1), pt(10, 1)], 5000)).toHaveLength(1);
    expect(segments([], 5000)).toEqual([]);
    expect(segments([pt(0, null), pt(5, null)], 5000)).toEqual([]);
  });
});

describe("formatting", () => {
  it("writes cores and percentages, and never invents a value", () => {
    expect(cores(null)).toBe("—");
    expect(cores(0.25)).toBe("0,25 núcleos");
    expect(cores(0.004)).toBe("0,004 núcleos");
    expect(cores(0.0002)).toBe("< 0,001 núcleo");
    expect(cores(1)).toBe("1,00 núcleo");
    expect(percentOf(null, 1)).toBeNull();
    expect(percentOf(0.5, null)).toBeNull();
    expect(percentOf(0.5, 0)).toBeNull();
    expect(percentOf(256, 1024)).toBe(25);
    expect(percent(null)).toBe("—");
    expect(percent(4.567)).toBe("4,6%");
    expect(percent(47.2)).toBe("47%");
  });
  it("says how old a reading is", () => {
    const now = Date.parse("2026-11-21T12:00:00Z");
    expect(age("2026-11-21T11:59:57Z", now)).toBe("há 3 s");
    expect(age("2026-11-21T11:58:00Z", now)).toBe("há 2 min");
    expect(age("2026-11-21T09:00:00Z", now)).toBe("há 3 h");
  });
});

describe("health and roles", () => {
  const base = { health: "", stale: false, oom_killed: false };
  it("is unknown, not healthy, without a recent reading", () => {
    expect(healthOf({ ...base, state: "running", stale: true })).toMatchObject({ tone: "neutral" });
  });
  it("tells Docker's state apart from a deploy result", () => {
    expect(healthOf({ ...base, state: "running" }).label).toBe("no ar");
    expect(healthOf({ ...base, state: "running", health: "healthy" }).label).toBe("no ar, saudável");
    expect(healthOf({ ...base, state: "running", health: "unhealthy" }).tone).toBe("danger");
    expect(healthOf({ ...base, state: "exited" }).label).toBe("parado");
    expect(healthOf({ ...base, state: "restarting" }).tone).toBe("warning");
    expect(healthOf({ ...base, state: "running", oom_killed: true }).tone).toBe("danger");
  });
  it("names the container's role", () => {
    expect(roleLabel({ role: "app", release_state: "active" })).toBe("aplicação ativa");
    expect(roleLabel({ role: "app", release_state: "superseded" })).toBe("release anterior");
    expect(roleLabel({ role: "service", service: "postgres" })).toBe("PostgreSQL");
  });
});
