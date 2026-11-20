// TEL-01 — Acesso: login, criação assistida do administrador e revalidação
// da sessão expirada. Não há cadastro público (DEC-27). As garantias citadas
// na tela são as que o código cumpre (argon2id, cookie de sessão, prazos).
import { useState, type FormEvent, type ReactNode } from "react";
import { Navigate, useLocation, useNavigate, useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, unwrap } from "../api/client";
import { useSession } from "../session";
import { Button, Field, Logo, Notice, Tag } from "../components/ui";
import { Icon, type IconName } from "../components/icons";

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
      default:
        return e.message;
    }
  }
  return e instanceof Error ? e.message : String(e);
}

/** O formulário de login, usado na página e na revalidação da sessão. */
export function LoginForm({ onDone, compact = false, secondary }: { onDone: () => void; compact?: boolean; secondary?: ReactNode }) {
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

  const fields = (
    <>
      <Field label="Login" htmlFor={compact ? "re-login" : "login"} icon="user">
        <input id={compact ? "re-login" : "login"} className="mono" autoComplete="username" value={login} onChange={(e) => setLogin(e.target.value)} required autoFocus={!compact && !login} />
      </Field>
      <Field label="Senha" htmlFor={compact ? "re-password" : "password"} icon="key">
        <input
          id={compact ? "re-password" : "password"}
          type="password"
          autoComplete="current-password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
          autoFocus={compact || !!login}
        />
      </Field>
    </>
  );

  return (
    <form onSubmit={submit} className="stack" noValidate>
      {compact ? <div className="grid-2">{fields}</div> : fields}
      {error && <Notice tone="danger">{error}</Notice>}
      {compact ? (
        <div className="dialog-actions">
          {secondary}
          <Button type="submit" variant="primary" icon="refresh" busy={busy} disabled={!login || !password}>
            Entrar de novo e continuar
          </Button>
        </div>
      ) : (
        <Button type="submit" variant="primary" className="btn-lg btn-block" busy={busy} disabled={!login || !password}>
          Entrar no painel <Icon name="arrowRight" size={18} />
        </Button>
      )}
    </form>
  );
}

function AuthFrame({ children, right }: { children: ReactNode; right?: ReactNode }) {
  return (
    <div className="auth">
      <header className="auth-top">
        <div className="brand">
          <Logo />
          <span className="brand-name">Cobogó</span>
          <span className="brand-tag">D0</span>
        </div>
        {right ?? (
          <span className="meta row">
            <Icon name={window.location.protocol === "https:" ? "lock" : "alert"} size={14} />
            {window.location.protocol === "https:" ? "Conexão HTTPS" : "Conexão sem HTTPS"}
          </span>
        )}
      </header>
      <Logo size={210} tile="var(--pattern)" className="pattern pattern-tl" />
      <Logo size={210} tile="var(--pattern)" className="pattern pattern-br" />
      <main className="auth-main">{children}</main>
      <footer className="auth-foot">
        <span>Cobogó D0 · deploy guiado por heurísticas explicáveis</span>
        <span>{window.location.host}</span>
      </footer>
    </div>
  );
}

function Fact({ icon, title, children }: { icon: IconName; title: string; children: ReactNode }) {
  return (
    <div className="auth-fact">
      <Icon name={icon} size={18} />
      <div>
        <strong>{title}</strong>
        {children}
      </div>
    </div>
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
    <AuthFrame>
      <div className="auth-column">
        <div className="auth-card">
          <div className="auth-strip">
            <span className="row">
              <span className="dot" aria-hidden="true" /> Instância: <strong>{window.location.host}</strong>
            </span>
            <span>painel do administrador</span>
          </div>
          <div className="auth-body">
            <div className="auth-title">
              <Logo size={52} />
              <div>
                <h1>Acesso ao Cobogó</h1>
                <p>Publique repositórios com decisões que você consegue conferir.</p>
              </div>
            </div>
            <LoginForm onDone={() => navigate(back, { replace: true })} />
            <Notice tone="info" icon="shield" title="Credenciais isoladas">
              A senha é verificada com argon2id e nunca vai para o log. A sessão vive num cookie HttpOnly desta origem e expira após 2 h sem uso ou 12 h no total.
            </Notice>
          </div>
        </div>
        <div className="auth-facts">
          <Fact icon="search" title="Análise estática">Lê o repositório sem executar nada.</Fact>
          <Fact icon="cpu" title="Contêineres limitados">Memória, CPU e processos com teto.</Fact>
          <Fact icon="lock" title="Segredos cifrados">Valores nunca voltam ao painel.</Fact>
        </div>
      </div>
    </AuthFrame>
  );
}

function strength(password: string): { bars: number; weak: boolean } {
  const n = password.length;
  if (n === 0) return { bars: 0, weak: false };
  if (n < 12) return { bars: 1, weak: true };
  return { bars: n >= 24 ? 4 : n >= 16 ? 3 : 2, weak: false };
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
  const s = strength(password);

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
    <AuthFrame right={<span className="meta">Primeiro acesso a {window.location.host}</span>}>
      <div className="auth-wide">
        <div className="auth-card">
          <div className="auth-strip">
            <span className="row">
              <span className="dot dot-pulse" aria-hidden="true" /> Inicialização · passo único
            </span>
            <span>conta do administrador</span>
          </div>
          <div className="auth-body">
            <div>
              <h1>Criação assistida do administrador</h1>
              <p className="muted">Crie a conta que administra esta instalação. Não há cadastro público: só ela entra no painel.</p>
            </div>
            <form onSubmit={submit} className="stack" noValidate>
              <Field
                label="Token de configuração"
                htmlFor="setup-token"
                icon="terminal"
                aside="uso único"
                hint={
                  <>
                    Impresso uma vez no log da API. Na VM: <code>deploy/create-admin.sh</code> ou <code>docker compose … logs api | grep -A3 'token de configuração'</code>
                  </>
                }
              >
                <input id="setup-token" className="mono" value={token} onChange={(e) => setToken(e.target.value)} autoComplete="off" spellCheck={false} required autoFocus />
              </Field>
              <Field label="Login do administrador" htmlFor="setup-login" icon="user" aside="padrão: admin" hint="De 3 a 64 caracteres: letras minúsculas, números, ponto, hífen e sublinhado.">
                <input id="setup-login" className="mono" value={login} onChange={(e) => setLogin(e.target.value)} autoComplete="username" required />
              </Field>
              <Field
                label="Senha"
                htmlFor="setup-password"
                icon="key"
                aside={`${password.length} caractere${password.length === 1 ? "" : "s"}`}
                error={short ? "Use ao menos 12 caracteres." : undefined}
                hint="De 12 a 128 caracteres. Uma frase longa é melhor que símbolos."
              >
                <input id="setup-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" required />
                <div className={`strength ${s.weak ? "weak" : ""}`} aria-hidden="true">
                  {[1, 2, 3, 4].map((i) => (
                    <span key={i} className={i <= s.bars ? "on" : ""} />
                  ))}
                </div>
              </Field>
              <Field label="Repita a senha" htmlFor="setup-confirm" icon="key" error={mismatch ? "As senhas não conferem." : undefined}>
                <input id="setup-confirm" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" required />
              </Field>
              {error && <Notice tone="danger">{error}</Notice>}
              <Button type="submit" variant="primary" className="btn-lg btn-block" busy={busy} disabled={!token || !login || !password || mismatch || short}>
                Criar administrador e entrar <Icon name="arrowRight" size={18} />
              </Button>
            </form>
          </div>
        </div>

        <div className="auth-side">
          <div className="card">
            <h2 className="card-title">
              <Icon name="shieldCheck" size={20} className="card-icon" /> Garantias
            </h2>
            <p className="muted card-text">
              A senha fica só aqui, guardada como hash. Nada sai desta instalação.
            </p>
            <dl className="kv small">
              <div>
                <dt>Hash da senha</dt>
                <dd className="mono">argon2id · 64 MiB · 3 passadas</dd>
              </div>
              <div>
                <dt>Token</dt>
                <dd>Vale uma vez e só aparece no log da API.</dd>
              </div>
              <div>
                <dt>Sessão</dt>
                <dd>Cookie HttpOnly; 2 h sem uso ou 12 h no total.</dd>
              </div>
            </dl>
          </div>
          <div className="card">
            <span className="label">Depois deste passo</span>
            <ol className="steps-list card-text">
              <li className="current">
                <span className="flow-num">1</span>
                <div>
                  Conta do administrador
                  <small>em andamento…</small>
                </div>
              </li>
              <li>
                <span className="flow-num">2</span>
                <div>
                  Importar um repositório
                  <small>análise sem executar código</small>
                </div>
              </li>
              <li>
                <span className="flow-num">3</span>
                <div>
                  Publicar
                  <small>no endereço do projeto</small>
                </div>
              </li>
            </ol>
          </div>
        </div>
      </div>
    </AuthFrame>
  );
}

/** Aparece sobre a tela quando uma chamada recebe 401: entra de novo sem perder o lugar. */
export function SessionExpiredDialog() {
  const { expired, signOut, user } = useSession();
  const qc = useQueryClient();
  const location = useLocation();
  if (!expired) return null;
  return (
    <div className="dialog-backdrop" role="presentation">
      <div className="dialog" role="dialog" aria-modal="true" aria-labelledby="expired-title">
        <div className="auth-strip">
          <span className="row">
            <Icon name="shield" size={14} /> Sessão encerrada por segurança
          </span>
          <span>{window.location.host}</span>
        </div>
        <div className="dialog-body">
          <div className="dialog-head">
            <span className="dialog-icon" aria-hidden="true">
              <Icon name="lockClock" size={26} />
            </span>
            <div>
              <div className="row">
                <Tag tone="brand">Sessão expirada</Tag>
                <Tag tone="success">aplicações seguem no ar</Tag>
              </div>
              <h2 id="expired-title">Sua sessão expirou</h2>
              <p className="muted">A sessão termina depois de 2 h sem uso ou 12 h no total. Entre de novo para continuar exatamente de onde parou.</p>
            </div>
          </div>
          <div className="tiles">
            <div className="tile">
              <span className="label">Administrador</span>
              <strong className="mono">{user?.login ?? "—"}</strong>
            </div>
            <div className="tile">
              <span className="label">Você estava em</span>
              <strong className="mono truncate">{location.pathname}</strong>
            </div>
            <div className="tile">
              <span className="label">Aplicações</span>
              <strong>Sem interrupção</strong>
            </div>
          </div>
          <LoginForm
            compact
            onDone={() => qc.invalidateQueries()}
            secondary={
              <Button variant="ghost" icon="logout" onClick={() => signOut()}>
                Sair
              </Button>
            }
          />
        </div>
      </div>
    </div>
  );
}
