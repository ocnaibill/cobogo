// Backup dos dados de um projeto (DEC-47 B): fazer, ver, baixar e apagar; e a trava
// "backup antes de apagar" que a exclusão do projeto e a do banco usam.
import { useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, unwrap } from "../api/client";
import { bytes } from "../lib/format";
import { BACKUP_STATE_LABEL, BACKUP_STATE_TONE, canDelete, hasUsableBackup, isBackupActive, isUsable, itemsSummary, projectBackupUrl, REASON_LABEL, type ProjectBackup } from "../lib/projectBackups";
import { clock } from "../lib/commands";
import { ReauthForm, useReauth } from "./Reauth";
import { Badge, Button, Empty, ErrorBox, Loading, Notice, Tag, When } from "./ui";
import { Icon } from "./icons";

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

export function useProjectBackups(projectId: string) {
  return useQuery({
    queryKey: ["project-backups", projectId],
    queryFn: () => unwrap(api.GET("/projects/{id}/backups", { params: { path: { id: projectId } } })),
    // Enquanto algum backup copia, a lista se atualiza sozinha.
    refetchInterval: (q) => (q.state.data?.backups.some(isBackupActive) ? 2000 : false),
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
export function ProjectBackupsCard({ projectId }: { projectId: string }) {
  const q = useProjectBackups(projectId);
  const reauth = useReauth();
  const { request, busy, error } = useRequestBackup(projectId);
  const [pause, setPause] = useState(false);

  if (q.isLoading) return <Loading />;
  if (q.isError) return <ErrorBox error={q.error} onRetry={() => q.refetch()} />;
  const view = q.data!;
  const active = view.backups.find(isBackupActive);
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
            <Button variant="primary" icon="download" busy={busy} disabled={!!active || busy} onClick={() => request(pause, "manual")}>
              {active ? "Backup em andamento…" : "Fazer backup agora"}
            </Button>
          </div>
        )}
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
            <BackupRow key={b.id} projectId={projectId} backup={b} unlocked={reauth.unlocked} />
          ))}
        </ul>
      )}
    </section>
  );
}

function BackupRow({ projectId, backup, unlocked }: { projectId: string; backup: ProjectBackup; unlocked: boolean }) {
  const qc = useQueryClient();
  const [confirming, setConfirming] = useState(false);
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
      {error && <Notice tone="danger">{error}</Notice>}
    </li>
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
