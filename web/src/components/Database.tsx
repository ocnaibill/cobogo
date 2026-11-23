// PostgreSQL auxiliar (DEC-42): a escolha do destino do banco, o estado do banco
// gerenciado e a exclusão. Criar um banco é sempre uma escolha da pessoa.
import { useState, type FormEvent } from "react";
import { Link } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type Schemas } from "../api/client";
import { useBackupGate } from "./ProjectBackups";
import { bytes } from "../lib/format";
import { Badge, Button, Card, ErrorBox, Field, Loading, Notice, Tag } from "./ui";
import { Icon } from "./icons";

export type DatabaseView = Schemas["DatabaseView"];

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** O banco relacional do projeto (sem engine) ou o Redis (`redis`, DEC-50 C). */
export function useDatabase(projectId: string, engine?: "redis") {
  return useQuery({
    queryKey: ["database", projectId, engine ?? ""],
    queryFn: () => unwrap(api.GET("/projects/{id}/database", { params: { path: { id: projectId }, query: engine ? { engine } : {} } })),
  });
}

type Choice = "managed" | "external" | "none";

const ENGINE_LABEL: Record<string, string> = { postgres: "PostgreSQL", mysql: "MySQL", redis: "Redis" };
const engineLabel = (engine?: string) => ENGINE_LABEL[engine ?? "postgres"] ?? "banco";

const CHOICE_TEXT = (engine?: string): Record<Choice, { title: string; text: string }> => ({
  managed: {
    title: `Criar um ${engineLabel(engine)} do Cobogó e conectar`,
    text:
      engine === "redis"
        ? "Um Redis só deste projeto, na rede privada dele, com senha, sem TLS e com a persistência em volume (AOF), ligado às variáveis acima. Começa vazio."
        : engine === "mysql"
          ? "Um banco só deste projeto, na rede privada dele e sem TLS (o TLS do MySQL entra depois), com os dados em volume, ligado às variáveis acima. Começa vazio."
          : "Um banco só deste projeto, com TLS e os dados em volume, ligado às variáveis acima. Começa vazio.",
  },
  external: { title: "Usar um banco que já tenho", text: "Você informa os dados do seu banco, como segredo, na aba Variáveis." },
  none: { title: "Não preciso de banco", text: "O deploy segue sem perguntar de novo." },
});

type Style = "url" | "parts";

/** Os papéis de cada estilo, na ordem em que aparecem; `required` precisam de um nome. */
const ROLES: Record<Style, { role: string; label: string; required: boolean; hint?: string }[]> = {
  url: [
    { role: "url", label: "URL de conexão", required: true },
    { role: "ca", label: "Certificado (CA)", required: false, hint: "Só se o código lê a CA do banco numa variável própria." },
  ],
  parts: [
    { role: "host", label: "Host", required: true },
    { role: "port", label: "Porta", required: false },
    { role: "user", label: "Usuário", required: false },
    { role: "password", label: "Senha", required: true },
    { role: "database", label: "Banco", required: false },
    { role: "ca", label: "Certificado (CA)", required: false, hint: "Só se o código lê a CA do banco numa variável própria." },
  ],
};

const PARTS_DEFAULT: Record<string, string> = { host: "POSTGRES_HOST", port: "POSTGRES_PORT", user: "POSTGRES_USER", password: "POSTGRES_PASSWORD", database: "POSTGRES_DB", ca: "" };

/** Os nomes de variável por papel de um estado inicial: o que a análise leu, o vínculo atual ou o padrão. */
function initialNames(view: DatabaseView): { style: Style; names: Record<string, string> } {
  const vars = view.service?.variables ?? view.need?.variables ?? {};
  const style: Style = view.need?.style === "parts" || ("host" in vars && "password" in vars) ? "parts" : "url";
  if (Object.keys(vars).length > 0) return { style, names: { ...vars } };
  return { style, names: style === "parts" ? { ...PARTS_DEFAULT } : { url: view.variable || "DATABASE_URL" } };
}

/** Os nomes de variável de uma conexão, em ordem de leitura (sem o certificado). */
export function connectionVariableNames(vars: Record<string, string> | undefined): string[] {
  return ["url", "host", "port", "user", "password", "database"].map((r) => vars?.[r]).filter((n): n is string => !!n);
}

/** As escolhas do destino do banco (HEU-06.6) e os nomes das variáveis que o código lê (DEC-44). Responder reenfileira o deploy que esperava. */
export function DatabaseChoice({ projectId, view, onChosen }: { projectId: string; view: DatabaseView; onChosen?: () => void }) {
  const qc = useQueryClient();
  const initial = initialNames(view);
  const [style, setStyle] = useState<Style>(initial.style);
  const [names, setNames] = useState<Record<string, string>>(initial.names);
  const [busy, setBusy] = useState<Choice | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [external, setExternal] = useState<string[] | null>(null);

  const roles = ROLES[style];
  const clean = (): Record<string, string> => {
    const out: Record<string, string> = {};
    for (const { role } of roles) {
      const n = (names[role] ?? "").trim();
      if (n) out[role] = n;
    }
    return out;
  };
  const complete = roles.every(({ role, required }) => !required || (names[role] ?? "").trim() !== "");

  function changeStyle(next: Style) {
    setStyle(next);
    setNames((cur): Record<string, string> => {
      const ca = cur.ca ?? "";
      if (next === "parts") return Object.assign({}, PARTS_DEFAULT, { ca });
      return { url: view.need?.variables?.url ?? "DATABASE_URL", ca };
    });
  }

  async function choose(choice: Choice) {
    setBusy(choice);
    setError(null);
    try {
      const variables = clean();
      await unwrap(api.POST("/projects/{id}/database", { params: { path: { id: projectId } }, body: { choice, engine: view.engine, variables } }));
      qc.invalidateQueries({ queryKey: ["database", projectId] });
      qc.invalidateQueries({ queryKey: ["env", projectId] });
      qc.invalidateQueries({ queryKey: ["persistent", projectId] });
      setExternal(choice === "external" ? connectionVariableNames(variables) : null);
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
      <div className="stack-sm">
        <span className="label">Como o código lê a conexão</span>
        <div className="options" role="radiogroup" aria-label="Estilo da conexão">
          {(["url", "parts"] as Style[]).map((st) => (
            <label key={st} className={`option row-between${style === st ? " selected" : ""}`}>
              <span className="stack-sm">
                <span className="strong">{st === "url" ? "Uma URL só" : "Em partes"}</span>
                <span className="muted small">{st === "url" ? "Uma variável com a URL completa, como DATABASE_URL." : "Host, porta, usuário, senha e banco em variáveis separadas, como POSTGRES_HOST."}</span>
              </span>
              <input type="radio" name="db-style" checked={style === st} onChange={() => changeStyle(st)} style={{ width: "auto" }} />
            </label>
          ))}
        </div>
        {view.need?.style === style && view.need.evidence.length > 0 && <span className="small faint">Lido do código: {view.need.evidence[0]}.</span>}
      </div>
      <div className="grid-2">
        {roles.map(({ role, label, required, hint }) => (
          <Field key={role} label={label + (required ? "" : " (opcional)")} htmlFor={`db-${role}`} hint={hint ?? "O nome da variável de ambiente que o código lê."}>
            <input id={`db-${role}`} className="mono" value={names[role] ?? ""} onChange={(e) => setNames({ ...names, [role]: e.target.value })} spellCheck={false} autoComplete="off" placeholder={required ? "" : "não usar"} />
          </Field>
        ))}
      </div>
      <div className="options" role="group" aria-label="Destino do banco">
        {(Object.keys(CHOICE_TEXT(view.engine)) as Choice[]).map((c) => (
          <div key={c} className="option row-between">
            <div className="stack-sm">
              <span className="strong">{CHOICE_TEXT(view.engine)[c].title}</span>
              <span className="muted small">{CHOICE_TEXT(view.engine)[c].text}</span>
            </div>
            <Button variant={c === "managed" ? "primary" : "secondary"} busy={busy === c} disabled={busy !== null || !complete} onClick={() => choose(c)}>
              Escolher
            </Button>
          </div>
        ))}
      </div>
      {external && (
        <Notice tone="info">
          <span>
            Agora defina {external.map((n, i) => (
              <span key={n}>
                {i > 0 && ", "}
                <span className="mono">{n}</span>
              </span>
            ))}{" "}
            com os dados do seu banco na <Link to={`/projetos/${projectId}?aba=variaveis`}>aba Variáveis</Link>.
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
export function DatabaseTab({ projectId, slug, engine }: { projectId: string; slug: string; engine?: "redis" }) {
  const q = useDatabase(projectId, engine);
  if (q.isLoading) return <Loading />;
  if (q.isError) return <ErrorBox error={q.error} onRetry={() => q.refetch()} />;
  const v = q.data!;
  const svc = v.service;

  return (
    <div className="stack">
      <Notice tone="info" icon="shield" title={engine === "redis" ? "O Redis é uma escolha sua" : "O banco é uma escolha sua"}>
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
        <Card title={engine === "redis" ? (v.need ? "Onde está o Redis?" : "Criar um Redis") : v.need ? "Onde está o banco?" : "Criar um banco"} icon="server">
          {!v.need && <p className="small muted card-text">A análise não achou uso de {engineLabel(v.engine)} neste projeto. Se ele usa um, você pode criar o banco aqui mesmo.</p>}
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
                  Você usa um banco que já tem, em <span className="mono">{connectionVariableNames(svc.variables).join(", ") || svc.variable}</span>. Defina os valores na <Link to={`/projetos/${projectId}?aba=variaveis`}>aba Variáveis</Link>.
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
  // O banco tem dados a perder: a exclusão pede um backup antes (DEC-47 item 10).
  const gate = useBackupGate(projectId, true, "o banco");

  async function remove(ev: FormEvent) {
    ev.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await unwrap(api.DELETE("/projects/{id}/database", { params: { path: { id: projectId } }, body: { confirm_slug: typed, engine: svc.engine } }));
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
    <Card title={`${engineLabel(svc.engine)} do projeto`} icon="server" actions={<Badge tone={badge.tone}>{badge.label}</Badge>}>
      <div className="stack">
        {svc.status === "failed" && <Notice tone="danger" title="O banco não ficou pronto">{svc.status_detail || "Veja o log do deploy."}</Notice>}
        {done && <Notice tone="success">A exclusão foi pedida; o worker a executa em instantes.</Notice>}
        <div className="grid-2">
          <div className="inset stack-sm">
            <span className="label">Conexão</span>
            <span className="mono small wrap">{connectionVariableNames(svc.variables).join(", ") || svc.variable}</span>
            <span className="small muted">
              Ligada à aplicação como <Badge tone="info">vinculada</Badge>. A senha e a URL nunca aparecem no painel.
            </span>
            {svc.engine !== "postgres" ? (
              <span className="small muted">
                <Badge tone="neutral">sem TLS</Badge> O serviço só existe na rede privada do projeto, sem porta pública. O TLS entra numa próxima versão.
              </span>
            ) : svc.tls ? (
              <span className="small muted">
                <Badge tone="success">TLS</Badge> O banco tem certificado próprio. A aplicação recebe a CA em <span className="mono">/etc/cobogo/db-ca.pem</span>, apontada por <span className="mono">NODE_EXTRA_CA_CERTS</span> e <span className="mono">PGSSLROOTCERT</span>
                {svc.variables?.ca ? (
                  <>
                    , e em <span className="mono">{svc.variables.ca}</span>, entregue na execução
                  </>
                ) : null}
                .
              </span>
            ) : (
              <span className="small muted">
                <Badge tone="neutral">sem TLS</Badge> Este banco foi criado antes do TLS e não é migrado. Para ter TLS, apague-o e crie de novo.
              </span>
            )}
          </div>
          <div className="inset stack-sm">
            <span className="label">Dados</span>
            <span className="strong">{view.volume ? bytes(view.volume.size_bytes) : "—"}</span>
            <span className="small muted">
              {svc.engine === "redis" ? "Redis 8, 256 MiB, persistência AOF" : svc.engine === "mysql" ? "MySQL 8.4 (LTS), 512 MiB" : "PostgreSQL 16, 256 MiB"}, só na rede do projeto, sem porta pública.{" "}
              {svc.engine === "redis" ? "Filas e caches são transitórios: o backup do projeto não inclui o Redis." : "Os dados entram no backup do projeto."}
            </span>
          </div>
        </div>
        <div className="small muted">
          <Icon name="alert" size={14} />{" "}
          {svc.engine === "redis" ? "O Redis nasce vazio e só aceita conexões com a senha, que o Cobogó entrega à aplicação nas variáveis acima." : "O banco nasce vazio. O Cobogó não roda migrações: a aplicação precisa criar as tabelas ao subir."}
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
            {gate.panel}
            <Field label={`Digite ${slug}`} htmlFor="confirm-db-slug">
              <input id="confirm-db-slug" className="mono" value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} autoFocus />
            </Field>
            {error && <Notice tone="danger">{error}</Notice>}
            <div className="row-end">
              <Button variant="ghost" onClick={() => { setOpen(false); setTyped(""); setError(null); }}>
                Cancelar
              </Button>
              <Button type="submit" variant="danger" busy={busy} disabled={typed !== slug || !gate.allowed}>
                Apagar o banco de {slug}
              </Button>
            </div>
          </form>
        )}
      </div>
    </Card>
  );
}

/** A aba de serviços: o banco relacional e, se o projeto usa ou tem, o Redis (DEC-50). */
export function ServicesTab({ projectId, slug }: { projectId: string; slug: string }) {
  const redis = useDatabase(projectId, "redis");
  const hasRedis = !!(redis.data?.need || redis.data?.service);
  return (
    <div className="stack">
      <DatabaseTab projectId={projectId} slug={slug} />
      {hasRedis && <DatabaseTab projectId={projectId} slug={slug} engine="redis" />}
    </div>
  );
}

