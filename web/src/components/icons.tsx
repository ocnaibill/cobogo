// Ícones em SVG embutido: o Stitch usa Material Symbols por uma fonte de
// terceiros, que a CSP do painel não deixa carregar. Traço único, 24×24.
import type { ReactNode } from "react";

const PATHS = {
  grid: <><rect x="3" y="3" width="7" height="7" rx="1" /><rect x="14" y="3" width="7" height="7" rx="1" /><rect x="3" y="14" width="7" height="7" rx="1" /><rect x="14" y="14" width="7" height="7" rx="1" /></>,
  layers: <><path d="M12 3l9 5-9 5-9-5 9-5z" /><path d="M3 13l9 5 9-5" /></>,
  rocket: <><path d="M5 15c-1.5 1.2-2 5-2 5s3.8-.5 5-2c.7-.8.7-2.1-.1-2.9a2.1 2.1 0 0 0-2.9-.1z" /><path d="M12 15l-3-3a20 20 0 0 1 2-4 12.5 12.5 0 0 1 10-5c0 2.7-.8 7.4-6 11a20 20 0 0 1-3 1z" /><path d="M9 12H5s.5-3 2-4c1.6-1.1 5 0 5 0" /><path d="M12 15v4s3-.5 4-2c1.1-1.6 0-5 0-5" /></>,
  download: <><path d="M12 3v12" /><path d="M7 10l5 5 5-5" /><path d="M5 21h14" /></>,
  plus: <path d="M12 5v14M5 12h14" />,
  check: <path d="M5 12.5l4.5 4.5L19 7.5" />,
  checkCircle: <><circle cx="12" cy="12" r="9" /><path d="M8 12.5l2.7 2.7L16 10" /></>,
  x: <path d="M6 6l12 12M18 6L6 18" />,
  xCircle: <><circle cx="12" cy="12" r="9" /><path d="M9 9l6 6M15 9l-6 6" /></>,
  alert: <><path d="M12 3.5L21.5 20h-19L12 3.5z" /><path d="M12 10v4.5M12 17.5v.01" /></>,
  info: <><circle cx="12" cy="12" r="9" /><path d="M12 11v5M12 7.5v.01" /></>,
  help: <><circle cx="12" cy="12" r="9" /><path d="M9.6 9.3a2.5 2.5 0 1 1 3.4 2.3c-.6.3-1 .8-1 1.5v.4M12 16.8v.01" /></>,
  clock: <><circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" /></>,
  commit: <><circle cx="12" cy="12" r="3.2" /><path d="M3 12h5.8M15.2 12H21" /></>,
  branch: <><circle cx="6" cy="18" r="2.5" /><circle cx="6" cy="6" r="2.5" /><circle cx="18" cy="7" r="2.5" /><path d="M6 8.5v7M18 9.5c0 4-4 5-9.5 7" /></>,
  external: <><path d="M14 4h6v6M20 4l-9 9" /><path d="M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5" /></>,
  copy: <><rect x="9" y="9" width="11" height="11" rx="1.5" /><path d="M5 15H4.5A1.5 1.5 0 0 1 3 13.5v-9A1.5 1.5 0 0 1 4.5 3h9A1.5 1.5 0 0 1 15 4.5V5" /></>,
  refresh: <><path d="M20 11a8 8 0 0 0-14.6-4.5M4 4v4h4" /><path d="M4 13a8 8 0 0 0 14.6 4.5M20 20v-4h-4" /></>,
  search: <><circle cx="11" cy="11" r="6.5" /><path d="M20 20l-4.3-4.3" /></>,
  terminal: <><rect x="3" y="4" width="18" height="16" rx="1.5" /><path d="M7 9l3 3-3 3M13 15h4" /></>,
  logout: <><path d="M9 20H5.5A1.5 1.5 0 0 1 4 18.5v-13A1.5 1.5 0 0 1 5.5 4H9" /><path d="M16 16.5l4.5-4.5L16 7.5M20.5 12H9" /></>,
  key: <><circle cx="8" cy="15" r="4" /><path d="M10.9 12.1L20 3M16 7l2.5 2.5M18.5 4.5L21 7" /></>,
  shield: <path d="M12 3l8 3v6c0 4.8-3.4 8-8 9-4.6-1-8-4.2-8-9V6l8-3z" />,
  shieldCheck: <><path d="M12 3l8 3v6c0 4.8-3.4 8-8 9-4.6-1-8-4.2-8-9V6l8-3z" /><path d="M8.8 12.2l2.2 2.2 4.2-4.4" /></>,
  lock: <><rect x="5" y="11" width="14" height="10" rx="1.5" /><path d="M8 11V7.5a4 4 0 0 1 8 0V11" /></>,
  server: <><rect x="3" y="4" width="18" height="7" rx="1.5" /><rect x="3" y="13" width="18" height="7" rx="1.5" /><path d="M7 7.5h.01M7 16.5h.01" /></>,
  cpu: <><rect x="6" y="6" width="12" height="12" rx="1.5" /><path d="M9 2.5V6M15 2.5V6M9 18v3.5M15 18v3.5M2.5 9H6M2.5 15H6M18 9h3.5M18 15h3.5" /></>,
  globe: <><circle cx="12" cy="12" r="9" /><path d="M3 12h18M12 3c2.5 2.6 3.7 5.6 3.7 9s-1.2 6.4-3.7 9c-2.5-2.6-3.7-5.6-3.7-9S9.5 5.6 12 3z" /></>,
  arrowRight: <path d="M5 12h14M13 6l6 6-6 6" />,
  menu: <path d="M4 6h16M4 12h16M4 18h16" />,
  fileCode: <><path d="M14 3H6.5A1.5 1.5 0 0 0 5 4.5v15A1.5 1.5 0 0 0 6.5 21h11a1.5 1.5 0 0 0 1.5-1.5V8l-5-5z" /><path d="M14 3v5h5M10 12.5L8 14.5l2 2M14 12.5l2 2-2 2" /></>,
  sliders: <><path d="M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1" /><circle cx="15" cy="6" r="2" /><circle cx="9" cy="12" r="2" /><circle cx="17" cy="18" r="2" /></>,
  sparkle: <><path d="M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3z" /><path d="M19 16l.8 2.2L22 19l-2.2.8L19 22l-.8-2.2L16 19l2.2-.8L19 16z" /></>,
  user: <><circle cx="12" cy="8" r="4" /><path d="M4 21a8 8 0 0 1 16 0" /></>,
  box: <><path d="M21 8l-9-5-9 5v8l9 5 9-5V8z" /><path d="M3 8l9 5 9-5M12 13v8" /></>,
  activity: <path d="M3 12h4l3-8 4 16 3-8h4" />,
  lockClock: <><rect x="4" y="11" width="12" height="10" rx="1.5" /><path d="M7 11V7.5a3 3 0 0 1 6 0V11" /><circle cx="18" cy="17" r="4" /><path d="M18 15.3V17l1.2.8" /></>,
  route: <><circle cx="6" cy="19" r="2.5" /><circle cx="18" cy="5" r="2.5" /><path d="M8.5 19H16a3.5 3.5 0 0 0 0-7H8a3.5 3.5 0 0 1 0-7h7.5" /></>,
  history: <><path d="M3.5 12a8.5 8.5 0 1 0 2.5-6L3.5 8.5" /><path d="M3.5 4v4.5H8M12 7.5V12l3 2" /></>,
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof PATHS;

export function Icon({ name, size = 18, className, label }: { name: IconName; size?: number; className?: string; label?: string }) {
  return (
    <svg
      className={`icon ${className ?? ""}`}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
    >
      {PATHS[name]}
    </svg>
  );
}
