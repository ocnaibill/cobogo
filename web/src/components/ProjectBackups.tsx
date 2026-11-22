// Backup dos dados de um projeto (DEC-47 B): fazer, ver, baixar e apagar; e a trava
// "backup antes de apagar" que a exclusão do projeto e a do banco usam.
import { useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, unwrap } from "../api/client";
import { bytes } from "../lib/format";
import { BACKUP_STATE_LABEL, BACKUP_STATE_TONE, canDelete, hasUsableBackup, isBackupActive, isRestoreActive, isUsable, itemsSummary, projectBackupUrl, REASON_LABEL, RESTORE_STATE_LABEL, restorePlan, type ProjectBackup, type ProjectRestore } from "../lib/projectBackups";
import { clock } from "../lib/commands";
import { ReauthForm, useReauth } from "./Reauth";
import { usePersistent } from "./Data";
import { useDatabase } from "./Database";
import { Badge, Button, Empty, ErrorBox, Loading, Notice, Tag, When } from "./ui";
import { Icon } from "./icons";

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

export function useProjectBackups(projectId: string) {
  return useQuery({
    queryKey: ["project-backups", projectId],
    queryFn: () => unwrap(api.GET("/projects/{id}/backups", { params: { path: { id: projectId } } })),
    // Enquanto algum backup copia, a lista se atualiza sozinha.
    refetchInterval: (q) => (q.state.data?.backups.some(isBackupActive) || q.state.data?.restores.some(isRestoreActive) ? 2000 : false),
  });
}

/** Pede um backup. `reason` é "manual" ou "before_delete". */
export function useRequestBackup(projectId: string) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function request(pauseApp: boolean, reason: "manual" | "before_delete") {
    setBusy(true);
    setError(null);
    try {
      await unwrap(api.POST("/projects/{id}/backups", { params: { path: { id: projectId } }, body: { pause_app: pauseApp, reason } }));
      await qc.invalidateQueries({ queryKey: ["project-backups", projectId] });
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }
  return { request, busy, error };
}

/** Cartão "Backups dos dados" da aba Dados. */
export function ProjectBackupsCard({ projectId, slug }: { projectId: string; slug: string }) {
  const q = useProjectBackups(projectId);
  const reauth = useReauth();
  const { request, busy, error } = useRequestBackup(projectId);
  const [pause, setPause] = useState(false);

  if (q.isLoading) return <Loading />;
  if (q.isError) return <ErrorBox error={q.error} onRetry={() => q.refetch()} />;
  const view = q.data!;
  const active = view.backups.find(isBackupActive) ?? (view.restores.some(isRestoreActive) ? view.backups[0] : undefined);
  const restoring = view.restores.some(isRestoreActive);
  const blocked = !view.configured ? "O volume de backups não está disponível nesta instalação." : !view.has_content ? "O projeto não tem dados para copiar: declare um caminho persistente acima ou crie o banco do Cobogó." : null;

  return (
    <section className="card card-flush" aria-label="Backups dos dados">
      <header className="card-head">
        <h2 className="card-title">
          <Icon name="shieldCheck" size={20} className="card-icon" /> Backups dos dados
        </h2>
        <Tag title="Quantos backups o projeto guarda; o mais antigo sai quando entra um novo">guarda os últimos {view.keep}</Tag>
      </header>
      <div className="stack card-text">
        <Notice tone="warning" icon="shield" title="Na mesma VM, e sem as variáveis">
          <span>
            Os backups ficam <strong>na mesma VM</strong>: protegem de um erro, não da perda do disco. <strong>Baixe</strong> e guarde fora daqui o que importa. Eles levam os <strong>volumes</strong> declarados e o <strong>banco do Cobogó</strong> (por <span className="mono">pg_dump</span>, nunca copiando arquivo em uso), e <strong>não</strong> as variáveis de ambiente, que estão no backup da plataforma. Os dados <strong>não são cifrados</strong>: quem tem o arquivo os lê.
          </span>
        </Notice>
        {blocked && <Notice tone="info">{blocked}</Notice>}
        {error && <Notice tone="danger">{error}</Notice>}
        {!blocked && (
          <div className="row-between wrap-gap">
            <label className="check">
              <input type="checkbox" checked={pause} onChange={(e) => setPause(e.target.checked)} />
              <span>
                <strong>Pausar a aplicação durante a cópia dos volumes</strong>
                <span className="small muted"> Sem isso, um arquivo em uso (como um SQLite) pode ser copiado no meio de uma escrita e não abrir. Pausar tira o projeto do ar até terminar. O banco do Cobogó não precisa: sai consistente de qualquer jeito.</span>
              </span>
            </label>
            <Button variant="primary" icon="download" busy={busy} disabled={!!active || restoring || busy} onClick={() => request(pause, "manual")}>
              {restoring ? "Restauração em andamento…" : active ? "Backup em andamento…" : "Fazer backup agora"}
            </Button>
          </div>
        )}
        {view.restores.length > 0 && <RestoreStatus restore={view.restores[0]!} />}
        {view.backups.length > 0 && !reauth.unlocked && (
          <ReauthForm title="Digite a sua senha para baixar" reason="Um backup tem os dados do projeto sem cifra. Por isso a senha é pedida de novo." onDone={reauth.refresh} />
        )}
        {view.backups.length > 0 && reauth.unlocked && (
          <Notice tone="success">
            <span>
              Senha liberada por mais <strong>{clock(reauth.left)}</strong>: os downloads funcionam.
            </span>
          </Notice>
        )}
      </div>
      {view.backups.length === 0 ? (
        <Empty title="Nenhum backup ainda." icon="shieldCheck">
          Faça o primeiro quando o projeto tiver dados que doeria perder.
        </Empty>
      ) : (
        <ul className="cmd-list">
          {view.backups.map((b) => (
            <BackupRow key={b.id} projectId={projectId} slug={slug} backup={b} unlocked={reauth.unlocked} busyProject={!!active || restoring} />
          ))}
        </ul>
      )}
    </section>
  );
}

function BackupRow({ projectId, slug, backup, unlocked, busyProject }: { projectId: string; slug: string; backup: ProjectBackup; unlocked: boolean; busyProject: boolean }) {
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const [restoring, setRestoring] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const active = isBackupActive(backup);

  async function remove() {
    setBusy(true);
    setError(null);
    try {
      await unwrap(api.DELETE("/projects/{id}/backups/{bid}", { params: { path: { id: projectId, bid: backup.id } } }));
      setConfirming(false);
      await qc.invalidateQueries({ queryKey: ["project-backups", projectId] });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : errText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="cmd-item">
      <div className="row-between wrap-gap">
        <div className="stack-sm">
          <span className="strong">
            <When iso={backup.created_at} /> <Tag>{REASON_LABEL[backup.reason]}</Tag>
            {backup.pause_app && <Tag tone="warning">pausou a aplicação</Tag>}
          </span>
          {backup.state === "succeeded" && (
            <span className="small muted">
              {bytes(backup.size_bytes)} · {itemsSummary(backup, bytes)}
            </span>
          )}
          {backup.error && <span className="small danger-text">{backup.error}</span>}
          {(backup.manifest.skipped ?? []).map((s) => (
            <span key={s} className="small muted">
              <Icon name="alert" size={14} /> {s}
            </span>
          ))}
        </div>
        <span className="row">
          <Badge tone={BACKUP_STATE_TONE[backup.state]} pulse={backup.state === "running"}>
            {backup.delete_requested ? "apagando…" : BACKUP_STATE_LABEL[backup.state]}
          </Badge>
          {isUsable(backup) &&
            (unlocked ? (
              <a className="btn btn-secondary" href={projectBackupUrl(projectId, backup.id)} download>
                <Icon name="download" size={16} /> Baixar
              </a>
            ) : (
              <Button variant="secondary" icon="download" disabled title="Digite a senha de novo acima">
                Baixar
              </Button>
            ))}
          {isUsable(backup) && !restoring && (
            <Button variant="secondary" icon="activity" disabled={busyProject} title={busyProject ? "Espere o backup ou a restauração em andamento terminar" : undefined} onClick={() => setRestoring(true)}>
              Restaurar…
            </Button>
          )}
          {!active && !backup.delete_requested && !confirming && (
            <Button variant="ghost" icon="x" onClick={() => setConfirming(true)}>
              Apagar
            </Button>
          )}
        </span>
      </div>
      {confirming && (
        <div className="row-between wrap-gap">
          <span className="small">Apagar este backup remove os arquivos da VM. Não dá para desfazer.</span>
          <span className="row">
            <Button variant="ghost" onClick={() => setConfirming(false)}>
              Cancelar
            </Button>
            <Button variant="danger" icon="x" busy={busy} onClick={remove}>
              Apagar o backup
            </Button>
          </span>
        </div>
      )}
      {restoring && <RestoreForm projectId={projectId} slug={slug} backup={backup} unlocked={unlocked} onClose={() => setRestoring(false)} />}
      {error && <Notice tone="danger">{error}</Notice>}
    </li>
  );
}

/** O que a restauração mais recente fez, ou o passo em que está. */
function RestoreStatus({ restore }: { restore: ProjectRestore }) {
  const active = isRestoreActive(restore);
  if (!active && restore.state === "succeeded" && Date.now() - new Date(restore.finished_at ?? restore.created_at).getTime() > 24 * 3600 * 1000) return null;
  const r = restore.result;
  return (
    <Notice tone={restore.state === "failed" ? "danger" : restore.state === "succeeded" ? "success" : "info"} icon="activity" title={`Restauração: ${RESTORE_STATE_LABEL[restore.state]}`}>
      <div className="stack-sm">
        {active && (
          <span>
            {restore.state === "queued" ? "Primeiro o Cobogó faz um backup do estado atual (de segurança)." : `Passo: ${restore.step || "iniciando"}.`} A aplicação fica fora do ar enquanto os dados são trocados.
          </span>
        )}
        {restore.state === "succeeded" && (
          <span>
            Restaurado: {[...r.volumes.map((v) => `volume ${v}`), ...(r.database ? ["banco"] : [])].join(" · ") || "nada"}.
            {r.app_restarted ? " A aplicação foi iniciada de novo." : ""} <When iso={restore.finished_at ?? restore.created_at} />
          </span>
        )}
        {restore.error && <span>{restore.error}</span>}
        {r.skipped.map((s) => (
          <span key={s} className="small">
            <Icon name="alert" size={14} /> Ignorado: {s}
          </span>
        ))}
        {r.warnings.map((s) => (
          <span key={s} className="small">
            <Icon name="alert" size={14} /> {s}
          </span>
        ))}
        {restore.state !== "queued" && <span className="small muted">O backup de segurança do estado de antes está na lista abaixo (antes de restaurar).</span>}
      </div>
    </Notice>
  );
}

/** Pede a restauração: mostra o que será trocado e o que será ignorado, e exige a senha e o nome do projeto. */
function RestoreForm({ projectId, slug, backup, unlocked, onClose }: { projectId: string; slug: string; backup: ProjectBackup; unlocked: boolean; onClose: () => void }) {
  const qc = useQueryClient();
  const paths = usePersistent(projectId).data?.paths ?? [];
  const managedDb = useDatabase(projectId).data?.service?.mode === "managed";
  const plan = restorePlan(backup, paths, managedDb);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit() {
    setBusy(true);
    setError(null);
    try {
      await unwrap(api.POST("/projects/{id}/backups/{bid}/restore", { params: { path: { id: projectId, bid: backup.id } }, body: { confirm_slug: typed } }));
      await qc.invalidateQueries({ queryKey: ["project-backups", projectId] });
      onClose();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack">
      <Notice tone="danger" title={`Restaurar o backup de ${slug}?`}>
        <div className="stack-sm">
          <span>
            Isto <strong>substitui o conteúdo atual</strong> pelo do backup. Antes, o Cobogó faz um <strong>backup de segurança do estado atual</strong> (que fica na lista, “antes de restaurar”); se ele falhar, nada é tocado. A aplicação fica <strong>fora do ar</strong> durante a troca e volta sozinha, se estava no ar.
          </span>
          {plan.restore.length > 0 ? (
            <span>
              <strong>Vai restaurar:</strong> {plan.restore.join(" · ")}
            </span>
          ) : (
            <span>
              <strong>Nada deste backup pode ser restaurado agora.</strong>
            </span>
          )}
          {plan.skipped.map((s) => (
            <span key={s} className="small">
              <Icon name="alert" size={14} /> <strong>Será ignorado:</strong> {s}
            </span>
          ))}
          <span className="small">O Cobogó não cria volumes que o projeto não declara nem apaga volumes. As variáveis de ambiente não fazem parte do backup e não mudam.</span>
        </div>
      </Notice>
      {!unlocked && <Notice tone="warning">Digite a sua senha de novo no quadro acima para poder restaurar.</Notice>}
      <div className="row wrap-gap">
        <input className="mono" style={{ maxWidth: 260 }} value={typed} onChange={(e) => setTyped(e.target.value)} placeholder={`Digite ${slug}`} aria-label="Nome do projeto para confirmar" autoComplete="off" spellCheck={false} />
        <Button variant="ghost" onClick={onClose}>
          Cancelar
        </Button>
        <Button variant="danger" icon="activity" busy={busy} disabled={typed !== slug || !unlocked || plan.restore.length === 0 || busy} onClick={submit}>
          Restaurar {slug}
        </Button>
      </div>
      {error && <Notice tone="danger">{error}</Notice>}
    </div>
  );
}

/**
 * A trava "backup antes de apagar" (DEC-47 item 10). `hasData` diz se há o que perder. Devolve se a
 * exclusão está liberada e o painel a mostrar: sem backup bom, oferece fazer um agora (ou apagar sem).
 */
export function useBackupGate(projectId: string, hasData: boolean, what: string): { allowed: boolean; panel: ReactNode } {
  const q = useProjectBackups(projectId);
  const { request, busy, error } = useRequestBackup(projectId);
  const [skip, setSkip] = useState(false);
  const list = q.data?.backups ?? [];
  const allowed = canDelete(hasData, list, skip);
  if (!hasData) return { allowed, panel: null };
  const running = list.find(isBackupActive);
  const good = hasUsableBackup(list);
  const panel = (
    <Notice tone={good ? "success" : "warning"} icon="shieldCheck" title={good ? "Há um backup dos dados" : "Faça um backup antes de apagar"}>
      <div className="stack-sm">
        {good ? (
          <span>
            O backup mais recente é de <When iso={list.find(isUsable)!.created_at} />. <strong>Baixe-o</strong> na aba Dados (com a senha de novo) antes de continuar: {what}
          </span>
        ) : (
          <span>
            Apagar {what} é irreversível, e este projeto não tem backup. Faça um agora, baixe-o na aba Dados, ou escolha apagar sem backup.
          </span>
        )}
        {error && <span className="small danger-text">{error}</span>}
        {!good && (
          <div className="row">
            <Button variant="primary" icon="download" busy={busy || !!running} disabled={busy || !!running || !q.data?.configured || !q.data?.has_content} onClick={() => request(false, "before_delete")}>
              {running ? "Copiando…" : "Fazer backup agora"}
            </Button>
            <label className="check">
              <input type="checkbox" checked={skip} onChange={(e) => setSkip(e.target.checked)} />
              <span>Apagar sem backup</span>
            </label>
          </div>
        )}
      </div>
    </Notice>
  );
  return { allowed, panel };
}
