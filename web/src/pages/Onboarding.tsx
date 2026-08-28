import { useState } from "react";
import { Building2, Loader2 } from "lucide-react";
import { api } from "../lib/api";
import { useRegistrySession } from "../lib/session";

export default function Onboarding() {
  const session = useRegistrySession();
  const [displayName, setDisplayName] = useState("");
  const [slug, setSlug] = useState("");
  const [working, setWorking] = useState(false);
  const [error, setError] = useState("");

  if (!session.authenticated) {
    return <div className="mx-auto max-w-xl px-5 py-20 text-center"><h1 className="text-xl font-semibold">Sign in to create a Registry workspace</h1><a className="btn-primary mt-6" href="/oauth2/start?rd=%2Fonboarding">Sign in</a></div>;
  }
  if (!session.onboarding_required) {
    return <div className="mx-auto max-w-xl px-5 py-20 text-center"><h1 className="text-xl font-semibold">Your workspace is ready</h1><a className="btn-primary mt-6" href="/settings/api-credentials">Manage API credentials</a></div>;
  }

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setWorking(true);
    setError("");
    try {
      await api.onboard({ display_name: displayName.trim(), slug: slug.trim() });
      window.location.assign("/oauth2/start?rd=%2Fsettings%2Fapi-credentials");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
      setWorking(false);
    }
  }

  return (
    <div className="mx-auto max-w-xl px-5 py-12 sm:py-20">
      <Building2 className="h-8 w-8 text-[var(--accent)]" />
      <h1 className="mt-5 text-2xl font-bold text-[var(--ink-strong)]">Create your Registry workspace</h1>
      <p className="mt-2 text-sm leading-6 text-[var(--ink-soft)]">This creates your tenant in the Tesserix identity control plane and a private default namespace. You will sign in again to receive the tenant claim.</p>
      {error && <div className="mt-6 rounded-xl border border-[var(--error-soft-bd)] bg-[var(--error-soft-bg)] p-4 text-sm text-[var(--error-ink)]">{error}</div>}
      <form className="mt-8 space-y-5" onSubmit={submit}>
        <label className="block text-sm text-[var(--ink-soft)]">Organization name<input className="field mt-2" required maxLength={100} value={displayName} onChange={(event) => setDisplayName(event.target.value)} placeholder="Acme AI" /></label>
        <label className="block text-sm text-[var(--ink-soft)]">Workspace slug<input className="field mt-2" required pattern="[a-z0-9]([-a-z0-9]*[a-z0-9])?" maxLength={63} value={slug} onChange={(event) => setSlug(event.target.value.toLowerCase())} placeholder="acme-ai" /><span className="mt-2 block text-xs text-[var(--ink-muted)]">Used as your default Registry namespace.</span></label>
        <button className="btn-primary w-full" disabled={working}>{working && <Loader2 className="h-4 w-4 animate-spin" />}Create workspace</button>
      </form>
    </div>
  );
}
