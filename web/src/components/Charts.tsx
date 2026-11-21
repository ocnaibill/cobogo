// Gráfico de linhas em SVG, sem biblioteca (DEC-43). Lacunas interrompem a linha
// (ausência de amostra nunca vira zero) e os marcadores de deploy e de reinício
// aparecem como linhas verticais com o nome.
import { useId } from "react";
import { segments, type ChartPoint, type MetricMarker } from "../lib/metrics";

export type Line = { id: string; label: string; color: string; points: ChartPoint[] };

const W = 640;
const H = 170;
const PAD = { l: 52, r: 10, t: 10, b: 22 };

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const pow = 10 ** Math.floor(Math.log10(v));
  const n = v / pow;
  return (n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10) * pow;
}

const clock = (ms: number, span: number) => {
  const d = new Date(ms);
  return span > 36 * 3600_000 ? d.toLocaleDateString("pt-BR", { day: "2-digit", month: "2-digit" }) : d.toLocaleTimeString("pt-BR", { hour: "2-digit", minute: "2-digit" });
};

const MARKER_COLOR: Record<string, string> = { deploy: "var(--brand)", restart: "var(--warning)", start: "var(--info)", oom: "var(--danger)" };

export function LineChart({
  lines,
  from,
  to,
  stepMs,
  format,
  limit,
  markers = [],
  title,
}: {
  lines: Line[];
  from: number;
  to: number;
  stepMs: number;
  format: (v: number) => string;
  /** Limite desenhado como linha tracejada (por exemplo a memória do contêiner). */
  limit?: number | null;
  markers?: MetricMarker[];
  title: string;
}) {
  const id = useId();
  const values = lines.flatMap((l) => l.points.map((p) => p.v ?? 0));
  const top = niceMax(Math.max(...values, limit && limit <= (Math.max(...values, 0) || 1) * 4 ? limit : 0, 0.000001));
  const x = (t: number) => PAD.l + ((t - from) / Math.max(1, to - from)) * (W - PAD.l - PAD.r);
  const y = (v: number) => PAD.t + (1 - v / top) * (H - PAD.t - PAD.b);
  const span = to - from;
  const hasData = lines.some((l) => l.points.some((p) => p.v !== null));

  return (
    <figure className="chart">
      <figcaption className="small strong">{title}</figcaption>
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={title} preserveAspectRatio="none">
        <title>{title}</title>
        {[0, 0.5, 1].map((f) => (
          <g key={f}>
            <line x1={PAD.l} x2={W - PAD.r} y1={y(top * f)} y2={y(top * f)} className="chart-grid" />
            <text x={PAD.l - 6} y={y(top * f) + 3} textAnchor="end" className="chart-axis">
              {format(top * f)}
            </text>
          </g>
        ))}
        {[0, 0.5, 1].map((f) => (
          <text key={f} x={x(from + span * f)} y={H - 6} textAnchor={f === 0 ? "start" : f === 1 ? "end" : "middle"} className="chart-axis">
            {clock(from + span * f, span)}
          </text>
        ))}
        {limit != null && limit <= top && <line x1={PAD.l} x2={W - PAD.r} y1={y(limit)} y2={y(limit)} className="chart-limit" />}
        {markers
          .filter((m) => new Date(m.at).getTime() >= from && new Date(m.at).getTime() <= to)
          .map((m, i) => {
            const mx = x(new Date(m.at).getTime());
            return (
              <g key={`${m.kind}-${i}`}>
                <line x1={mx} x2={mx} y1={PAD.t} y2={H - PAD.b} stroke={MARKER_COLOR[m.kind] ?? "var(--text-muted)"} className="chart-marker" />
                <title>{`${m.label} · ${new Date(m.at).toLocaleString("pt-BR")}`}</title>
              </g>
            );
          })}
        {lines.map((l) =>
          segments(l.points, stepMs).map((seg, i) => {
            const d = seg.map((p, k) => `${k === 0 ? "M" : "L"}${x(p.t).toFixed(1)},${y(p.v as number).toFixed(1)}`).join(" ");
            const only = seg[0];
            // Um ponto isolado (entre lacunas) vira um círculo: não some.
            return seg.length === 1 && only ? (
              <circle key={`${l.id}-${i}`} cx={x(only.t)} cy={y(only.v as number)} r={2.2} fill={l.color} />
            ) : (
              <path key={`${l.id}-${i}`} d={d} fill="none" stroke={l.color} strokeWidth={1.6} strokeLinejoin="round" />
            );
          }),
        )}
      </svg>
      {!hasData && <p className="chart-empty small muted">Sem leitura nesta janela.</p>}
      <div className="chart-legend small" id={id}>
        {lines.map((l) => (
          <span key={l.id} className="row">
            <span className="chart-swatch" style={{ background: l.color }} aria-hidden="true" /> {l.label}
          </span>
        ))}
        {markers.some((m) => m.kind === "deploy") && (
          <span className="row">
            <span className="chart-swatch" style={{ background: MARKER_COLOR.deploy }} aria-hidden="true" /> deploy
          </span>
        )}
        {markers.some((m) => m.kind === "restart") && (
          <span className="row">
            <span className="chart-swatch" style={{ background: MARKER_COLOR.restart }} aria-hidden="true" /> reinício
          </span>
        )}
        {markers.some((m) => m.kind === "start") && (
          <span className="row">
            <span className="chart-swatch" style={{ background: MARKER_COLOR.start }} aria-hidden="true" /> início do contêiner
          </span>
        )}
        {markers.some((m) => m.kind === "oom") && (
          <span className="row">
            <span className="chart-swatch" style={{ background: MARKER_COLOR.oom }} aria-hidden="true" /> falta de memória
          </span>
        )}
      </div>
    </figure>
  );
}
