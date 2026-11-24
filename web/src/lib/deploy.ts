// Regras de apresentação de um deploy: etapas do §15.2 do mestre, rótulos,
// tons e a tradução dos eventos em linhas legíveis. Sem React: testável à parte.
import type { DeploymentEvent, DeploymentState } from "../api/client";

export type Tone = "neutral" | "info" | "success" | "warning" | "danger";

export const STATE_LABEL: Record<DeploymentState, string> = {
  QUEUED: "Na fila",
  FETCHING: "Obtendo o código",
  ANALYZING: "Analisando",
  NEEDS_INPUT: "Aguardando você",
  UNSUPPORTED: "Não suportado",
  GENERATING: "Gerando o plano",
  BUILDING: "Construindo",
  PROVISIONING: "Provisionando",
  STARTING: "Iniciando",
  VERIFYING: "Verificando",
  SUCCEEDED: "No ar",
  FAILED: "Falhou",
  CANCELLED: "Cancelado",
};

export function stateTone(state: DeploymentState): Tone {
  switch (state) {
    case "SUCCEEDED":
      return "success";
    case "FAILED":
    case "UNSUPPORTED":
      return "danger";
    case "NEEDS_INPUT":
      return "warning";
    case "CANCELLED":
      return "neutral";
    default:
      return "info";
  }
}

export const TERMINAL: DeploymentState[] = ["SUCCEEDED", "FAILED", "UNSUPPORTED", "CANCELLED"];
export const isTerminal = (s: DeploymentState) => TERMINAL.includes(s);
export const isActive = (s: DeploymentState) => !isTerminal(s) && s !== "NEEDS_INPUT" && s !== "QUEUED";
export const canCancel = (s: DeploymentState) => s === "QUEUED" || s === "NEEDS_INPUT";

/** As etapas na ordem em que o worker as percorre (§15). */
export const STAGES: { state: DeploymentState; label: string; key: string }[] = [
  { state: "FETCHING", label: "Código", key: "fetching" },
  { state: "ANALYZING", label: "Análise", key: "analyzing" },
  { state: "GENERATING", label: "Plano", key: "generating" },
  { state: "BUILDING", label: "Build", key: "building" },
  { state: "STARTING", label: "Início", key: "starting" },
  { state: "VERIFYING", label: "Verificação", key: "verifying" },
];

export type StageStatus = "pending" | "current" | "done" | "failed" | "waiting";

/**
 * Situação de cada etapa a partir do estado atual e dos eventos. Uma etapa
 * concluída tem evento `completed` (ou `succeeded`); a que falhou, um `failed`
 * com o seu nome. PROVISIONING conta como parte de Início.
 */
export function stageStatuses(state: DeploymentState, events: DeploymentEvent[]): Record<string, StageStatus> {
  const done = new Set<string>();
  let failedStage: string | null = null;
  // Uma retomada (evento `attempt`) recomeça do zero: só vale o que veio depois.
  let lastAttempt = -1;
  events.forEach((e, i) => {
    if (e.stage === "pipeline" && e.event_type === "attempt") lastAttempt = i;
  });
  events.slice(lastAttempt + 1).forEach((e) => {
    const stage = e.stage.toLowerCase();
    if (e.event_type === "completed" || e.event_type === "succeeded") done.add(stage === "provisioning" ? "starting" : stage);
    if (e.event_type === "failed") failedStage = stage === "provisioning" ? "starting" : stage;
  });
  if (state === "SUCCEEDED") STAGES.forEach((s) => done.add(s.key));

  const current = state === "PROVISIONING" ? "starting" : state.toLowerCase();
  const out: Record<string, StageStatus> = {};
  for (const s of STAGES) {
    if (failedStage === s.key || (state === "FAILED" && failedStage === null && current === s.key)) out[s.key] = "failed";
    // A análise "concluiu" pedindo informação: ela espera por você, não terminou.
    else if (state === "NEEDS_INPUT" && s.key === "analyzing") out[s.key] = "waiting";
    else if (done.has(s.key)) out[s.key] = "done";
    else if (current === s.key) out[s.key] = "current";
    else out[s.key] = "pending";
  }
  return out;
}

/** Uma linha do terminal do deploy. */
export interface LogLine {
  seq: number;
  at: string;
  kind: "output" | "step" | "warning" | "error" | "success";
  /** A etapa, curta, para a etiqueta do terminal. */
  tag: string;
  text: string;
}

const STAGE_NAME: Record<string, string> = {
  pipeline: "pipeline",
  fetching: "código",
  analyzing: "análise",
  generating: "plano",
  building: "build",
  provisioning: "provisionamento",
  starting: "início",
  verifying: "verificação",
  routing: "roteamento",
  needs_input: "pendência",
};

function str(v: unknown): string {
  return typeof v === "string" ? v : v === undefined || v === null ? "" : JSON.stringify(v);
}

/** Traduz um evento em linha(s) do terminal. Valores de variáveis nunca chegam aqui (RN-11). */
export function eventToLines(e: DeploymentEvent): LogLine[] {
  const p = (e.payload ?? {}) as Record<string, unknown>;
  const stage = STAGE_NAME[e.stage.toLowerCase()] ?? e.stage.toLowerCase();
  const base = { seq: e.sequence, at: e.occurred_at, tag: stage };
  switch (e.event_type) {
    case "log":
      return [{ ...base, kind: "output", text: str(p.line) }];
    case "attempt":
      return [{ ...base, kind: p.resumed ? "warning" : "step", text: p.resumed ? `Retomando (tentativa ${str(p.number)}) do início, no mesmo commit` : `Tentativa ${str(p.number)}` }];
    case "started":
      return [{ ...base, kind: "step", text: `▸ ${stage}: começou` }];
    case "completed": {
      const extra =
        e.stage === "fetching" && p.commit_sha ? ` (commit ${str(p.commit_sha).slice(0, 7)})` :
        e.stage === "generating" && p.profile === "P-STATIC"
          ? ` (site estático: ${p.build_command ? `\`${str(p.build_command)}\` → ` : ""}${str(p.output_dir)}/ servido pelo nginx na porta ${str(p.port)})`
          : e.stage === "generating" && p.start_command
          ? ` (${runtimeVersion(p)}, porta ${str(p.port)}, \`${str(p.start_command)}\`)`
          :
        e.stage === "starting" && Array.isArray(p.env) ? ` (${p.env.length} variável(is) entregue(s))` : "";
      return [{ ...base, kind: "step", text: `✓ ${stage}: concluído${extra}` }];
    }
    case "probe":
      return [{ ...base, kind: p.healthy ? "step" : "warning", text: `✓ verificação: ${str(p.path)} respondeu ${str(p.status)}${p.warning ? ` — ${str(p.warning)}` : ""}${p.cause ? ` ${str(p.cause)}` : ""}` }];
    case "postgres":
    case "mysql":
    case "redis":
      return [{ ...base, kind: "step", text: `✓ serviço ${e.event_type === "postgres" ? "PostgreSQL" : e.event_type === "mysql" ? "MySQL" : "Redis"}: ${p.created ? "criado" : "já existia"} (${str(p.container)})` }];
    case "process":
      return [{ ...base, kind: "step", text: `▸ processo ${str(p.name)} iniciado (${str(p.container)}): \`${str(p.command)}\`` }];
    case "release":
      return [{ ...base, kind: "step", text: `▸ fase de release: \`${str(p.command)}\` (da imagem nova, antes de a versão nova subir)` }];
    case "release_done": {
      const out = str(p.output)
        .split("\n")
        .filter((l) => l.trim() !== "")
        .slice(-12)
        .map((l, i) => ({ ...base, seq: base.seq + (i + 1) / 100, kind: "output" as const, text: l }));
      return [...out, { ...base, kind: "step", text: `✓ fase de release concluída em ${str(p.seconds)} s` }];
    }
    case "processes":
      return [{ ...base, kind: "step", text: `✓ ${str(p.count)} processo(s) de pé por ${str(p.stable_seconds)} s, sem sair` }];
    case "promoted":
      return [{ ...base, kind: "success", text: `✓ roteamento: publicado em ${str(p.url)}` }];
    case "succeeded":
      return [{ ...base, kind: "success", text: `✓ No ar em ${str(p.url)}` }];
    case "answered":
      return [{ ...base, kind: "step", text: `▸ Respostas registradas (${(p.fields as string[] | undefined)?.join(", ") ?? ""}); nova análise no mesmo commit` }];
    case "cancelled":
      return [{ ...base, kind: "warning", text: "Deploy cancelado" }];
    case "failed": {
      const lines: LogLine[] = [{ ...base, kind: "error", text: `✗ ${stage}: ${str(p.error)}` }];
      return lines;
    }
    default:
      return [{ ...base, kind: "step", text: `${stage}: ${e.event_type}` }];
  }
}

/** O que se sabe de uma falha: o último evento `failed` traz log, código de saída e diagnóstico (DEC-26). */
export interface FailureDetail {
  stage: string;
  error: string;
  exitCode?: number;
  logTail?: string;
  oomKilled?: boolean;
  /** `field`: o campo do plano suspeito, corrigível com um override num novo deploy (DEC-35). */
  diagnosis?: {
    confidence: "confirmed" | "possible" | "unknown";
    message: string;
    variables?: string[];
    field?: string;
    /** Uma correção que o motor conhece, oferecida como "novo deploy com ..." (DEC-54 C). */
    suggestion?: { overrides: Record<string, string>; label: string };
  };
}

export function failureDetail(events: DeploymentEvent[]): FailureDetail | null {
  const last = [...events].reverse().find((e) => e.event_type === "failed");
  if (!last) return null;
  const p = (last.payload ?? {}) as Record<string, unknown>;
  const d = p.diagnosis as FailureDetail["diagnosis"] | undefined;
  return {
    stage: STAGE_NAME[last.stage.toLowerCase()] ?? last.stage,
    error: str(p.error),
    exitCode: typeof p.exit_code === "number" ? p.exit_code : undefined,
    logTail: typeof p.log_tail === "string" && p.log_tail ? p.log_tail : undefined,
    oomKilled: p.oom_killed === true,
    diagnosis: d && d.message ? d : undefined,
  };
}

export const FIELD_LABEL: Record<string, string> = {
  profile: "Perfil",
  package_manager: "Gerenciador de pacotes",
  start_command: "Comando de execução",
  build_command: "Comando de build",
  execution_mode: "Modo de execução",
  node_version: "Versão do Node",
  bun_version: "Versão do Bun",
  ecosystem: "Ecossistema",
  python_version: "Versão do Python",
  app_target: "Alvo da aplicação",
  python_interface: "Interface (WSGI/ASGI)",
  python_framework: "Framework",
  django_settings: "Settings do Django",
  django_platform_hosts: "Ajuste de hosts do Django",
  process_worker: "Comando do worker",
  process_scheduler: "Comando do agendador",
  release_command: "Fase de release (migrações)",
  output_dir: "Diretório servido",
  spa_fallback: "Fallback de SPA",
  health_path: "Caminho da verificação",
  port: "Porta",
};

/** Nome legível de um perfil do analisador (`p-node` → `Node.js`). */
export function profileLabel(id?: string): string | undefined {
  if (!id) return undefined;
  const known: Record<string, string> = { "p-node": "Node.js", "p-bun": "Bun", "p-static": "Site estático", "p-py": "Python" };
  return known[id.toLowerCase()] ?? id.replace(/^p-/i, "");
}

export const ORIGIN_LABEL: Record<string, string> = {
  override: "informado por você",
  declaration: "declarado no repositório",
  platform: "definida pela plataforma",
  inference: "inferido",
  default: "padrão",
};

/** A versão do runtime de um plano ou evento: `Python 3.13`, `Bun 1.3`, `Node 26`. */
export function runtimeVersion(p: { python_version?: unknown; bun_version?: unknown; node_version?: unknown }): string {
  if (p.python_version) return `Python ${String(p.python_version)}`;
  if (p.bun_version) return `Bun ${String(p.bun_version)}`;
  return `Node ${p.node_version ? String(p.node_version) : "—"}`;
}
