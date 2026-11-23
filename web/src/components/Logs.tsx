// Logs de execução (DEC-46): o que a aplicação e o banco do projeto imprimem depois
// do deploy. O painel traz as últimas linhas e acompanha por polling de 2 s com o
// cursor (o id da última linha).
import { useCallback, useEffect, useRef, useState } from "react";
import { api, unwrap } from "../api/client";
import { appendLines, asText, clockTime, filterLines, prependLines, startsNewContainer, type LogLine, type LogOrigin } from "../lib/logs";
import { Button, CopyButton, Empty, ErrorBox, Notice, Tag } from "./ui";
import { Icon } from "./icons";

const POLL_MS = 2000;

const ORIGINS: [LogOrigin, string][] = [
  ["", "Tudo"],
  ["app", "Aplicação"],
  ["database", "Banco"],
];

/** Aba "Logs". */
export function LogsTab({ projectId }: { projectId: string }) {
  const [origin, setOrigin] = useState<LogOrigin>("");
  const [lines, setLines] = useState<LogLine[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [hasOlder, setHasOlder] = useState(false);
  const [retention, setRetention] = useState<{ hours: number; lines: number } | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [follow, setFollow] = useState(true);
  const [search, setSearch] = useState("");
  const [loadingOlder, setLoadingOlder] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  const lastId = useRef(0);

  const query = useCallback(
    (extra: { after?: number; before?: number; limit?: number }) =>
      unwrap(api.GET("/projects/{id}/logs", { params: { path: { id: projectId }, query: { ...extra, origin: origin || undefined } } })),
    [projectId, origin],
  );

  // Carga inicial (e ao trocar a origem): as últimas linhas.
  useEffect(() => {
    let cancelled = false;
    setLoaded(false);
    setLines([]);
    lastId.current = 0;
    query({})
      .then((r) => {
        if (cancelled) return;
        setLines(r.lines);
        lastId.current = r.lines.at(-1)?.id ?? 0;
        setHasOlder(r.has_older);
        setRetention({ hours: r.retention_hours, lines: r.retention_lines });
        setError(null);
        setLoaded(true);
        setFollow(true);
      })
      .catch((e) => !cancelled && setError(e));
    return () => {
      cancelled = true;
    };
  }, [query]);

  // Acompanhar: a cada 2 s, o que veio depois da última linha (e só com a aba à vista).
  useEffect(() => {
    if (!loaded) return;
    let stopped = false;
    const tick = async () => {
      if (document.hidden) return;
      try {
        // Se há mais de uma página atrasada, lê todas de uma vez.
        for (let guard = 0; guard < 20 && !stopped; guard++) {
          const r = await query({ after: lastId.current, limit: 500 });
          if (stopped) return;
          if (r.lines.length) {
            lastId.current = r.lines.at(-1)?.id ?? lastId.current;
            setLines((cur) => appendLines(cur, r.lines));
          }
          setError(null);
          if (!r.has_more) break;
        }
      } catch (e) {
        if (!stopped) setError(e);
      }
    };
    const t = setInterval(tick, POLL_MS);
    return () => {
      stopped = true;
      clearInterval(t);
    };
  }, [loaded, query]);

  // Seguir o fim: rola até o final quando chega linha nova.
  useEffect(() => {
    if (follow && box.current) box.current.scrollTop = box.current.scrollHeight;
  }, [lines, follow, search]);

  function onScroll() {
    const el = box.current;
    if (!el) return;
    const atEnd = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
    if (!atEnd && follow) setFollow(false);
    if (atEnd && !follow) setFollow(true);
  }

  async function older() {
    if (!lines.length) return;
    setLoadingOlder(true);
    try {
      const r = await query({ before: lines[0]?.id, limit: 300 });
      setLines((cur) => prependLines(cur, r.lines));
      setHasOlder(r.has_older);
      setFollow(false);
    } catch (e) {
      setError(e);
    } finally {
      setLoadingOlder(false);
    }
  }

  const shown = filterLines(lines, search);

  return (
    <div className="stack">
      <Notice tone="info" icon="shield" title="O que a aplicação e o banco imprimem">
        <span>
          Guardamos as últimas {retention?.hours ?? 24} horas e até {(retention?.lines ?? 20000).toLocaleString("pt-BR")} linhas por projeto. Os valores das variáveis do projeto saem como <span className="mono">[REDIGIDO]</span>, mas um segredo que a aplicação monte ou codifique antes de imprimir passa: não imprima segredos nem dados pessoais. O log do build e do deploy está no histórico de deploys.
        </span>
      </Notice>

      <section className="card card-flush" aria-label="Logs de execução">
        <header className="card-head">
          <h2 className="card-title">
            <Icon name="terminal" size={20} className="card-icon" /> Logs
          </h2>
          <span className="row">
            <div className="segmented" role="group" aria-label="Origem">
              {ORIGINS.map(([value, label]) => (
                <button key={value || "all"} type="button" className={origin === value ? "seg active" : "seg"} aria-pressed={origin === value} onClick={() => setOrigin(value)}>
                  {label}
                </button>
              ))}
            </div>
          </span>
        </header>

        <div className="log-tools">
          <input type="search" className="mono" placeholder="Buscar no que está carregado" aria-label="Buscar nos logs" value={search} onChange={(e) => setSearch(e.target.value)} spellCheck={false} autoComplete="off" />
          <span className="row">
            <span className="small muted">{search ? `${shown.length} de ${lines.length} linhas` : `${lines.length} linhas`}</span>
            <Button variant={follow ? "primary" : "secondary"} icon="activity" aria-pressed={follow} onClick={() => setFollow(!follow)}>
              Seguir o fim
            </Button>
            <CopyButton text={asText(shown)} label="Copiar" />
          </span>
        </div>

        {!!error && <ErrorBox error={error} />}
        {!loaded && !error && <div className="log-empty muted small">Carregando…</div>}
        {loaded && lines.length === 0 && (
          <Empty title="Nenhuma linha guardada ainda." icon="terminal">
            Quando a aplicação imprimir algo, as linhas aparecem aqui em poucos segundos. Um deploy novo e um reinício começam a ser capturados sozinhos.
          </Empty>
        )}
        {loaded && lines.length > 0 && (
          <div className="log-box" ref={box} onScroll={onScroll} role="log" aria-live="off" aria-label="Linhas do log">
            {hasOlder && !search && (
              <div className="log-older">
                <Button variant="secondary" busy={loadingOlder} onClick={older}>
                  Carregar anteriores
                </Button>
              </div>
            )}
            {shown.map((l, i) => (
              <div key={l.id}>
                {!search && startsNewContainer(shown[i - 1], l) && (
                  <div className="log-divider">
                    <Tag>{l.origin !== "app" ? "banco" : "aplicação"}</Tag> {l.container} começou
                  </div>
                )}
                <div className={`log-line log-${l.stream}`}>
                  <time dateTime={l.at} className="log-time">
                    {clockTime(l.at)}
                  </time>
                  {origin === "" && <span className={`log-origin log-origin-${l.origin}`}>{l.origin !== "app" ? "banco" : "app"}</span>}
                  <span className="log-text">{l.line}</span>
                </div>
              </div>
            ))}
            {search && shown.length === 0 && <div className="log-empty muted small">Nenhuma linha carregada contém "{search}".</div>}
          </div>
        )}
      </section>
    </div>
  );
}
