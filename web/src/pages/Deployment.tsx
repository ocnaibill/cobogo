// TEL-06 — Deploy em andamento e falha explicada, com TEL-04/05 — pendências,
// plano e evidências. O progresso vem dos eventos incrementais (DEC-28); a
// análise e o plano mostram o que o Cobogó decidiu e por quê.
import { DatabaseChoice, useDatabase } from "../components/Database";
import { useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, idempotencyKey, unwrap, type Analysis, type DeploymentEvent, type DeploymentState, type Finding } from "../api/client";
import { useEvents } from "../hooks/useEvents";
import {
  canCancel,
  eventToLines,
  failureDetail,
  FIELD_LABEL,
  isTerminal,
  ORIGIN_LABEL,
  profileLabel,
  STAGES,
  stageStatuses,
  STATE_LABEL,
  type FailureDetail,
  type LogLine,
  type StageStatus,
} from "../lib/deploy";
import { absolute, duration, shortSha } from "../lib/format";
import { Badge, Button, Card, CodeBlock, Empty, ErrorBox, LinkButton, Loading, Notice, PageHead, StateBadge, Tag, When } from "../components/ui";
import { Icon, type IconName } from "../components/icons";

type Tab = "progresso" | "plano" | "analise";

/** Relógio que só anda enquanto o deploy está vivo. */
function useNow(active: boolean) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [active]);
  return now;
}

function headline(state: DeploymentState, failure: FailureDetail | null): string {
  switch (state) {
    case "SUCCEEDED":
      return "Publicado e no ar";
    case "FAILED":
      return failure ? `Falhou na etapa de ${failure.stage}` : "O deploy falhou";
    case "NEEDS_INPUT":
      return "Aguardando uma resposta sua";
    case "UNSUPPORTED":
      return "Repositório não suportado";
    case "CANCELLED":
      return "Deploy cancelado";
    case "QUEUED":
      return "Na fila do worker";
    default:
      return `${STATE_LABEL[state]}…`;
  }
}

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
  const projectId = dep?.project_id;
  const project = useQuery({
    queryKey: ["project", projectId],
    queryFn: () => unwrap(api.GET("/projects/{id}", { params: { path: { id: projectId! } } })),
    enabled: !!projectId,
  });
  const history = useQuery({
    queryKey: ["deployments", projectId],
    queryFn: () => unwrap(api.GET("/projects/{id}/deployments", { params: { path: { id: projectId! }, query: { limit: 50 } } })),
    enabled: !!projectId,
    refetchInterval: ev.settled ? false : 5000,
  });

  const state = ev.state ?? dep?.state ?? "QUEUED";
  // Quando o estado muda, o resumo (release, motivo) também precisa mudar.
  useEffect(() => {
    if (ev.state && dep && ev.state !== dep.state) {
      qc.invalidateQueries({ queryKey: ["deployment", id] });
      qc.invalidateQueries({ queryKey: ["deployments", dep.project_id] });
    }
  }, [ev.state, dep, id, qc]);

  const stages = useMemo(() => stageStatuses(state, ev.events), [state, ev.events]);
  const failure = useMemo(() => (state === "FAILED" ? failureDetail(ev.events) : null), [state, ev.events]);
  const now = useNow(!isTerminal(state));

  if (detail.isLoading) return <Loading />;
  if (detail.isError) return <ErrorBox error={detail.error} onRetry={() => detail.refetch()} />;
  if (!dep) return <Empty title="Deploy não encontrado." />;
  const release = detail.data?.release;
  const elapsed = duration(dep.created_at, isTerminal(state) ? dep.updated_at : new Date(now).toISOString());
  // A versão que serve o tráfego: o deploy concluído mais recente do projeto.
  const live = history.data?.deployments.find((d) => d.state === "SUCCEEDED");

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

  async function redeploy(overrides?: Record<string, string>) {
    setBusy("redeploy");
    setActionError(null);
    try {
      const d = await unwrap(
        api.POST("/projects/{id}/deployments", { params: { path: { id: dep!.project_id } }, body: { idempotency_key: idempotencyKey(), overrides } }),
      );
      navigate(`/deploys/${d.id}`);
    } catch (e) {
      setActionError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  const slug = project.data?.slug;
  return (
    <div className="page">
      <PageHead
        crumbs={
          <>
            <Link to="/">Projetos</Link> / {project.data ? <Link to={`/projetos/${project.data.id}`}>{slug}</Link> : "…"} / <strong>deploy {dep.id.slice(0, 8)}</strong>
          </>
        }
        eyebrow={
          <>
            <Icon name="rocket" size={14} /> Execução do deploy
          </>
        }
        title={headline(state, failure)}
        actions={
          <>
            {canCancel(state) && (
              <Button variant="danger" icon="x" onClick={cancel} busy={busy === "cancel"}>
                Cancelar
              </Button>
            )}
            {isTerminal(state) && (
              <Button icon="refresh" onClick={() => redeploy()} busy={busy === "redeploy"}>
                Novo deploy
              </Button>
            )}
            {release?.url && state === "SUCCEEDED" && (
              <LinkButton href={release.url} variant="primary" iconEnd="external">
                Abrir aplicação
              </LinkButton>
            )}
          </>
        }
      >
        <StateBadge state={state} />
        <span className="meta row">
          <Icon name="commit" size={14} /> {dep.commit_sha ? shortSha(dep.commit_sha) : "commit ainda não fixado"}
        </span>
        <span className="meta">
          pedido <When iso={dep.created_at} />
        </span>
      </PageHead>
      {actionError && <Notice tone="danger">{actionError}</Notice>}

      <StatusBanner state={state} failure={failure} reason={dep.failure_reason} elapsed={elapsed} release={release} probePath={lastProbe(ev.events)?.path} live={live} thisId={dep.id} />

      <Stages statuses={stages} />

      <div className="split">
        <div className="stack">
          {state === "NEEDS_INPUT" && dep.analysis_id && (
            <PendingPanel
              deploymentId={id}
              projectId={projectId}
              onAnswered={() => {
                ev.resume();
                qc.invalidateQueries({ queryKey: ["deployment", id] });
              }}
            />
          )}
          {state === "UNSUPPORTED" && <UnsupportedPanel deploymentId={id} />}
          {state === "FAILED" && <FailurePanel reason={dep.failure_reason} failure={failure} projectId={dep.project_id} onRedeploy={redeploy} busy={busy === "redeploy"} />}
          {state === "CANCELLED" && (
            <Notice tone="neutral" title="Deploy cancelado.">
              Nada foi executado. Peça um novo deploy quando quiser.
            </Notice>
          )}

          <div className="tabs" role="tablist" aria-label="Detalhes do deploy">
            {(
              [
                ["progresso", "Progresso e log", "terminal"],
                ["plano", "Plano e Dockerfile", "fileCode"],
                ["analise", "Análise e evidências", "search"],
              ] as [Tab, string, IconName][]
            ).map(([t, label, icon]) => (
              <button key={t} role="tab" aria-selected={tab === t} className={tab === t ? "tab active" : "tab"} onClick={() => setTab(t)}>
                <Icon name={icon} size={16} />
                {label}
              </button>
            ))}
          </div>
          {tab === "progresso" && <LogView deploymentId={dep.id} state={state} events={ev.events} live={!ev.settled} error={ev.error} />}
          {tab === "plano" && <PlanView deploymentId={id} hasAnalysis={!!dep.analysis_id} />}
          {tab === "analise" && <AnalysisView deploymentId={id} hasAnalysis={!!dep.analysis_id} />}
        </div>

        <aside className="stack" aria-label="Contexto">
          <ProductionCard live={live} thisId={dep.id} state={state} />
          <LimitsCard projectId={dep.project_id} oom={!!failure?.oomKilled} />
        </aside>
      </div>
    </div>
  );
}

function lastProbe(events: DeploymentEvent[]) {
  return [...events].reverse().find((e) => e.event_type === "probe")?.payload as { path?: string } | undefined;
}

function StatusBanner({
  state,
  failure,
  reason,
  elapsed,
  release,
  probePath,
  live,
  thisId,
}: {
  state: DeploymentState;
  failure: FailureDetail | null;
  reason?: string;
  elapsed: string;
  release?: { url?: string; host_port: number };
  probePath?: string;
  live?: { id: string; commit_sha: string };
  thisId: string;
}) {
  let tone = "";
  let icon: IconName = "activity";
  let title: ReactNode = "O worker está trabalhando neste deploy";
  let text: ReactNode = "Cada etapa aparece abaixo assim que acontece. Se algo falhar, a versão no ar continua no ar.";
  if (state === "SUCCEEDED") {
    tone = "banner-success";
    icon = "checkCircle";
    title = "A aplicação está no ar";
    text = release?.url ? (
      <>
        Em{" "}
        <a href={release.url} target="_blank" rel="noreferrer noopener" className="mono">
          {release.url}
        </a>
        . A verificação passou em <code>{probePath ?? "/"}</code>.
      </>
    ) : (
      <>Sem domínio configurado: acessível só na porta {release?.host_port} do servidor.</>
    );
  } else if (state === "FAILED") {
    tone = "banner-danger";
    icon = "xCircle";
    title = (
      <>
        Falha na etapa de {failure?.stage ?? "—"} <Tag tone="danger">interrompido</Tag>
      </>
    );
    text = <span className="wrap">{failure?.error || reason || "Sem detalhe."}</span>;
  } else if (state === "NEEDS_INPUT") {
    tone = "banner-warning";
    icon = "help";
    title = "A análise parou para perguntar";
    text = "Em vez de adivinhar, o Cobogó pede a informação que faltou. Responda e o deploy continua no mesmo commit.";
  } else if (state === "UNSUPPORTED" || state === "CANCELLED") {
    tone = state === "UNSUPPORTED" ? "banner-danger" : "";
    icon = state === "UNSUPPORTED" ? "alert" : "x";
    title = state === "UNSUPPORTED" ? "O Cobogó ainda não sabe publicar este repositório" : "Cancelado antes de executar";
    text = state === "UNSUPPORTED" ? "Os motivos estão abaixo." : "Nada foi executado.";
  }
  const servingOther = live && live.id !== thisId;
  return (
    <div className={`banner ${tone} banner-white`}>
      <div className="banner-main">
        <span className="banner-icon" aria-hidden="true">
          <Icon name={icon} size={24} />
        </span>
        <div>
          <div className="banner-title">{title}</div>
          <p>{text}</p>
        </div>
      </div>
      <div className="banner-figures">
        <div className="figure">
          <span className="label">{isTerminal(state) ? "Duração" : "Tempo decorrido"}</span>
          <strong>{elapsed}</strong>
        </div>
        <div className="figure">
          <span className="label">Em produção</span>
          <span className="row meta">
            {live ? (
              <>
                <span className="dot dot-success" aria-hidden="true" />
                {servingOther ? `versão ${shortSha(live.commit_sha)}` : "esta versão"}
              </>
            ) : (
              "nenhuma versão"
            )}
          </span>
        </div>
      </div>
    </div>
  );
}

const STATUS_TEXT: Record<StageStatus, [string, IconName | null]> = {
  done: ["Concluída", "check"],
  current: ["Em andamento", null],
  failed: ["Falhou", "xCircle"],
  waiting: ["Aguardando você", "help"],
  pending: ["Pendente", null],
};

function Stages({ statuses }: { statuses: Record<string, StageStatus> }) {
  return (
    <ol className="stages" aria-label="Etapas">
      {STAGES.map((s, i) => {
        const st = statuses[s.key] ?? "pending";
        const [text, icon] = STATUS_TEXT[st];
        return (
          <li key={s.key} className={`stage stage-${st}`} aria-current={st === "current" ? "step" : undefined}>
            <div className="stage-top">
              <span className="stage-num">Etapa {String(i + 1).padStart(2, "0")}</span>
              {icon && <Icon name={icon} size={16} />}
              {st === "current" && <span className="spinner" aria-hidden="true" />}
            </div>
            <span className="stage-name">{s.label}</span>
            <span className="stage-status">{text}</span>
          </li>
        );
      })}
    </ol>
  );
}

/** Tira do texto o nome da etapa quando a etiqueta já o mostra. */
function lineText(l: LogLine): string {
  return l.text.replace(new RegExp(`^([▸✓✗] )${l.tag.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}: `), "$1");
}

function offset(fromMs: number, iso: string): string {
  const s = Math.max(0, (new Date(iso).getTime() - fromMs) / 1000);
  const m = Math.floor(s / 60);
  return `${String(m).padStart(2, "0")}:${(s % 60).toFixed(1).padStart(4, "0")}`;
}

function LogView({ deploymentId, state, events, live, error }: { deploymentId: string; state: DeploymentState; events: DeploymentEvent[]; live: boolean; error: string | null }) {
  const ref = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [following, setFollowing] = useState(true);
  const lines = useMemo(() => events.flatMap(eventToLines), [events]);
  const start = lines.length ? new Date(lines[0]!.at).getTime() : 0;
  useEffect(() => {
    const el = ref.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [lines.length]);
  return (
    <div className="stack-sm">
      {error && <Notice tone="warning">Falha ao atualizar: {error}. Tentando de novo…</Notice>}
      <div className="term">
        <div className="term-bar">
          <span className="term-lights" aria-hidden="true">
            <span />
            <span />
            <span />
          </span>
          <span className="term-title">cobogo@worker: deploy {deploymentId.slice(0, 8)}</span>
          <span className="term-status">
            {live && <span className="dot dot-pulse" aria-hidden="true" />}
            {live ? `${STATE_LABEL[state]} · ao vivo` : STATE_LABEL[state]}
          </span>
        </div>
        <div
          className="term-body"
          ref={ref}
          role="log"
          aria-live="polite"
          aria-label="Log do deploy"
          tabIndex={0}
          onScroll={(e) => {
            const el = e.currentTarget;
            stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
            if (stick.current !== following) setFollowing(stick.current);
          }}
        >
          {lines.length === 0 ? (
            <div className="term-line term-step">
              <span className="term-time">00:00.0</span>
              <span className="term-tag">fila</span>
              <span className="term-text">Aguardando o worker…</span>
            </div>
          ) : (
            lines.map((l, i) => (
              <div key={`${l.seq}-${i}`} className={`term-line term-${l.kind}`} title={absolute(l.at)}>
                <span className="term-time">{offset(start, l.at)}</span>
                <span className="term-tag">{l.tag}</span>
                <span className="term-text">{lineText(l)}</span>
              </div>
            ))
          )}
        </div>
        <div className="term-foot">
          <span>
            {lines.length} linha(s) · {following ? "acompanhando o fim" : "rolagem pausada"}
          </span>
          <span>valores de variáveis nunca aparecem aqui</span>
        </div>
      </div>
    </div>
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
function PendingPanel({ deploymentId, projectId, onAnswered }: { deploymentId: string; projectId?: string; onAnswered: () => void }) {
  const analysis = useAnalysis(deploymentId);
  const [values, setValues] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const allPending = (analysis.data?.result.pending ?? []).filter((f): f is Finding & { field: string } => !!f.field);
  // A pergunta do banco (DEC-42) não se responde por overrides: tem o próprio painel.
  const databaseQuestion = allPending.find((f) => f.field === "database");
  const pending = allPending.filter((f) => f.field !== "database");
  const open = pending.filter((f) => !(values[f.field] ?? "").trim());

  async function submit(evt: FormEvent) {
    evt.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const overrides = Object.fromEntries(
        Object.entries(values)
          .filter(([, v]) => v.trim() !== "")
          .map(([k, v]) => [k, v.trim()]),
      );
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
  if (databaseQuestion && projectId) {
    return (
      <div className="stack">
        <DatabaseQuestion projectId={projectId} message={databaseQuestion.message} onChosen={onAnswered} />
        {pending.length > 0 && <Notice tone="info">Há outras pendências; elas aparecem depois que você escolher o banco.</Notice>}
      </div>
    );
  }

  return (
    <Card title="Configuração necessária" icon="sliders" className="card-warning" actions={<Tag tone="warning">{pending.length} pendência(s)</Tag>}>
      <form className="stack" onSubmit={submit}>
        {pending.map((f) => {
          const label = FIELD_LABEL[f.field] ?? f.field;
          return (
            <fieldset key={f.field} className="pending">
              <legend>
                <span className="row">
                  <span className="pending-name">{label}</span>
                  <Tag tone="danger">obrigatória</Tag>
                </span>
                <span className="meta">{f.field}</span>
              </legend>
              <p className="pending-why">{f.message}</p>
              {f.options && f.options.length > 0 && (
                <div className="options" role="radiogroup" aria-label={`Opções para ${label}`}>
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
                className="mono"
                aria-label={`${label}: outro valor`}
                inputMode={f.field === "port" ? "numeric" : undefined}
                placeholder={f.options?.length ? "ou informe outro valor" : f.field === "port" ? "por exemplo 3000" : "informe o valor"}
                value={f.options?.some((o) => o.value === values[f.field]) ? "" : (values[f.field] ?? "")}
                onChange={(e) => setValues((v) => ({ ...v, [f.field]: e.target.value }))}
                spellCheck={false}
              />
              {f.omitted && f.omitted.length > 0 && <p className="muted small">Não oferecidos como opção: {f.omitted.join(", ")}.</p>}
            </fieldset>
          );
        })}
        {pending.length === 0 && <Empty title="Nenhuma pendência com campo editável.">Veja a aba de análise.</Empty>}
        {error && <Notice tone="danger">{error}</Notice>}
        <div className="action-bar">
          <span className={`small row ${open.length ? "" : "text-success"}`}>
            <span className={`dot ${open.length ? "dot-danger" : "dot-success"}`} aria-hidden="true" />
            {open.length ? `${open.length} pendência(s) restante(s): ${open.map((f) => f.field).join(", ")}` : "Tudo respondido. O deploy segue no mesmo commit."}
          </span>
          <Button type="submit" variant="primary" icon="arrowRight" busy={busy} disabled={!pending.some((f) => (values[f.field] ?? "").trim())}>
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

/** Mostra como código os trechos entre crases das mensagens do worker. */
function withCode(text: string): ReactNode[] {
  return text.split(/`([^`]+)`/).map((part, i) => (i % 2 ? <code key={i}>{part}</code> : part));
}

function FailurePanel({
  reason,
  failure,
  projectId,
  onRedeploy,
  busy,
}: {
  reason?: string;
  failure: FailureDetail | null;
  projectId: string;
  onRedeploy: (overrides?: Record<string, string>) => void;
  busy: boolean;
}) {
  const d = failure?.diagnosis;
  const [port, setPort] = useState("");
  const portSuspect = !failure?.oomKilled && d?.field === "port";
  const portValid = /^\d{1,5}$/.test(port) && Number(port) > 0 && Number(port) < 65536;
  return (
    <Card
      title="Diagnóstico da falha"
      icon="search"
      className="card-danger"
      actions={
        <>
          {failure?.exitCode !== undefined && <Tag tone="danger">código de saída {failure.exitCode}</Tag>}
          <Tag>etapa: {failure?.stage ?? "—"}</Tag>
        </>
      }
    >
      <div className="stack">
        <div className="grid-2">
          <div className="inset stack-sm">
            <span className="label">O que falhou</span>
            <p className="mono small wrap">{failure?.error || reason || "Sem detalhe."}</p>
          </div>
          <div className="inset stack-sm">
            <span className="label">{failure?.oomKilled ? "Causa confirmada" : d ? (d.confidence === "confirmed" ? "Causa provável" : "Possível causa") : "Causa"}</span>
            <p className="small">
              {failure?.oomKilled
                ? "O kernel encerrou a aplicação por exceder o limite de memória."
                : d?.message
                  ? withCode(d.message)
                  : "O Cobogó não reconheceu um padrão conhecido neste erro. O log abaixo mostra o que a aplicação escreveu."}
            </p>
          </div>
        </div>
        {failure?.logTail && (
          <CodeBlock label="Log da aplicação" title="Últimas linhas do log · valores de variáveis trocados por [REDIGIDO]">
            {failure.logTail}
          </CodeBlock>
        )}
        <div className="action-bar">
          <span className="small row">
            <Icon name="sparkle" size={16} className="card-icon" />
            <strong>Próximo passo:</strong>
            {failure?.oomKilled
              ? "aumente a memória do projeto e publique de novo."
              : portSuspect
                ? "informe a porta em que a aplicação escuta e publique de novo."
                : d?.variables?.length
                ? `preencha ${d.variables.join(", ")} e publique de novo.`
                : "corrija no repositório e peça um novo deploy."}
          </span>
          <div className="row">
            {failure?.oomKilled && (
              <LinkButton to={`/projetos/${projectId}?aba=limites`} icon="sliders">
                Ajustar limites
              </LinkButton>
            )}
            {!failure?.oomKilled && d?.variables && d.variables.length > 0 && (
              <LinkButton to={`/projetos/${projectId}?aba=variaveis`} icon="key">
                Preencher variáveis
              </LinkButton>
            )}
            {portSuspect ? (
              <form
                className="row"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (portValid) onRedeploy({ port });
                }}
              >
                <label className="sr-only" htmlFor="fix-port">
                  Porta em que a aplicação escuta
                </label>
                <input id="fix-port" className="input-sm mono" inputMode="numeric" placeholder="porta, ex.: 3000" value={port} onChange={(e) => setPort(e.target.value.trim())} />
                <Button type="submit" variant="primary" icon="refresh" busy={busy} disabled={!portValid}>
                  Novo deploy com esta porta
                </Button>
              </form>
            ) : (
              <Button variant="primary" icon="refresh" onClick={() => onRedeploy()} busy={busy}>
                Novo deploy
              </Button>
            )}
          </div>
        </div>
      </div>
    </Card>
  );
}

function ProductionCard({ live, thisId, state }: { live?: { id: string; commit_sha: string; updated_at: string }; thisId: string; state: DeploymentState }) {
  const isThis = live?.id === thisId;
  return (
    <Card title="Produção" icon="shieldCheck">
      {live ? (
        <div className="stack-sm">
          <div className="inset stack-sm">
            <div className="row-between">
              <span className="label">Versão servindo tráfego</span>
              <Badge tone="success">no ar</Badge>
            </div>
            <strong className="mono">
              {shortSha(live.commit_sha)} {isThis && <span className="faint">(este deploy)</span>}
            </strong>
            <span className="meta">
              publicada <When iso={live.updated_at} />
            </span>
          </div>
          {!isThis && (
            <p className="small muted">
              {state === "FAILED"
                ? "A falha foi só na versão candidata: o roteamento só troca depois que a nova versão passa na verificação."
                : "Ela continua servindo até esta versão passar na verificação; a troca é de uma vez."}{" "}
              <Link to={`/deploys/${live.id}`}>Ver o deploy dela</Link>
            </p>
          )}
        </div>
      ) : (
        <p className="small muted">Nenhuma versão deste projeto está no ar ainda.</p>
      )}
    </Card>
  );
}

function LimitsCard({ projectId, oom }: { projectId: string; oom: boolean }) {
  const q = useQuery({ queryKey: ["limits", projectId], queryFn: () => unwrap(api.GET("/projects/{id}/limits", { params: { path: { id: projectId } } })) });
  const e = q.data?.effective;
  const tile = (label: string, v: number | undefined, unit: string, hot = false) => (
    <div className={`tile ${hot ? "tile-hot" : ""}`}>
      <span className="label">{label}</span>
      <strong>{v !== undefined ? `${v} ${unit}` : "—"}</strong>
    </div>
  );
  return (
    <Card title="Limites" icon="cpu" actions={<Link to={`/projetos/${projectId}?aba=limites`} className="small">Ajustar</Link>}>
      {q.isLoading && <Loading />}
      {e && (
        <div className="stack-sm">
          <span className="label">Aplicação</span>
          <div className="tiles">
            {tile("Memória", e.app_memory_mb?.value, "MiB", oom)}
            {tile("CPU", e.app_cpus?.value, "")}
            {tile("Processos", e.app_pids?.value, "")}
          </div>
          <span className="label">Build</span>
          <div className="tiles">
            {tile("Memória", e.build_memory_mb?.value, "MiB")}
            {tile("CPU", e.build_cpus?.value, "")}
          </div>
          {oom && <Notice tone="danger">A aplicação passou do limite de memória e foi encerrada.</Notice>}
        </div>
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
  if (!hasAnalysis) return <Empty title="A análise ainda não terminou." icon="clock">O plano aparece assim que o worker analisar o commit.</Empty>;
  if (plan.isLoading) return <Loading />;
  if (plan.error instanceof ApiError && plan.error.code === "plan_unavailable")
    return (
      <Empty title="Sem plano ainda." icon="help">
        A análise precisa de uma informação antes de gerar o plano.
      </Empty>
    );
  if (plan.isError) return <ErrorBox error={plan.error} />;
  const p = plan.data!;
  const statik = p.profile === "P-STATIC";
  const rows: [string, string][] = statik
    ? [
        ["Perfil", `${profileLabel(p.profile)} (${p.profile})`],
        ["Gerenciador", p.package_manager === "none" ? "nenhum: servido como está" : p.package_manager],
        ...(p.package_manager === "none"
          ? []
          : ([
              ["Versão", p.bun_version ? `Bun ${p.bun_version}` : `Node ${p.node_version ?? "—"}`],
              ["Imagem de build", p.build_image],
              ["Instalação", p.install_command],
              ["Build", p.build_command || "—"],
            ] as [string, string][])),
        ["Diretório servido", p.output_dir === "." ? "a raiz do repositório" : `${p.output_dir}/`],
        ["Fallback de SPA", p.spa_fallback ? "sim: rota desconhecida devolve index.html" : "não: rota desconhecida dá 404"],
        ["Variáveis públicas no build", p.build_args?.length ? p.build_args.join(", ") : "nenhuma"],
        ["Servidor", `${p.runtime} (sem root)`],
        ["Porta", `${p.port} (${ORIGIN_LABEL[p.port_origin] ?? p.port_origin})`],
        ["Verificação", p.health_path],
      ]
    : [
    ["Perfil", `${profileLabel(p.profile) ?? p.profile} (${p.profile})`],
    ["Gerenciador", p.package_manager],
    ["Versão", p.bun_version ? `Bun ${p.bun_version}` : `Node ${p.node_version ?? "—"}`],
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
    <div className="stack">
      <Card title="O que será executado" icon="terminal">
        <dl className="kv">
          {rows.map(([k, v]) => (
            <div key={k}>
              <dt>{k}</dt>
              <dd className="mono">{v}</dd>
            </div>
          ))}
        </dl>
      </Card>
      <CodeBlock label="Dockerfile" title="Dockerfile gerado pelo Cobogó · o do repositório, se houver, não é usado">
        {p.dockerfile}
      </CodeBlock>
    </div>
  );
}

function AnalysisView({ deploymentId, hasAnalysis }: { deploymentId: string; hasAnalysis: boolean }) {
  const analysis = useAnalysis(deploymentId, hasAnalysis);
  if (!hasAnalysis) return <Empty title="A análise ainda não terminou." icon="clock" />;
  if (analysis.isLoading) return <Loading />;
  if (analysis.isError) return <ErrorBox error={analysis.error} />;
  return <AnalysisDetail analysis={analysis.data!} />;
}

const ORIGIN_TONE: Record<string, "info" | "neutral" | "success" | "brand"> = { override: "info", default: "neutral", declaration: "success", platform: "success", inference: "brand" };

export function AnalysisDetail({ analysis }: { analysis: Analysis }) {
  const r = analysis.result;
  const evidence = new Map(r.evidences.map((e) => [e.id, e]));
  const profile = profileLabel(r.fields.profile?.value);
  const decisions = Object.entries(r.fields).filter(([name]) => name !== "profile");
  return (
    <div className="stack">
      <Card>
        <div className="stack">
          <div className="profile-head">
            <span className="profile-mark" aria-hidden="true">
              {profile === "Node.js" ? "JS" : profile === "Bun" ? "BUN" : profile === "Site estático" ? "WWW" : (profile ?? "?").slice(0, 2).toUpperCase()}
            </span>
            <div className="stack-sm">
              <h2>{profile ?? "Perfil não decidido"}</h2>
              <span className="row">
                <Tag tone="brand">regras {analysis.rules_version}</Tag>
                <Tag>commit {shortSha(analysis.commit_sha)}</Tag>
                <Tag>{r.coverage.files_read} arquivo(s) lido(s)</Tag>
              </span>
            </div>
          </div>
          <div className="row-between">
            <h3>Decisões e evidências</h3>
            <span className="meta">{decisions.length} decisão(ões)</span>
          </div>
          {decisions.map(([name, f]) => (
            <div key={name} className="decision">
              <div className="decision-head">
                <span className="decision-title">
                  <span className="dot" aria-hidden="true" />
                  {FIELD_LABEL[name] ?? name}: <code>{f.value}</code>
                </span>
                <Tag tone={ORIGIN_TONE[f.origin] ?? "neutral"}>{ORIGIN_LABEL[f.origin] ?? f.origin}</Tag>
              </div>
              {(f.evidence_ids ?? []).map((id) => {
                const e = evidence.get(id);
                return e ? (
                  <p key={id} className="evidence">
                    Evidência: <code>{`${e.source}${e.line ? `:${e.line}` : ""}`}</code>
                    {e.name && <> · {e.name}</>}
                    {e.value && (
                      <>
                        {" "}
                        = <code>{e.value}</code>
                      </>
                    )}
                  </p>
                ) : null;
              })}
              {f.origin === "platform" && name === "port" && (
                <p className="evidence">O app lê <code>PORT</code>, então o Cobogó escolhe a porta e a entrega nessa variável. Se ele escutar em outra, a verificação mostra.</p>
              )}
              {(f.rules ?? []).length > 0 && <p className="meta">regra: {(f.rules ?? []).join(", ")}</p>}
            </div>
          ))}
        </div>
      </Card>
      {r.warnings.length > 0 && (
        <Card title="Avisos" icon="alert">
          <div className="stack-sm">
            {r.warnings.map((w, i) => (
              <Notice key={i} tone="warning">
                {w.message}
              </Notice>
            ))}
          </div>
        </Card>
      )}
      {r.config_variables.length > 0 && (
        <Card title="Variáveis de ambiente encontradas" icon="key" actions={<Tag>{r.config_variables.length}</Tag>}>
          <div className="table-wrap">
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
                    <td>
                      <Tag tone={v.class === "platform" ? "brand" : v.class === "generable" ? "success" : v.class === "public" ? "info" : "warning"}>
                        {v.class === "platform" ? "o Cobogó" : v.class === "generable" ? "pode ser gerada" : v.class === "public" ? "você · pública" : "você"}
                      </Tag>
                    </td>
                    <td className="small">{v.example}</td>
                    <td className="small muted">{v.hint?.message ?? "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      )}
      <Card title="Cobertura" icon="layers">
        <p className="small">
          {r.coverage.files_read} arquivo(s) lido(s)
          {r.coverage.gaps.length > 0 ? `; não interpretados: ${r.coverage.gaps.map((g) => g.path).join(", ")}` : "; nada ficou de fora."}
        </p>
      </Card>
    </div>
  );
}

/** A pergunta do banco antes do build (HEU-06.6, DEC-42). */
function DatabaseQuestion({ projectId, message, onChosen }: { projectId: string; message: string; onChosen: () => void }) {
  const db = useDatabase(projectId);
  return (
    <Card title="Onde está o banco?" icon="server" className="card-warning" actions={<Tag tone="warning">antes do build</Tag>}>
      <div className="stack">
        <p>{message}</p>
        <p className="small muted">O deploy espera a sua escolha, no mesmo commit, e nada foi construído ainda. O Cobogó só cria um banco se você escolher criar.</p>
        {db.isLoading && <Loading />}
        {db.isError && <ErrorBox error={db.error} onRetry={() => db.refetch()} />}
        {db.data && <DatabaseChoice projectId={projectId} view={db.data} onChosen={onChosen} />}
      </div>
    </Card>
  );
}
