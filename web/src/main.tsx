import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { createBrowserRouter, Link, RouterProvider } from "react-router";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ApiError } from "./api/client";
import { SessionProvider } from "./session";
import { RequireSession } from "./components/Layout";
import { LoginPage, SetupPage } from "./pages/Auth";
import { OverviewPage } from "./pages/Overview";
import { ImportPage } from "./pages/Import";
import { ProjectPage } from "./pages/Project";
import { DeploymentPage } from "./pages/Deployment";
import { Empty } from "./components/ui";
import "./styles/tokens.css";
import "./styles/app.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Um 401/403/404 não melhora com nova tentativa.
      retry: (count, error) => !(error instanceof ApiError && [401, 403, 404].includes(error.status)) && count < 2,
      refetchOnWindowFocus: true,
    },
  },
});

function NotFound() {
  return (
    <div className="page narrow">
      <Empty title="Página não encontrada.">
        <Link to="/">Voltar à visão geral</Link>
      </Empty>
    </div>
  );
}

const router = createBrowserRouter([
  { path: "/entrar", element: <LoginPage /> },
  { path: "/configurar", element: <SetupPage /> },
  {
    element: <RequireSession />,
    children: [
      { path: "/", element: <OverviewPage /> },
      { path: "/importar", element: <ImportPage /> },
      { path: "/projetos/:id", element: <ProjectPage /> },
      { path: "/deploys/:id", element: <DeploymentPage /> },
      { path: "*", element: <NotFound /> },
    ],
  },
]);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <SessionProvider>
        <RouterProvider router={router} />
      </SessionProvider>
    </QueryClientProvider>
  </StrictMode>,
);
