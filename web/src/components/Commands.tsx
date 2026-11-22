// Comandos pontuais (DEC-45): rodar um comando no ambiente do projeto, num
// contêiner descartável da imagem da release ativa. A senha digitada de novo vale
// por uma janela curta, nesta sessão.
import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, unwrap } from "../api/client";
import { clock, COMMAND_STATE_LABEL, COMMAND_STATE_TONE, isActive, reauthSecondsLeft, type CommandRun } from "../lib/commands";
import { duration } from "../lib/format";
import { Badge, Button, Card, Empty, ErrorBox, Field, Loading, Notice, Tag, When } from "./ui";
import { Icon } from "./icons";

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

export function useCommands(projectId: string) {
  return useQuery({
    queryKey: ["commands", projectId],
    queryFn: () => unwrap(api.GET("/projects/{id}/commands", { params: { path: { id: projectId } } })),
    // Enquanto há um comando em andamento a lista se atualiza sozinha.
    refetchInterval: (q) => (q.state.data?.commands.some(isActive) ? 1500 : false),
  });
}

/** Um comando com a saída; se ainda roda, atualiza a cada 1,5 s. */
function useCommand(projectId: string, id: string, active: boolean) {
  return useQuery({
    queryKey: ["command", projectId, id],
    queryFn: () => unwrap(api.GET("/projects/{id}/commands/{cid}", { params: { path: { id: projectId, cid: id } } })),
    refetchInterval: (q) => (q.state.data && !isActive(q.state.data) ? false : active ? 1500 : false),
  });
}

/** Aba "Comandos". */
export function CommandsTab({ projectId }: { projectId: string }) {
  const qc = useQueryClient();
  const q = useCommands(projectId);
  const [now, setNow] = useState(() => Date.now());
  const [pause, setPause] = useState(false);
  const [custom, setCustom] = useState("");
  const [timeout, setTimeoutSeconds] = useState<number | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [opened, setOpened] = useState<string | null>(null);

  // Um relógio de 1 s só para a contagem da janela da senha.
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);

  if (q.isLoading) return <Loading />;
  if (q.isError) return <ErrorBox error={q.error} onRetry={() => q.refetch()} />;
  const view = q.data!;
  const left = reauthSecondsLeft(view.reauth_until, now);
  const unlocked = left > 0;
  const running = view.commands.find(isActive);

  async function run(body: { script?: string; command?: string }, key: string) {
    setBusy(key);
    setError(null);
    try {
      const c = await unwrap(api.POST("/projects/{id}/commands", { params: { path: { id: projectId } }, body: { ...body, pause_app: pause, timeout_seconds: timeout ?? undefined } }));
      setOpened(c.id);
      setCustom("");
      await qc.invalidateQueries({ queryKey: ["commands", projectId] });
    } catch (e) {
      // A janela fechou entre o clique e o pedido: volta a pedir a senha.
      if (e instanceof ApiError && e.code === "reauth_required") await qc.invalidateQueries({ queryKey: ["commands", projectId] });
      setError(errText(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="stack">
      <Notice tone="info" icon="shield" title="Um comando por vez, num contêiner descartável">
        <span>
          O comando roda na <strong>imagem da release ativa</strong>, com as variáveis do projeto, só na rede dele, sem porta pública, sem privilégios e com limite de memória, CPU e tempo. O contêiner é removido ao terminar. A saída e o texto saem com os valores das variáveis trocados por <span className="mono">[REDIGIDO]</span> (isso reduz vazamento, não o garante). Como o comando enxerga as variáveis, a senha é pedida de novo antes de rodar.
        </span>
      </Notice>

      {!view.available && (
        <Notice tone="warning" title="Este projeto não aceita comandos agora">
          {view.reason}
        </Notice>
      )}

      {view.available && !unlocked && <ReauthForm onDone={() => qc.invalidateQueries({ queryKey: ["commands", projectId] })} />}

      {view.available && unlocked && (
        <Card
          title="Rodar um comando"
          icon="terminal"
          actions={
            <Badge tone="success" title="A senha digitada de novo vale só nesta sessão">
              senha liberada · {clock(left)}
            </Badge>
          }
        >
          <div className="stack">
            {error && <Notice tone="danger">{error}</Notice>}
            {running && (
              <Notice tone="info">
                Já há um comando em andamento neste projeto. Espere ele terminar ou cancele.
              </Notice>
            )}

            <div className="stack-sm">
              <span className="label">Scripts do projeto</span>
              {view.scripts.length === 0 ? (
                <span className="small muted">O package.json não tem scripts pontuais (os de início, build, desenvolvimento e teste ficam de fora). Use o comando livre abaixo.</span>
              ) : (
                <div className="options" role="group" aria-label="Scripts do projeto">
                  {view.scripts.map((s) => (
                    <div key={s.name} className="option row-between">
                      <div className="stack-sm">
                        <span className="strong mono">{s.command}</span>
                        <span className="muted small mono wrap">{s.runs}</span>
                      </div>
                      <Button variant="secondary" icon="terminal" busy={busy === s.name} disabled={busy !== null || !!running} onClick={() => run({ script: s.name }, s.name)}>
                        Rodar
                      </Button>
                    </div>
                  ))}
                </div>
              )}
            </div>

            <details>
              <summary className="small">Outro comando</summary>
              <form
                className="stack card-text"
                onSubmit={(ev: FormEvent) => {
                  ev.preventDefault();
                  if (custom.trim()) run({ command: custom.trim() }, "custom");
                }}
              >
                <Field label="Comando" htmlFor="cmd-custom" hint="Executado por sh -c, na pasta da aplicação. Sem teclado interativo: o comando não pode pedir resposta.">
                  <textarea id="cmd-custom" className="mono" rows={2} value={custom} onChange={(e) => setCustom(e.target.value)} spellCheck={false} autoComplete="off" maxLength={2000} placeholder="ls -la /app/data" />
                </Field>
                <div className="row-end">
                  <Button type="submit" variant="primary" icon="terminal" busy={busy === "custom"} disabled={busy !== null || !!running || !custom.trim()}>
                    Rodar comando
                  </Button>
                </div>
              </form>
            </details>

            <div className="grid-2">
              <label className="inset stack-sm">
                <span className="row">
                  <input type="checkbox" checked={pause} onChange={(e) => setPause(e.target.checked)} style={{ width: "auto" }} />
                  <span className="strong">Pausar a aplicação durante o comando</span>
                </span>
                <span className="small muted">
                  Sem isso o comando roda com a aplicação no ar, e os dois podem usar o mesmo volume ao mesmo tempo (um arquivo SQLite pode ser disputado). Pausar tira o projeto do ar até o comando terminar.
                </span>
              </label>
              <label className="inset stack-sm">
                <span className="strong">Prazo</span>
                <select value={timeout ?? view.default_timeout} onChange={(e) => setTimeoutSeconds(Number(e.target.value))} aria-label="Prazo do comando">
                  {[30, 60, 120, 300, 600].map((s) => (
                    <option key={s} value={s}>
                      {s < 60 ? `${s} s` : `${s / 60} min`}
                    </option>
                  ))}
                </select>
                <span className="small muted">Passado o prazo o comando é encerrado.</span>
              </label>
            </div>
          </div>
        </Card>
      )}

      <section className="card card-flush" aria-label="Comandos recentes">
        <header className="card-head">
          <h2 className="card-title">
            <Icon name="history" size={20} className="card-icon" /> Comandos recentes
          </h2>
          <span className="meta">a saída fica guardada, sem os valores das variáveis</span>
        </header>
        {view.commands.length === 0 ? (
          <Empty title="Nenhum comando ainda." icon="terminal">
            Os comandos que você rodar aparecem aqui, com a saída e o código de saída.
          </Empty>
        ) : (
          <ul className="cmd-list">
            {view.commands.map((c) => (
              <CommandRow key={c.id} projectId={projectId} command={c} open={opened === c.id || (opened === null && isActive(c))} onToggle={() => setOpened(opened === c.id ? "" : c.id)} />
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

/** Pede a senha de novo e abre a janela de 10 minutos. */
function ReauthForm({ onDone }: { onDone: () => void }) {
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await unwrap(api.POST("/auth/reauth", { body: { password } }));
      setPassword("");
      onDone();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Digite a sua senha para liberar comandos" icon="key">
      <form className="stack" onSubmit={submit}>
        <p className="small muted card-text">
          Um comando enxerga as variáveis do projeto, e o painel nunca mostra os valores dos segredos. Por isso a senha é pedida de novo; ela vale por <strong>10 minutos</strong>, só nesta sessão.
        </p>
        {error && <Notice tone="danger">{error}</Notice>}
        <Field label="Senha" htmlFor="reauth-password">
          <input id="reauth-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
        </Field>
        <div className="row-end">
          <Button type="submit" variant="primary" icon="shieldCheck" busy={busy} disabled={!password}>
            Liberar por 10 minutos
          </Button>
        </div>
      </form>
    </Card>
  );
}

function CommandRow({ projectId, command, open, onToggle }: { projectId: string; command: CommandRun; open: boolean; onToggle: () => void }) {
  const qc = useQueryClient();
  const active = isActive(command);
  const detail = useCommand(projectId, command.id, open);
  const c = detail.data ?? command;
  const [busy, setBusy] = useState(false);

  async function cancel() {
    setBusy(true);
    try {
      await unwrap(api.POST("/projects/{id}/commands/{cid}/cancel", { params: { path: { id: projectId, cid: command.id } } }));
      await qc.invalidateQueries({ queryKey: ["commands", projectId] });
      await qc.invalidateQueries({ queryKey: ["command", projectId, command.id] });
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="cmd-item">
      <div className="row-between">
        <button type="button" className="linklike stack-sm" onClick={onToggle} aria-expanded={open}>
          <span className="mono strong wrap">{command.command}</span>
          <span className="small muted">
            <When iso={command.created_at} />
            {command.finished_at && command.started_at ? ` · ${duration(command.started_at, command.finished_at)}` : ""}
            {command.exit_code !== null && command.exit_code !== undefined ? ` · saída ${command.exit_code}` : ""}
          </span>
        </button>
        <span className="row">
          {command.source === "script" && <Tag>script</Tag>}
          {command.pause_app && <Tag tone="warning">pausou a aplicação</Tag>}
          <Badge tone={COMMAND_STATE_TONE[command.state]} pulse={command.state === "running"}>
            {c.cancel_requested && active ? "cancelando…" : COMMAND_STATE_LABEL[command.state]}
          </Badge>
          {active && !c.cancel_requested && (
            <Button variant="danger" icon="x" busy={busy} onClick={cancel}>
              Cancelar
            </Button>
          )}
        </span>
      </div>
      {open && (
        <div className="stack-sm">
          {c.error && <Notice tone={c.state === "succeeded" ? "info" : "warning"}>{c.error}</Notice>}
          {detail.isLoading ? (
            <Loading />
          ) : (
            <pre className="code" aria-label="Saída do comando">
              {c.output || (active ? "Aguardando a saída…" : "(sem saída)")}
            </pre>
          )}
          {c.output_truncated && <span className="small muted">A saída passou do limite: ficaram o começo e o fim.</span>}
        </div>
      )}
    </li>
  );
}
