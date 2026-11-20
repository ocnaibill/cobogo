// TEL-03 — Importar repositório: registra o projeto e já pede o primeiro deploy;
// a análise acontece no worker e a tela de acompanhamento mostra o resto. O
// quadro à direita descreve o que o pipeline realmente faz, nesta ordem.
import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, idempotencyKey, unwrap } from "../api/client";
import { Button, Card, Field, Notice, PageHead, Tag } from "../components/ui";
import { Icon, type IconName } from "../components/icons";
import { useAppsDomain } from "../hooks/useInstance";

const SLUG = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

function suggestSlug(url: string): string {
  const last = url.trim().replace(/\.git$/, "").split("/").filter(Boolean).pop() ?? "";
  return last.toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63);
}

/** As quatro fases que o usuário atravessa, do envio à publicação. */
export function FlowStrip({ current }: { current: 1 | 2 | 3 | 4 }) {
  const steps = ["Origem do repositório", "Análise heurística", "Pendências", "Publicação"];
  return (
    <ol className="flow" aria-label="Fases">
      {steps.map((label, i) => {
        const n = i + 1;
        const cls = n < current ? "flow-done" : n === current ? "flow-current" : "";
        return (
          <li key={label} className={`flow-step ${cls}`} aria-current={n === current ? "step" : undefined}>
            <span className="flow-num">{n < current ? <Icon name="check" size={16} /> : String(n).padStart(2, "0")}</span>
            <span className="flow-text">
              <span className="label">Fase {String(n).padStart(2, "0")}</span>
              <strong className="truncate">{label}</strong>
            </span>
          </li>
        );
      })}
    </ol>
  );
}

const PIPELINE: { tag: string; text: string }[] = [
  { tag: "clone", text: "git clone --depth 1 --single-branch, sem tags; o commit fica fixado" },
  { tag: "análise", text: "lê package.json, lockfiles e .env.example; nada do repositório é executado" },
  { tag: "pergunta", text: "se faltar uma decisão (porta, comando), o deploy para e pergunta" },
  { tag: "plano", text: "gera um Dockerfile próprio; o do repositório não é usado" },
  { tag: "build", text: "constrói a imagem com teto de memória e CPU" },
  { tag: "início", text: "sobe o contêiner sem capabilities do kernel, com limites e rede própria" },
  { tag: "verificação", text: "confere a resposta HTTP antes de publicar" },
  { tag: "rota", text: "publica no endereço do projeto e troca o tráfego de uma vez" },
];

function Guarantee({ icon, children }: { icon: IconName; children: string }) {
  return (
    <div className="tile row">
      <Icon name={icon} size={16} className="card-icon" />
      <span className="small">{children}</span>
    </div>
  );
}

export function ImportPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const apps = useAppsDomain();
  const [url, setUrl] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [root, setRoot] = useState("");
  const [error, setError] = useState<{ field?: "url" | "slug" | "root"; message: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const effectiveSlug = slugTouched ? slug : suggestSlug(url);
  const slugInvalid = effectiveSlug.length > 0 && !SLUG.test(effectiveSlug);
  const publicUrl = apps.urlFor(effectiveSlug);

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const project = await unwrap(
        api.POST("/projects", { body: { slug: effectiveSlug, repository_url: url.trim(), root_directory: root.trim() } }),
      );
      const dep = await unwrap(
        api.POST("/projects/{id}/deployments", { params: { path: { id: project.id } }, body: { idempotency_key: idempotencyKey() } }),
      );
      qc.invalidateQueries({ queryKey: ["projects"] });
      navigate(`/deploys/${dep.id}`);
    } catch (e) {
      if (e instanceof ApiError) {
        const m = e.message;
        const field = /slug/i.test(m) ? "slug" : /root_directory|diretório/i.test(m) ? "root" : /host|reposit|url|https/i.test(m) ? "url" : undefined;
        setError({ field, message: e.code === "conflict" ? "Já existe um projeto com esse nome. Escolha outro." : m });
      } else setError({ message: String(e) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="page">
      <PageHead
        crumbs={
          <>
            <Link to="/">Projetos</Link> / <strong>Importar repositório</strong>
          </>
        }
        title="Importar repositório"
      >
        O Cobogó clona, analisa sem executar nada e propõe um plano. Quando faltar uma informação, ele pergunta em vez de adivinhar.
      </PageHead>

      <FlowStrip current={1} />

      <div className="split-even">
        <Card title="Origem do repositório" icon="branch" actions={<Tag>HTTPS público</Tag>}>
          <form className="stack" onSubmit={submit} noValidate>
            <Field
              label="URL do repositório"
              htmlFor="repo-url"
              icon="globe"
              error={error?.field === "url" ? error.message : undefined}
              hint="GitHub, GitLab, Codeberg, Bitbucket ou sourcehut (e os hosts extras configurados)."
            >
              <input
                id="repo-url"
                className="mono"
                type="url"
                inputMode="url"
                placeholder="https://github.com/dono/repositorio"
                value={url}
                onChange={(e) => setUrl(e.target.value)}
                required
                autoFocus
                spellCheck={false}
              />
            </Field>
            <div className="grid-2">
              <Field
                label="Nome do projeto"
                htmlFor="slug"
                icon="box"
                error={error?.field === "slug" ? error.message : slugInvalid ? "Use letras minúsculas, números e hífen (até 63)." : undefined}
                hint="Vira o nome do endereço público."
              >
                <input
                  id="slug"
                  className="mono"
                  value={effectiveSlug}
                  onChange={(e) => {
                    setSlugTouched(true);
                    setSlug(e.target.value.toLowerCase());
                  }}
                  required
                  spellCheck={false}
                />
              </Field>
              <Field label="Diretório raiz" htmlFor="root" icon="layers" aside="monorrepo" error={error?.field === "root" ? error.message : undefined} hint="Vazio é a raiz. Ex.: apps/web">
                <input id="root" className="mono" value={root} onChange={(e) => setRoot(e.target.value)} placeholder="./" spellCheck={false} />
              </Field>
            </div>
            <div className="inset row-between">
              <span className="row small">
                <Icon name="route" size={16} className="card-icon" /> Endereço público
              </span>
              <span className="mono small wrap">
                {publicUrl ? (slugInvalid ? "—" : publicUrl) : apps.loaded ? "sem domínio configurado: só pela porta no servidor" : "…"}
              </span>
            </div>
            {error && !error.field && <Notice tone="danger">{error.message}</Notice>}
            <div className="form-actions">
              <span className="muted small">Repositórios privados e Dockerfiles prontos ainda não fazem parte desta versão.</span>
              <Button type="submit" variant="primary" className="btn-lg" busy={busy} disabled={!url || !effectiveSlug || slugInvalid}>
                Analisar e publicar <Icon name="arrowRight" size={18} />
              </Button>
            </div>
          </form>
        </Card>

        <div className="stack">
          <div className="term">
            <div className="term-bar">
              <span className="term-lights" aria-hidden="true">
                <span />
                <span />
                <span />
              </span>
              <span className="term-title">o que acontece ao enviar</span>
              <span className="term-status">worker</span>
            </div>
            <div className="term-body short">
              {PIPELINE.map((s, i) => (
                <div key={s.tag} className="term-line term-step">
                  <span className="term-time">{String(i + 1).padStart(2, "0")}</span>
                  <span className="term-tag">{s.tag}</span>
                  <span className="term-text">{s.text}</span>
                </div>
              ))}
            </div>
            <div className="term-foot">
              <span>cada passo aparece ao vivo na tela do deploy</span>
              <span>falhou? a versão no ar continua no ar</span>
            </div>
          </div>
          <Card title="Garantias de isolamento" icon="shield">
            <div className="tiles">
              <Guarantee icon="branch">Clone raso: só o commit publicado</Guarantee>
              <Guarantee icon="search">Análise estática, sem execução</Guarantee>
              <Guarantee icon="cpu">Build e app com limites</Guarantee>
              <Guarantee icon="lock">Contêiner sem capabilities extras</Guarantee>
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}
