// Backups da plataforma (DEC-47): o estado do backup do banco interno, os arquivos
// (com download pela senha digitada de novo) e a retenção.
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, unwrap } from "../api/client";
import { backupHealth, downloadUrl, HEALTH_TEXT, isScheduled, numberSetting, type BackupFile, type BackupsView } from "../lib/backups";
import { clock } from "../lib/commands";
import { bytes } from "../lib/format";
import { ReauthForm, useReauth } from "../components/Reauth";
import { Badge, Button, Card, Empty, ErrorBox, Field, Loading, Notice, PageHead, Tag, When } from "../components/ui";
import { Icon } from "../components/icons";

export function useBackups() {
  return useQuery({
    queryKey: ["backups"],
    queryFn: () => unwrap(api.GET("/backups")),
    refetchInterval: 60_000,
  });
}

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

export function BackupsPage() {
  const q = useBackups();
  const reauth = useReauth();

  return (
    <div className="page">
      <PageHead
        crumbs={
          <>
            <Link to="/">Projetos</Link> / <strong>Backups</strong>
          </>
        }
        title="Backups da plataforma"
      >
        O banco interno do Cobogó (projetos, histórico, variáveis cifradas) é copiado todo dia. Os dados de cada projeto têm o backup na aba "Dados" dele.
      </PageHead>

      <div className="stack">
        <Notice tone="warning" icon="shield" title="O que este backup protege, e o que não">
          <span>
            As cópias ficam <strong>na mesma VM</strong>: protegem de um erro, <strong>não da perda do disco</strong>. A cópia que vale é a que você <strong>baixa e guarda fora daqui</strong>. O arquivo leva as variáveis dos projetos <strong>cifradas</strong>; a chave que as abre (<span className="mono">COBOGO_SECRET_KEY</span>, no <span className="mono">.env</span> da VM) <strong>não está nele</strong> e deve ser guardada à parte, em outro lugar: sem ela, as variáveis de uma restauração não voltam.
          </span>
        </Notice>

        {q.isLoading && <Loading />}
        {q.isError && <ErrorBox error={q.error} onRetry={() => q.refetch()} />}
        {q.data && <StatusCard view={q.data} />}
        {q.data?.configured && (
          <>
            {!reauth.unlocked ? (
              <ReauthForm title="Digite a sua senha para baixar backups" reason="Um backup tem os segredos de todos os projetos, cifrados. Por isso a senha é pedida de novo." onDone={reauth.refresh} />
            ) : (
              <Notice tone="success">
                <span>
                  Senha liberada por mais <strong>{clock(reauth.left)}</strong>: os downloads abaixo funcionam.
                </span>
              </Notice>
            )}
            <FilesCard files={q.data.backups} total={q.data.total_bytes} unlocked={reauth.unlocked} />
            <RetentionCard />
          </>
        )}
      </div>
    </div>
  );
}

function StatusCard({ view }: { view: BackupsView }) {
  const health = backupHealth(view);
  const h = HEALTH_TEXT[health];
  return (
    <Card title="Estado" icon="shieldCheck" actions={<Badge tone={h.tone}>{h.label}</Badge>}>
      <div className="stack">
        {health === "unavailable" && (
          <Notice tone="warning">O painel não enxerga o volume de backups. Confira se o serviço <span className="mono">backup</span> está na pilha e se a API monta o volume (veja o roteiro em deploy/README.md).</Notice>
        )}
        {(health === "stale" || health === "never") && (
          <Notice tone="danger" title="Sem backup recente">
            {health === "never" ? "O serviço ainda não fez nenhum backup." : <>O último backup bem-sucedido tem mais de {view.stale_after.replace("h0m0s", " horas")}.</>} Veja o log do serviço (<span className="mono">docker compose logs backup</span>) e rode um agora: <span className="mono">docker compose run --rm backup once manual</span>.
          </Notice>
        )}
        {view.status.last_error && <Notice tone="warning" title="A última tentativa falhou">{view.status.last_error}</Notice>}
        <div className="grid-2">
          <div className="inset stack-sm">
            <span className="label">Último backup bom</span>
            <span className="strong">{view.status.last_ok_at ? <When iso={view.status.last_ok_at} /> : "—"}</span>
            <span className="small muted mono wrap">{view.status.last_file || ""}</span>
          </div>
          <div className="inset stack-sm">
            <span className="label">Próximo</span>
            <span className="strong">{view.status.next_run_at ? <When iso={view.status.next_run_at} /> : "—"}</span>
            <span className="small muted">
              {view.backups.length} arquivo(s), {bytes(view.total_bytes)} ao todo
            </span>
          </div>
        </div>
      </div>
    </Card>
  );
}

function FilesCard({ files, total, unlocked }: { files: BackupFile[]; total: number; unlocked: boolean }) {
  return (
    <section className="card card-flush" aria-label="Arquivos de backup">
      <header className="card-head">
        <h2 className="card-title">
          <Icon name="server" size={20} className="card-icon" /> Arquivos
        </h2>
        <span className="meta">{bytes(total)}</span>
      </header>
      {files.length === 0 ? (
        <Empty title="Nenhum backup ainda." icon="server">
          O serviço faz o primeiro assim que sobe, e depois todo dia à hora marcada.
        </Empty>
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Arquivo</th>
                <th scope="col">Quando</th>
                <th scope="col">Tamanho</th>
                <th scope="col">SHA-256</th>
                <th scope="col" />
              </tr>
            </thead>
            <tbody>
              {files.map((f) => (
                <tr key={f.name}>
                  <td className="mono small wrap">
                    {f.name} {!isScheduled(f) && <Tag>antes de atualizar</Tag>}
                  </td>
                  <td>
                    <When iso={f.created_at} />
                  </td>
                  <td>{bytes(f.size_bytes)}</td>
                  <td className="mono small" title={f.sha256}>
                    {f.sha256 ? f.sha256.slice(0, 12) : "—"}
                  </td>
                  <td className="row-end">
                    {unlocked ? (
                      <a className="btn btn-secondary" href={downloadUrl(f.name)} download={f.name}>
                        <Icon name="download" size={16} /> Baixar
                      </a>
                    ) : (
                      <Button variant="secondary" icon="download" disabled title="Digite a senha de novo acima">
                        Baixar
                      </Button>
                    )}
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

const KEYS: { key: string; label: string; hint: string; min: number; max: number }[] = [
  { key: "backup_keep_daily", label: "Dias guardados", hint: "O mais novo de cada um dos últimos N dias.", min: 1, max: 90 },
  { key: "backup_keep_weekly", label: "Semanas guardadas", hint: "O mais novo de cada uma das últimas N semanas (0 desliga).", min: 0, max: 52 },
  { key: "backup_project_keep", label: "Backups por projeto", hint: "Quantos backups de dados cada projeto guarda.", min: 1, max: 20 },
];

/** A retenção: padrão 7 dias, 4 semanas e 3 backups por projeto; o serviço lê o valor a cada execução. */
function RetentionCard() {
  const qc = useQueryClient();
  const settings = useQuery({ queryKey: ["settings"], queryFn: () => unwrap(api.GET("/settings")), staleTime: 60_000 });
  const [draft, setDraft] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState<string | null>(null);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);

  async function save(key: string) {
    const n = Number(draft[key]);
    setBusy(key);
    setMsg(null);
    try {
      await unwrap(api.PUT("/settings/{key}", { params: { path: { key: key as never } }, body: { value: n } }));
      await qc.invalidateQueries({ queryKey: ["settings"] });
      setDraft((d) => {
        const { [key]: _gone, ...rest } = d;
        return rest;
      });
      setMsg({ tone: "success", text: "Salvo. Vale a partir do próximo backup." });
    } catch (e) {
      setMsg({ tone: "danger", text: errText(e) });
    } finally {
      setBusy(null);
    }
  }

  return (
    <Card title="Retenção" icon="sliders">
      <div className="stack">
        {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
        <div className="grid-2">
          {KEYS.map((k) => {
            const current = numberSetting(settings.data?.settings, k.key);
            const value = draft[k.key] ?? (current !== undefined ? String(current) : "");
            const dirty = draft[k.key] !== undefined && Number(draft[k.key]) !== current;
            return (
              <Field key={k.key} label={k.label} htmlFor={k.key} hint={k.hint}>
                <div className="row">
                  <input id={k.key} type="number" min={k.min} max={k.max} value={value} onChange={(e) => setDraft({ ...draft, [k.key]: e.target.value })} style={{ maxWidth: 110 }} />
                  <Button variant="secondary" busy={busy === k.key} disabled={!dirty || busy !== null} onClick={() => save(k.key)}>
                    Salvar
                  </Button>
                </div>
              </Field>
            );
          })}
        </div>
      </div>
    </Card>
  );
}
