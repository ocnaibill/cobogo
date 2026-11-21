// TEL-02 — Visão geral: o que está no ar e o que precisa de atenção. Os
// indicadores contam projetos pelo estado do último deploy, com o denominador
// explícito (§18.3: nada de "taxa de sucesso" sem janela). A saúde em tempo
// real de cada aplicação entra com o coletor (MVP); aqui ela não é inventada.
import { useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Link, useLocation } from "react-router";
import { api, unwrap, type ProjectSummary } from "../api/client";
import { Badge, Button, Empty, ErrorBox, LinkButton, Loading, Metric, Notice, StateBadge, Tag, When } from "../components/ui";
import { Icon } from "../components/icons";
import { isActive } from "../lib/deploy";
import { repoName } from "../lib/format";
import { useAppsDomain } from "../hooks/useInstance";

type Bucket = "ok" | "attention" | "running" | "none";

function bucket(p: ProjectSummary): Bucket {
  const s = p.last_deployment?.state;
  if (!s) return "none";
  if (s === "SUCCEEDED") return "ok";
  if (s === "FAILED" || s === "NEEDS_INPUT" || s === "UNSUPPORTED") return "attention";
  if (isActive(s) || s === "QUEUED") return "running";
  return "none";
}

const FILTERS: { key: Bucket | "all"; label: string; dot?: string }[] = [
  { key: "all", label: "Todos" },
  { key: "ok", label: "Publicados", dot: "success" },
  { key: "attention", label: "Atenção", dot: "danger" },
  { key: "running", label: "Em andamento", dot: "info" },
  { key: "none", label: "Sem deploy", dot: "neutral" },
];

export function OverviewPage() {
  const deleted = (useLocation().state as { deleted?: string } | null)?.deleted;
  const [filter, setFilter] = useState<Bucket | "all">("all");
  const [query, setQuery] = useState("");
  const apps = useAppsDomain();
  const q = useInfiniteQuery({
    queryKey: ["projects"],
    queryFn: ({ pageParam }) => unwrap(api.GET("/projects", { params: { query: { limit: 50, before: pageParam || undefined } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_before || undefined,
    refetchInterval: 5000,
  });

  const projects = useMemo(() => q.data?.pages.flatMap((p) => p.projects) ?? [], [q.data]);
  const counts: Record<Bucket, number> = { ok: 0, attention: 0, running: 0, none: 0 };
  projects.forEach((p) => counts[bucket(p)]++);
  const total = projects.length;
  const needle = query.trim().toLowerCase();
  const visible = projects.filter(
    (p) => (filter === "all" || bucket(p) === filter) && (!needle || `${p.slug} ${p.repository_url} ${p.root_directory}`.toLowerCase().includes(needle)),
  );
  const checked = q.dataUpdatedAt ? new Date(q.dataUpdatedAt).toLocaleTimeString("pt-BR") : "—";

  return (
    <div className="page">
      <div className="banner">
        <div className="banner-main">
          <span className="banner-icon" aria-hidden="true">
            <Icon name="terminal" size={22} />
          </span>
          <div>
            <div className="banner-title">
              Painel do Cobogó
              <Tag>{window.location.host}</Tag>
              {apps.domain ? (
                <Tag tone="brand">aplicações em *.{apps.domain}</Tag>
              ) : (
                apps.loaded && <Tag tone="warning">sem domínio para as aplicações</Tag>
              )}
            </div>
            <p className="small">Os indicadores contam o último deploy de cada projeto e se atualizam a cada 5 s. A saúde em tempo real entra com o coletor (MVP).</p>
          </div>
        </div>
        <div className="row">
          <span className="meta row">
            <Icon name="clock" size={14} /> Última checagem: {checked}
          </span>
          <Button icon="refresh" onClick={() => q.refetch()} busy={q.isRefetching && !q.isFetchingNextPage}>
            Sincronizar
          </Button>
        </div>
      </div>

      {deleted && (
        <Notice tone="success" title={`${deleted} foi excluído.`}>
          O endereço saiu do ar e a limpeza segue em segundo plano. O histórico dos deploys foi preservado.
        </Notice>
      )}
      {q.isLoading && <Loading />}
      {q.isError && <ErrorBox error={q.error} onRetry={() => q.refetch()} />}

      {q.isSuccess && (
        <>
          <div className="metrics" role="list" aria-label="Resumo">
            <Metric label="Publicados" value={counts.ok} denominator={`/ ${total} projetos`} tone="success" icon="checkCircle" foot={<span>o último deploy terminou no ar</span>} />
            <Metric
              label="Precisam de atenção"
              value={counts.attention}
              denominator={`/ ${total}`}
              tone="danger"
              icon="alert"
              foot={<span>último deploy falhou, aguarda resposta ou não é suportado</span>}
            />
            <Metric label="Em andamento" value={counts.running} denominator={`/ ${total}`} tone="info" icon="activity" foot={<span>na fila ou no worker agora</span>} />
            <Metric label="Sem deploy" value={counts.none} denominator={`/ ${total}`} tone="neutral" icon="box" foot={<span>importados, ainda não publicados</span>} />
          </div>

          <div className="banner banner-white">
            <div className="banner-main">
              <span className="banner-icon" aria-hidden="true">
                <Icon name="sparkle" size={24} />
              </span>
              <div>
                <h2>Do repositório ao ar, com decisões explicáveis</h2>
                <p>
                  O Cobogó lê os manifestos (<code>package.json</code>, lockfiles, <code>.env.example</code>) sem executar nada, propõe um plano e pergunta quando falta informação.
                </p>
              </div>
            </div>
            <LinkButton to="/importar" variant="primary" icon="download">
              Importar repositório
            </LinkButton>
          </div>

          <div className="toolbar">
            <div className="chips" role="group" aria-label="Filtrar por situação">
              {FILTERS.map((f) => (
                <button key={f.key} className="chip" aria-pressed={filter === f.key} onClick={() => setFilter(f.key)}>
                  {f.dot ? <span className={`dot dot-${f.dot}`} aria-hidden="true" /> : <Icon name="grid" size={14} />}
                  {f.label} ({f.key === "all" ? total : counts[f.key]})
                </button>
              ))}
            </div>
            <label className="search">
              <Icon name="search" size={16} />
              <span className="sr-only">Filtrar projetos</span>
              <input type="search" placeholder="Filtrar por nome ou repositório…" value={query} onChange={(e) => setQuery(e.target.value)} />
            </label>
          </div>

          <section className="card card-flush" aria-label="Projetos">
            {total === 0 ? (
              <Empty title="Nenhum projeto ainda.">
                Importe um repositório público para o Cobogó analisar e publicar. <Link to="/importar">Importar o primeiro</Link>.
              </Empty>
            ) : visible.length === 0 ? (
              <Empty title="Nenhum projeto neste filtro." icon="search" />
            ) : (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">Projeto e repositório</th>
                      <th scope="col">Endereço</th>
                      <th scope="col">Último deploy</th>
                      <th scope="col" className="num">
                        Ações
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {visible.map((p) => (
                      <ProjectRow key={p.id} p={p} url={apps.urlFor(p.slug)} />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {q.hasNextPage && (
              <div className="card-foot">
                <Button onClick={() => q.fetchNextPage()} busy={q.isFetchingNextPage}>
                  Carregar mais
                </Button>
              </div>
            )}
            <div className="table-foot">
              <span>
                Total: <strong>{total} projeto(s)</strong> · <span className="text-success">{counts.ok} publicado(s)</span> · {counts.attention} com atenção · {counts.running} em andamento
              </span>
              <span className="row">
                <Icon name="shield" size={14} /> Cada aplicação roda isolada, com limites de memória, CPU e processos
              </span>
            </div>
          </section>
        </>
      )}
    </div>
  );
}

function ProjectRow({ p, url }: { p: ProjectSummary; url: string | null }) {
  const d = p.last_deployment;
  const live = d?.state === "SUCCEEDED";
  return (
    <tr>
      <td>
        <div className="cell-main">
          <Icon name="box" size={20} />
          <div className="cell-title">
            <Link to={`/projetos/${p.id}`}>{p.slug}</Link>
            <span className="cell-sub" title={p.repository_url}>
              {repoName(p.repository_url)}
              {p.root_directory ? ` · ${p.root_directory}` : ""}
            </span>
          </div>
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
          <span className="faint">—</span>
        )}
      </td>
      <td>
        {d ? (
          <div className="stack-sm">
            <Link to={`/deploys/${d.id}`}>
              <StateBadge state={d.state} />
            </Link>
            <span className="meta">
              <When iso={d.created_at} />
            </span>
          </div>
        ) : (
          <Badge tone="neutral">sem deploy</Badge>
        )}
      </td>
      <td className="num">
        <div className="row-end">
          {d && (
            <Link className="icon-btn" to={`/deploys/${d.id}`} title="Ver o log do último deploy" aria-label={`Log do último deploy de ${p.slug}`}>
              <Icon name="terminal" />
            </Link>
          )}
          <Link className="icon-btn" to={`/projetos/${p.id}`} title="Abrir o projeto" aria-label={`Abrir ${p.slug}`}>
            <Icon name="arrowRight" />
          </Link>
        </div>
      </td>
    </tr>
  );
}
