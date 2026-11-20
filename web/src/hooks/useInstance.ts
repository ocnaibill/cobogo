// Dados da instalação que várias telas mostram: o domínio das aplicações
// (configuração editável, DEC-30/31) e a lista curta de projetos da navegação.
import { useQuery } from "@tanstack/react-query";
import { api, unwrap } from "../api/client";

/** Endereço público que um projeto recebe, ou null sem domínio configurado. */
export function useAppsDomain() {
  const q = useQuery({
    queryKey: ["settings"],
    queryFn: () => unwrap(api.GET("/settings")),
    staleTime: 60_000,
  });
  const get = (key: string) => q.data?.settings.find((s) => s.key === key)?.value;
  const domain = typeof get("apps_domain") === "string" ? (get("apps_domain") as string) : "";
  const scheme = typeof get("apps_scheme") === "string" ? (get("apps_scheme") as string) : "https";
  return {
    loaded: q.isSuccess,
    domain,
    urlFor: (slug: string) => (domain && slug ? `${scheme}://${slug}.${domain}` : null),
  };
}

/** Os projetos mais recentes, para a navegação lateral. */
export function useProjectNav() {
  return useQuery({
    queryKey: ["projects", "nav"],
    queryFn: () => unwrap(api.GET("/projects", { params: { query: { limit: 50 } } })),
    refetchInterval: 10_000,
  });
}
