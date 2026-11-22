// Dados persistentes (DEC-41): os caminhos que o usuário declara, os volumes que o
// Cobogó cria para eles e os volumes que ficaram sem projeto. Nada aqui é
// inferido: quem declara é a pessoa.
import { ProjectBackupsCard } from "./ProjectBackups";
import { useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap, type Schemas } from "../api/client";
import { bytes } from "../lib/format";
import { Badge, Button, Card, Empty, ErrorBox, Field, Loading, Notice, Tag, When } from "./ui";
import { Icon } from "./icons";

export type ProjectVolume = Schemas["ProjectVolume"];
export type PersistentSuggestion = Schemas["PersistentSuggestion"];

/** Sugestões que apontam um caminho (as sem caminho são só um aviso). */
export const withPath = (list: PersistentSuggestion[]) => list.filter((s) => s.path !== "");

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** Os caminhos escritos, um por linha, sem vazios nem repetidos. */
export function parsePaths(text: string): string[] {
  return [...new Set(text.split("\n").map((l) => l.trim()).filter(Boolean))];
}

export function usePersistent(projectId: string) {
  return useQuery({ queryKey: ["persistent", projectId], queryFn: () => unwrap(api.GET("/projects/{id}/persistent-paths", { params: { path: { id: projectId } } })) });
}

/** Aba "Dados": declarar caminhos e ver os volumes. */
export function DataTab({ projectId, slug }: { projectId: string; slug: string }) {
  const qc = useQueryClient();
  const q = usePersistent(projectId);
  const [text, setText] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ tone: "success" | "danger"; text: string } | null>(null);

  if (q.isLoading) return <Loading />;
  if (q.isError) return <ErrorBox error={q.error} onRetry={() => q.refetch()} />;
  const data = q.data!;
  const current = data.paths.join("\n");
  const value = text ?? current;
  const dirty = parsePaths(value).join("\n") !== current;

  async function save() {
    setBusy(true);
    setMsg(null);
    try {
      const r = await unwrap(api.PUT("/projects/{id}/persistent-paths", { params: { path: { id: projectId } }, body: { paths: parsePaths(value) } }));
      qc.setQueryData(["persistent", projectId], r);
      qc.invalidateQueries({ queryKey: ["project", projectId] });
      qc.invalidateQueries({ queryKey: ["orphan-volumes"] });
      setText(null);
      setMsg({ tone: "success", text: "Salvo. Vale a partir do próximo deploy." });
    } catch (e) {
      setMsg({ tone: "danger", text: errText(e) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="stack">
      <Notice tone="info" icon="shield" title="Dados que sobrevivem a novos deploys">
        <span>
          Cada caminho declarado vira um volume do Cobogó. Na primeira vez ele recebe o conteúdo que a imagem tinha naquele caminho; <strong>depois disso o volume prevalece</strong>, e mudanças nesses arquivos no repositório não chegam mais ao ar. Em projeto com volume, a versão anterior para antes de a nova subir (alguns segundos fora do ar), para dois processos não escreverem no mesmo arquivo.
        </span>
      </Notice>
      {msg && <Notice tone={msg.tone}>{msg.text}</Notice>}
      <Suggestions projectId={projectId} paths={data.paths} suggestions={data.suggestions} onDone={(r) => qc.setQueryData(["persistent", projectId], r)} />
      <Card title="Dados persistentes" icon="server">
        <div className="stack">
          <Field
            label="Caminhos dentro do contêiner"
            htmlFor="persistent-paths"
            hint="Um por linha, caminhos absolutos como /app/data. Só você declara: o Cobogó não adivinha. Não vale para site estático."
          >
            <textarea
              id="persistent-paths"
              className="mono"
              rows={4}
              value={value}
              onChange={(e) => setText(e.target.value)}
              placeholder={"/app/data\n/app/content"}
              spellCheck={false}
              autoComplete="off"
            />
          </Field>
          <div className="row">
            <Button variant="primary" busy={busy} disabled={!dirty} onClick={save}>
              Salvar caminhos
            </Button>
            {dirty && (
              <Button variant="ghost" onClick={() => setText(null)}>
                Desfazer
              </Button>
            )}
          </div>
        </div>
      </Card>
      <VolumesTable volumes={data.volumes} title="Volumes do projeto" empty="Nenhum volume ainda. Eles são criados no próximo deploy, depois de você declarar um caminho." />
      <ProjectBackupsCard projectId={projectId} slug={slug} />
    </div>
  );
}

function VolumesTable({ volumes, title, empty }: { volumes: ProjectVolume[]; title: string; empty: string }) {
  return (
    <section className="card card-flush" aria-label={title}>
      <header className="card-head">
        <h2 className="card-title">
          <Icon name="box" size={20} className="card-icon" /> {title}
        </h2>
        <Tag title="O tamanho é medido a cada poucos minutos">tamanho aproximado</Tag>
      </header>
      {volumes.length === 0 ? (
        <Empty title={empty} icon="box" />
      ) : (
        <div className="table-wrap">
          <table className="table">
            <thead>
              <tr>
                <th scope="col">Caminho</th>
                <th scope="col">Volume</th>
                <th scope="col">Tamanho</th>
                <th scope="col">Situação</th>
              </tr>
            </thead>
            <tbody>
              {volumes.map((v) => (
                <tr key={v.name}>
                  <td className="mono strong">{v.path}</td>
                  <td className="mono small muted">{v.name}</td>
                  <td>{bytes(v.size_bytes)}</td>
                  <td>{v.orphan ? <Badge tone="warning">sem projeto</Badge> : <Badge tone="success">em uso</Badge>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

/** Volumes sem projeto: ocupam disco e só saem com uma ação confirmada. */
export function OrphanVolumes() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["orphan-volumes"], queryFn: () => unwrap(api.GET("/volumes/orphans")), refetchInterval: 15000 });
  const [target, setTarget] = useState<string | null>(null);
  const [typed, setTyped] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState<string | null>(null);

  const vols = q.data?.volumes ?? [];
  if (vols.length === 0 && !done) return null;

  async function remove(name: string) {
    setBusy(true);
    setError(null);
    try {
      await unwrap(api.DELETE("/volumes/{name}", { params: { path: { name } }, body: { confirm_name: name } }));
      setDone(`${name}: a exclusão foi pedida e o worker a executa em instantes.`);
      setTarget(null);
      setTyped("");
      setTimeout(() => qc.invalidateQueries({ queryKey: ["orphan-volumes"] }), 3000);
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title="Volumes sem projeto" icon="server">
      <div className="stack">
        {done && <Notice tone="success">{done}</Notice>}
        {error && <Notice tone="danger">{error}</Notice>}
        {vols.length > 0 && (
          <>
            <p className="small muted">São dados de projetos excluídos ou de caminhos que você tirou da lista. Ocupam disco até serem apagados. <strong>Apagar é irreversível.</strong></p>
            <div className="table-wrap">
              <table className="table">
                <thead>
                  <tr>
                    <th scope="col">Projeto</th>
                    <th scope="col">Caminho</th>
                    <th scope="col">Tamanho</th>
                    <th scope="col">Criado</th>
                    <th scope="col" className="num">
                      Ações
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {vols.map((v) => (
                    <tr key={v.name}>
                      <td className="strong">
                        {v.project_slug} {v.project_deleted && <Tag>excluído</Tag>}
                      </td>
                      <td className="mono">{v.path}</td>
                      <td>{bytes(v.size_bytes)}</td>
                      <td className="meta">
                        <When iso={v.created_at} />
                      </td>
                      <td>
                        {target === v.name ? (
                          <form
                            className="row-end"
                            onSubmit={(e) => {
                              e.preventDefault();
                              void remove(v.name);
                            }}
                          >
                            <input aria-label={`Digite ${v.name} para confirmar`} className="mono" placeholder={v.name} value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" spellCheck={false} />
                            <Button type="submit" variant="danger" busy={busy} disabled={typed !== v.name}>
                              Apagar
                            </Button>
                            <Button variant="ghost" onClick={() => setTarget(null)}>
                              Cancelar
                            </Button>
                          </form>
                        ) : (
                          <div className="row-end">
                            <Button variant="ghost" onClick={() => { setTarget(v.name); setTyped(""); }}>
                              Apagar…
                            </Button>
                          </div>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="small faint">Para confirmar, digite o nome do volume mostrado no campo.</p>
          </>
        )}
      </div>
    </Card>
  );
}

/** O que a análise achou no código: sugerido, nunca aplicado sem o seu clique. */
function Suggestions({ projectId, paths, suggestions, onDone }: { projectId: string; paths: string[]; suggestions: PersistentSuggestion[]; onDone: (r: Schemas["PersistentView"]) => void }) {
  const qc = useQueryClient();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  if (suggestions.length === 0) return null;

  async function use(path: string) {
    setBusy(path);
    setError(null);
    try {
      const r = await unwrap(api.PUT("/projects/{id}/persistent-paths", { params: { path: { id: projectId } }, body: { paths: [...paths, path] } }));
      onDone(r);
      qc.invalidateQueries({ queryKey: ["project", projectId] });
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <Notice tone="warning" icon="sparkle" title="O código parece guardar dados em disco">
      <div className="stack-sm">
        {error && <span className="text-danger">{error}</span>}
        <span className="small">Sem volume, o que for gravado ali some a cada deploy. É uma leitura do código, então confira antes de usar.</span>
        {suggestions.map((s, i) => (
          <div key={`${s.path}-${i}`} className="stack-sm">
            <div className="row-between">
              <span>
                {s.path ? <span className="mono strong">{s.path}</span> : <span className="strong">Caminho não encontrado</span>}{" "}
                <Badge tone={s.confidence === "strong" ? "success" : "neutral"}>{s.confidence === "strong" ? "provável" : "possível"}</Badge>
              </span>
              {s.path && (
                <Button icon="check" busy={busy === s.path} onClick={() => use(s.path)}>
                  Usar este caminho
                </Button>
              )}
            </div>
            <span className="small muted">{s.reason}</span>
            <span className="small faint mono">{s.evidence.join(" · ")}</span>
          </div>
        ))}
      </div>
    </Notice>
  );
}
