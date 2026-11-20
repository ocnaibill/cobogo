// TEL-03 — Importar repositório: registra o projeto e já pede o primeiro deploy;
// a análise acontece no worker e a tela de acompanhamento mostra o resto.
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, idempotencyKey, unwrap } from "../api/client";
import { Button, Card, Field, Notice } from "../components/ui";

const SLUG = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

function suggestSlug(url: string): string {
  const last = url.trim().replace(/\.git$/, "").split("/").filter(Boolean).pop() ?? "";
  return last.toLowerCase().replace(/[^a-z0-9-]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 63);
}

export function ImportPage() {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [url, setUrl] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [root, setRoot] = useState("");
  const [error, setError] = useState<{ field?: "url" | "slug" | "root"; message: string } | null>(null);
  const [busy, setBusy] = useState(false);

  const effectiveSlug = slugTouched ? slug : suggestSlug(url);
  const slugInvalid = effectiveSlug.length > 0 && !SLUG.test(effectiveSlug);

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
    <div className="page narrow">
      <div className="page-head">
        <div>
          <h1>Importar repositório</h1>
          <p className="muted">O Cobogó clona o repositório, analisa sem executar nada e propõe um plano. Quando faltar uma informação, ele pergunta em vez de adivinhar.</p>
        </div>
      </div>
      <Card>
        <form className="stack" onSubmit={submit} noValidate>
          <Field
            label="URL do repositório"
            htmlFor="repo-url"
            error={error?.field === "url" ? error.message : undefined}
            hint="Repositório público por HTTPS: GitHub, GitLab, Codeberg, Bitbucket ou sourcehut (e os hosts extras configurados)."
          >
            <input id="repo-url" type="url" inputMode="url" placeholder="https://github.com/dono/repositorio" value={url} onChange={(e) => setUrl(e.target.value)} required autoFocus spellCheck={false} />
          </Field>
          <Field
            label="Nome do projeto"
            htmlFor="slug"
            error={error?.field === "slug" ? error.message : slugInvalid ? "Use letras minúsculas, números e hífen (até 63)." : undefined}
            hint={effectiveSlug ? <>Vira o endereço público: <code>{effectiveSlug}.&lt;domínio das aplicações&gt;</code></> : "Vira o endereço público da aplicação."}
          >
            <input
              id="slug"
              value={effectiveSlug}
              onChange={(e) => {
                setSlugTouched(true);
                setSlug(e.target.value.toLowerCase());
              }}
              required
              spellCheck={false}
            />
          </Field>
          <Field label="Diretório do projeto (opcional)" htmlFor="root" error={error?.field === "root" ? error.message : undefined} hint="Para monorrepositórios, por exemplo apps/web. Vazio é a raiz.">
            <input id="root" value={root} onChange={(e) => setRoot(e.target.value)} placeholder="apps/web" spellCheck={false} />
          </Field>
          {error && !error.field && <Notice tone="danger">{error.message}</Notice>}
          <div className="row-end">
            <Button type="submit" variant="primary" busy={busy} disabled={!url || !effectiveSlug || slugInvalid}>
              Analisar e publicar
            </Button>
          </div>
        </form>
      </Card>
      <p className="muted small">Repositórios privados, credenciais e Dockerfiles prontos ainda não fazem parte desta versão.</p>
    </div>
  );
}
