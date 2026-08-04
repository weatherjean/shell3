import { LockIcon, TriangleAlertIcon } from "lucide-react";
import { useEffect, useRef, useState, type FC, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { login } from "@/lib/api";

/**
 * The login screen: the whole interface sits behind it.
 *
 * Deliberately says what is being protected. A password prompt on a note-taking
 * app guards notes; this one guards a shell — the agent's first verb is bash on
 * the machine serving this page — and someone choosing whether to reuse a
 * password should know that before they decide.
 *
 * The code field appears only once the server asks for it. It cannot be known
 * up front: /api/capabilities is behind the gate, so whether this install has a
 * second factor is something the server reveals in response to a correct
 * password, and never to an unauthenticated guess.
 */
export const LoginScreen: FC<{ onSignedIn: () => void }> = ({ onSignedIn }) => {
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const [needCode, setNeedCode] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const codeRef = useRef<HTMLInputElement>(null);

  // Focus follows the step, so the second factor needs no reach for the mouse.
  useEffect(() => {
    if (needCode) codeRef.current?.focus();
  }, [needCode]);

  const insecure = window.location.protocol === "http:" && window.location.hostname !== "localhost" &&
    window.location.hostname !== "127.0.0.1";

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    setError("");
    try {
      const result = await login(password, needCode ? code : undefined);
      if (result.ok) {
        onSignedIn();
        return;
      }
      if (result.needCode) {
        setNeedCode(true);
        return;
      }
      setError(result.error);
      setCode("");
    } catch {
      setError("Could not reach the server.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="bg-background flex min-h-dvh items-center justify-center p-4">
      <form
        onSubmit={submit}
        className="w-full max-w-sm space-y-5"
        aria-labelledby="login-title"
      >
        <div className="flex flex-col items-center gap-2 text-center">
          <span className="mark-face text-primary text-2xl leading-none tracking-tighter" aria-hidden>
            ๑ï
          </span>
          <h1 id="login-title" className="text-lg font-medium">
            shell3
          </h1>
          <p className="text-muted-foreground text-sm">
            {needCode
              ? "Enter the code from your authenticator app."
              : "This interface runs commands on the machine serving it."}
          </p>
        </div>

        {insecure && (
          <p className="text-muted-foreground flex items-start gap-2 rounded-lg border border-dashed p-3 text-xs">
            <TriangleAlertIcon className="mt-0.5 size-3.5 shrink-0" aria-hidden />
            <span>
              This connection is not encrypted, so the password crosses the
              network in clear. Serve it over https — Tailscale or a TLS proxy.
            </span>
          </p>
        )}

        <div className="space-y-3">
          <div>
            <label htmlFor="password" className="sr-only">
              Password
            </label>
            <Input
              id="password"
              type="password"
              autoComplete="current-password"
              autoFocus
              required
              value={password}
              disabled={needCode}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="Password"
            />
          </div>

          {needCode && (
            <div>
              <label htmlFor="code" className="sr-only">
                Authentication code
              </label>
              <Input
                id="code"
                ref={codeRef}
                inputMode="numeric"
                autoComplete="one-time-code"
                pattern="[0-9]*"
                maxLength={6}
                required
                value={code}
                onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
                placeholder="6-digit code"
              />
            </div>
          )}
        </div>

        {error && (
          <p role="alert" className="text-destructive text-sm">
            {error}
          </p>
        )}

        <Button type="submit" className="w-full" disabled={busy}>
          <LockIcon className="size-4" aria-hidden />
          {busy ? "Checking…" : needCode ? "Verify" : "Sign in"}
        </Button>
      </form>
    </div>
  );
};
