// Sessão do administrador (DEC-27): a API guarda a sessão num cookie
// HttpOnly; o painel só pergunta quem está logado e reage a 401.
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, SESSION_EXPIRED_EVENT, unwrap, type User } from "./api/client";

interface SessionValue {
  user: User | null;
  loading: boolean;
  needsSetup: boolean | null;
  expired: boolean;
  signedIn: (u: User) => void;
  signOut: () => Promise<void>;
}

const SessionContext = createContext<SessionValue | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient();
  const [expired, setExpired] = useState(false);

  const setup = useQuery({
    queryKey: ["setup-status"],
    queryFn: () => unwrap(api.GET("/setup/status")),
    staleTime: Infinity,
  });
  const me = useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      try {
        return (await unwrap(api.GET("/auth/me"))).user;
      } catch (e) {
        if (e instanceof ApiError && e.status === 401) {
          // Quem já estava dentro e perdeu a sessão vê o diálogo de revalidação
          // no mesmo lugar, em vez de ser jogado para a tela de login.
          const previous = qc.getQueryData<User | null>(["me"]);
          if (previous) {
            setExpired(true);
            return previous;
          }
          return null;
        }
        throw e;
      }
    },
    enabled: setup.data?.needs_setup === false,
    staleTime: 60_000,
  });

  useEffect(() => {
    const onExpired = () => {
      if (qc.getQueryData(["me"])) setExpired(true);
    };
    window.addEventListener(SESSION_EXPIRED_EVENT, onExpired);
    return () => window.removeEventListener(SESSION_EXPIRED_EVENT, onExpired);
  }, [qc]);

  const value: SessionValue = {
    user: me.data ?? null,
    loading: setup.isLoading || (setup.data?.needs_setup === false && me.isLoading),
    needsSetup: setup.data?.needs_setup ?? null,
    expired,
    signedIn: (u) => {
      setExpired(false);
      qc.setQueryData(["setup-status"], { needs_setup: false });
      qc.setQueryData(["me"], u);
    },
    signOut: async () => {
      await api.POST("/auth/logout").catch(() => undefined);
      setExpired(false);
      qc.clear();
      qc.setQueryData(["setup-status"], { needs_setup: false });
      qc.setQueryData(["me"], null);
    },
  };
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): SessionValue {
  const v = useContext(SessionContext);
  if (!v) throw new Error("useSession fora do SessionProvider");
  return v;
}
