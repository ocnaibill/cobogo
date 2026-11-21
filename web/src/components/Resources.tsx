// Recursos e saúde dos contêineres do projeto (DEC-43, TEL-07/TEL-08). Saúde
// atual (do Docker), resultado do deploy e sonda de rota ficam separados, e
// ausência de leitura nunca vira zero.
import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "../api/client";
import { bytes } from "../lib/format";
import { age, cores, healthOf, percent, percentOf, PROBE_LABEL, roleLabel, seriesOf, WINDOW_LABEL, WINDOWS, type ContainerMetrics, type ProjectMetrics, type Window } from "../lib/metrics";
import { Badge, Card, Empty, ErrorBox, Loading, Notice, Tag } from "./ui";
import { Icon } from "./icons";
import { LineChart, type Line } from "./Charts";

const COLORS = ["var(--brand)", "var(--success)", "var(--warning)", "var(--info)", "var(--danger)"];

export function useProjectMetrics(projectId: string, window: Window) {
  return useQuery({
    queryKey: ["metrics", projectId, window],
    queryFn: () => unwrap(api.GET("/projects/{id}/metrics", { params: { path: { id: projectId }, query: { window } } })),
    refetchInterval: 5000,
  });
}

/** O coletor está vivo? Sem ele, a saúde atual é desconhecida. */
export function CollectorLine({ collector }: { collector: { last_at: string; stale: boolean; interval_ms: number; duration_ms: number; errors: number; last_error?: string } | null | undefined }) {
  if (!collector) {
    return (
      <Notice tone="warning" title="O coletor ainda não rodou">
        <span>Sem coletor, não há leitura de recursos nem de saúde. Ele roda como um serviço à parte (DEC-43).</span>
      </Notice>
    );
  }
  if (collector.stale) {
    return (
      <Notice tone="danger" title={`Sem coleta ${age(collector.last_at)}`}>
        <span>A saúde atual é desconhecida: o que aparece abaixo é a última leitura, não o estado de agora.</span>
      </Notice>
    );
  }
  return (
    <span className="meta row">
      <Icon name="activity" size={14} /> Coletado {age(collector.last_at)} · a cada {collector.interval_ms / 1000} s · rodada de {collector.duration_ms} ms
      {collector.errors > 0 && <Tag tone="warning" title={collector.last_error}>{collector.errors} erro(s) na rodada</Tag>}
    </span>
  );
}

export function ResourcesTab({ projectId }: { projectId: string }) {
  const [window, setWindow] = useState<Window>("15m");
  const q = useProjectMetrics(projectId, window);

  if (q.isLoading) return <Loading />;
  if (q.isError) return <ErrorBox error={q.error} onRetry={() => q.refetch()} />;
  const m = q.data!;
  const from = new Date(m.from).getTime();
  const to = new Date(m.to).getTime();
  const stepMs = m.step_seconds * 1000;
  const containers = m.containers;
  const withLimit = (c: ContainerMetrics) => c.current?.mem_limit_bytes ?? null;

  const lines = (kind: "cpu" | "mem"): Line[] =>
    containers.map((c, i) => ({ id: c.container_id, label: `${c.name} (${roleLabel(c)})`, color: COLORS[i % COLORS.length] ?? "var(--brand)", points: seriesOf(c, kind) }));
  const apps = containers.filter((c) => c.role === "app");
  const onlyApp = apps.length === 1 ? apps[0] : undefined;
  const memLimit = onlyApp ? withLimit(onlyApp) : null;

  return (
    <div className="stack">
      <Notice tone="info" icon="shield" title="Leitura do Docker, não do aplicativo">
        <span>
          CPU em núcleos usados e memória sem o cache de arquivos, lidos do Docker a cada poucos segundos. Não há latência de usuários nem taxa de erro aqui. A <strong>saúde</strong> é o estado do contêiner agora; o <strong>resultado do deploy</strong> é o que o pipeline concluiu, e são coisas diferentes.
        </span>
      </Notice>

      <div className="row-between">
        <CollectorLine collector={m.collector} />
        <div className="chips" role="group" aria-label="Janela">
          {WINDOWS.map((w) => (
            <button key={w} className="chip" aria-pressed={window === w} onClick={() => setWindow(w)}>
              {WINDOW_LABEL[w]}
            </button>
          ))}
        </div>
      </div>

      {m.probe && <ProbeCard probe={m.probe} />}

      <section className="card card-flush" aria-label="Contêineres do projeto">
        <header className="card-head">
          <h2 className="card-title">
            <Icon name="box" size={20} className="card-icon" /> Contêineres
          </h2>
          <Tag>{containers.length} na janela de {WINDOW_LABEL[window]}</Tag>
        </header>
        {containers.length === 0 ? (
          <Empty title="Nenhuma leitura nesta janela." icon="activity">
            Ou o projeto ainda não tem contêiner, ou o coletor não leu nada nesse período. Sem leitura não é o mesmo que zero.
          </Empty>
        ) : (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Contêiner</th>
                  <th scope="col">Saúde agora</th>
                  <th scope="col">Reinícios</th>
                  <th scope="col">CPU</th>
                  <th scope="col">Memória</th>
                  <th scope="col">Leitura</th>
                </tr>
              </thead>
              <tbody>
                {containers.map((c) => {
                  const h = healthOf(c);
                  const cpuPct = percentOf(c.current?.cpu_cores, c.current?.cpu_limit_cores);
                  const memPct = percentOf(c.current?.mem_bytes, c.current?.mem_limit_bytes);
                  return (
                    <tr key={c.container_id}>
                      <td>
                        <div className="stack-sm">
                          <span className="mono strong small">{c.name}</span>
                          <span className="small muted">{roleLabel(c)}</span>
                        </div>
                      </td>
                      <td>
                        <Badge tone={h.tone}>{h.label}</Badge>
                      </td>
                      <td>{c.restarts}</td>
                      <td>
                        {cores(c.current?.cpu_cores)}
                        {cpuPct != null && <span className="small muted"> · {percent(cpuPct)} do limite</span>}
                      </td>
                      <td>
                        {c.current?.mem_bytes != null ? bytes(c.current.mem_bytes) : "—"}
                        {c.current?.mem_limit_bytes != null && <span className="small muted"> de {bytes(c.current.mem_limit_bytes)}{memPct != null ? ` · ${percent(memPct)}` : ""}</span>}
                      </td>
                      <td className="meta">{c.current ? age(c.current.at) : "—"}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {containers.length > 0 && (
        <div className="grid-2">
          <Card title="CPU" icon="cpu">
            <LineChart title="CPU em núcleos usados" lines={lines("cpu")} from={from} to={to} stepMs={stepMs} format={(v) => (v < 0.1 ? v.toFixed(3) : v.toFixed(2))} markers={m.markers} />
          </Card>
          <Card title="Memória" icon="layers">
            <LineChart title="Memória em uso (sem cache)" lines={lines("mem")} from={from} to={to} stepMs={stepMs} format={(v) => bytes(v)} limit={memLimit} markers={m.markers} />
          </Card>
        </div>
      )}
      <p className="small muted">
        Janela de {WINDOW_LABEL[window]}, um ponto a cada {m.step_seconds} s ({m.source === "raw" ? "leituras brutas" : "agregado por minuto"}). Trechos sem linha são lacunas: o coletor não leu nesse intervalo. Atualiza a cada 5 s.
      </p>
    </div>
  );
}

function ProbeCard({ probe }: { probe: NonNullable<ProjectMetrics["probe"]> }) {
  const label = PROBE_LABEL[probe.outcome] ?? { tone: "neutral" as const, label: probe.outcome };
  const recent = probe.series.slice(-30);
  const failed = probe.series.reduce((n, p) => n + p.failed, 0);
  const total = probe.series.reduce((n, p) => n + p.total, 0);
  return (
    <Card title="Resposta da rota" icon="route" actions={<Tag>verificação sintética</Tag>}>
      <div className="stack-sm">
        <div className="row">
          {probe.stale ? (
            <Badge tone="neutral">desconhecida (sem sonda recente)</Badge>
          ) : (
            <Badge tone={label.tone}>{label.label}</Badge>
          )}
          {probe.status != null && <span className="mono small">HTTP {probe.status}</span>}
          {probe.duration_ms != null && <span className="mono small">{probe.duration_ms} ms</span>}
          <span className="meta">{age(probe.at)}</span>
        </div>
        <div className="probe-strip" role="img" aria-label="Últimas sondas">
          {recent.map((p) => (
            <span key={p.t} title={`${new Date(p.t).toLocaleTimeString("pt-BR")} · ${p.ok} ok, ${p.failed} falhas${p.avg_ms != null ? `, ${p.avg_ms} ms` : ""}`} className={p.failed > 0 ? "probe-bad" : p.stopped > 0 ? "probe-stop" : "probe-ok"} />
          ))}
        </div>
        <p className="small muted">
          Uma requisição <code>GET /</code> por <span className="mono">{probe.host}</span>, pelo mesmo caminho do visitante. Mostra o status e o tempo como são; não é disponibilidade nem latência de usuários.
          {total > 0 && ` Na janela: ${failed} falha(s) em ${total} sonda(s).`}
        </p>
      </div>
    </Card>
  );
}

/** Consumo das cargas geridas e estado do coletor, na visão geral (TEL-02). */
export function FleetResources() {
  const q = useQuery({ queryKey: ["metrics-overview"], queryFn: () => unwrap(api.GET("/metrics/overview")), refetchInterval: 5000 });
  if (q.isLoading) return null;
  if (q.isError) return <ErrorBox error={q.error} onRetry={() => q.refetch()} />;
  const o = q.data!;
  const memPct = percentOf(o.mem_bytes, o.mem_limit_bytes);
  return (
    <section className="card" aria-label="Consumo das cargas geridas">
      <header className="card-head">
        <h2 className="card-title">
          <Icon name="activity" size={20} className="card-icon" /> Consumo agora
        </h2>
        <CollectorLine collector={o.collector} />
      </header>
      {o.unknown > 0 && (
        <Notice tone="warning" title={`${o.unknown} contêiner(es) sem leitura recente`}>
          <span>Estado desconhecido: o coletor pode estar parado, ou o contêiner sumiu há pouco. Não contam como saudáveis nem como parados.</span>
        </Notice>
      )}
      <div className="grid-3">
        <div className="inset stack-sm">
          <span className="label">Memória em uso</span>
          <span className="strong">{bytes(o.mem_bytes)}</span>
          <span className="small muted">de {bytes(o.mem_limit_bytes)} de limite{memPct != null ? ` · ${percent(memPct)}` : ""}</span>
        </div>
        <div className="inset stack-sm">
          <span className="label">CPU</span>
          <span className="strong">{cores(o.cpu_cores)}</span>
          <span className="small muted">soma dos contêineres com leitura</span>
        </div>
        <div className="inset stack-sm">
          <span className="label">Contêineres</span>
          <span className="strong">
            {o.running} no ar · {o.not_running} parado(s)
          </span>
          <span className="small muted">
            {o.apps} aplicação(ões) e {o.services} banco(s)
          </span>
        </div>
      </div>
      {o.by_profile.length > 0 && (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Perfil</th>
                <th scope="col">Projetos</th>
                <th scope="col">Contêineres</th>
                <th scope="col">Memória</th>
                <th scope="col">CPU</th>
              </tr>
            </thead>
            <tbody>
              {o.by_profile.map((u) => (
                <tr key={u.profile}>
                  <td className="mono strong">{u.profile}</td>
                  <td>{u.projects}</td>
                  <td>{u.containers}</td>
                  <td>{bytes(u.mem_bytes)}</td>
                  <td>{cores(u.cpu_cores)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
