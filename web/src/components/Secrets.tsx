// Variáveis e segredos guiados (DEC-39): importar um .env, aceitar sugestões,
// exigir variáveis e ver o histórico. Nada aqui recebe um valor guardado de
// volta; o texto colado vive só neste estado e é limpo ao importar.
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "../api/client";
import { ACTION_LABEL, asPreview, asResult, chosen, defaultSelection, describeChange, toggle, type ImportPreview, type ImportResult } from "../lib/secrets";
import { Badge, Button, Card, Empty, ErrorBox, Field, Loading, Notice, Tag, When } from "./ui";
import { Icon } from "./icons";

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** Cola um .env, revê o que mudaria (sem valores) e importa só o que for marcado. */
export function ImportEnv({ projectId }: { projectId: string }) {
  const qc = useQueryClient();
  const [text, setText] = useState("");
  const [preview, setPreview] = useState<ImportPreview | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [result, setResult] = useState<ImportResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<"preview" | "apply" | null>(null);

  const path = { params: { path: { id: projectId } } };

  async function review() {
    setBusy("preview");
    setError(null);
    setResult(null);
    try {
      const p = asPreview(await unwrap(api.POST("/projects/{id}/env/import", { ...path, body: { text, mode: "preview" } })));
      setPreview(p);
      setSelected(defaultSelection(p));
    } catch (e) {
      setPreview(null);
      setError(errText(e));
    } finally {
      setBusy(null);
    }
  }

  async function apply() {
    if (!preview) return;
    setBusy("apply");
    setError(null);
    try {
      const r = asResult(await unwrap(api.POST("/projects/{id}/env/import", { ...path, body: { text, mode: "apply", names: chosen(preview, selected) } })));
      setResult(r);
      setText("");
      setPreview(null);
      setSelected(new Set());
      qc.invalidateQueries({ queryKey: ["env", projectId] });
      qc.invalidateQueries({ queryKey: ["env-history", projectId] });
      qc.invalidateQueries({ queryKey: ["env-suggestions", projectId] });
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(null);
    }
  }

  function reset() {
    setText("");
    setPreview(null);
    setSelected(new Set());
    setError(null);
  }

  const count = preview ? chosen(preview, selected).length : 0;

  return (
    <Card title="Importar de um .env" icon="download">
      <div className="stack">
        {error && <Notice tone="danger">{error}</Notice>}
        {result && (
          <Notice tone="success" title={`${result.applied.length} variável(is) importada(s).`}>
            <span>
              Elas valem a partir do próximo deploy.
              {result.skipped.length > 0 && ` ${result.skipped.length} ficou(aram) de fora: ${result.skipped.map((s) => s.name).join(", ")}.`}
            </span>
          </Notice>
        )}
        {!preview && (
          <>
            <Field label="Conteúdo do .env" htmlFor="env-import-text" hint="Nada é executado nem expandido: $VAR e crases ficam como texto. O texto não é guardado; só as variáveis que você escolher, cifradas.">
              <textarea
                id="env-import-text"
                className="mono"
                rows={7}
                value={text}
                onChange={(e) => setText(e.target.value)}
                placeholder={"DATABASE_URL=postgres://…\nJWT_SECRET=…"}
                spellCheck={false}
                autoComplete="off"
                autoCapitalize="off"
              />
            </Field>
            <div>
              <Button variant="primary" icon="search" busy={busy === "preview"} disabled={!text.trim()} onClick={review}>
                Revisar antes de importar
              </Button>
            </div>
          </>
        )}
        {preview && (
          <>
            <p className="small muted">Os valores não aparecem aqui. Marque o que quer importar; o que substituiria um valor ou parece placeholder vem desmarcado.</p>
            {preview.variables.length === 0 ? (
              <Empty title="Nenhuma variável reconhecida no texto." icon="search" />
            ) : (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">Importar</th>
                      <th scope="col">Nome</th>
                      <th scope="col">O que acontece</th>
                      <th scope="col">Avisos</th>
                    </tr>
                  </thead>
                  <tbody>
                    {preview.variables.map((v) => (
                      <tr key={v.name}>
                        <td>
                          <label className="check">
                            <input
                              type="checkbox"
                              checked={selected.has(v.name) && v.action !== "blocked"}
                              disabled={v.action === "blocked"}
                              onChange={() => setSelected((s) => toggle(s, v.name))}
                              aria-label={`Importar ${v.name}`}
                            />
                          </label>
                        </td>
                        <td className="mono strong">
                          {v.name} <span className="faint small">linha {v.line}</span>
                        </td>
                        <td>
                          <Badge tone={v.action === "blocked" ? "danger" : v.action === "replace" ? "warning" : "success"}>{ACTION_LABEL[v.action]}</Badge>
                        </td>
                        <td className="small muted">{v.warnings.length === 0 ? "—" : v.warnings.map((w) => w.message).join(" · ")}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {preview.diagnostics.length > 0 && (
              <Notice tone="warning" title="Linhas ignoradas">
                <ul className="plain">
                  {preview.diagnostics.map((d, i) => (
                    <li key={i}>
                      Linha {d.line}
                      {d.name ? ` (${d.name})` : ""}: {d.message}
                    </li>
                  ))}
                </ul>
              </Notice>
            )}
            <div className="row">
              <Button variant="primary" busy={busy === "apply"} disabled={count === 0} onClick={apply}>
                Importar {count} variável(is)
              </Button>
              <Button variant="ghost" onClick={reset}>
                Cancelar e limpar
              </Button>
            </div>
          </>
        )}
      </div>
    </Card>
  );
}

/** Valores que o Cobogó conhece, oferecidos e nunca aplicados sozinhos. */
export function Suggestions({ projectId }: { projectId: string }) {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["env-suggestions", projectId], queryFn: () => unwrap(api.GET("/projects/{id}/env/suggestions", { params: { path: { id: projectId } } })) });
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  if (!q.data || q.data.suggestions.length === 0) return null;

  async function use(name: string, value: string) {
    setBusy(name);
    setError(null);
    try {
      await unwrap(api.PUT("/projects/{id}/env/{name}", { params: { path: { id: projectId, name } }, body: { value } }));
      qc.invalidateQueries({ queryKey: ["env", projectId] });
      qc.invalidateQueries({ queryKey: ["env-history", projectId] });
      qc.invalidateQueries({ queryKey: ["env-suggestions", projectId] });
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <Notice tone="info" icon="sparkle" title="O Cobogó conhece o valor de algumas variáveis">
      <div className="stack-sm">
        {error && <span className="text-danger">{error}</span>}
        {q.data.suggestions.map((s) => (
          <div key={s.name} className="row-between">
            <span>
              <span className="mono strong">{s.name}</span> <span className="small muted">→ {s.value}</span>
            </span>
            <Button icon="check" busy={busy === s.name} onClick={() => use(s.name, s.value)}>
              Usar o endereço do projeto
            </Button>
          </div>
        ))}
        <span className="small muted">Nada é aplicado sem o seu clique. O Cobogó só sugere para nomes que o código usa e que ainda não têm valor.</span>
      </div>
    </Notice>
  );
}

/** O que mudou nas variáveis, lido da auditoria: sem valores, nem os antigos. */
export function EnvHistory({ projectId }: { projectId: string }) {
  const q = useQuery({ queryKey: ["env-history", projectId], queryFn: () => unwrap(api.GET("/projects/{id}/env-history", { params: { path: { id: projectId } } })) });
  return (
    <section className="card card-flush" aria-label="Histórico das variáveis">
      <header className="card-head">
        <h2 className="card-title">
          <Icon name="history" size={20} className="card-icon" /> Histórico das variáveis
        </h2>
        <Tag title="Só quem mudou e quando; os valores antigos não são guardados">sem valores</Tag>
      </header>
      {q.isLoading && <Loading />}
      {q.isError && <ErrorBox error={q.error} onRetry={() => q.refetch()} />}
      {q.data && q.data.changes.length === 0 && (
        <Empty title="Nenhuma mudança registrada ainda." icon="history">
          Cada variável definida, gerada, importada ou removida aparece aqui, com a versão e quem fez.
        </Empty>
      )}
      {q.data && q.data.changes.length > 0 && (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Variável</th>
                <th scope="col">O que mudou</th>
                <th scope="col">Quando</th>
              </tr>
            </thead>
            <tbody>
              {q.data.changes.map((c, i) => (
                <tr key={`${c.at}-${i}`}>
                  <td className="mono strong">{c.name ?? "—"}</td>
                  <td className="small">{describeChange(c)}</td>
                  <td className="meta">
                    <When iso={c.at} />
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
