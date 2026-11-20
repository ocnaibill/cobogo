// Acompanha os eventos de um deploy por consulta periódica (DEC-28 itens 2 a 4):
// pede só o que veio depois do último `sequence` e para quando `settled` e sem
// `has_more`. Volta a consultar quando o deploy é reaberto (resposta a pendência).
import { useCallback, useEffect, useRef, useState } from "react";
import { api, unwrap, type DeploymentEvent, type DeploymentState } from "../api/client";

export const POLL_MS = 2000;

export interface EventsState {
  events: DeploymentEvent[];
  state: DeploymentState | null;
  settled: boolean;
  error: string | null;
}

export function useEvents(deploymentId: string) {
  const [snap, setSnap] = useState<EventsState>({ events: [], state: null, settled: false, error: null });
  const after = useRef(0);
  const settledRef = useRef(false);
  const [generation, setGeneration] = useState(0);

  /** Reabre o acompanhamento (por exemplo, depois de responder uma pendência). */
  const resume = useCallback(() => {
    settledRef.current = false;
    setSnap((s) => ({ ...s, settled: false }));
    setGeneration((g) => g + 1);
  }, []);

  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    after.current = 0;

    async function tick() {
      try {
        const page = await unwrap(
          api.GET("/deployments/{id}/events", {
            params: { path: { id: deploymentId }, query: { after: after.current, limit: 500 } },
          }),
        );
        if (stopped) return;
        after.current = page.next_after;
        settledRef.current = page.settled && !page.has_more;
        setSnap((s) => ({
          events: page.events.length ? [...s.events.filter((e) => e.sequence <= (page.events[0]?.sequence ?? 0) - 1), ...page.events] : s.events,
          state: page.state,
          settled: settledRef.current,
          error: null,
        }));
        // Ainda há eventos já gravados: busca o resto sem esperar.
        timer = setTimeout(tick, page.has_more ? 0 : settledRef.current ? 15000 : POLL_MS);
      } catch (e) {
        if (stopped) return;
        setSnap((s) => ({ ...s, error: e instanceof Error ? e.message : String(e) }));
        timer = setTimeout(tick, POLL_MS * 2);
      }
    }
    setSnap({ events: [], state: null, settled: false, error: null });
    tick();
    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
    };
  }, [deploymentId, generation]);

  return { ...snap, resume };
}
