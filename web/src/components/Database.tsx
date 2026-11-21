// PostgreSQL auxiliar (DEC-42): a escolha do destino do banco, o estado do banco
// gerenciado e a exclusão. Criar um banco é sempre uma escolha da pessoa.
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type Schemas } from "../api/client";
import { bytes } from "../lib/format";
import { Badge, Button, Card, ErrorBox, Field, Loading, Notice, Tag } from "./ui";
import { Icon } from "./icons";

export type DatabaseView = Schemas["DatabaseView"];

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

export function useDatabase(projectId: string) {
  return useQuery({ queryKey: ["database", projectId], queryFn: () => unwrap(api.GET("/projects/{id}/database", { params: { path: { id: projectId } } })) });
}

type Choice = "managed" | "external" | "none";

const CHOICE_TEXT: Record<Choice, { title: string; text: string }> = {
  managed: { title: "Criar um PostgreSQL do Cobogó e conectar", text: "Um banco só deste projeto, com os dados em volume, ligado à variável de conexão. Começa vazio." },
  external: { title: "Usar um banco que já tenho", text: "Você informa a URL do seu banco, como segredo, na aba Variáveis." },
  none: { title: "Não preciso de banco", text: "O deploy segue sem perguntar de novo." },
};

/** As três escolhas do destino do banco (HEU-06.6). Responder reenfileira o deploy que esperava. */
export function DatabaseChoice({ projectId, view, onChosen }: { projectId: string; view: DatabaseView; onChosen?: () => void }) {
  const qc = useQueryClient();
  const [variable, setVariable] = useState(view.variable);
  const [busy, setBusy] = useState<Choice | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [external, setExternal] = useState(false);

  async function choose(choice: Choice) {
    setBusy(choice);
    setError(null);
    try {
      await unwrap(api.POST("/projects/{id}/database", { params: { path: { id: projectId } }, body: { choice, variable: variable.trim() || undefined } }));
      qc.invalidateQueries({ queryKey: ["database", projectId] });
      qc.invalidateQueries({ queryKey: ["env", projectId] });
      qc.invalidateQueries({ queryKey: ["persistent", projectId] });
      setExternal(choice === "external");
      onChosen?.();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <div className="stack">
      {error && <Notice tone="danger">{error}</Notice>}
      <Field label="Variável de conexão" htmlFor="db-variable" hint="É por ela que a aplicação recebe a URL do banco.">
        <input id="db-variable" className="mono" value={variable} onChange={(e) => setVariable(e.target.value)} spellCheck={false} autoComplete="off" />
      </Field>
      <div className="options" role="group" aria-label="Destino do banco">
        {(Object.keys(CHOICE_TEXT) as Choice[]).map((c) => (
          <div key={c} className="option row-between">
            <div className="stack-sm">
              <span className="strong">{CHOICE_TEXT[c].title}</span>
              <span className="muted small">{CHOICE_TEXT[c].text}</span>
            </div>
            <Button variant={c === "managed" ? "primary" : "secondary"} busy={busy === c} disabled={busy !== null || !variable.trim()} onClick={() => choose(c)}>
              Escolher
            </Button>
          </div>
        ))}
      </div>
      {external && (
        <Notice tone="info">
          <span>
            Agora defina <span className="mono">{variable}</span> com a URL do seu banco na <Link to={`/projetos/${projectId}?aba=variaveis`}>aba Variáveis</Link>.
          </span>
        </Notice>
      )}
    </div>
  );
}

const STATUS_BADGE: Record<string, { tone: "success" | "warning" | "danger" | "neutral"; label: string }> = {
  ready: { tone: "success", label: "no ar" },
  pending: { tone: "warning", label: "será criado no próximo deploy" },
  failed: { tone: "danger", label: "falhou" },
  deleting: { tone: "warning", label: "sendo apagado" },
};

/** Aba "Banco": o que a análise achou, a escolha e o banco gerenciado. */
export function DatabaseTab({ projectId, slug }: { projectId: string; slug: string }) {
  const q = useDatabase(projectId);
  if (q.isLoading) return <Loading />;
  if (q.isError) return <ErrorBox error={q.error} onRetry={() => q.refetch()} />;
  const v = q.data!;
  const svc = v.service;

  return (
    <div className="stack">
      <Notice tone="info" icon="shield" title="O banco é uma escolha sua">
        <span>
          O Cobogó nunca cria um banco só porque o código usa um driver. Quando a evidência é forte, o deploy para <strong>antes do build</strong> e pergunta; você escolhe criar um banco do Cobogó, usar um que já tem ou dizer que não precisa. A senha de um banco do Cobogó é gerada e nunca é mostrada.
        </span>
      </Notice>

      {v.need && (
        <Card title="O que a análise achou" icon="search" actions={<Tag tone={v.need.level === "required" ? "danger" : "neutral"}>{v.need.level === "required" ? "uso no código" : "só a dependência"}</Tag>}>
          <div className="stack-sm">
            <span>{v.need.reason}.</span>
            <span className="small faint mono">{v.need.evidence.join(" · ")}</span>
          </div>
        </Card>
      )}

      {!svc && (
        <Card title={v.need ? "Onde está o banco?" : "Criar um banco"} icon="server">
          {!v.need && <p className="small muted card-text">A análise não achou uso de PostgreSQL neste projeto. Se ele usa um, você pode criar o banco aqui mesmo.</p>}
          <DatabaseChoice projectId={projectId} view={v} />
        </Card>
      )}

      {svc && svc.mode === "managed" && <ManagedDatabase projectId={projectId} slug={slug} view={v} />}

      {svc && svc.mode !== "managed" && (
        <Card title="Escolha registrada" icon="server">
          <div className="stack">
            <span>
              {svc.mode === "external" ? (
                <>
                  Você usa um banco que já tem, em <span className="mono">{svc.variable}</span>. Defina a URL na <Link to={`/projetos/${projectId}?aba=variaveis`}>aba Variáveis</Link>.
                </>
              ) : (
                "O projeto não precisa de banco."
              )}
            </span>
            <details>
              <summary className="small">Mudar a escolha</summary>
              <div className="card-text">
                <DatabaseChoice projectId={projectId} view={v} />
              </div>
            </details>
          </div>
        </Card>
      )}
    </div>
  );
}

function ManagedDatabase({ projectId, slug, view }: { projectId: string; slug: string; view: DatabaseView }) {
  const qc = useQueryClient();
  const svc = view.service!;
  const badge = STATUS_BADGE[svc.status] ?? { tone: "neutral" as const, label: svc.status };
  const [open, setOpen] = useState(false);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);

  async function remove(ev: FormEvent) {
    ev.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await unwrap(api.DELETE("/projects/{id}/database", { params: { path: { id: projectId } }, body: { confirm_slug: typed } }));
      setDone(true);
      setOpen(false);
      setTyped("");
      for (const key of [["database", projectId], ["env", projectId], ["persistent", projectId]]) qc.invalidateQueries({ queryKey: key });
      setTimeout(() => qc.invalidateQueries({ queryKey: ["database", projectId] }), 4000);
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="PostgreSQL do projeto" icon="server" actions={<Badge tone={badge.tone}>{badge.label}</Badge>}>
      <div className="stack">
        {svc.status === "failed" && <Notice tone="danger" title="O banco não ficou pronto">{svc.status_detail || "Veja o log do deploy."}</Notice>}
        {done && <Notice tone="success">A exclusão foi pedida; o worker a executa em instantes.</Notice>}
        <div className="grid-2">
          <div className="inset stack-sm">
            <span className="label">Conexão</span>
            <span className="mono small wrap">{svc.variable}</span>
            <span className="small muted">
              Ligada à aplicação como <Badge tone="info">vinculada</Badge>. A URL e a senha nunca aparecem no painel.
            </span>
          </div>
          <div className="inset stack-sm">
            <span className="label">Dados</span>
            <span className="strong">{view.volume ? bytes(view.volume.size_bytes) : "—"}</span>
            <span className="small muted">PostgreSQL 16, 256 MiB, só na rede do projeto, sem porta pública. Sem backup (issue #28), perder o disco perde os dados.</span>
          </div>
        </div>
        <div className="small muted">
          <Icon name="alert" size={14} /> O banco nasce vazio. O Cobogó não roda migrações: a aplicação precisa criar as tabelas ao subir.
        </div>
        {!open ? (
          <div className="action-bar">
            <span className="small">Apagar remove o banco e <strong>todos os dados</strong>, e tira a variável de conexão.</span>
            <Button variant="danger" icon="x" onClick={() => setOpen(true)} disabled={svc.status === "deleting"}>
              Apagar o banco e os dados…
            </Button>
          </div>
        ) : (
          <form className="stack" onSubmit={remove}>
            <Notice tone="danger" title="Apagar o banco?">Os dados não podem ser recuperados. Para confirmar, digite o nome do projeto.</Notice>
            <Field label={`Digite ${slug}`} htmlFor="confirm-db-slug">
              <input id="confirm-db-slug" className="mono" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} autoFocus />
            </Field>
            {error && <Notice tone="danger">{error}</Notice>}
            <div className="row-end">
              <Button variant="ghost" onClick={() => { setOpen(false); setTyped(""); setError(null); }}>
                Cancelar
              </Button>
              <Button type="submit" variant="danger" busy={busy} disabled={typed !== slug}>
                Apagar o banco de {slug}
              </Button>
            </div>
          </form>
        )}
      </div>
    </Card>
  );
}
