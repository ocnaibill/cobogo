// Datas e números em português, sempre com a hora absoluta disponível (title).
const rtf = new Intl.RelativeTimeFormat("pt-BR", { numeric: "auto" });
const dtf = new Intl.DateTimeFormat("pt-BR", { dateStyle: "short", timeStyle: "medium" });

export function absolute(iso: string): string {
  return dtf.format(new Date(iso));
}

/** "há 3 minutos", "ontem"... */
export function relative(iso: string, now = Date.now()): string {
  const diff = (new Date(iso).getTime() - now) / 1000;
  const abs = Math.abs(diff);
  if (abs < 45) return "agora";
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour");
  if (abs < 86400 * 30) return rtf.format(Math.round(diff / 86400), "day");
  return dtf.format(new Date(iso));
}

/** Duração entre dois instantes: "1 min 12 s", "8 s". */
export function duration(fromIso: string, toIso: string): string {
  const s = Math.max(0, Math.round((new Date(toIso).getTime() - new Date(fromIso).getTime()) / 1000));
  if (s < 60) return `${s} s`;
  const m = Math.floor(s / 60);
  return s % 60 ? `${m} min ${s % 60} s` : `${m} min`;
}

export function shortSha(sha?: string): string {
  return sha ? sha.slice(0, 7) : "—";
}

/** Nome do repositório a partir da URL (github.com/dono/repo → dono/repo). */
export function repoName(url: string): string {
  try {
    const u = new URL(url);
    return `${u.host}${u.pathname.replace(/\.git$/, "")}`;
  } catch {
    return url;
  }
}
