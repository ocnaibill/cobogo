// TEL-06 — Deploy em andamento (e falha explicada) com TEL-04 — Plano e
// pendências. O progresso vem dos eventos incrementais (DEC-28); a análise e o
// plano mostram o que o Cobogó decidiu e por quê.
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, idempotencyKey, unwrap, type Analysis, type DeploymentEvent, type Finding } from "../api/client";
import { useEvents } from "../hooks/useEvents";
import {
  canCancel,
  eventToLines,
  failureDetail,
  FIELD_LABEL,
  isTerminal,
  ORIGIN_LABEL,
  STAGES,
  stageStatuses,
  STATE_LABEL,
  type StageStatus,
} from "../lib/deploy";
import { absolute, duration, shortSha } from "../lib/format";
import { Badge, Button, Card, CodeBlock, Empty, ErrorBox, Loading, Notice, StateBadge, When } from "../components/ui";

type Tab = "progresso" | "plano" | "analise";

export function DeploymentPage() {
  const { id = "" } = useParams();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const ev = useEvents(id);
  const [tab, setTab] = useState<Tab>("progresso");
  const [actionError, setActionError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"cancel" | "redeploy" | null>(null);

  const detail = useQuery({
    queryKey: ["deployment", id],
    queryFn: () => unwrap(api.GET("/deployments/{id}", { params: { path: { id } } })),
    // O resumo acompanha o estado que os eventos já trouxeram.
    refetchInterval: ev.settled ? false : 3000,
  });
  const dep = detail.data?.deployment;
  const project = useQuery({
    queryKey: ["project", dep?.project_id],
    queryFn: () => unwrap(api.GET("/projects/{id}", { params: { path: { id: dep!.project_id } } })),
    enabled: !!dep,
  });

  const state = ev.state ?? dep?.state ?? "QUEUED";
  // Quando o estado muda, o resumo (release, motivo) também precisa mudar.
  useEffect(() => {
    if (ev.state && dep && ev.state !== dep.state) qc.invalidateQueries({ queryKey: ["deployment", id] });
  }, [ev.state, dep, id, qc]);

  const stages = useMemo(() => stageStatuses(state, ev.events), [state, ev.events]);
  const failure = useMemo(() => (state === "FAILED" ? failureDetail(ev.events) : null), [state, ev.events]);

  if (detail.isLoading) return <Loading />;
  if (detail.isError) return <ErrorBox error={detail.error} onRetry={() => detail.refetch()} />;
  if (!dep) return <Empty title="Deploy não encontrado." />;
  const release = detail.data?.release;

  async function cancel() {
    if (!window.confirm("Cancelar este deploy? Ele ainda não executou nada.")) return;
    setBusy("cancel");
    setActionError(null);
    try {
      await unwrap(api.POST("/deployments/{id}/cancel", { params: { path: { id } } }));
      ev.resume();
      qc.invalidateQueries({ queryKey: ["deployment", id] });
    } catch (e) {
      setActionError(e instanceof ApiError && e.code === "deployment_started" ? "O worker já começou este deploy; não dá mais para cancelar." : String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(null);
    }
  }

  async function redeploy() {
    setBusy("redeploy");
    setActionError(null);
    try {
      const d = await unwrap(api.POST("/projects/{id}/deployments", { params: { path: { id: dep!.project_id } }, body: { idempotency_key: idempotencyKey() } }));
      navigate(`/deploys/${d.id}`);
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="page">
      <nav className="crumbs" aria-label="Você está em">
        <Link to="/">Visão geral</Link> / {project.data ? <Link to={`/projetos/${project.data.id}`}>{project.data.slug}</Link> : "…"} / <span>deploy {dep.id.slice(0, 8)}</span>
      </nav>
      <div className="page-head">
        <div>
          <h1 className="row">
            {STATE_LABEL[state]} <StateBadge state={state} />
          </h1>
          <p className="muted">
            Commit <span className="mono">{shortSha(dep.commit_sha)}</span> · pedido <When iso={dep.created_at} />
            {isTerminal(state) && <> · durou {duration(dep.created_at, dep.updated_at)}</>}
          </p>
        </div>
        <div className="row">
          {canCancel(state) && (
            <Button variant="danger" onClick={cancel} busy={busy === "cancel"}>
              Cancelar
            </Button>
          )}
          {isTerminal(state) && (
            <Button onClick={redeploy} busy={busy === "redeploy"}>
              Novo deploy
            </Button>
          )}
          {release?.url && state === "SUCCEEDED" && (
            <a className="btn btn-primary" href={release.url} target="_blank" rel="noreferrer noopener">
              Abrir aplicação ↗
            </a>
          )}
        </div>
      </div>
      {actionError && <Notice tone="danger">{actionError}</Notice>}

      <Stepper statuses={stages} />

      {state === "SUCCEEDED" && (
        <Notice tone="success" title="A aplicação está no ar.">
          {release?.url ? (
            <>
              Endereço público: <a href={release.url} target="_blank" rel="noreferrer noopener">{release.url}</a>
            </>
          ) : (
            <>Sem domínio configurado: acessível só na porta {release?.host_port} do servidor.</>
          )}{" "}
          A verificação passou em <span className="mono">{String(lastProbe(ev.events)?.path ?? "/")}</span>.
        </Notice>
      )}
      {state === "NEEDS_INPUT" && dep.analysis_id && (
        <PendingPanel deploymentId={id} onAnswered={() => { ev.resume(); qc.invalidateQueries({ queryKey: ["deployment", id] }); }} />
      )}
      {state === "UNSUPPORTED" && <UnsupportedPanel deploymentId={id} />}
      {state === "FAILED" && <FailurePanel reason={dep.failure_reason} failure={failure} projectId={dep.project_id} />}
      {state === "CANCELLED" && <Notice tone="neutral" title="Deploy cancelado.">Nada foi executado. Peça um novo deploy quando quiser.</Notice>}

      <div className="tabs" role="tablist" aria-label="Detalhes do deploy">
        {(["progresso", "plano", "analise"] as Tab[]).map((t) => (
          <button key={t} role="tab" aria-selected={tab === t} className={tab === t ? "tab active" : "tab"} onClick={() => setTab(t)}>
            {t === "progresso" ? "Progresso e log" : t === "plano" ? "Plano e Dockerfile" : "Análise e evidências"}
          </button>
        ))}
      </div>
      {tab === "progresso" && <LogView events={ev.events} live={!ev.settled} error={ev.error} />}
      {tab === "plano" && <PlanView deploymentId={id} hasAnalysis={!!dep.analysis_id} />}
      {tab === "analise" && <AnalysisView deploymentId={id} hasAnalysis={!!dep.analysis_id} />}
    </div>
  );
}

function lastProbe(events: DeploymentEvent[]) {
  return [...events].reverse().find((e) => e.event_type === "probe")?.payload as Record<string, unknown> | undefined;
}

const STEP_ICON: Record<StageStatus, string> = { done: "✓", current: "●", failed: "✗", waiting: "?", pending: "○" };

function Stepper({ statuses }: { statuses: Record<string, StageStatus> }) {
  return (
    <ol className="stepper" aria-label="Etapas">
      {STAGES.map((s) => {
        const st = statuses[s.key] ?? "pending";
        return (
          <li key={s.key} className={`step step-${st}`} aria-current={st === "current" ? "step" : undefined}>
            <span className="step-icon" aria-hidden="true">{STEP_ICON[st]}</span>
            <span className="step-label">{s.label}</span>
            <span className="sr-only">{st === "done" ? "concluída" : st === "failed" ? "falhou" : st === "current" ? "em andamento" : st === "waiting" ? "aguardando você" : "pendente"}</span>
          </li>
        );
      })}
    </ol>
  );
}

function LogView({ events, live, error }: { events: DeploymentEvent[]; live: boolean; error: string | null }) {
  const ref = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const lines = useMemo(() => events.flatMap(eventToLines), [events]);
  useEffect(() => {
    const el = ref.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [lines.length]);
  return (
    <Card
      title="Log por etapa"
      actions={live ? <Badge tone="info">ao vivo</Badge> : <span className="muted small">{lines.length} linhas</span>}
    >
      {error && <Notice tone="warning">Falha ao atualizar: {error}. Tentando de novo…</Notice>}
      <div
        className="terminal"
        ref={ref}
        role="log"
        aria-live="polite"
        tabIndex={0}
        onScroll={(e) => {
          const el = e.currentTarget;
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
        }}
      >
        {lines.length === 0 ? (
          <div className="term-line term-step">Aguardando o worker…</div>
        ) : (
          lines.map((l, i) => (
            <div key={`${l.seq}-${i}`} className={`term-line term-${l.kind}`} title={absolute(l.at)}>
              {l.text}
            </div>
          ))
        )}
      </div>
    </Card>
  );
}

function useAnalysis(deploymentId: string, enabled = true) {
  return useQuery({
    queryKey: ["analysis", deploymentId],
    queryFn: () => unwrap(api.GET("/deployments/{id}/analysis", { params: { path: { id: deploymentId } } })),
    enabled,
    retry: (n, e) => !(e instanceof ApiError && e.code === "analysis_unavailable") && n < 2,
  });
}

/** O formulário de uma pendência: opções sugeridas (com o tipo de cada uma) ou valor livre. */
function PendingPanel({ deploymentId, onAnswered }: { deploymentId: string; onAnswered: () => void }) {
  const analysis = useAnalysis(deploymentId);
  const [values, setValues] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const pending = (analysis.data?.result.pending ?? []).filter((f): f is Finding & { field: string } => !!f.field);

  async function submit(evt: FormEvent) {
    evt.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const overrides = Object.fromEntries(Object.entries(values).filter(([, v]) => v.trim() !== "").map(([k, v]) => [k, v.trim()]));
      await unwrap(api.POST("/deployments/{id}/answers", { params: { path: { id: deploymentId } }, body: { overrides } }));
      onAnswered();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  if (analysis.isLoading) return <Loading label="Carregando as pendências…" />;
  if (analysis.isError) return <ErrorBox error={analysis.error} onRetry={() => analysis.refetch()} />;

  return (
    <Card title="O Cobogó precisa de uma informação" className="card-warning">
      <p className="muted">Em vez de adivinhar, a análise parou aqui. Responda e o deploy continua no mesmo commit.</p>
      <form className="stack" onSubmit={submit}>
        {pending.map((f) => (
          <fieldset key={f.field} className="pending">
            <legend>{FIELD_LABEL[f.field] ?? f.field}</legend>
            <p className="pending-why">{f.message}</p>
            {f.options && f.options.length > 0 && (
              <div className="options" role="radiogroup" aria-label={`Opções para ${FIELD_LABEL[f.field] ?? f.field}`}>
                {f.options.map((o) => (
                  <label key={o.value} className={`option ${values[f.field] === o.value ? "selected" : ""}`}>
                    <input type="radio" name={f.field} value={o.value} checked={values[f.field] === o.value} onChange={() => setValues((v) => ({ ...v, [f.field]: o.value }))} />
                    <span className="mono">{o.value}</span>
                    {o.kind && <Badge tone={o.kind === "production" ? "success" : "warning"}>{o.kind === "production" ? "produção" : "desenvolvimento"}</Badge>}
                    {o.note && <span className="muted small">{o.note}</span>}
                  </label>
                ))}
              </div>
            )}
            <input
              aria-label={`${FIELD_LABEL[f.field] ?? f.field}: outro valor`}
              inputMode={f.field === "port" ? "numeric" : undefined}
              placeholder={f.options?.length ? "ou informe outro valor" : f.field === "port" ? "por exemplo 3000" : "informe o valor"}
              value={f.options?.some((o) => o.value === values[f.field]) ? "" : values[f.field] ?? ""}
              onChange={(e) => setValues((v) => ({ ...v, [f.field]: e.target.value }))}
              spellCheck={false}
            />
            {f.omitted && f.omitted.length > 0 && <p className="muted small">Não oferecidos como opção: {f.omitted.join(", ")}.</p>}
          </fieldset>
        ))}
        {pending.length === 0 && <Empty title="Nenhuma pendência com campo editável.">Veja a aba de análise.</Empty>}
        {error && <Notice tone="danger">{error}</Notice>}
        <div className="row-end">
          <Button type="submit" variant="primary" busy={busy} disabled={!pending.some((f) => (values[f.field] ?? "").trim())}>
            Responder e continuar
          </Button>
        </div>
      </form>
    </Card>
  );
}

function UnsupportedPanel({ deploymentId }: { deploymentId: string }) {
  const analysis = useAnalysis(deploymentId);
  const items = analysis.data?.result.unsupported ?? [];
  return (
    <Notice tone="danger" title="Este repositório ainda não é suportado.">
      {items.length ? (
        <ul>
          {items.map((f, i) => (
            <li key={i}>{f.message}</li>
          ))}
        </ul>
      ) : (
        "A análise não encontrou um perfil que o Cobogó saiba publicar."
      )}
    </Notice>
  );
}

function FailurePanel({ reason, failure, projectId }: { reason?: string; failure: ReturnType<typeof failureDetail>; projectId: string }) {
  const d = failure?.diagnosis;
  return (
    <Card title="O deploy falhou" className="card-danger">
      <p>
        <strong>Etapa:</strong> {failure?.stage ?? "—"}
        {failure?.exitCode !== undefined && <> · <strong>código de saída</strong> <span className="mono">{failure.exitCode}</span></>}
      </p>
      <p className="mono wrap">{failure?.error || reason}</p>
      {failure?.oomKilled && (
        <Notice tone="danger" title="Falta de memória">
          O kernel encerrou a aplicação por exceder o limite de memória. Aumente o limite nas configurações do projeto e faça um novo deploy.
        </Notice>
      )}
      {d && !failure?.oomKilled && (
        <Notice tone={d.confidence === "confirmed" ? "danger" : "warning"} title={d.confidence === "confirmed" ? "Causa provável" : "Possível causa"}>
          {d.message}
          {d.variables && d.variables.length > 0 && (
            <>
              {" "}
              <Link to={`/projetos/${projectId}?aba=variaveis`}>Preencher {d.variables.join(", ")}</Link>
            </>
          )}
        </Notice>
      )}
      {failure?.logTail && (
        <details open>
          <summary>Últimas linhas do log da aplicação (valores das variáveis trocados por [REDIGIDO])</summary>
          <CodeBlock label="Log da aplicação">{failure.logTail}</CodeBlock>
        </details>
      )}
    </Card>
  );
}

function PlanView({ deploymentId, hasAnalysis }: { deploymentId: string; hasAnalysis: boolean }) {
  const plan = useQuery({
    queryKey: ["plan", deploymentId],
    queryFn: () => unwrap(api.GET("/deployments/{id}/plan", { params: { path: { id: deploymentId } } })),
    enabled: hasAnalysis,
    retry: (n, e) => !(e instanceof ApiError && (e.code === "plan_unavailable" || e.code === "analysis_unavailable")) && n < 2,
  });
  if (!hasAnalysis) return <Empty title="A análise ainda não terminou.">O plano aparece assim que o worker analisar o commit.</Empty>;
  if (plan.isLoading) return <Loading />;
  if (plan.error instanceof ApiError && plan.error.code === "plan_unavailable") return <Empty title="Sem plano ainda.">A análise precisa de uma informação antes de gerar o plano.</Empty>;
  if (plan.isError) return <ErrorBox error={plan.error} />;
  const p = plan.data!;
  const rows: [string, string][] = [
    ["Perfil", p.profile],
    ["Imagem de build", p.build_image],
    ["Imagem de execução", p.runtime],
    ["Instalação", p.install_command],
    ["Build", p.build_command || "—"],
    ["Execução", p.start_command],
    ["Modo", p.execution_mode === "production" ? "produção" : "desenvolvimento"],
    ["Porta", `${p.port} (${ORIGIN_LABEL[p.port_origin] ?? p.port_origin})`],
    ["Verificação", p.health_path],
  ];
  return (
    <div className="grid-2">
      <Card title="O que será executado">
        <dl className="kv">
          {rows.map(([k, v]) => (
            <div key={k}>
              <dt>{k}</dt>
              <dd className="mono">{v}</dd>
            </div>
          ))}
        </dl>
      </Card>
      <Card title="Dockerfile gerado">
        <CodeBlock label="Dockerfile">{p.dockerfile}</CodeBlock>
        <p className="muted small">Gerado pelo Cobogó; o Dockerfile do repositório, se houver, não é alterado nem usado.</p>
      </Card>
    </div>
  );
}

function AnalysisView({ deploymentId, hasAnalysis }: { deploymentId: string; hasAnalysis: boolean }) {
  const analysis = useAnalysis(deploymentId, hasAnalysis);
  if (!hasAnalysis) return <Empty title="A análise ainda não terminou." />;
  if (analysis.isLoading) return <Loading />;
  if (analysis.isError) return <ErrorBox error={analysis.error} />;
  return <AnalysisDetail analysis={analysis.data!} />;
}

export function AnalysisDetail({ analysis }: { analysis: Analysis }) {
  const r = analysis.result;
  const evidence = new Map(r.evidences.map((e) => [e.id, e]));
  return (
    <div className="stack">
      <Card title="Decisões" actions={<span className="muted small">regras {analysis.rules_version} · commit {shortSha(analysis.commit_sha)}</span>}>
        <table className="table">
          <thead>
            <tr>
              <th scope="col">Campo</th>
              <th scope="col">Valor</th>
              <th scope="col">Origem</th>
              <th scope="col">Por quê</th>
            </tr>
          </thead>
          <tbody>
            {Object.entries(r.fields).map(([name, f]) => (
              <tr key={name}>
                <td>{FIELD_LABEL[name] ?? name}</td>
                <td className="mono">{f.value}</td>
                <td>
                  <Badge tone={f.origin === "override" ? "info" : f.origin === "default" ? "neutral" : "success"}>{ORIGIN_LABEL[f.origin] ?? f.origin}</Badge>
                </td>
                <td className="small">
                  {(f.rules ?? []).join(", ")}
                  {(f.evidence_ids ?? []).map((id) => {
                    const e = evidence.get(id);
                    return e ? (
                      <div key={id} className="muted mono">
                        {e.source}
                        {e.line ? `:${e.line}` : ""} {e.name ? `· ${e.name}` : ""}
                      </div>
                    ) : null;
                  })}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>
      {r.warnings.length > 0 && (
        <Card title="Avisos">
          {r.warnings.map((w, i) => (
            <Notice key={i} tone="warning">
              {w.message}
            </Notice>
          ))}
        </Card>
      )}
      {r.config_variables.length > 0 && (
        <Card title="Variáveis de ambiente encontradas">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Nome</th>
                <th scope="col">Quem fornece</th>
                <th scope="col">No exemplo</th>
                <th scope="col">Aviso</th>
              </tr>
            </thead>
            <tbody>
              {r.config_variables.map((v) => (
                <tr key={v.name}>
                  <td className="mono">{v.name}</td>
                  <td>{v.class === "platform" ? "o Cobogó" : v.class === "generable" ? "pode ser gerada" : "você"}</td>
                  <td className="small">{v.example}</td>
                  <td className="small muted">{v.hint?.message ?? "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
      )}
      <Card title="Cobertura">
        <p className="small">
          {r.coverage.files_read} arquivo(s) lido(s)
          {r.coverage.gaps.length > 0 ? `; não interpretados: ${r.coverage.gaps.map((g) => g.path).join(", ")}` : "; nada ficou de fora."}
        </p>
      </Card>
    </div>
  );
}
