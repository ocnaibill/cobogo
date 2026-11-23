// TEL-02 — Visão geral: o que está no ar e o que precisa de atenção. Os
// indicadores contam projetos pelo estado do último deploy, com o denominador
// explícito (§18.3: nada de "taxa de sucesso" sem janela). A saúde em tempo
// real de cada aplicação entra com o coletor (MVP); aqui ela não é inventada.
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link, useLocation } from "react-router";
import { api, unwrap, type ProjectSummary, type Stack } from "../api/client";
import { Badge, Button, Empty, ErrorBox, LinkButton, Loading, Metric, Notice, StateBadge, Tag, When } from "../components/ui";
import { Icon } from "../components/icons";
import { OrphanVolumes } from "../components/Data";
import { FleetResources } from "../components/Resources";
import { isActive } from "../lib/deploy";
import { repoName } from "../lib/format";
import { ROLE_LABEL } from "../lib/stacks";
import { useAppsDomain } from "../hooks/useInstance";
import { useBackups } from "./Backups";

type Bucket = "ok" | "attention" | "running" | "none" | "stopped";

function bucket(p: ProjectSummary): Bucket {
  if (p.app_state === "stopped") return "stopped";
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
  { key: "stopped", label: "Parados", dot: "warning" },
  { key: "none", label: "Sem deploy", dot: "neutral" },
];

export function OverviewPage() {
  const deleted = (useLocation().state as { deleted?: string } | null)?.deleted;
  const [filter, setFilter] = useState<Bucket | "all">("all");
  const [query, setQuery] = useState("");
  const apps = useAppsDomain();
  const backups = useBackups();
  const q = useQuery({
    queryKey: ["stacks"],
    queryFn: () => unwrap(api.GET("/stacks")),
    refetchInterval: 5000,
  });

  // Os indicadores contam aplicações; a tabela mostra projetos (repositórios, DEC-52).
  const stacks = useMemo(() => q.data?.stacks ?? [], [q.data]);
  const projects = useMemo(() => stacks.flatMap((s) => s.apps), [stacks]);
  const counts: Record<Bucket, number> = { ok: 0, attention: 0, running: 0, none: 0, stopped: 0 };
  projects.forEach((p) => counts[bucket(p)]++);
  const total = projects.length;
  const needle = query.trim().toLowerCase();
  const visible = stacks.filter(
    (s) =>
      (filter === "all" || s.apps.some((p) => bucket(p) === filter) || (filter === "none" && s.apps.length === 0)) &&
      (!needle || `${s.slug} ${s.repository_url} ${s.apps.map((a) => a.slug).join(" ")}`.toLowerCase().includes(needle)),
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
            <p className="small">Os indicadores contam o último deploy de cada projeto e se atualizam a cada 5 s. O consumo e a saúde dos contêineres vêm do coletor, no cartão abaixo, e são coisas diferentes do resultado do deploy.</p>
          </div>
        </div>
        <div className="row">
          <span className="meta row">
            <Icon name="clock" size={14} /> Última checagem: {checked}
          </span>
          <Button icon="refresh" onClick={() => q.refetch()} busy={q.isRefetching}>
            Sincronizar
          </Button>
        </div>
      </div>

      {deleted && (
        <Notice tone="success" title={`${deleted} foi excluído.`}>
          O endereço saiu do ar e a limpeza segue em segundo plano. O histórico dos deploys foi preservado.
        </Notice>
      )}
      {backups.data?.configured && backups.data.stale && (
        <Notice tone="danger" icon="alert" title="O backup da plataforma está atrasado">
          <span>
            O último backup bem-sucedido tem mais de {backups.data.stale_after.replace("h0m0s", " horas")} (ou não há nenhum). <Link to="/backups">Ver os backups</Link>.
          </span>
        </Notice>
      )}
      <OrphanVolumes />
      {q.isLoading && <Loading />}
      {q.isError && <ErrorBox error={q.error} onRetry={() => q.refetch()} />}

      {q.isSuccess && (
        <>
          <div className="metrics" role="list" aria-label="Resumo">
            <Metric label="Publicadas" value={counts.ok} denominator={`/ ${total} aplicações`} tone="success" icon="checkCircle" foot={<span>o último deploy terminou no ar</span>} />
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

          <FleetResources />

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
              Novo projeto
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
            {stacks.length === 0 ? (
              <Empty title="Nenhum projeto ainda.">
                Cole o link de um repositório público: o Cobogó lê, propõe as aplicações e publica. <Link to="/importar">Criar o primeiro</Link>.
              </Empty>
            ) : visible.length === 0 ? (
              <Empty title="Nenhum projeto neste filtro." icon="search" />
            ) : (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">Projeto e repositório</th>
                      <th scope="col">Aplicações e endereços</th>
                      <th scope="col">Último deploy</th>
                      <th scope="col" className="num">
                        Ações
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {visible.map((s) => (
                      <StackRow key={s.id} s={s} urlFor={apps.urlFor} />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            <div className="table-foot">
              <span>
                Total: <strong>{stacks.length} projeto(s)</strong>, {total} aplicação(ões) · <span className="text-success">{counts.ok} publicada(s)</span> · {counts.attention} com atenção · {counts.running} em andamento
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

function StackRow({ s, urlFor }: { s: Stack; urlFor: (slug: string) => string | null }) {
  const to = `/projetos/${s.slug}`;
  return (
    <tr>
      <td>
        <div className="cell-main">
          <Icon name="box" size={20} />
          <div className="cell-title">
            <Link to={to}>{s.slug}</Link>
            <span className="cell-sub" title={s.repository_url}>
              {repoName(s.repository_url)}
            </span>
          </div>
        </div>
      </td>
      <td>
        {s.apps.length === 0 ? (
          <span className="small faint">{s.scan_state === "scanning" ? "lendo o repositório…" : s.scan_state === "failed" ? "a leitura falhou" : "aguardando a sua confirmação"}</span>
        ) : (
          <div className="stack-sm">
            {s.apps.map((p) => (
              <AppAddress key={p.id} p={p} url={urlFor(p.slug)} />
            ))}
          </div>
        )}
      </td>
      <td>
        <div className="stack-sm">
          {s.apps.map((p) => {
            const d = p.last_deployment;
            return d ? (
              <span key={p.id} className="row">
                <Link to={`/deploys/${d.id}`}>{p.app_state === "stopped" ? <Badge tone="warning">Parado</Badge> : <StateBadge state={d.state} />}</Link>
                <span className="meta">
                  <When iso={d.created_at} />
                </span>
              </span>
            ) : (
              <Badge key={p.id} tone="neutral">
                sem deploy
              </Badge>
            );
          })}
        </div>
      </td>
      <td className="num">
        <div className="row-end">
          <Link className="icon-btn" to={to} title="Abrir o projeto" aria-label={`Abrir ${s.slug}`}>
            <Icon name="arrowRight" />
          </Link>
        </div>
      </td>
    </tr>
  );
}

function AppAddress({ p, url }: { p: ProjectSummary; url: string | null }) {
  const live = p.last_deployment?.state === "SUCCEEDED" && p.app_state !== "stopped";
  const host = url?.replace(/^https?:\/\//, "");
  return (
    <span className="row small">
      <Tag>{ROLE_LABEL[p.role ?? ""] ?? "Aplicação"}</Tag>
      {url && live ? (
        <a href={url} target="_blank" rel="noreferrer noopener" className="mono row">
          {host} <Icon name="external" size={13} />
        </a>
      ) : (
        <span className="mono faint">{host ?? p.slug}</span>
      )}
    </span>
  );
}
