// Casca do painel (TEL-02 do Stitch): barra superior com a instância e a
// conta, navegação lateral por grupos (§18.1) e a guarda de sessão.
import { useBackups } from "../pages/Backups";
import { useEffect, useState } from "react";
import { Link, Navigate, NavLink, Outlet, useLocation } from "react-router";
import { useSession } from "../session";
import { SessionExpiredDialog } from "../pages/Auth";
import { useProjectNav } from "../hooks/useInstance";
import { stateTone } from "../lib/deploy";
import { Loading, Logo } from "./ui";
import { Icon } from "./icons";

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
  const location = useLocation();
  const [navOpen, setNavOpen] = useState(false);
  useEffect(() => setNavOpen(false), [location.pathname]);

  return (
    <div className={`shell ${navOpen ? "nav-open" : ""}`}>
      <a href="#conteudo" className="skip">
        Ir para o conteúdo
      </a>
      <header className="topbar">
        <button className="icon-btn menu-btn" aria-label="Abrir a navegação" aria-expanded={navOpen} aria-controls="navegacao" onClick={() => setNavOpen((o) => !o)}>
          <Icon name="menu" />
        </button>
        <Link to="/" className="brand" aria-label="Cobogó, visão geral">
          <Logo />
          <span className="brand-name">Cobogó</span>
          <span className="brand-tag">D0</span>
        </Link>
        <span className="topbar-sep" aria-hidden="true" />
        <div className="instance-chip" title="Instalação deste painel">
          <span className="dot dot-pulse" aria-hidden="true" />
          <span className="faint">Instância:</span>
          <strong>{window.location.host}</strong>
        </div>
        <div className="topbar-right">
          <Link to="/importar" className="btn btn-primary">
            <Icon name="plus" size={16} />
            <span className="hide-sm">Novo projeto</span>
          </Link>
          <span className="topbar-sep" aria-hidden="true" />
          <div className="user">
            <div className="user-text hide-sm">
              <strong>{user?.login}</strong>
              <span>administrador</span>
            </div>
            <span className="avatar" aria-hidden="true">
              <Icon name="user" size={16} />
            </span>
            <button className="icon-btn" onClick={() => signOut()} title="Sair" aria-label="Sair">
              <Icon name="logout" />
            </button>
          </div>
        </div>
      </header>

      <Sidebar />
      <div className="scrim" onClick={() => setNavOpen(false)} aria-hidden="true" />

      <main id="conteudo" className="content">
        <Outlet />
      </main>
      <SessionExpiredDialog />
    </div>
  );
}

function Sidebar() {
  const projects = useProjectNav();
  const list = projects.data?.projects ?? [];
  const backups = useBackups();
  // O ponto vermelho só acende com o volume visível e o backup velho ou ausente.
  const backupAlert = !!backups.data && backups.data.configured && backups.data.stale;
  return (
    <aside className="sidebar" id="navegacao">
      <nav aria-label="Principal" className="nav">
        <span className="nav-caption">Navegação</span>

        <div className="nav-group">
          <div className="nav-group-title">
            <Icon name="grid" size={16} />
            Visão e aplicações
          </div>
          <NavLink to="/" end className="nav-link">
            <span>Visão geral</span>
            {projects.isSuccess && <span className="nav-count">{list.length}{projects.data?.next_before ? "+" : ""}</span>}
          </NavLink>
          {list.length > 0 && (
            <div className="nav-sub" aria-label="Projetos">
              {list.slice(0, 12).map((p) => (
                <NavLink key={p.id} to={`/projetos/${p.id}`} className="nav-link nav-project" title={p.repository_url}>
                  <span className={`dot dot-${p.last_deployment ? stateTone(p.last_deployment.state) : "neutral"}`} aria-hidden="true" />
                  <span className="truncate">{p.slug}</span>
                </NavLink>
              ))}
            </div>
          )}
        </div>

        <div className="nav-group">
          <div className="nav-group-title">
            <Icon name="rocket" size={16} />
            Esteira de publicação
          </div>
          <NavLink to="/importar" className="nav-link">
            <span>Importar repositório</span>
          </NavLink>
        </div>

        <div className="nav-group">
          <div className="nav-group-title">
            <Icon name="shieldCheck" size={16} />
            Plataforma
          </div>
          <NavLink to="/backups" className="nav-link">
            <span>Backups</span>
            {backupAlert && <span className="dot dot-danger" role="img" aria-label="backup atrasado" title="Backup atrasado ou ausente" />}
          </NavLink>
        </div>
      </nav>

      <div className="engine-card">
        <div className="row-between">
          <span className="faint">Motor</span>
          <span className="dot dot-success" aria-hidden="true" />
        </div>
        <strong>Cobogó · D0</strong>
        <span className="faint">Análise estática, sem executar o repositório</span>
      </div>
    </aside>
  );
}
