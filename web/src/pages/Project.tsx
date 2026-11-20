// TEL-07 — Resumo do projeto: endereço, release ativa, histórico de deploys,
// variáveis (valores nunca voltam, RF-10) e limites de recursos (DEC-30).
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, idempotencyKey, unwrap, type ConfigVariable } from "../api/client";
import { Badge, Button, Card, Empty, ErrorBox, Field, Loading, Notice, StateBadge, When } from "../components/ui";
import { duration, repoName, shortSha } from "../lib/format";

type Tab = "deploys" | "variaveis" | "limites";

export function ProjectPage() {
  const { id = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const tab = (params.get("aba") as Tab) || "deploys";
  const navigate = useNavigate();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const project = useQuery({ queryKey: ["project", id], queryFn: () => unwrap(api.GET("/projects/{id}", { params: { path: { id } } })) });
  const deploys = useQuery({
    queryKey: ["deployments", id],
    queryFn: () => unwrap(api.GET("/projects/{id}/deployments", { params: { path: { id }, query: { limit: 50 } } })),
    refetchInterval: 5000,
  });
  const lastOk = deploys.data?.deployments.find((d) => d.state === "SUCCEEDED");
  const lastOkDetail = useQuery({
    queryKey: ["deployment", lastOk?.id],
    queryFn: () => unwrap(api.GET("/deployments/{id}", { params: { path: { id: lastOk!.id } } })),
    enabled: !!lastOk,
  });
  const latest = deploys.data?.deployments[0];

  if (project.isLoading) return <Loading />;
  if (project.isError) return <ErrorBox error={project.error} onRetry={() => project.refetch()} />;
  const p = project.data!;
  const release = lastOkDetail.data?.release;

  async function deploy() {
    setBusy(true);
    setError(null);
    try {
      const d = await unwrap(api.POST("/projects/{id}/deployments", { params: { path: { id } }, body: { idempotency_key: idempotencyKey() } }));
      navigate(`/deploys/${d.id}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="page">
      <nav className="crumbs" aria-label="Você está em">
        <Link to="/">Visão geral</Link> / <span>{p.slug}</span>
      </nav>
      <div className="page-head">
        <div>
          <h1>{p.slug}</h1>
          <p className="muted mono">
            {repoName(p.repository_url)}
            {p.root_directory ? ` · ${p.root_directory}` : ""}
          </p>
        </div>
        <div className="row">
          <Button onClick={deploy} busy={busy}>
            Novo deploy
          </Button>
          {release?.url && (
            <a className="btn btn-primary" href={release.url} target="_blank" rel="noreferrer noopener">
              Abrir aplicação ↗
            </a>
          )}
        </div>
      </div>
      {error && <Notice tone="danger">{error}</Notice>}

      <div className="grid-3">
        <Card title="Endereço">
          {release?.url ? (
            <a href={release.url} target="_blank" rel="noreferrer noopener" className="mono wrap">
              {release.url}
            </a>
          ) : lastOk ? (
            <span className="muted">Sem domínio configurado (porta {release?.host_port} no servidor).</span>
          ) : (
            <span className="muted">Ainda não publicado.</span>
          )}
        </Card>
        <Card title="Release ativa">
          {lastOk ? (
            <>
              <Link to={`/deploys/${lastOk.id}`} className="mono">
                {shortSha(lastOk.commit_sha)}
              </Link>{" "}
              · <When iso={lastOk.updated_at} />
            </>
          ) : (
            <span className="muted">Nenhuma.</span>
          )}
        </Card>
        <Card title="Último deploy">
          {latest ? (
            <Link to={`/deploys/${latest.id}`}>
              <StateBadge state={latest.state} />
            </Link>
          ) : (
            <span className="muted">Nenhum.</span>
          )}
          {latest && lastOk && latest.id !== lastOk.id && latest.state === "FAILED" && (
            <p className="small muted">A release anterior continua no ar: a falha foi só na candidata.</p>
          )}
        </Card>
      </div>

      <div className="tabs" role="tablist" aria-label="Seções do projeto">
        {(["deploys", "variaveis", "limites"] as Tab[]).map((t) => (
          <button key={t} role="tab" aria-selected={tab === t} className={tab === t ? "tab active" : "tab"} onClick={() => setParams(t === "deploys" ? {} : { aba: t }, { replace: true })}>
            {t === "deploys" ? "Deploys" : t === "variaveis" ? "Variáveis" : "Limites"}
          </button>
        ))}
      </div>

      {tab === "deploys" && (
        <Card>
          {deploys.isLoading && <Loading />}
          {deploys.isError && <ErrorBox error={deploys.error} />}
          {deploys.data && deploys.data.deployments.length === 0 && <Empty title="Nenhum deploy ainda." />}
          {deploys.data && deploys.data.deployments.length > 0 && (
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Deploy</th>
                  <th scope="col">Estado</th>
                  <th scope="col">Commit</th>
                  <th scope="col">Pedido</th>
                  <th scope="col">Duração</th>
                </tr>
              </thead>
              <tbody>
                {deploys.data.deployments.map((d) => (
                  <tr key={d.id}>
                    <td>
                      <Link to={`/deploys/${d.id}`} className="mono">
                        {d.id.slice(0, 8)}
                      </Link>
                    </td>
                    <td>
                      <StateBadge state={d.state} />
                    </td>
                    <td className="mono">{shortSha(d.commit_sha)}</td>
                    <td className="muted">
                      <When iso={d.created_at} />
                    </td>
                    <td className="muted">{duration(d.created_at, d.updated_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </Card>
      )}
      {tab === "variaveis" && <VariablesTab projectId={id} analysisDeploymentId={latest?.analysis_id ? latest.id : lastOk?.id} />}
      {tab === "limites" && <LimitsTab projectId={id} />}
    </div>
  );
}

function VariablesTab({ projectId, analysisDeploymentId }: { projectId: string; analysisDeploymentId?: string }) {
  const qc = useQueryClient();
  const vars = useQuery({ queryKey: ["env", projectId], queryFn: () => unwrap(api.GET("/projects/{id}/env", { params: { path: { id: projectId } } })) });
  const analysis = useQuery({
    queryKey: ["analysis", analysisDeploymentId],
    queryFn: () => unwrap(api.GET("/deployments/{id}/analysis", { params: { path: { id: analysisDeploymentId! } } })),
    enabled: !!analysisDeploymentId,
    retry: false,
  });
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [msg, setMsg] = useState<{ tone: "success" | "danger" | "warning"; text: string } | null>(null);
  const [busy, setBusy] = useState<string | null>(null);

  const stored = new Map((vars.data?.variables ?? []).map((v) => [v.name, v]));
  const discovered: ConfigVariable[] = (analysis.data?.result.config_variables ?? []).filter((v) => v.class !== "platform");
  const extraNames = [...stored.keys()].filter((n) => !discovered.some((d) => d.name === n));

  const refresh = () => qc.invalidateQueries({ queryKey: ["env", projectId] });

  async function save(evt: FormEvent) {
    evt.preventDefault();
    setBusy("save");
    setMsg(null);
    try {
      const r = await unwrap(api.PUT("/projects/{id}/env/{name}", { params: { path: { id: projectId, name: name.trim() } }, body: { value } }));
      setMsg({ tone: "success", text: r.redeploy_required ? `${r.name} atualizada (versão ${r.version}). Ela vale a partir do próximo deploy.` : `${r.name} salva. Ela vale a partir do próximo deploy.` });
      setName("");
      setValue("");
      refresh();
    } catch (e) {
      setMsg({ tone: "danger", text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(null);
    }
  }

  async function generate(n: string, replace = false) {
    setBusy(`gen-${n}`);
    setMsg(null);
    try {
      await unwrap(api.POST("/projects/{id}/env/{name}/generate", { params: { path: { id: projectId, name: n } }, body: replace ? { replace: true } : undefined }));
      setMsg({ tone: "success", text: `Valor aleatório gerado e guardado para ${n}. Ele nunca é mostrado.` });
      refresh();
    } catch (e) {
      if (e instanceof ApiError && e.code === "conflict" && window.confirm(`${n} já tem valor. Gerar outro invalida o que foi assinado com o anterior. Substituir?`)) {
        await generate(n, true);
        return;
      }
      setMsg({ tone: "danger", text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(null);
    }
  }

  async function remove(n: string) {
    if (!window.confirm(`Remover ${n}? O valor guardado é apagado.`)) return;
    setBusy(`del-${n}`);
    try {
      await unwrap(api.DELETE("/projects/{id}/env/{name}", { params: { path: { id: projectId, name: n } } }));
      refresh();
    } catch (e) {
      setMsg({ tone: "danger", text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="stack">
      <Notice tone="info">Os valores ficam cifrados e nunca voltam para o painel. Uma mudança vale a partir do próximo deploy.</Notice>
      {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
      <Card title="Variáveis do projeto">
        {vars.isLoading && <Loading />}
        {vars.isError && <ErrorBox error={vars.error} />}
        {(discovered.length > 0 || extraNames.length > 0) ? (
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Nome</th>
                <th scope="col">Situação</th>
                <th scope="col">Observação</th>
                <th scope="col" className="sr-only">Ações</th>
              </tr>
            </thead>
            <tbody>
              {discovered.map((d) => {
                const s = stored.get(d.name);
                return (
                  <tr key={d.name}>
                    <td className="mono">{d.name}</td>
                    <td>{s ? <Badge tone="success">definida (v{s.version}{s.source === "generated" ? ", gerada" : ""})</Badge> : d.hint?.code === "likely_required" ? <Badge tone="warning">falta</Badge> : <Badge tone="neutral">sem valor</Badge>}</td>
                    <td className="small muted">{d.hint?.message ?? (d.declared_in_example ? "declarada no exemplo" : "referenciada no código")}</td>
                    <td className="row-end">
                      <Button variant="ghost" onClick={() => setName(d.name)}>Definir</Button>
                      {d.class === "generable" && <Button variant="ghost" busy={busy === `gen-${d.name}`} onClick={() => generate(d.name)}>Gerar</Button>}
                      {s && <Button variant="ghost" busy={busy === `del-${d.name}`} onClick={() => remove(d.name)}>Remover</Button>}
                    </td>
                  </tr>
                );
              })}
              {extraNames.map((n) => (
                <tr key={n}>
                  <td className="mono">{n}</td>
                  <td><Badge tone="success">definida (v{stored.get(n)!.version})</Badge></td>
                  <td className="small muted">não encontrada na última análise</td>
                  <td className="row-end">
                    <Button variant="ghost" onClick={() => setName(n)}>Trocar</Button>
                    <Button variant="ghost" busy={busy === `del-${n}`} onClick={() => remove(n)}>Remover</Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          !vars.isLoading && <Empty title="Nenhuma variável encontrada nem definida.">A análise lista as variáveis que o código usa; você também pode definir uma pelo nome.</Empty>
        )}
      </Card>
      <Card title="Definir uma variável">
        <form className="form-inline" onSubmit={save}>
          <Field label="Nome" htmlFor="env-name">
            <input id="env-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="DATABASE_URL" spellCheck={false} autoComplete="off" />
          </Field>
          <Field label="Valor" htmlFor="env-value" hint="Não aparece de novo depois de salvo.">
            <input id="env-value" type="password" value={value} onChange={(e) => setValue(e.target.value)} autoComplete="off" />
          </Field>
          <Button type="submit" variant="primary" busy={busy === "save"} disabled={!name.trim() || !value}>
            Salvar
          </Button>
        </form>
      </Card>
    </div>
  );
}

const LIMIT_FIELDS: { key: "app_memory_mb" | "app_cpus" | "app_pids" | "build_memory_mb" | "build_cpus"; label: string; unit: string }[] = [
  { key: "app_memory_mb", label: "Memória da aplicação", unit: "MiB" },
  { key: "app_cpus", label: "CPUs da aplicação", unit: "CPUs" },
  { key: "app_pids", label: "Processos da aplicação", unit: "processos" },
  { key: "build_memory_mb", label: "Memória do build", unit: "MiB" },
  { key: "build_cpus", label: "CPUs do build", unit: "CPUs" },
];

const SOURCE_LABEL: Record<string, string> = { project: "deste projeto", database: "configuração global", env: "ambiente da instalação", default: "padrão" };

function LimitsTab({ projectId }: { projectId: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["limits", projectId], queryFn: () => unwrap(api.GET("/projects/{id}/limits", { params: { path: { id: projectId } } })) });
  const [draft, setDraft] = useState<Record<string, string> | null>(null);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  if (q.isLoading) return <Loading />;
  if (q.isError) return <ErrorBox error={q.error} />;
  const view = q.data!;
  const values = draft ?? Object.fromEntries(LIMIT_FIELDS.map((f) => [f.key, view.overrides[f.key] ? String(view.overrides[f.key]) : ""]));

  async function save(evt: FormEvent) {
    evt.preventDefault();
    setBusy(true);
    setMsg(null);
    try {
      const body = Object.fromEntries(Object.entries(values).filter(([, v]) => v.trim() !== "").map(([k, v]) => [k, Number(v)]));
      await unwrap(api.PUT("/projects/{id}/limits", { params: { path: { id: projectId } }, body }));
      setDraft(null);
      setMsg({ tone: "success", text: "Limites salvos. Valem a partir do próximo deploy." });
      qc.invalidateQueries({ queryKey: ["limits", projectId] });
    } catch (e) {
      setMsg({ tone: "danger", text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Limites de recursos">
      <p className="muted small">Campo vazio herda da configuração global. Passar do limite de memória encerra a aplicação, e a falha diz isso.</p>
      <form className="stack" onSubmit={save}>
        <table className="table">
          <thead>
            <tr>
              <th scope="col">Recurso</th>
              <th scope="col">Valor deste projeto</th>
              <th scope="col">Vale hoje</th>
            </tr>
          </thead>
          <tbody>
            {LIMIT_FIELDS.map((f) => {
              const eff = view.effective[f.key];
              return (
                <tr key={f.key}>
                  <td>
                    <label htmlFor={`lim-${f.key}`}>{f.label}</label>
                  </td>
                  <td>
                    <input id={`lim-${f.key}`} className="input-sm" inputMode="decimal" value={values[f.key] ?? ""} placeholder="herdar" onChange={(e) => setDraft({ ...values, [f.key]: e.target.value })} />
                  </td>
                  <td className="small">
                    {eff ? (
                      <>
                        {eff.value} {f.unit} <span className="muted">({SOURCE_LABEL[eff.source] ?? eff.source})</span>
                      </>
                    ) : (
                      "—"
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
        <div className="row-end">
          <Button type="submit" variant="primary" busy={busy} disabled={!draft}>
            Salvar limites
          </Button>
        </div>
      </form>
    </Card>
  );
}
