// O mapa do repositório (DEC-49): cada peça com o papel, a tecnologia e a evidência; as aplicações
// irmãs ganham o botão de criar o projeto da outra pasta (sem deploy automático).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { api, unwrap } from "../api/client";
import type { components } from "../api/schema";
import { normalizeDir, ROLE_LABEL, ROLE_TONE, siblingSlug } from "../lib/repomap";
import { Button, Card, Notice, Tag } from "./ui";

type Component = components["schemas"]["Component"];

export function RepoMap({ project, items }: { project: { id: string; slug: string; repository_url: string; root_directory: string }; items: Component[] }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [error, setError] = useState<string | null>(null);
  const projects = useQuery({
    queryKey: ["projects", "same-repo", project.repository_url],
    queryFn: () => unwrap(api.GET("/projects", { params: { query: { limit: 100 } } })),
  });
  const create = useMutation({
    mutationFn: (c: Component) => {
      const root = [normalizeDir(project.root_directory), normalizeDir(c.dir)].filter(Boolean).join("/");
      return unwrap(api.POST("/projects", { body: { slug: siblingSlug(project.slug, c.dir), repository_url: project.repository_url, root_directory: root } }));
    },
    onSuccess: (created) => {
      qc.invalidateQueries({ queryKey: ["projects"] });
      navigate(`/projetos/${created.id}`);
    },
    onError: (e) => setError(e instanceof Error ? e.message : "Não foi possível criar o projeto."),
  });
  if (items.length === 0) return null;
  const existing = (c: Component) => {
    const root = [normalizeDir(project.root_directory), normalizeDir(c.dir)].filter(Boolean).join("/");
    return projects.data?.projects?.find((x) => x.repository_url === project.repository_url && normalizeDir(x.root_directory) === root);
  };
  return (
    <Card title="Mapa do repositório" icon="layers">
      <div className="stack">
        {error && <Notice tone="danger">{error}</Notice>}
        {items.map((c, i) => {
          const other = c.sibling ? existing(c) : undefined;
          return (
            <div className="inset row-between" key={`${c.dir}-${c.role}-${c.label}-${i}`}>
              <div className="stack-sm">
                <span className="row">
                  <Tag tone={ROLE_TONE[c.role] ?? "neutral"}>{ROLE_LABEL[c.role] ?? c.role}</Tag>
                  <strong>{c.label}</strong>
                  {c.dir !== "." && <span className="mono meta">{c.dir}/</span>}
                  {!c.supported && <Tag tone="warning">fora de suporte</Tag>}
                </span>
                {c.note && <span className="meta">{c.note}</span>}
                <span className="meta mono">{c.evidence.join(" · ")}</span>
              </div>
              {c.sibling &&
                (other ? (
                  <Link to={`/projetos/${other.id}`}>projeto {other.slug}</Link>
                ) : (
                  <Button variant="primary" disabled={create.isPending} onClick={() => create.mutate(c)}>
                    Criar projeto para esta pasta
                  </Button>
                ))}
            </div>
          );
        })}
        <span className="meta">Inferido dos arquivos do repositório, sem executar nada. Criar o projeto não faz deploy.</span>
      </div>
    </Card>
  );
}
