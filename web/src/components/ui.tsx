// Peças visuais básicas. Tudo que é cor, espaço e tipografia vem dos tokens de
// styles/tokens.css (o design system das telas do Stitch).
import { useId, useState, type ButtonHTMLAttributes, type ReactNode } from "react";
import { Link } from "react-router";
import { absolute, relative } from "../lib/format";
import { isActive, STATE_LABEL, stateTone, type Tone } from "../lib/deploy";
import type { DeploymentState } from "../api/client";
import { Icon, type IconName } from "./icons";

type Variant = "primary" | "secondary" | "danger" | "ghost";

export function Button({
  variant = "secondary",
  busy = false,
  icon,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; busy?: boolean; icon?: IconName }) {
  return (
    // type="button" por padrão: dentro de um <form>, o padrão do navegador é
    // "submit", e um "Cancelar" sem tipo enviaria o formulário (a exclusão de
    // projeto foi enviada assim num teste). Quem envia pede type="submit".
    <button {...rest} type={rest.type ?? "button"} className={`btn btn-${variant} ${rest.className ?? ""}`} disabled={rest.disabled || busy} aria-busy={busy || undefined}>
      {busy ? <span className="spinner" aria-hidden="true" /> : icon && <Icon name={icon} size={16} />}
      {children}
    </button>
  );
}

/** Um link com cara de botão (navegação interna ou externa). */
export function LinkButton({ to, href, variant = "secondary", icon, iconEnd, children }: { to?: string; href?: string; variant?: Variant; icon?: IconName; iconEnd?: IconName; children: ReactNode }) {
  const inner = (
    <>
      {icon && <Icon name={icon} size={16} />}
      {children}
      {iconEnd && <Icon name={iconEnd} size={16} />}
    </>
  );
  if (href)
    return (
      <a className={`btn btn-${variant}`} href={href} target="_blank" rel="noreferrer noopener">
        {inner}
      </a>
    );
  return (
    <Link className={`btn btn-${variant}`} to={to ?? "/"}>
      {inner}
    </Link>
  );
}

export function Badge({ tone, children, title, pulse = false }: { tone: Tone; children: ReactNode; title?: string; pulse?: boolean }) {
  return (
    <span className={`badge badge-${tone}`} title={title}>
      <span className={`dot ${pulse ? "dot-pulse" : ""}`} aria-hidden="true" />
      {children}
    </span>
  );
}

export function StateBadge({ state }: { state: DeploymentState }) {
  return (
    <Badge tone={stateTone(state)} pulse={isActive(state)}>
      {STATE_LABEL[state]}
    </Badge>
  );
}

/** Rótulo curto em fonte mono: versão, porta, commit, origem. */
export function Tag({ children, tone, title }: { children: ReactNode; tone?: Tone | "brand"; title?: string }) {
  return (
    <span className={`tag ${tone ? `tag-${tone}` : ""}`} title={title}>
      {children}
    </span>
  );
}

export function When({ iso }: { iso: string }) {
  return (
    <time dateTime={iso} title={absolute(iso)}>
      {relative(iso)}
    </time>
  );
}

export function Card({
  title,
  icon,
  actions,
  children,
  className,
  flush = false,
}: {
  title?: ReactNode;
  icon?: IconName;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  flush?: boolean;
}) {
  return (
    <section className={`card ${flush ? "card-flush" : ""} ${className ?? ""}`}>
      {(title || actions) && (
        <header className="card-head">
          {title && (
            <h2 className="card-title">
              {icon && <Icon name={icon} size={20} className="card-icon" />}
              {title}
            </h2>
          )}
          {actions && <div className="card-actions">{actions}</div>}
        </header>
      )}
      {children}
    </section>
  );
}

/** Cabeçalho de página: trilha, sobretítulo, título, descrição e ações. */
export function PageHead({ crumbs, eyebrow, title, children, actions }: { crumbs?: ReactNode; eyebrow?: ReactNode; title: ReactNode; children?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="page-head">
      <div className="page-head-main">
        {crumbs && (
          <nav className="crumbs" aria-label="Você está em">
            {crumbs}
          </nav>
        )}
        {eyebrow && <div className="eyebrow">{eyebrow}</div>}
        <h1>{title}</h1>
        {children && <div className="page-sub">{children}</div>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </div>
  );
}

export function Metric({ label, value, denominator, tone = "neutral", icon, foot }: { label: string; value: ReactNode; denominator?: ReactNode; tone?: Tone; icon: IconName; foot?: ReactNode }) {
  return (
    <div className={`metric metric-${tone}`} role="listitem">
      <div className="metric-top">
        <div>
          <span className="label">{label}</span>
          <div className="metric-value">
            <strong>{value}</strong>
            {denominator && <span>{denominator}</span>}
          </div>
        </div>
        <span className="metric-icon" aria-hidden="true">
          <Icon name={icon} size={18} />
        </span>
      </div>
      {foot && <div className="metric-foot">{foot}</div>}
    </div>
  );
}

export function Loading({ label = "Carregando…" }: { label?: string }) {
  return (
    <div className="state state-loading" role="status">
      <span className="spinner" aria-hidden="true" /> {label}
    </div>
  );
}

export function ErrorBox({ error, onRetry }: { error: unknown; onRetry?: () => void }) {
  const msg = error instanceof Error ? error.message : String(error);
  return (
    <div className="state state-error" role="alert">
      <Icon name="alert" />
      <strong>Não foi possível carregar.</strong> {msg}
      {onRetry && (
        <Button variant="ghost" icon="refresh" onClick={onRetry}>
          Tentar de novo
        </Button>
      )}
    </div>
  );
}

export function Empty({ title, children, icon = "box" }: { title: string; children?: ReactNode; icon?: IconName }) {
  return (
    <div className="state state-empty">
      <span className="state-icon" aria-hidden="true">
        <Icon name={icon} size={20} />
      </span>
      <strong>{title}</strong>
      {children && <div className="muted">{children}</div>}
    </div>
  );
}

const NOTICE_ICON: Record<Tone, IconName> = { info: "info", success: "checkCircle", warning: "help", danger: "alert", neutral: "info" };

export function Notice({ tone, title, children, icon }: { tone: Tone; title?: ReactNode; children?: ReactNode; icon?: IconName }) {
  return (
    <div className={`notice notice-${tone}`} role={tone === "danger" ? "alert" : "note"}>
      <Icon name={icon ?? NOTICE_ICON[tone]} size={18} className="notice-icon" />
      <div className="notice-body">
        {title && <strong className="notice-title">{title}</strong>}
        {children}
      </div>
    </div>
  );
}

export function Field({ label, hint, error, children, htmlFor, aside, icon }: { label: string; hint?: ReactNode; error?: string; children: ReactNode; htmlFor: string; aside?: ReactNode; icon?: IconName }) {
  return (
    <div className={`field ${error ? "field-invalid" : ""}`}>
      <div className="field-label">
        <label htmlFor={htmlFor}>
          {icon && <Icon name={icon} size={16} />}
          {label}
        </label>
        {aside && <span className="field-aside">{aside}</span>}
      </div>
      {children}
      {hint && !error && <p className="field-hint">{hint}</p>}
      {error && (
        <p className="field-error" role="alert">
          {error}
        </p>
      )}
    </div>
  );
}

export function CodeBlock({ children, label, title }: { children: string; label?: string; title?: ReactNode }) {
  return (
    <div className="code-wrap">
      {title && <div className="code-title">{title}</div>}
      <pre className="code" aria-label={label} tabIndex={0}>
        <code>{children}</code>
      </pre>
    </div>
  );
}

export function CopyButton({ text, label = "Copiar" }: { text: string; label?: string }) {
  const [done, setDone] = useState(false);
  return (
    <Button
      variant="ghost"
      icon={done ? "check" : "copy"}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
          setDone(true);
          setTimeout(() => setDone(false), 1500);
        } catch {
          /* sem permissão de área de transferência: nada a fazer */
        }
      }}
    >
      {done ? "Copiado" : label}
    </Button>
  );
}

/**
 * A marca do Cobogó: quatro placas de cobogó lidas da esquerda para a direita,
 * de cima para baixo (C, O, B, O quadrado), desenhadas pelo autor em
 * docs/marca/. Os motivos são vazados de verdade (máscara): o fundo aparece por
 * eles, então a mesma logo serve em qualquer cor e nos dois temas. Abaixo de
 * 40 px a junta e o traço do C engrossam para continuarem visíveis.
 */
export function Logo({ size = 32, tile = "var(--brand)", className }: { size?: number; tile?: string; className?: string }) {
  const id = `vazados-${useId().replace(/[^a-zA-Z0-9_-]/g, "")}`;
  const small = size <= 40;
  const g = small ? 10 : 2;
  const stroke = small ? 10 : size <= 96 ? 6 : 3;
  const o = 100 + g;
  const full = 200 + g;
  const tri = (dy: number) => `M67 ${33.5 + dy} 39.25 ${49.5215 + dy}V${17.4785 + dy}Z`;
  return (
    <svg width={size} height={size} viewBox={`0 0 ${full} ${full}`} aria-hidden="true" className={`logo ${className ?? ""}`}>
      <mask id={id} maskUnits="userSpaceOnUse" x="0" y="0" width={full} height={full}>
        <rect width={full} height={full} fill="#fff" />
        <g fill="none" stroke="#000" strokeWidth={stroke}>
          <path d="M32.6496 20.3113C38.2336 49.4226 46.438 62.1237 71.5613 77.2927" />
          <path d="M70.8374 37.2212 31.1981 64.2902" />
        </g>
        <circle cx={o + 50} cy="50" r="33" fill="#000" />
        <path d={tri(o) + tri(o + 32)} fill="#000" />
        <rect x={o + 22} y={o + 22} width="56" height="56" fill="#000" />
      </mask>
      <g mask={`url(#${id})`} fill={tile}>
        <rect width="100" height="100" />
        <rect x={o} width="100" height="100" />
        <rect y={o} width="100" height="100" />
        <rect x={o} y={o} width="100" height="100" />
      </g>
    </svg>
  );
}
