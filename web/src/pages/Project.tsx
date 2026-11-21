// TEL-07 — Resumo do projeto: endereço, release ativa, histórico de deploys,
// variáveis (valores nunca voltam, RF-10) e limites de recursos (DEC-30).
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, idempotencyKey, unwrap, type ConfigVariable } from "../api/client";
import { Badge, Button, Card, CopyButton, Empty, ErrorBox, Field, LinkButton, Loading, Notice, PageHead, StateBadge, Tag, When } from "../components/ui";
import { Icon, type IconName } from "../components/icons";
import { duration, repoName, shortSha } from "../lib/format";
import { AppButtons, ApplyLimits, DangerZone, OperationsTab, useAppControl } from "../components/Lifecycle";
import { profileLabel } from "../lib/deploy";
import { EnvHistory, ImportEnv, Suggestions } from "../components/Secrets";
import { missingLikely, withRequired } from "../lib/secrets";

type Tab = "deploys" | "variaveis" | "limites" | "operacoes";

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
  const analysisDeploymentId = latest?.analysis_id ? latest.id : lastOk?.id;
  const analysis = useQuery({
    queryKey: ["analysis", analysisDeploymentId],
    queryFn: () => unwrap(api.GET("/deployments/{id}/analysis", { params: { path: { id: analysisDeploymentId! } } })),
    enabled: !!analysisDeploymentId,
    retry: false,
  });
  const appControl = useAppControl(id);
  const limits = useQuery({ queryKey: ["limits", id], queryFn: () => unwrap(api.GET("/projects/{id}/limits", { params: { path: { id } } })) });

  if (project.isLoading) return <Loading />;
  if (project.isError) return <ErrorBox error={project.error} onRetry={() => project.refetch()} />;
  const p = project.data!;
  const release = lastOkDetail.data?.release;
  const profile = profileLabel(analysis.data?.result.fields.profile?.value);
  const port = analysis.data?.result.fields.port?.value;

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

  const candidateFailed = latest && lastOk && latest.id !== lastOk.id && latest.state === "FAILED";
  const e = limits.data?.effective;

  return (
    <div className="page">
      <PageHead
        crumbs={
          <>
            <Link to="/">Projetos</Link> / <span>{repoName(p.repository_url)}</span> / <strong>resumo</strong>
          </>
        }
        title={
          <span className="row">
            {p.slug}
            {profile && <Tag tone="brand">{profile}</Tag>}
          </span>
        }
        actions={
          <>
            <AppButtons state={p.app_state} busy={appControl.busy} run={appControl.run} />
            <Button variant={p.app_state === "stopped" ? "secondary" : "primary"} icon="rocket" onClick={deploy} busy={busy}>
              Novo deploy
            </Button>
            {latest && (
              <LinkButton to={`/deploys/${latest.id}`} icon="terminal">
                Último log
              </LinkButton>
            )}
            <Button icon="key" onClick={() => setParams({ aba: "variaveis" }, { replace: true })}>
              Variáveis
            </Button>
          </>
        }
      >
        {p.app_state === "stopped" ? (
          <Badge tone="warning">Parado</Badge>
        ) : lastOk ? (
          <Badge tone="success">No ar</Badge>
        ) : latest ? (
          <StateBadge state={latest.state} />
        ) : (
          <Badge tone="neutral">Sem deploy</Badge>
        )}
        <span className="meta row">
          <Icon name="branch" size={14} /> {repoName(p.repository_url)}
          {p.root_directory ? ` · ${p.root_directory}` : ""}
        </span>
        {lastOk && (
          <span className="meta row">
            <Icon name="commit" size={14} /> {shortSha(lastOk.commit_sha)}
          </span>
        )}
      </PageHead>
      {error && <Notice tone="danger">{error}</Notice>}
      {appControl.notice && <Notice tone={appControl.notice.tone}>{appControl.notice.text}</Notice>}
      {p.app_state === "stopped" && !appControl.busy && (
        <Notice tone="warning" title="A aplicação está parada.">
          <span>O endereço público mostra a página de projeto inativo. Parar não é uma falha: o contêiner foi mantido, e Iniciar o traz de volta. Um novo deploy também o coloca no ar.</span>
        </Notice>
      )}
      {candidateFailed && (
        <Notice tone="warning" title="O último deploy falhou, mas a versão anterior continua no ar.">
          A falha foi só na candidata: o tráfego só troca depois que uma versão passa na verificação. <Link to={`/deploys/${latest!.id}`}>Ver a falha</Link>
        </Notice>
      )}

      <div className="grid-2">
        <Card title="Roteamento" icon="route" actions={release?.url?.startsWith("https:") ? <Tag tone="brand">HTTPS</Tag> : undefined}>
          <div className="stack">
            <div className="inset stack-sm">
              <span className="label">Endereço público</span>
              {release?.url ? (
                <div className="row-between">
                  <a href={release.url} target="_blank" rel="noreferrer noopener" className="mono wrap strong">
                    {release.url}
                  </a>
                  <span className="row">
                    <CopyButton text={release.url} />
                    <LinkButton href={release.url} variant="primary" iconEnd="external">
                      Abrir
                    </LinkButton>
                  </span>
                </div>
              ) : lastOk && lastOkDetail.isLoading ? (
                <span className="muted">…</span>
              ) : lastOk ? (
                <span className="muted">Sem domínio configurado: só pela porta {release?.host_port} no servidor.</span>
              ) : (
                <span className="muted">Ainda não publicado. O endereço aparece no primeiro deploy concluído.</span>
              )}
            </div>
            <dl className="kv small">
              <div>
                <dt>Repositório</dt>
                <dd className="mono">
                  <a href={p.repository_url} target="_blank" rel="noreferrer noopener">
                    {repoName(p.repository_url)}
                  </a>
                </dd>
              </div>
              <div>
                <dt>Diretório</dt>
                <dd className="mono">{p.root_directory || "./ (raiz)"}</dd>
              </div>
              <div>
                <dt>Porta da aplicação</dt>
                <dd className="mono">{port ?? "—"}</dd>
              </div>
              {release && (
                <div>
                  <dt>Acesso direto</dt>
                  <dd className="mono">127.0.0.1:{release.host_port} (no servidor)</dd>
                </div>
              )}
            </dl>
          </div>
        </Card>

        <Card title="Release ativa" icon="layers" actions={lastOk ? p.app_state === "stopped" ? <Badge tone="warning">parada</Badge> : <Badge tone="success">no ar</Badge> : undefined}>
          {lastOk ? (
            <div className="stack">
              <div className="inset row-between">
                <div className="stack-sm">
                  <strong className="mono">{shortSha(lastOk.commit_sha)}</strong>
                  <span className="meta">
                    publicada <When iso={lastOk.updated_at} /> · <Link to={`/deploys/${lastOk.id}`}>deploy {lastOk.id.slice(0, 8)}</Link>
                  </span>
                </div>
                {release && <Tag tone={release.health === "healthy" ? "success" : "neutral"}>{release.operational_state}</Tag>}
              </div>
              <div className="tiles">
                <div className="tile">
                  <span className="label">Memória</span>
                  <strong>{e?.app_memory_mb ? `${e.app_memory_mb.value} MiB` : "—"}</strong>
                </div>
                <div className="tile">
                  <span className="label">CPU</span>
                  <strong>{e?.app_cpus?.value ?? "—"}</strong>
                </div>
                <div className="tile">
                  <span className="label">Processos</span>
                  <strong>{e?.app_pids?.value ?? "—"}</strong>
                </div>
              </div>
              <p className="small muted">Limites que valem no próximo deploy. Métricas de uso em tempo real entram com o coletor (MVP).</p>
            </div>
          ) : (
            <Empty title="Nenhuma release no ar." icon="layers">
              Peça um deploy para publicar a primeira versão.
            </Empty>
          )}
        </Card>
      </div>

      <div className="tabs" role="tablist" aria-label="Seções do projeto">
        {(
          [
            ["deploys", "Histórico de deploys", "history"],
            ["variaveis", "Variáveis e segredos", "key"],
            ["limites", "Limites", "sliders"],
            ["operacoes", "Operações", "activity"],
          ] as [Tab, string, IconName][]
        ).map(([t, label, icon]) => (
          <button key={t} role="tab" aria-selected={tab === t} className={tab === t ? "tab active" : "tab"} onClick={() => setParams(t === "deploys" ? {} : { aba: t }, { replace: true })}>
            <Icon name={icon} size={16} />
            {label}
          </button>
        ))}
      </div>

      {tab === "deploys" && (
        <section className="card card-flush" aria-label="Histórico de deploys">
          {deploys.isLoading && <Loading />}
          {deploys.isError && <ErrorBox error={deploys.error} />}
          {deploys.data && deploys.data.deployments.length === 0 && <Empty title="Nenhum deploy ainda." icon="history" />}
          {deploys.data && deploys.data.deployments.length > 0 && (
            <>
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">Deploy</th>
                      <th scope="col">Commit</th>
                      <th scope="col">Estado</th>
                      <th scope="col">Pedido</th>
                      <th scope="col">Duração</th>
                    </tr>
                  </thead>
                  <tbody>
                    {deploys.data.deployments.map((d) => (
                      <tr key={d.id} className={d.id === lastOk?.id ? "row-active" : undefined}>
                        <td>
                          <Link to={`/deploys/${d.id}`} className="mono strong">
                            {d.id.slice(0, 8)}
                          </Link>
                        </td>
                        <td>
                          <Tag>{shortSha(d.commit_sha)}</Tag>
                        </td>
                        <td>
                          <span className="row">
                            <StateBadge state={d.state} />
                            {d.id === lastOk?.id && <Tag tone="success">ativa · 100% do tráfego</Tag>}
                          </span>
                        </td>
                        <td className="meta">
                          <When iso={d.created_at} />
                        </td>
                        <td className="meta">{duration(d.created_at, d.updated_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <div className="table-foot">
                <span>{deploys.data.deployments.length} deploy(s) mais recentes</span>
                <span>uma versão no ar por projeto; a troca é de uma vez</span>
              </div>
            </>
          )}
        </section>
      )}
      {tab === "variaveis" && <VariablesTab projectId={id} discovered={(analysis.data?.result.config_variables ?? []).filter((v) => v.class !== "platform")} />}
      {tab === "limites" && <LimitsTab projectId={id} />}
      {tab === "operacoes" && <OperationsTab projectId={id} />}
      <DangerZone projectId={id} slug={p.slug} />
    </div>
  );
}

function VariablesTab({ projectId, discovered }: { projectId: string; discovered: ConfigVariable[] }) {
  const qc = useQueryClient();
  const vars = useQuery({ queryKey: ["env", projectId], queryFn: () => unwrap(api.GET("/projects/{id}/env", { params: { path: { id: projectId } } })) });
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [msg, setMsg] = useState<{ tone: "success" | "danger" | "warning"; text: string } | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const required = useQuery({ queryKey: ["required-env", projectId], queryFn: () => unwrap(api.GET("/projects/{id}/required-env", { params: { path: { id: projectId } } })) });
  const requiredNames = required.data?.names ?? [];
  const hasPublic = discovered.some((d) => d.class === "public");

  const stored = new Map((vars.data?.variables ?? []).map((v) => [v.name, v]));
  const extraNames = [...stored.keys()].filter((n) => !discovered.some((d) => d.name === n));
  // Exigidas que o código não cita e ainda não têm valor: sem uma linha, ficariam invisíveis.
  const requiredOnly = requiredNames.filter((n) => !stored.has(n) && !discovered.some((d) => d.name === n));
  const missing = discovered.filter((d) => !stored.has(d.name) && d.hint?.code === "likely_required").length;

  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["env", projectId] });
    qc.invalidateQueries({ queryKey: ["env-history", projectId] });
    qc.invalidateQueries({ queryKey: ["env-suggestions", projectId] });
  };

  async function setRequired(names: string[]) {
    setBusy("required");
    setMsg(null);
    try {
      const r = await unwrap(api.PUT("/projects/{id}/required-env", { params: { path: { id: projectId } }, body: { names } }));
      qc.setQueryData(["required-env", projectId], r);
      qc.invalidateQueries({ queryKey: ["env-history", projectId] });
    } catch (e) {
      setMsg({ tone: "danger", text: e instanceof Error ? e.message : String(e) });
    } finally {
      setBusy(null);
    }
  }

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
      <Notice tone="info" icon="shield" title="Valores nunca voltam ao painel">
        Ficam cifrados no banco e só são entregues ao contêiner. A API do painel não consegue nem ler o texto cifrado. Uma mudança vale a partir do próximo deploy.
      </Notice>
      {hasPublic && (
        <Notice tone="warning" title="Variáveis públicas pedem um novo build">
          <span>
            Variáveis como <code>VITE_*</code> e <code>NEXT_PUBLIC_*</code> entram no JavaScript entregue ao navegador na hora do build, e qualquer visitante pode lê-las. Mudar uma exige um novo deploy.
          </span>
        </Notice>
      )}
      {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
      <Suggestions projectId={projectId} />
      <section className="card card-flush" aria-label="Variáveis do projeto">
        <header className="card-head">
          <h2 className="card-title">
            <Icon name="key" size={20} className="card-icon" /> Variáveis de ambiente
          </h2>
          <span className="row">
            <Tag>{discovered.length + extraNames.length + requiredOnly.length} variável(is)</Tag>
            {missing > 0 && <Tag tone="danger">{missing} faltando</Tag>}
            {requiredNames.length > 0 && <Tag tone="warning">{requiredNames.length} exigida(s)</Tag>}
            {missingLikely(requiredNames, required.data?.likely ?? []).length > 0 && (
              <Button icon="check" busy={busy === "required"} onClick={() => setRequired([...new Set([...requiredNames, ...(required.data?.likely ?? [])])].sort())}>
                Marcar as prováveis obrigatórias
              </Button>
            )}
          </span>
        </header>
        {vars.isLoading && <Loading />}
        {vars.isError && <ErrorBox error={vars.error} />}
        {discovered.length > 0 || extraNames.length > 0 || requiredOnly.length > 0 ? (
          <div className="table-wrap">
            <table className="table">
              <thead>
                <tr>
                  <th scope="col">Chave</th>
                  <th scope="col">Situação</th>
                  <th scope="col">Valor</th>
                  <th scope="col">Origem</th>
                  <th scope="col" title="Sem valor, o deploy falha antes do build">
                    Exigir
                  </th>
                  <th scope="col" className="num">
                    Ações
                  </th>
                </tr>
              </thead>
              <tbody>
                {discovered.map((d) => {
                  const s = stored.get(d.name);
                  return (
                    <tr key={d.name}>
                      <td>
                        <span className="row mono strong">
                          <Icon name="lock" size={14} className={s ? "text-success" : "faint"} />
                          {d.name}
                        </span>
                      </td>
                      <td>
                        {s ? (
                          <Badge tone="success">definida · v{s.version}{s.source === "generated" ? " · gerada" : s.source === "imported" ? " · importada" : ""}</Badge>
                        ) : d.hint?.code === "likely_required" ? (
                          <Badge tone="danger">falta</Badge>
                        ) : (
                          <Badge tone="neutral">sem valor</Badge>
                        )}
                      </td>
                      <td className="mono faint">{s ? "••••••••••••" : "—"}</td>
                      <td className="small muted">
                        {d.class === "public" && <Tag tone="info">pública: vai para o JavaScript do navegador</Tag>}{" "}
                        {d.hint?.message ?? (d.declared_in_example ? "declarada no exemplo" : "referenciada no código")}
                      </td>
                      <td>
                        <RequiredBox name={d.name} names={requiredNames} busy={busy === "required"} onChange={setRequired} />
                      </td>
                      <td>
                        <div className="row-end">
                          <Button variant="ghost" onClick={() => setName(d.name)}>
                            Definir
                          </Button>
                          {d.class === "generable" && (
                            <Button variant="ghost" icon="sparkle" busy={busy === `gen-${d.name}`} onClick={() => generate(d.name)}>
                              Gerar
                            </Button>
                          )}
                          {s && (
                            <Button variant="ghost" busy={busy === `del-${d.name}`} onClick={() => remove(d.name)}>
                              Remover
                            </Button>
                          )}
                        </div>
                      </td>
                    </tr>
                  );
                })}
                {requiredOnly.map((n) => (
                  <tr key={n}>
                    <td>
                      <span className="row mono strong">
                        <Icon name="lock" size={14} className="faint" />
                        {n}
                      </span>
                    </td>
                    <td>
                      <Badge tone="danger">falta</Badge>
                    </td>
                    <td className="mono faint">—</td>
                    <td className="small muted">exigida por você; o código não a cita</td>
                    <td>
                      <RequiredBox name={n} names={requiredNames} busy={busy === "required"} onChange={setRequired} />
                    </td>
                    <td>
                      <div className="row-end">
                        <Button variant="ghost" onClick={() => setName(n)}>
                          Definir
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
                {extraNames.map((n) => (
                  <tr key={n}>
                    <td>
                      <span className="row mono strong">
                        <Icon name="lock" size={14} className="text-success" />
                        {n}
                      </span>
                    </td>
                    <td>
                      <Badge tone="success">definida · v{stored.get(n)!.version}</Badge>
                    </td>
                    <td className="mono faint">••••••••••••</td>
                    <td className="small muted">não encontrada na última análise</td>
                    <td>
                      <RequiredBox name={n} names={requiredNames} busy={busy === "required"} onChange={setRequired} />
                    </td>
                    <td>
                      <div className="row-end">
                        <Button variant="ghost" onClick={() => setName(n)}>
                          Trocar
                        </Button>
                        <Button variant="ghost" busy={busy === `del-${n}`} onClick={() => remove(n)}>
                          Remover
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          !vars.isLoading && (
            <Empty title="Nenhuma variável encontrada nem definida." icon="key">
              A análise lista as variáveis que o código usa; você também pode definir uma pelo nome.
            </Empty>
          )
        )}
      </section>
      <Card title="Definir uma variável" icon="plus">
        <form className="form-inline" onSubmit={save}>
          <Field label="Nome" htmlFor="env-name">
            <input id="env-name" className="mono" value={name} onChange={(e) => setName(e.target.value)} placeholder="DATABASE_URL" spellCheck={false} autoComplete="off" />
          </Field>
          <Field label="Valor" htmlFor="env-value" hint="Não aparece de novo depois de salvo.">
            <input id="env-value" type="password" value={value} onChange={(e) => setValue(e.target.value)} autoComplete="off" />
          </Field>
          <Button type="submit" variant="primary" busy={busy === "save"} disabled={!name.trim() || !value}>
            Salvar
          </Button>
        </form>
      </Card>
      <ImportEnv projectId={projectId} />
      <EnvHistory projectId={projectId} />
    </div>
  );
}

function RequiredBox({ name, names, busy, onChange }: { name: string; names: string[]; busy: boolean; onChange: (names: string[]) => void }) {
  const on = names.includes(name);
  return (
    <label className="check">
      <input type="checkbox" checked={on} disabled={busy} onChange={(e) => onChange(withRequired(names, name, e.target.checked))} aria-label={`Exigir ${name} para publicar`} />
    </label>
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
      const body = Object.fromEntries(
        Object.entries(values)
          .filter(([, v]) => v.trim() !== "")
          .map(([k, v]) => [k, Number(v)]),
      );
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
    <section className="card card-flush" aria-label="Limites de recursos">
      <header className="card-head">
        <h2 className="card-title">
          <Icon name="cpu" size={20} className="card-icon" /> Limites de recursos
        </h2>
        <span className="meta">vazio herda da configuração global</span>
      </header>
      <form onSubmit={save}>
        <div className="table-wrap">
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
                      <label htmlFor={`lim-${f.key}`} className="strong">
                        {f.label}
                      </label>
                    </td>
                    <td>
                      <input id={`lim-${f.key}`} className="input-sm mono" inputMode="decimal" value={values[f.key] ?? ""} placeholder="herdar" onChange={(e) => setDraft({ ...values, [f.key]: e.target.value })} />
                    </td>
                    <td className="small">
                      {eff ? (
                        <span className="row">
                          <strong className="mono">
                            {eff.value} {f.unit}
                          </strong>
                          <Tag tone={eff.source === "project" ? "brand" : "neutral"}>{SOURCE_LABEL[eff.source] ?? eff.source}</Tag>
                        </span>
                      ) : (
                        "—"
                      )}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
        <div className="table-foot">
          <span>Passar do limite de memória encerra a aplicação, e a falha diz isso.</span>
          <Button type="submit" variant="primary" busy={busy} disabled={!draft}>
            Salvar limites
          </Button>
        </div>
        {msg && (
          <div className="card-foot">
            <Notice tone={msg.tone}>{msg.text}</Notice>
          </div>
        )}
      </form>
      <div className="card-foot">
        <div className="full">
          <ApplyLimits projectId={projectId} onApplied={() => qc.invalidateQueries({ queryKey: ["operations", projectId] })} />
        </div>
      </div>
    </section>
  );
}
