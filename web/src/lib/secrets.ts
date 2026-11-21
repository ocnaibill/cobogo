// Variáveis e segredos guiados (DEC-39): textos e escolhas puras, sem tela.
import type { Schemas } from "../api/client";

export type ImportPreview = Schemas["ImportPreview"];
export type ImportResult = Schemas["ImportResult"];
export type EnvChange = Schemas["EnvChange"];
export type ImportItem = ImportPreview["variables"][number];

/** A importação responde a prévia ou o resultado conforme o modo pedido. */
export const asPreview = (r: ImportPreview | ImportResult): ImportPreview => r as ImportPreview;
export const asResult = (r: ImportPreview | ImportResult): ImportResult => r as ImportResult;

/** Nomes marcados de saída: o que a prévia indica (as de risco ficam de fora). */
export function defaultSelection(preview: ImportPreview): Set<string> {
  return new Set(preview.variables.filter((v) => v.default_selected).map((v) => v.name));
}

/** Liga ou desliga um nome sem mudar o conjunto original. */
export function toggle(set: ReadonlySet<string>, name: string): Set<string> {
  const next = new Set(set);
  if (next.has(name)) next.delete(name);
  else next.add(name);
  return next;
}

/** Bloqueadas nunca entram: nem se alguém tentar marcar. */
export function chosen(preview: ImportPreview, selected: ReadonlySet<string>): string[] {
  return preview.variables.filter((v) => v.action !== "blocked" && selected.has(v.name)).map((v) => v.name);
}

export const ACTION_LABEL: Record<ImportItem["action"], string> = {
  create: "nova",
  replace: "substitui",
  blocked: "bloqueada",
};

const SOURCE_PHRASE: Record<string, string> = { user: "digitada", generated: "gerada", imported: "importada" };

/** "v3 · digitada por admin": a linha do histórico de uma variável. */
export function describeChange(c: EnvChange): string {
  const who = c.actor ? ` por ${c.actor}` : "";
  switch (c.action) {
    case "env.set":
    case "env.generate":
      return `v${c.version ?? "?"} · ${SOURCE_PHRASE[c.source ?? "user"] ?? "definida"}${who}`;
    case "env.delete":
      return `removida${who}`;
    case "env.import":
      return `importou ${c.names?.length ?? 0} variável(is)${who}`;
    case "env.required":
      return c.names && c.names.length > 0 ? `exigiu ${c.names.join(", ")}${who}` : `deixou de exigir variáveis${who}`;
    default:
      return c.action;
  }
}

/** Novas obrigatórias ao marcar ou desmarcar uma, em ordem estável. */
export function withRequired(names: readonly string[], name: string, on: boolean): string[] {
  const set = new Set(names);
  if (on) set.add(name);
  else set.delete(name);
  return [...set].sort();
}

/** As prováveis que ainda não estão exigidas. */
export function missingLikely(required: readonly string[], likely: readonly string[]): string[] {
  const have = new Set(required);
  return likely.filter((n) => !have.has(n));
}
