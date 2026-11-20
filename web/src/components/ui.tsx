// Peças visuais básicas. Tudo que é cor, espaço e tipografia vem dos tokens de
// styles/tokens.css, para o visual das telas do Stitch entrar trocando só eles.
import type { ButtonHTMLAttributes, ReactNode } from "react";
import { absolute, relative } from "../lib/format";
import { STATE_LABEL, stateTone, type Tone } from "../lib/deploy";
import type { DeploymentState } from "../api/client";

export function Button({
  variant = "secondary",
  busy = false,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "secondary" | "danger" | "ghost"; busy?: boolean }) {
  return (
    <button {...rest} className={`btn btn-${variant} ${rest.className ?? ""}`} disabled={rest.disabled || busy} aria-busy={busy || undefined}>
      {busy && <span className="spinner" aria-hidden="true" />}
      {children}
    </button>
  );
}

export function Badge({ tone, children, title }: { tone: Tone; children: ReactNode; title?: string }) {
  return (
    <span className={`badge badge-${tone}`} title={title}>
      <span className="badge-dot" aria-hidden="true" />
      {children}
    </span>
  );
}

export function StateBadge({ state }: { state: DeploymentState }) {
  return <Badge tone={stateTone(state)}>{STATE_LABEL[state]}</Badge>;
}

export function When({ iso }: { iso: string }) {
  return (
    <time dateTime={iso} title={absolute(iso)}>
      {relative(iso)}
    </time>
  );
}

export function Card({ title, actions, children, className }: { title?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`card ${className ?? ""}`}>
      {(title || actions) && (
        <header className="card-head">
          {title && <h2>{title}</h2>}
          {actions && <div className="card-actions">{actions}</div>}
        </header>
      )}
      {children}
    </section>
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
      <strong>Não foi possível carregar.</strong> {msg}
      {onRetry && (
        <Button variant="ghost" onClick={onRetry}>
          Tentar de novo
        </Button>
      )}
    </div>
  );
}

export function Empty({ title, children }: { title: string; children?: ReactNode }) {
  return (
    <div className="state state-empty">
      <strong>{title}</strong>
      {children && <div>{children}</div>}
    </div>
  );
}

export function Notice({ tone, title, children }: { tone: Tone; title?: ReactNode; children?: ReactNode }) {
  return (
    <div className={`notice notice-${tone}`} role={tone === "danger" ? "alert" : "note"}>
      {title && <strong className="notice-title">{title}</strong>}
      {children}
    </div>
  );
}

export function Field({ label, hint, error, children, htmlFor }: { label: string; hint?: ReactNode; error?: string; children: ReactNode; htmlFor: string }) {
  return (
    <div className={`field ${error ? "field-invalid" : ""}`}>
      <label htmlFor={htmlFor}>{label}</label>
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

export function CodeBlock({ children, label }: { children: string; label?: string }) {
  return (
    <pre className="code" aria-label={label}>
      <code>{children}</code>
    </pre>
  );
}

export function Logo({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 32 32" aria-hidden="true">
      <rect width="32" height="32" rx="7" fill="var(--brand)" />
      <g fill="none" stroke="var(--brand-contrast)" strokeWidth="2.2">
        <circle cx="11" cy="11" r="5" />
        <circle cx="21" cy="11" r="5" />
        <circle cx="11" cy="21" r="5" />
        <circle cx="21" cy="21" r="5" />
      </g>
    </svg>
  );
}
