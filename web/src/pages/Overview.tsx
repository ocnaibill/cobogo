// TEL-02 — Visão geral: o que está no ar e o que precisa de atenção. Os
// indicadores contam projetos pelo estado do último deploy, com o denominador
// explícito (§18.3: nada de "taxa de sucesso" sem janela).
import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, unwrap, type ProjectSummary } from "../api/client";
import { Button, Card, Empty, ErrorBox, Loading, StateBadge, When } from "../components/ui";
import { isActive } from "../lib/deploy";
import { repoName } from "../lib/format";

function bucket(p: ProjectSummary): "ok" | "attention" | "running" | "none" {
  const s = p.last_deployment?.state;
  if (!s) return "none";
  if (s === "SUCCEEDED") return "ok";
  if (s === "FAILED" || s === "NEEDS_INPUT" || s === "UNSUPPORTED") return "attention";
  if (isActive(s) || s === "QUEUED") return "running";
  return "none";
}

export function OverviewPage() {
  const q = useInfiniteQuery({
    queryKey: ["projects"],
    queryFn: ({ pageParam }) => unwrap(api.GET("/projects", { params: { query: { limit: 50, before: pageParam || undefined } } })),
    initialPageParam: "",
    getNextPageParam: (last) => last.next_before || undefined,
    refetchInterval: 5000,
  });

  const projects = q.data?.pages.flatMap((p) => p.projects) ?? [];
  const counts = { ok: 0, attention: 0, running: 0, none: 0 };
  projects.forEach((p) => counts[bucket(p)]++);

  return (
    <div className="page">
      <div className="page-head">
        <div>
          <h1>Visão geral</h1>
          <p className="muted">Projetos desta instalação e o resultado do último deploy de cada um.</p>
        </div>
        <Link to="/importar" className="btn btn-primary">
          Importar repositório
        </Link>
      </div>

      {q.isLoading && <Loading />}
      {q.isError && <ErrorBox error={q.error} onRetry={() => q.refetch()} />}

      {q.isSuccess && (
        <>
          <div className="stats" role="list" aria-label="Resumo">
            <Stat label="No ar" value={counts.ok} total={projects.length} tone="success" />
            <Stat label="Precisam de atenção" value={counts.attention} total={projects.length} tone="danger" hint="falhou, aguarda resposta ou não é suportado" />
            <Stat label="Em andamento" value={counts.running} total={projects.length} tone="info" />
            <Stat label="Sem deploy" value={counts.none} total={projects.length} tone="neutral" />
          </div>

          <Card title="Projetos">
            {projects.length === 0 ? (
              <Empty title="Nenhum projeto ainda.">
                Importe um repositório público para o Cobogó analisar e publicar. <Link to="/importar">Importar o primeiro</Link>.
              </Empty>
            ) : (
              <table className="table">
                <thead>
                  <tr>
                    <th scope="col">Projeto</th>
                    <th scope="col">Repositório</th>
                    <th scope="col">Último deploy</th>
                    <th scope="col">Quando</th>
                  </tr>
                </thead>
                <tbody>
                  {projects.map((p) => (
                    <tr key={p.id}>
                      <td>
                        <Link to={`/projetos/${p.id}`} className="strong">
                          {p.slug}
                        </Link>
                      </td>
                      <td className="mono muted">{repoName(p.repository_url)}{p.root_directory ? `/${p.root_directory}` : ""}</td>
                      <td>{p.last_deployment ? <Link to={`/deploys/${p.last_deployment.id}`}><StateBadge state={p.last_deployment.state} /></Link> : <span className="muted">—</span>}</td>
                      <td className="muted">{p.last_deployment ? <When iso={p.last_deployment.created_at} /> : "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
            {q.hasNextPage && (
              <div className="card-foot">
                <Button onClick={() => q.fetchNextPage()} busy={q.isFetchingNextPage}>
                  Carregar mais
                </Button>
              </div>
            )}
          </Card>
          <p className="muted small">Atualizado a cada 5 s. A saúde em tempo real de cada aplicação entra com o coletor (MVP).</p>
        </>
      )}
    </div>
  );
}

function Stat({ label, value, total, tone, hint }: { label: string; value: number; total: number; tone: string; hint?: string }) {
  return (
    <div className={`stat stat-${tone}`} role="listitem" title={hint}>
      <span className="stat-value">{value}</span>
      <span className="stat-label">{label}</span>
      <span className="stat-total muted">de {total}</span>
    </div>
  );
}
