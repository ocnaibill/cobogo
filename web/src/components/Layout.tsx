// Casca do painel: barra superior, navegação global (§18.1) e a guarda de sessão.
import { Navigate, NavLink, Outlet, useLocation } from "react-router";
import { useSession } from "../session";
import { SessionExpiredDialog } from "../pages/Auth";
import { Button, Loading, Logo } from "./ui";

export function RequireSession() {
  const { user, needsSetup, loading } = useSession();
  const location = useLocation();
  if (loading) return <Loading label="Abrindo o painel…" />;
  if (needsSetup) return <Navigate to="/configurar" replace />;
  if (!user) return <Navigate to={`/entrar?voltar=${encodeURIComponent(location.pathname + location.search)}`} replace />;
  return <Layout />;
}

function Layout() {
  const { user, signOut } = useSession();
  return (
    <div className="shell">
      <a href="#conteudo" className="skip">
        Ir para o conteúdo
      </a>
      <header className="topbar">
        <NavLink to="/" className="brand" aria-label="Cobogó, visão geral">
          <Logo />
          <span>Cobogó</span>
        </NavLink>
        <nav aria-label="Principal" className="topnav">
          <NavLink to="/" end>
            Visão geral
          </NavLink>
          <NavLink to="/importar">Importar repositório</NavLink>
        </nav>
        <div className="topbar-user">
          <span className="muted" title="Administrador">
            {user?.login}
          </span>
          <Button variant="ghost" onClick={() => signOut()}>
            Sair
          </Button>
        </div>
      </header>
      <main id="conteudo" className="content">
        <Outlet />
      </main>
      <SessionExpiredDialog />
    </div>
  );
}
