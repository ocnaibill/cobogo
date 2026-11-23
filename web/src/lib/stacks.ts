// O projeto é o repositório (DEC-52): as regras de apresentação de um projeto e das
// aplicações dele. Sem React: testável à parte.
import type { ProjectSummary, Stack } from "../api/client";
import type { Tone } from "./deploy";
import { stateTone } from "./deploy";

export const ROLE_LABEL: Record<string, string> = {
  frontend: "Frontend",
  backend: "Backend",
  fullstack: "Aplicação web",
  app: "Aplicação",
  "": "Aplicação",
};

/** O nome de uma aplicação na aba do projeto: o papel, e a pasta quando dois têm o mesmo papel. */
export function appTabLabel(app: ProjectSummary, apps: ProjectSummary[]): string {
  const label = ROLE_LABEL[app.role ?? ""] ?? "Aplicação";
  const same = apps.filter((a) => (a.role ?? "") === (app.role ?? ""));
  return same.length > 1 ? `${label} · ${app.root_directory || "raiz"}` : label;
}

const TONE_RANK: Record<Tone, number> = { danger: 4, warning: 3, info: 2, success: 1, neutral: 0 };

/** O tom do projeto: o pior entre as aplicações (uma falha pesa mais do que um deploy em curso). */
export function stackTone(stack: Stack): Tone {
  let worst: Tone = "neutral";
  for (const a of stack.apps) {
    const t: Tone = a.app_state === "stopped" ? "warning" : a.last_deployment ? stateTone(a.last_deployment.state) : "neutral";
    if (TONE_RANK[t] > TONE_RANK[worst]) worst = t;
  }
  if (stack.apps.length === 0 && stack.scan_state === "failed") return "danger";
  return worst;
}

/** As aplicações propostas que ainda não existem no projeto (pela pasta). */
export function pendingProposals(stack: Stack) {
  const have = new Set(stack.apps.map((a) => a.root_directory));
  return (stack.scan?.apps ?? []).filter((a) => !have.has(a.root_directory));
}

/** A aplicação que guarda os serviços (banco, Redis): a que não é só frontend. */
export function dataApp(stack: Stack): ProjectSummary | undefined {
  return stack.apps.find((a) => a.role === "backend" || a.role === "fullstack") ?? stack.apps.find((a) => a.role !== "frontend");
}
