// Métricas dos contêineres (DEC-43): formatação e montagem das séries, sem tela.
// A regra de ouro: ausência de leitura não vira zero. Um ponto sem valor, ou um
// intervalo sem amostra, interrompe a linha.
import type { Schemas } from "../api/client";

export type ProjectMetrics = Schemas["ProjectMetrics"];
export type ContainerMetrics = Schemas["ContainerMetrics"];
export type MetricsOverview = Schemas["MetricsOverview"];
export type MetricPoint = Schemas["MetricPoint"];
export type MetricMarker = Schemas["MetricMarker"];

export const WINDOWS = ["15m", "1h", "6h", "24h", "7d"] as const;
export type Window = (typeof WINDOWS)[number];
export const WINDOW_LABEL: Record<Window, string> = { "15m": "15 min", "1h": "1 h", "6h": "6 h", "24h": "24 h", "7d": "7 dias" };

/** Um ponto de um gráfico: `v` nulo é lacuna, não zero. */
export type ChartPoint = { t: number; v: number | null };

/** Segmentos contínuos da série: uma lacuna (valor nulo, ou mais de 2,5 passos sem ponto) corta a linha. */
export function segments(points: ChartPoint[], stepMs: number): ChartPoint[][] {
  const out: ChartPoint[][] = [];
  let current: ChartPoint[] = [];
  let prev: ChartPoint | null = null;
  for (const p of points) {
    const gap = prev !== null && p.t - prev.t > stepMs * 2.5;
    if (p.v === null || gap) {
      if (current.length) out.push(current);
      current = [];
    }
    if (p.v !== null) current.push(p);
    prev = p;
  }
  if (current.length) out.push(current);
  return out;
}

/** Pontos da série de um contêiner para CPU (núcleos) ou memória (bytes), com a média. */
export function seriesOf(c: ContainerMetrics, kind: "cpu" | "mem"): ChartPoint[] {
  return c.series.map((p) => ({ t: new Date(p.t).getTime(), v: kind === "cpu" ? p.cpu_avg : p.mem_avg }));
}

/** Núcleos usados, legível ("0,25 núcleo", "1,5 núcleos"). */
export function cores(v: number | null | undefined): string {
  if (v == null) return "—";
  if (v > 0 && v < 0.001) return "< 0,001 núcleo";
  const n = v < 0.1 ? v.toFixed(3) : v.toFixed(2);
  return `${n.replace(".", ",")} ${v === 1 ? "núcleo" : "núcleos"}`;
}

/** Fração de um limite em porcentagem, ou null sem limite ou sem leitura. */
export function percentOf(value: number | null | undefined, limit: number | null | undefined): number | null {
  if (value == null || limit == null || limit <= 0) return null;
  return (value / limit) * 100;
}

export function percent(p: number | null): string {
  return p == null ? "—" : `${p < 10 ? p.toFixed(1) : Math.round(p)}%`.replace(".", ",");
}

/** Há quanto tempo foi a leitura: "há 3 s", "há 2 min". */
export function age(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 60) return `há ${s} s`;
  if (s < 3600) return `há ${Math.round(s / 60)} min`;
  return `há ${Math.round(s / 3600)} h`;
}

type Tone = "success" | "warning" | "danger" | "neutral";

/**
 * Saúde atual de um contêiner (do Docker, nunca o resultado do deploy). Uma
 * leitura antiga vira "desconhecida": sem coleta recente não se afirma estar bem.
 */
export function healthOf(c: Pick<ContainerMetrics, "state" | "health" | "stale" | "oom_killed">): { tone: Tone; label: string } {
  if (c.stale) return { tone: "neutral", label: "desconhecida (sem coleta recente)" };
  if (c.oom_killed) return { tone: "danger", label: "morto por falta de memória" };
  switch (c.state) {
    case "running":
      if (c.health === "unhealthy") return { tone: "danger", label: "no ar, verificação falhando" };
      if (c.health === "starting") return { tone: "warning", label: "iniciando" };
      return { tone: "success", label: c.health === "healthy" ? "no ar, saudável" : "no ar" };
    case "restarting":
      return { tone: "warning", label: "reiniciando" };
    case "exited":
    case "dead":
      return { tone: "neutral", label: "parado" };
    case "paused":
      return { tone: "warning", label: "pausado" };
    case "created":
      return { tone: "neutral", label: "criado" };
    default:
      return { tone: "neutral", label: c.state || "desconhecido" };
  }
}

/** O papel de um contêiner na página: aplicação ativa, anterior, ou o banco. */
export function roleLabel(c: Pick<ContainerMetrics, "role" | "release_state" | "service">): string {
  if (c.role === "service") return c.service === "postgres" ? "PostgreSQL" : "serviço";
  switch (c.release_state) {
    case "active":
      return "aplicação ativa";
    case "superseded":
      return "release anterior";
    case "retired":
    case "removed":
      return "release antiga";
    default:
      return "aplicação";
  }
}

export const PROBE_LABEL: Record<string, { tone: Tone; label: string }> = {
  responded: { tone: "success", label: "respondeu" },
  client_error: { tone: "warning", label: "respondeu com erro de cliente (4xx)" },
  failed: { tone: "danger", label: "falhou" },
  stopped: { tone: "neutral", label: "aplicação parada" },
};

/** Tamanho de uma janela em ms (para o eixo do tempo). */
export function windowMs(w: string): number {
  return { "15m": 15, "1h": 60, "6h": 360, "24h": 1440, "7d": 10080 }[w as Window] * 60_000;
}
