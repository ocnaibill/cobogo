// O projeto é o repositório (DEC-52): uma página com abas para a visão geral, cada
// aplicação (frontend, backend) e o banco de dados. Um projeto novo mostra antes o que
// o Cobogó achou no repositório, e cria e publica tudo com um clique.
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, idempotencyKey, unwrap, type ProjectSummary, type Stack, type StackScanApp } from "../api/client";
import { Badge, Button, Card, Empty, ErrorBox, Loading, Notice, PageHead, StateBadge, Tag, When } from "../components/ui";
import { Icon } from "../components/icons";
import { ServicesTab } from "../components/Database";
import { ProjectPage } from "./Project";
import { isActive, profileLabel } from "../lib/deploy";
import { repoName } from "../lib/format";
import { appTabLabel, dataApp, pendingProposals, ROLE_LABEL } from "../lib/stacks";
import { useAppsDomain } from "../hooks/useInstance";

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function StackPage() {
  const { id = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const stack = useQuery({
    queryKey: ["stack", id],
    queryFn: () => unwrap(api.GET("/stacks/{id}", { params: { path: { id } } })),
    retry: false,
    refetchInterval: (q) => {
      const s = q.state.data;
      if (!s) return false;
      const busy = s.scan_state === "scanning" || s.apps.some((a) => a.last_deployment && (isActive(a.last_deployment.state) || a.last_deployment.state === "QUEUED"));
      return busy ? 2000 : 10000;
    },
  });
  // Um link antigo (/projetos/<id da aplicação>) leva ao projeto, na aba da aplicação.
  const notFound = stack.isError && stack.error instanceof ApiError && stack.error.status === 404;
  const legacy = useQuery({
    queryKey: ["project", id],
    queryFn: () => unwrap(api.GET("/projects/{id}", { params: { path: { id } } })),
    enabled: notFound && UUID.test(id),
    retry: false,
  });
  useEffect(() => {
    const p = legacy.data;
    if (!p?.stack_id) return;
    const aba = params.get("aba");
    const q = aba === "banco" ? "secao=banco" : `app=${p.id}${aba ? `&aba=${aba}` : ""}`;
    navigate(`/projetos/${p.stack_id}?${q}`, { replace: true });
  }, [legacy.data, navigate, params]);

  if (stack.isLoading || (notFound && legacy.isLoading)) return <Loading />;
  if (notFound && legacy.data && !legacy.data.stack_id) return <ProjectPage appId={legacy.data.id} />;
  if (stack.isError) return <ErrorBox error={notFound && legacy.isError ? legacy.error : stack.error} onRetry={() => stack.refetch()} />;
  const s = stack.data!;

  const appId = params.get("app");
  const section = params.get("secao");
  const current = s.apps.find((a) => a.id === appId);
  const db = dataApp(s);
  const go = (next: Record<string, string>) => setParams(next, { replace: true });

  return (
    <div className="page">
      <StackHead stack={s} />
      <div className="tabs" role="tablist" aria-label="Partes do projeto">
        <button role="tab" aria-selected={!current && section !== "banco"} className={!current && section !== "banco" ? "tab active" : "tab"} onClick={() => go({})}>
          <Icon name="grid" size={16} />
          Visão geral
        </button>
        {s.apps.map((a) => (
          <button key={a.id} role="tab" aria-selected={current?.id === a.id} className={current?.id === a.id ? "tab active" : "tab"} onClick={() => go({ app: a.id })}>
            <Icon name={a.role === "frontend" ? "globe" : "server"} size={16} />
            {appTabLabel(a, s.apps)}
          </button>
        ))}
        {db && (
          <button role="tab" aria-selected={section === "banco"} className={section === "banco" ? "tab active" : "tab"} onClick={() => go({ secao: "banco" })}>
            <Icon name="layers" size={16} />
            Banco de dados
          </button>
        )}
      </div>
      {current ? (
        <ProjectPage key={current.id} appId={current.id} embedded />
      ) : section === "banco" && db ? (
        <ServicesTab projectId={db.id} slug={db.slug} />
      ) : (
        <StackOverview stack={s} onOpen={(a) => go({ app: a.id })} />
      )}
    </div>
  );
}

function StackHead({ stack: s }: { stack: Stack }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [busy, setBusy] = useState<"deploy" | "scan" | null>(null);
  const [error, setError] = useState<string | null>(null);

  async function deployAll() {
    setBusy("deploy");
    setError(null);
    try {
      const out = await unwrap(api.POST("/stacks/{id}/deployments", { params: { path: { id: s.id } }, body: { idempotency_key: idempotencyKey() } }));
      qc.invalidateQueries({ queryKey: ["stack"] });
      qc.invalidateQueries({ queryKey: ["stacks"] });
      const only = out.deployments.length === 1 ? out.deployments[0] : undefined;
      if (only) navigate(`/deploys/${only.id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }
  async function rescan() {
    setBusy("scan");
    setError(null);
    try {
      await unwrap(api.POST("/stacks/{id}/scan", { params: { path: { id: s.id } } }));
      qc.invalidateQueries({ queryKey: ["stack"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <>
      <PageHead
        crumbs={
          <>
            <Link to="/">Projetos</Link> / <strong>{s.slug}</strong>
          </>
        }
        title={s.slug}
        actions={
          <>
            <Button icon="search" onClick={rescan} busy={busy === "scan"} disabled={s.scan_state === "scanning"}>
              Ler o repositório de novo
            </Button>
            {s.apps.length > 0 && (
              <Button variant="primary" icon="rocket" onClick={deployAll} busy={busy === "deploy"}>
                Publicar {s.apps.length > 1 ? "tudo" : ""}
              </Button>
            )}
          </>
        }
      >
        <span className="meta row">
          <Icon name="branch" size={14} />
          <a href={s.repository_url} target="_blank" rel="noreferrer noopener">
            {repoName(s.repository_url)}
          </a>
          {s.scan?.commit_sha && <span className="mono">· lido em {s.scan.commit_sha.slice(0, 7)}</span>}
        </span>
        <span className="meta">
          {s.apps.length} aplicação(ões){s.apps.length > 1 ? ": publicar tudo sobe o backend antes do frontend" : ""}
        </span>
      </PageHead>
      {error && <Notice tone="danger">{error}</Notice>}
    </>
  );
}

function StackOverview({ stack: s, onOpen }: { stack: Stack; onOpen: (a: ProjectSummary) => void }) {
  const apps = useAppsDomain();
  const proposals = pendingProposals(s);
  return (
    <div className="stack">
      {s.scan_state === "scanning" && (
        <Notice tone="info" icon="search" title="Lendo o repositório…">
          O Cobogó clona o repositório e analisa cada pasta, sem executar nada. Em alguns segundos, a proposta aparece aqui.
        </Notice>
      )}
      {s.scan_state === "failed" && (
        <Notice tone="danger" title="Não foi possível ler o repositório">
          {s.scan_error || "Veja se o link está certo e se o repositório é público."} Use “Ler o repositório de novo” depois de corrigir.
        </Notice>
      )}
      {s.scan_state === "done" && s.apps.length === 0 && proposals.length === 0 && (
        <Empty title="Nenhuma aplicação encontrada." icon="search">
          O Cobogó não achou uma aplicação na raiz nem nas pastas (até dois níveis). Hoje ele publica projetos JavaScript, Python e sites estáticos.
        </Empty>
      )}
      {proposals.length > 0 && <Proposal stack={s} proposals={proposals} />}

      {s.apps.length > 0 && (
        <section className="card card-flush" aria-label="Aplicações">
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Aplicação</th>
                  <th scope="col">Endereço</th>
                  <th scope="col">Último deploy</th>
                  <th scope="col" className="num">
                    Ações
                  </th>
                </tr>
              </thead>
              <tbody>
                {s.apps.map((a) => {
                  const url = apps.urlFor(a.slug);
                  const live = a.last_deployment?.state === "SUCCEEDED" && a.app_state !== "stopped";
                  const scanned = s.scan?.apps.find((x) => x.root_directory === a.root_directory);
                  return (
                    <tr key={a.id}>
                      <td>
                        <div className="cell-title">
                          <button className="linklike strong" onClick={() => onOpen(a)}>
                            {appTabLabel(a, s.apps)}
                          </button>
                          <span className="cell-sub mono">
                            {a.root_directory || "./ (raiz)"}
                            {scanned?.label ? ` · ${scanned.label}` : ""}
                          </span>
                          {scanned && scanned.components.length > 0 && (
                            <span className="row">
                              {scanned.components.map((c) => (
                                <Tag key={c.role + c.label} tone={c.supported ? "brand" : "warning"} title={c.note || c.evidence.join(", ")}>
                                  {c.label}
                                </Tag>
                              ))}
                            </span>
                          )}
                        </div>
                      </td>
                      <td>
                        {url ? (
                          live ? (
                            <a href={url} target="_blank" rel="noreferrer noopener" className="mono small row">
                              {url.replace(/^https?:\/\//, "")} <Icon name="external" size={13} />
                            </a>
                          ) : (
                            <span className="mono small faint">{url.replace(/^https?:\/\//, "")}</span>
                          )
                        ) : (
                          <span className="mono small">{a.slug}</span>
                        )}
                      </td>
                      <td>
                        {a.last_deployment ? (
                          <span className="row">
                            <Link to={`/deploys/${a.last_deployment.id}`}>
                              {a.app_state === "stopped" ? <Badge tone="warning">Parado</Badge> : <StateBadge state={a.last_deployment.state} />}
                            </Link>
                            <span className="meta">
                              <When iso={a.last_deployment.created_at} />
                            </span>
                          </span>
                        ) : (
                          <Badge tone="neutral">sem deploy</Badge>
                        )}
                      </td>
                      <td className="num">
                        <div className="row-end">
                          {a.last_deployment && (
                            <Link className="icon-btn" to={`/deploys/${a.last_deployment.id}`} title="Log do último deploy" aria-label={`Log do último deploy de ${a.slug}`}>
                              <Icon name="terminal" />
                            </Link>
                          )}
                          <button className="icon-btn" onClick={() => onOpen(a)} title="Abrir a aplicação" aria-label={`Abrir ${a.slug}`}>
                            <Icon name="arrowRight" />
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </div>
  );
}

/** A proposta: as aplicações que o Cobogó achou e ainda não existem, com o endereço editável. */
function Proposal({ stack: s, proposals }: { stack: Stack; proposals: StackScanApp[] }) {
  const qc = useQueryClient();
  const apps = useAppsDomain();
  const [chosen, setChosen] = useState<Record<string, { on: boolean; slug: string }>>(() =>
    Object.fromEntries(proposals.map((p) => [p.root_directory, { on: !p.questions.includes("unsupported"), slug: p.slug }])),
  );
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const picked = proposals.filter((p) => chosen[p.root_directory]?.on);
  const first = s.apps.length === 0;

  async function create() {
    setBusy(true);
    setError(null);
    try {
      await unwrap(
        api.POST("/stacks/{id}/apps", {
          params: { path: { id: s.id } },
          body: {
            deploy: true,
            idempotency_key: idempotencyKey(),
            apps: picked.map((p) => ({ root_directory: p.root_directory, slug: (chosen[p.root_directory]?.slug ?? p.slug).trim(), role: p.role as "frontend" })),
          },
        }),
      );
      qc.invalidateQueries({ queryKey: ["stack"] });
      qc.invalidateQueries({ queryKey: ["stacks"] });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card
      title={first ? "O que o Cobogó achou no repositório" : "Outras aplicações no repositório"}
      icon="sparkle"
      actions={<Tag>{proposals.length} aplicação(ões)</Tag>}
    >
      <div className="stack">
        <p className="small muted card-text">
          {first
            ? "Cada aplicação vira uma aba deste projeto, com o próprio endereço, deploys, logs e variáveis. O banco, o Redis e os processos são criados no deploy da aplicação que os usa."
            : "Estas pastas também parecem aplicações. Marque as que devem entrar no projeto."}
        </p>
        {error && <Notice tone="danger">{error}</Notice>}
        {proposals.map((p) => {
          const c = chosen[p.root_directory] ?? { on: false, slug: p.slug };
          const url = apps.urlFor(c.slug);
          const unsupported = p.questions.includes("unsupported");
          const questions = p.questions.filter((q) => q !== "unsupported");
          return (
            <label key={p.root_directory} className={`option${c.on ? " selected" : ""}`} style={{ alignItems: "flex-start" }}>
              <input
                type="checkbox"
                checked={c.on}
                disabled={unsupported}
                onChange={(e) => setChosen({ ...chosen, [p.root_directory]: { ...c, on: e.target.checked } })}
                style={{ width: "auto", marginTop: 4 }}
              />
              <span className="stack-sm" style={{ flex: 1 }}>
                <span className="row">
                  <strong>{ROLE_LABEL[p.role] ?? "Aplicação"}</strong>
                  <span className="mono small">{p.root_directory || "./ (raiz)"}</span>
                  {p.label && <Tag tone="brand">{p.label}</Tag>}
                  {profileLabel(p.profile) && <Tag>{profileLabel(p.profile)}</Tag>}
                </span>
                {p.components.length > 0 && (
                  <span className="row small">
                    <span className="muted">Pede:</span>
                    {p.components.map((cp) => (
                      <Tag key={cp.role + cp.label} tone={cp.supported ? "success" : "warning"} title={cp.note || cp.evidence.join(", ")}>
                        {cp.label}
                      </Tag>
                    ))}
                  </span>
                )}
                {p.evidence.length > 0 && <span className="small faint mono">{p.evidence.join(" · ")}</span>}
                {unsupported && <span className="small text-danger">O Cobogó ainda não publica esta pasta.</span>}
                {questions.length > 0 && <span className="small muted">O deploy vai perguntar: {questions.join(", ")}.</span>}
                {p.warnings > 0 && <span className="small muted">{p.warnings} aviso(s) da análise, no deploy.</span>}
                <span className="row small">
                  <span className="muted">Endereço:</span>
                  <input
                    className="mono"
                    value={c.slug}
                    onChange={(e) => setChosen({ ...chosen, [p.root_directory]: { ...c, slug: e.target.value.toLowerCase() } })}
                    aria-label={`Endereço de ${p.root_directory || "raiz"}`}
                    style={{ maxWidth: 220 }}
                    spellCheck={false}
                  />
                  {url && <span className="mono faint">{url.replace(/^https?:\/\//, "")}</span>}
                </span>
              </span>
            </label>
          );
        })}
        <div className="form-actions">
          <span className="muted small">Publicar sobe o backend antes do frontend. Nada é criado antes deste clique.</span>
          <Button variant="primary" icon="rocket" onClick={create} busy={busy} disabled={picked.length === 0}>
            {first ? "Criar e publicar" : "Adicionar e publicar"}
          </Button>
        </div>
      </div>
    </Card>
  );
}
