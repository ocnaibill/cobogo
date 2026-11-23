// Logs de execução (DEC-46): as regras do painel sobre as linhas que a API devolve.
import type { Schemas } from "../api/client";

export type LogLine = Schemas["LogLine"];
export type LogOrigin = "" | "app" | "database" | "postgres" | "mysql" | "redis";

/** Quantas linhas o painel guarda em memória; as mais antigas saem quando passa. */
export const MAX_IN_MEMORY = 4000;

/** Acrescenta linhas novas (ordem crescente de id) sem repetir, e corta o excesso do começo. */
export function appendLines(current: LogLine[], incoming: LogLine[], cap = MAX_IN_MEMORY): LogLine[] {
  if (incoming.length === 0) return current;
  const last = current.at(-1)?.id ?? -1;
  const fresh = incoming.filter((l) => l.id > last);
  if (fresh.length === 0) return current;
  const merged = current.concat(fresh);
  return merged.length > cap ? merged.slice(merged.length - cap) : merged;
}

/** Põe linhas mais antigas no começo, sem repetir. */
export function prependLines(current: LogLine[], older: LogLine[]): LogLine[] {
  if (older.length === 0) return current;
  const first = current[0]?.id ?? Number.MAX_SAFE_INTEGER;
  const fresh = older.filter((l) => l.id < first);
  return fresh.concat(current);
}

/** Só as linhas que contêm o texto (sem diferenciar maiúsculas). Texto vazio devolve todas. */
export function filterLines(lines: LogLine[], text: string): LogLine[] {
  const q = text.trim().toLowerCase();
  if (!q) return lines;
  return lines.filter((l) => l.line.toLowerCase().includes(q));
}

/** A linha é a primeira de um contêiner novo (a aplicação foi reiniciada ou trocada por um deploy)? */
export function startsNewContainer(prev: LogLine | undefined, line: LogLine): boolean {
  return !!prev && prev.origin === line.origin && prev.container_id !== line.container_id;
}

/** HH:MM:SS.mmm no horário local. */
export function clockTime(iso: string): string {
  const d = new Date(iso);
  const p = (n: number, w = 2) => String(n).padStart(w, "0");
  return `${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}.${p(d.getMilliseconds(), 3)}`;
}

/** O texto das linhas como um log, para copiar. */
export function asText(lines: LogLine[]): string {
  return lines.map((l) => `${l.at} ${l.stream === "stderr" ? "ERR" : l.stream === "system" ? "SYS" : "OUT"} ${l.line}`).join("\n");
}
