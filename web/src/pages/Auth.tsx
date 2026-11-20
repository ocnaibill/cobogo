// TEL-01 — Acesso: login, criação assistida do administrador e revalidação
// da sessão expirada. Não há cadastro público (DEC-27).
import { useState, type FormEvent } from "react";
import { Navigate, useNavigate, useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, unwrap } from "../api/client";
import { useSession } from "../session";
import { Button, Field, Logo, Notice } from "../components/ui";

function messageFor(e: unknown): string {
  if (e instanceof ApiError) {
    switch (e.code) {
      case "invalid_credentials":
        return "Login ou senha incorretos.";
      case "rate_limited":
        return "Muitas tentativas. Aguarde alguns minutos antes de tentar de novo.";
      case "invalid_setup_token":
        return "Token de configuração inválido. Confira o log da API.";
      case "setup_closed":
        return "O administrador já foi configurado. Entre com login e senha.";
      case "weak_password":
        return e.message;
      default:
        return e.message;
    }
  }
  return e instanceof Error ? e.message : String(e);
}

/** O formulário de login, usado na página e na revalidação da sessão. */
export function LoginForm({ onDone, compact = false }: { onDone: () => void; compact?: boolean }) {
  const { signedIn, user } = useSession();
  const [login, setLogin] = useState(user?.login ?? "");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const res = await unwrap(api.POST("/auth/login", { body: { login, password } }));
      signedIn(res.user);
      onDone();
    } catch (e) {
      setError(messageFor(e));
    } finally {
      setBusy(false);
      setPassword("");
    }
  }

  return (
    <form onSubmit={submit} className="stack" noValidate>
      <Field label="Login" htmlFor="login">
        <input id="login" autoComplete="username" value={login} onChange={(e) => setLogin(e.target.value)} required autoFocus={!compact} />
      </Field>
      <Field label="Senha" htmlFor="password">
        <input id="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
      </Field>
      {error && <Notice tone="danger">{error}</Notice>}
      <Button type="submit" variant="primary" busy={busy} disabled={!login || !password}>
        Entrar
      </Button>
    </form>
  );
}

function AuthShell({ title, subtitle, children }: { title: string; subtitle: string; children: React.ReactNode }) {
  return (
    <main className="auth">
      <div className="auth-card">
        <div className="auth-brand">
          <Logo size={36} />
          <span>Cobogó</span>
        </div>
        <h1>{title}</h1>
        <p className="muted">{subtitle}</p>
        {children}
      </div>
      <p className="auth-foot muted">Deploy guiado por heurísticas explicáveis</p>
    </main>
  );
}

export function LoginPage() {
  const { user, needsSetup, loading } = useSession();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const back = params.get("voltar") || "/";
  if (loading) return null;
  if (needsSetup) return <Navigate to="/configurar" replace />;
  if (user) return <Navigate to={back} replace />;
  return (
    <AuthShell title="Entrar" subtitle="Acesso do administrador desta instalação.">
      <LoginForm onDone={() => navigate(back, { replace: true })} />
    </AuthShell>
  );
}

export function SetupPage() {
  const { needsSetup, signedIn, loading } = useSession();
  const navigate = useNavigate();
  const [token, setToken] = useState("");
  const [login, setLogin] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  if (loading) return null;
  if (needsSetup === false) return <Navigate to="/entrar" replace />;

  const mismatch = confirm.length > 0 && confirm !== password;
  const short = password.length > 0 && password.length < 12;

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    if (mismatch || short) return;
    setBusy(true);
    setError(null);
    try {
      const res = await unwrap(api.POST("/setup", { body: { token: token.trim(), login: login.trim(), password } }));
      signedIn(res.user);
      navigate("/", { replace: true });
    } catch (e) {
      setError(messageFor(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title="Configurar o administrador" subtitle="Primeiro acesso: crie a conta que administra esta instalação.">
      <form onSubmit={submit} className="stack" noValidate>
        <Field
          label="Token de configuração"
          htmlFor="setup-token"
          hint={
            <>
              Impresso uma única vez no log da API. Na VM: <code>docker compose … logs --no-log-prefix api | grep -A3 'token de configuração'</code>
            </>
          }
        >
          <input id="setup-token" value={token} onChange={(e) => setToken(e.target.value)} autoComplete="off" spellCheck={false} required autoFocus />
        </Field>
        <Field label="Login" htmlFor="setup-login" hint="De 3 a 64 caracteres: letras minúsculas, números, ponto, hífen e sublinhado.">
          <input id="setup-login" value={login} onChange={(e) => setLogin(e.target.value)} autoComplete="username" required />
        </Field>
        <Field label="Senha" htmlFor="setup-password" error={short ? "Use ao menos 12 caracteres." : undefined} hint="De 12 a 128 caracteres. Uma frase longa é melhor que símbolos.">
          <input id="setup-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required />
        </Field>
        <Field label="Repita a senha" htmlFor="setup-confirm" error={mismatch ? "As senhas não conferem." : undefined}>
          <input id="setup-confirm" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required />
        </Field>
        {error && <Notice tone="danger">{error}</Notice>}
        <Button type="submit" variant="primary" busy={busy} disabled={!token || !login || !password || mismatch || short}>
          Criar administrador
        </Button>
      </form>
    </AuthShell>
  );
}

/** Aparece sobre a tela quando uma chamada recebe 401: entra de novo sem perder o lugar. */
export function SessionExpiredDialog() {
  const { expired, signOut } = useSession();
  const qc = useQueryClient();
  if (!expired) return null;
  return (
    <div className="dialog-backdrop" role="presentation">
      <div className="dialog" role="dialog" aria-modal="true" aria-labelledby="expired-title">
        <h2 id="expired-title">Sua sessão expirou</h2>
        <p className="muted">Por segurança, a sessão termina depois de 2 h sem uso ou 12 h no total. Entre de novo para continuar de onde parou.</p>
        <LoginForm compact onDone={() => qc.invalidateQueries()} />
        <Button variant="ghost" onClick={() => signOut()}>
          Sair
        </Button>
      </div>
    </div>
  );
}
