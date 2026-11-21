// Ciclo de vida do projeto (DEC-38): operações recentes, aplicar limites na
// hora e a zona de perigo (excluir). A API só pede; o worker executa.
import { useEffect, useRef, useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, unwrap } from "../api/client";
import { appTransition, isSettled, OP_LABEL, OP_STATE_LABEL, summarize, TRANSITION_NOTICE, type AppTransition, type Operation } from "../lib/operations";
import { Badge, Button, Card, Empty, ErrorBox, Field, Loading, Notice, Tag, When } from "./ui";
import { Icon } from "./icons";

/** Acompanha uma operação até ela terminar (consulta a cada segundo). */
export function useOperation(id: string | null) {
  return useQuery({
    queryKey: ["operation", id],
    queryFn: () => unwrap(api.GET("/operations/{id}", { params: { path: { id: id! } } })),
    enabled: !!id,
    refetchInterval: (q) => (q.state.data && isSettled(q.state.data) ? false : 1000),
  });
}

const stateTone = (s: Operation["state"]) => (s === "DONE" ? "success" : s === "FAILED" ? "danger" : s === "RUNNING" ? "info" : "neutral");

export function OperationsTab({ projectId }: { projectId: string }) {
  const q = useQuery({
    queryKey: ["operations", projectId],
    queryFn: () => unwrap(api.GET("/projects/{id}/operations", { params: { path: { id: projectId } } })),
    refetchInterval: 4000,
  });
  if (q.isLoading) return <Loading />;
  if (q.isError) return <ErrorBox error={q.error} />;
  const ops = q.data!.operations;
  return (
    <section className="card card-flush" aria-label="Operações">
      <header className="card-head">
        <h2 className="card-title">
          <Icon name="history" size={20} className="card-icon" /> Operações de ciclo de vida
        </h2>
        <span className="meta">releases antigas, limites e faxina; o worker as executa</span>
      </header>
      {ops.length === 0 ? (
        <Empty title="Nenhuma operação ainda." icon="history">
          Quando uma release nova entra no ar, a anterior é aposentada aqui, depois do período de espera.
        </Empty>
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Operação</th>
                <th scope="col">Estado</th>
                <th scope="col">O que fez</th>
                <th scope="col">Quando</th>
              </tr>
            </thead>
            <tbody>
              {ops.map((op) => (
                <tr key={op.id}>
                  <td className="strong">{OP_LABEL[op.kind]}</td>
                  <td>
                    <Badge tone={stateTone(op.state)} pulse={op.state === "RUNNING"}>
                      {OP_STATE_LABEL[op.state]}
                    </Badge>
                  </td>
                  <td className="small">{summarize(op) || (op.state === "PENDING" ? <span className="muted">aguardando até <When iso={op.not_before} /></span> : "—")}</td>
                  <td className="meta">
                    <When iso={op.created_at} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

/** "Aplicar agora": atualiza os contêineres no ar com os limites de hoje. */
export function ApplyLimits({ projectId, onApplied }: { projectId: string; onApplied: () => void }) {
  const [opId, setOpId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const op = useOperation(opId);
  const settled = op.data && isSettled(op.data);

  useEffect(() => {
    if (settled) onApplied();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [settled]);

  async function apply() {
    setBusy(true);
    setError(null);
    setOpId(null);
    try {
      const created = await unwrap(api.POST("/projects/{id}/limits/apply", { params: { path: { id: projectId } } }));
      setOpId(created.id);
    } catch (e) {
      setError(e instanceof ApiError && e.code === "no_active_release" ? "Não há release no ar: os limites valem no próximo deploy." : e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack-sm">
      <div className="action-bar">
        <span className="small row">
          <Icon name="cpu" size={16} className="card-icon" /> Os limites salvos valem no próximo deploy. Para valer já, aplique ao contêiner no ar, sem reiniciá-lo.
        </span>
        <Button icon="refresh" onClick={apply} busy={busy || (!!opId && !settled)}>
          Aplicar agora
        </Button>
      </div>
      {error && <Notice tone="danger">{error}</Notice>}
      {op.data && settled && (
        <Notice tone={op.data.state === "DONE" ? "success" : "danger"} title={op.data.state === "DONE" ? "Limites aplicados" : "Os limites não foram alterados"}>
          {summarize(op.data)}
        </Notice>
      )}
    </div>
  );
}

/** Zona de perigo: excluir o projeto, confirmando com o nome. */
export function DangerZone({ projectId, slug }: { projectId: string; slug: string }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function remove(ev: FormEvent) {
    ev.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await unwrap(api.DELETE("/projects/{id}", { params: { path: { id: projectId } }, body: { confirm_slug: typed } }));
      qc.removeQueries({ queryKey: ["project", projectId] });
      qc.invalidateQueries({ queryKey: ["projects"] });
      navigate("/", { replace: true, state: { deleted: slug } });
    } catch (e) {
      setError(
        e instanceof ApiError && e.code === "deployment_in_progress"
          ? "Há um deploy em andamento. Espere ele terminar para excluir o projeto."
          : e instanceof Error
            ? e.message
            : String(e),
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Zona de perigo" icon="alert" className="card-danger">
      {!open ? (
        <div className="action-bar">
          <span className="small">
            Excluir tira o endereço do ar, apaga as variáveis cifradas e remove contêineres, rede e imagens. <strong>O histórico de deploys fica</strong>; volumes nunca são tocados.
          </span>
          <Button variant="danger" icon="x" onClick={() => setOpen(true)}>
            Excluir projeto…
          </Button>
        </div>
      ) : (
        <form className="stack" onSubmit={remove}>
          <Notice tone="danger" title={`Excluir ${slug}?`}>
            O endereço público sai do ar em segundos e o projeto some da lista. Para confirmar, digite o nome do projeto.
          </Notice>
          <Field label={`Digite ${slug}`} htmlFor="confirm-slug">
            <input id="confirm-slug" className="mono" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} autoFocus />
          </Field>
          {error && <Notice tone="danger">{error}</Notice>}
          <div className="row-end">
            <Button
              variant="ghost"
              onClick={() => {
                setOpen(false);
                setTyped("");
                setError(null);
              }}
            >
              Cancelar
            </Button>
            <Button type="submit" variant="danger" icon="x" busy={busy} disabled={typed !== slug}>
              Excluir {slug}
            </Button>
          </div>
        </form>
      )}
      <p className="muted small card-text">
        <Tag>histórico preservado</Tag> Para apagar o histórico de vez, existe uma operação de administrador no servidor (fora do painel).
      </p>
    </Card>
  );
}

type AppAction = "stop" | "start" | "restart";

const APP_DONE: Record<AppAction, string> = {
  stop: "Aplicação parada. O endereço mostra a página de projeto inativo.",
  start: "Aplicação no ar de novo.",
  restart: "Aplicação reiniciada.",
};

/**
 * Parar, iniciar e reiniciar (DEC-40). A API só enfileira; o worker executa, e
 * a operação é acompanhada até terminar. Parado não é falhou.
 */
export function useAppControl(projectId: string) {
  const qc = useQueryClient();
  const [pending, setPending] = useState<{ id: string; action: AppAction } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const op = useOperation(pending?.id ?? null);
  const settled = !!op.data && isSettled(op.data);
  // O que está acontecendo com a aplicação vem das operações do projeto (e não
  // só do clique desta tela): vale depois de recarregar ou de voltar à página.
  const ops = useQuery({
    queryKey: ["operations", projectId],
    queryFn: () => unwrap(api.GET("/projects/{id}/operations", { params: { path: { id: projectId } } })),
    refetchInterval: (q) => (appTransition(q.state.data?.operations) ? 1500 : false),
  });
  const transition: AppTransition | null = appTransition(ops.data?.operations);
  const wasChanging = useRef(false);
  useEffect(() => {
    // Quando a mudança termina, o estado do projeto e as listas se atualizam.
    if (wasChanging.current && !transition) {
      for (const key of [["project", projectId], ["projects"]]) qc.invalidateQueries({ queryKey: key });
    }
    wasChanging.current = !!transition;
  }, [transition, projectId, qc]);

  useEffect(() => {
    if (!settled) return;
    for (const key of [["project", projectId], ["projects"], ["operations", projectId]]) qc.invalidateQueries({ queryKey: key });
  }, [settled, projectId, qc]);

  async function run(action: AppAction) {
    setError(null);
    const path = { params: { path: { id: projectId } } };
    try {
      const created = await unwrap(
        action === "stop" ? api.POST("/projects/{id}/app/stop", path) : action === "start" ? api.POST("/projects/{id}/app/start", path) : api.POST("/projects/{id}/app/restart", path),
      );
      setPending({ id: created.id, action });
      // A operação nova entra na lista já, e a lista passa a ser consultada de perto.
      qc.invalidateQueries({ queryKey: ["operations", projectId] });
    } catch (e) {
      setPending(null);
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  const busy = (!!pending && !settled) || !!transition;
  let notice: { tone: "success" | "danger" | "info"; text: string } | null = null;
  if (error) notice = { tone: "danger", text: error };
  else if (transition) notice = { tone: "info", text: TRANSITION_NOTICE[transition] };
  else if (pending && !settled) notice = { tone: "info", text: "Enviando o pedido…" };
  else if (pending && op.data?.state === "DONE") notice = { tone: "success", text: APP_DONE[pending.action] };
  else if (pending && op.data?.state === "FAILED") notice = { tone: "danger", text: op.data.error || "A operação falhou." };
  return { run, busy, notice, transition };
}

/** Os botões da aplicação: no ar, para ou reinicia; parada, inicia. */
export function AppButtons({ state, busy, run }: { state: "none" | "running" | "stopped"; busy: boolean; run: (a: AppAction) => void }) {
  if (state === "none") return null;
  if (state === "stopped") {
    return (
      <Button variant="primary" icon="rocket" busy={busy} onClick={() => run("start")}>
        Iniciar
      </Button>
    );
  }
  return (
    <>
      <Button icon="refresh" disabled={busy} onClick={() => run("restart")} title="Não relê variáveis; para isso, faça um novo deploy">
        Reiniciar
      </Button>
      <Button disabled={busy} onClick={() => window.confirm("Parar a aplicação? O endereço passa a mostrar a página de projeto inativo, e você pode iniciá-la de novo quando quiser.") && run("stop")}>
        Parar
      </Button>
    </>
  );
}
