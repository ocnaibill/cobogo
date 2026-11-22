// A senha digitada de novo (DEC-45): abre uma janela de 10 minutos, só nesta sessão.
// Quem precisa dela (comandos, download de backup) usa este formulário e este gancho.
import { useEffect, useState, type FormEvent } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, unwrap } from "../api/client";
import { reauthSecondsLeft } from "../lib/commands";
import { Button, Card, Field, Notice } from "./ui";

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e));

/** Até quando a senha digitada de novo vale nesta sessão, com uma contagem que anda sozinha. */
export function useReauth() {
  const qc = useQueryClient();
  const q = useQuery({ queryKey: ["reauth"], queryFn: () => unwrap(api.GET("/auth/me")), staleTime: 30_000 });
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  const left = reauthSecondsLeft(q.data?.reauth_until, now);
  return { left, unlocked: left > 0, refresh: () => qc.invalidateQueries({ queryKey: ["reauth"] }) };
}

/** Pede a senha de novo e abre a janela de 10 minutos. */
export function ReauthForm({ onDone, title = "Digite a sua senha para liberar", reason }: { onDone: () => void; title?: string; reason: string }) {
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(ev: FormEvent) {
    ev.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await unwrap(api.POST("/auth/reauth", { body: { password } }));
      setPassword("");
      onDone();
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Card title={title} icon="key">
      <form className="stack" onSubmit={submit}>
        <p className="small muted card-text">
          {reason} A senha vale por <strong>10 minutos</strong>, só nesta sessão.
        </p>
        {error && <Notice tone="danger">{error}</Notice>}
        <Field label="Senha" htmlFor="reauth-password">
          <input id="reauth-password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete="current-password" required />
        </Field>
        <div className="row-end">
          <Button type="submit" variant="primary" icon="shieldCheck" busy={busy} disabled={!password}>
            Liberar por 10 minutos
          </Button>
        </div>
      </form>
    </Card>
  );
}
