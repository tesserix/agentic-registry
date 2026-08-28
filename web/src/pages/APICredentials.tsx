import { useEffect, useState } from "react";
import { Copy, KeyRound, Loader2, RotateCcw, ShieldCheck, Trash2 } from "lucide-react";
import { api, type APICredential, type APICredentialSecret } from "../lib/api";
import { useRegistrySession } from "../lib/session";

const READ = "registry:read";
const PUBLISH = "registry:publish";
const DELETE = "registry:delete";
const ARTIFACT_KINDS = ["Agent", "MCPServer", "Tool", "Skill", "Prompt", "Workflow", "Blueprint", "Dataset", "EvalSuite"];
const DEFAULT_TOKEN_URL = "https://auth.tesserix.app/oauth/v2/token";
const DEFAULT_AUDIENCE = "386930054896026901";

function credentialEnvironment(clientID = "<client-id>", clientSecret = "<client-secret>") {
  return [
    `AGENTIC_CLIENT_ID=${clientID}`,
    `AGENTIC_CLIENT_SECRET=${clientSecret}`,
    `AGENTIC_TOKEN_URL=${DEFAULT_TOKEN_URL}`,
    `AGENTIC_AUDIENCE=${DEFAULT_AUDIENCE}`,
  ].join("\n");
}

export default function APICredentials() {
  const session = useRegistrySession();
  const [credentials, setCredentials] = useState<APICredential[]>([]);
  const [name, setName] = useState("");
  const [allowDelete, setAllowDelete] = useState(false);
  const [lifetime, setLifetime] = useState(30);
  const [namespace, setNamespace] = useState("");
  const [kinds, setKinds] = useState<string[]>(["Agent"]);
  const [secret, setSecret] = useState<APICredentialSecret | null>(null);
  const [loading, setLoading] = useState(session.authenticated && !session.onboarding_required);
  const [working, setWorking] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    if (!session.authenticated || session.onboarding_required) return;
    let active = true;
    api.listCredentials()
      .then((items) => active && setCredentials(items))
      .catch((cause) => active && setError(cause instanceof Error ? cause.message : String(cause)))
      .finally(() => active && setLoading(false));
    return () => { active = false; };
  }, [session.authenticated, session.onboarding_required]);

  if (!session.authenticated) {
    return (
      <EmptyState
        title="Sign in to manage credentials"
        detail="Use your Tesserix account to create tenant-scoped credentials for the Agentic Registry CLI and CI."
        href="/oauth2/start?rd=%2Fsettings%2Fapi-credentials"
        action="Sign in"
      />
    );
  }
  if (session.onboarding_required) {
    return (
      <EmptyState
        title="Create your Registry workspace"
        detail="Choose a tenant and namespace before creating publishing credentials."
        href="/onboarding"
        action="Continue onboarding"
      />
    );
  }

  async function createCredential(event: React.FormEvent) {
    event.preventDefault();
    setWorking(true);
    setError("");
    try {
      const created = await api.createCredential({
        name: name.trim(),
        scopes: [READ, PUBLISH, ...(allowDelete ? [DELETE] : [])],
        lifetime_days: lifetime,
        namespaces: [namespace.trim()],
        kinds,
      });
      setSecret(created);
      setCredentials((current) => [created, ...current.filter((item) => item.id !== created.id)]);
      setName("");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setWorking(false);
    }
  }

  async function rotateCredential(item: APICredential) {
    if (!window.confirm(`Rotate ${item.name}? The current secret remains valid only for the identity provider's overlap window.`)) return;
    setWorking(true);
    setError("");
    try {
      const rotated = await api.rotateCredential(item.id);
      setSecret(rotated);
      setCredentials((current) => current.map((existing) => existing.id === item.id ? rotated : existing));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setWorking(false);
    }
  }

  async function revokeCredential(item: APICredential) {
    if (!window.confirm(`Revoke ${item.name}? New access tokens will be denied immediately.`)) return;
    setWorking(true);
    setError("");
    try {
      await api.revokeCredential(item.id);
      setCredentials((current) => current.filter((existing) => existing.id !== item.id));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setWorking(false);
    }
  }

  return (
    <div className="mx-auto max-w-5xl px-5 py-10 sm:px-8">
      <div className="flex items-start gap-4">
        <span className="grid h-11 w-11 place-items-center rounded-xl bg-[var(--accent-soft)] text-[var(--accent)]">
          <KeyRound className="h-5 w-5" />
        </span>
        <div>
          <div className="label-eyebrow">Settings</div>
          <h1 className="mt-1 text-2xl font-bold text-[var(--ink-strong)]">API credentials</h1>
          <p className="mt-2 max-w-2xl text-sm leading-6 text-[var(--ink-soft)]">
            Short-lived, tenant-scoped OAuth clients for the Agentic Registry CLI and CI. Secrets are issued by Zitadel and shown only once.
          </p>
        </div>
      </div>

      {secret && (
        <section className="mt-7 rounded-2xl border border-[var(--warn-soft-bd)] bg-[var(--warn-soft-bg)] p-5">
          <div className="flex items-center gap-2 font-semibold text-[var(--ink-strong)]"><ShieldCheck className="h-4 w-4" />Copy this secret now</div>
          <p className="mt-2 text-sm text-[var(--ink-soft)]">It cannot be retrieved after this message is closed.</p>
          <div className="mt-4 grid gap-3 sm:grid-cols-[1fr_auto]">
            <code className="overflow-x-auto rounded-xl border bg-[var(--surface)] px-4 py-3 text-xs">{secret.client_secret}</code>
            <button className="btn-secondary" onClick={() => navigator.clipboard.writeText(secret.client_secret)}><Copy className="h-4 w-4" />Copy</button>
          </div>
          <div className="mt-3 text-xs text-[var(--ink-muted)]">Client ID: <code>{secret.client_id}</code></div>
          <pre className="mt-4 overflow-x-auto rounded-xl border bg-[var(--surface)] px-4 py-3 text-xs"><code>{credentialEnvironment(secret.client_id, secret.client_secret)}</code></pre>
          <button className="btn-ghost mt-4" onClick={() => setSecret(null)}>I have stored it securely</button>
        </section>
      )}

      {error && <div className="mt-6 rounded-xl border border-[var(--error-soft-bd)] bg-[var(--error-soft-bg)] p-4 text-sm text-[var(--error-ink)]">{error}</div>}

      <section className="mt-8 rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-5 sm:p-6">
        <h2 className="font-semibold text-[var(--ink-strong)]">Configure the CLI or CI</h2>
        <p className="mt-2 text-sm leading-6 text-[var(--ink-soft)]">
          Set all four values together. Store <code>AGENTIC_CLIENT_SECRET</code> in your secret manager; the client ID, token URL, and audience are non-secret configuration.
        </p>
        <pre className="mt-4 overflow-x-auto rounded-xl border bg-[var(--surface-raised)] px-4 py-3 text-xs"><code>{credentialEnvironment()}</code></pre>
        <p className="mt-3 text-xs leading-5 text-[var(--ink-muted)]">
          For GitHub Actions, add the client secret under GitHub Actions secrets and the other three values under repository or environment variables. The CLI exchanges them for a short-lived access token at runtime and never writes the client secret to its config file.
        </p>
      </section>

      <section className="mt-8 rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-5 sm:p-6">
        <h2 className="font-semibold text-[var(--ink-strong)]">Create credential</h2>
        <form className="mt-5 grid gap-4 sm:grid-cols-2" onSubmit={createCredential}>
          <label className="text-sm text-[var(--ink-soft)]">Name<input className="field mt-2" required maxLength={64} value={name} onChange={(event) => setName(event.target.value)} placeholder="GitHub Actions" /></label>
          <label className="text-sm text-[var(--ink-soft)]">Lifetime<select className="field mt-2" value={lifetime} onChange={(event) => setLifetime(Number(event.target.value))}><option value={7}>7 days</option><option value={30}>30 days</option><option value={60}>60 days</option><option value={90}>90 days</option></select></label>
          <label className="text-sm text-[var(--ink-soft)] sm:col-span-2">Allowed namespace<input className="field mt-2" required pattern="[a-z0-9]([-a-z0-9]*[a-z0-9])?" maxLength={63} value={namespace} onChange={(event) => setNamespace(event.target.value.toLowerCase())} placeholder="acme-ai" /></label>
          <fieldset className="sm:col-span-2"><legend className="text-sm text-[var(--ink-soft)]">Allowed artifact kinds</legend><div className="mt-3 flex flex-wrap gap-3">{ARTIFACT_KINDS.map((kind) => <label key={kind} className="flex items-center gap-2 text-xs text-[var(--ink-soft)]"><input type="checkbox" checked={kinds.includes(kind)} onChange={(event) => setKinds((current) => event.target.checked ? [...current, kind] : current.filter((value) => value !== kind))} />{kind}</label>)}</div></fieldset>
          <label className="flex items-center gap-2 text-sm text-[var(--ink-soft)]"><input type="checkbox" checked={allowDelete} onChange={(event) => setAllowDelete(event.target.checked)} />Allow deleting published versions</label>
          <button className="btn-primary sm:justify-self-end" disabled={working || kinds.length === 0}>{working && <Loader2 className="h-4 w-4 animate-spin" />}Create credential</button>
        </form>
      </section>

      <section className="mt-8">
        <h2 className="font-semibold text-[var(--ink-strong)]">Active credentials</h2>
        {loading ? <div className="mt-5 flex items-center gap-2 text-sm text-[var(--ink-muted)]"><Loader2 className="h-4 w-4 animate-spin" />Loading credentials…</div> : credentials.length === 0 ? <div className="mt-5 rounded-2xl border p-8 text-center text-sm text-[var(--ink-muted)]">No API credentials yet.</div> : (
          <div className="mt-5 overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--surface)]">
            {credentials.map((item) => (
              <div key={item.id} className="flex flex-col gap-4 border-b border-[var(--border-subtle)] p-5 last:border-0 sm:flex-row sm:items-center">
                <div className="min-w-0 flex-1"><div className="font-semibold text-[var(--ink-strong)]">{item.name}</div><code className="mt-1 block truncate text-xs text-[var(--ink-muted)]">{item.client_id}</code><div className="mt-2 text-xs text-[var(--ink-soft)]">{item.scopes?.join(" · ") || "registry:read · registry:publish"}{item.namespaces?.length ? ` · ${item.namespaces.join(", ")}` : ""}{item.kinds?.length ? ` · ${item.kinds.join(", ")}` : ""}{item.expires_at ? ` · expires ${new Date(item.expires_at).toLocaleDateString()}` : ""}</div></div>
                <div className="flex gap-2"><button className="btn-secondary" disabled={working} onClick={() => rotateCredential(item)}><RotateCcw className="h-4 w-4" />Rotate</button><button className="btn-ghost text-[var(--error)]" disabled={working} onClick={() => revokeCredential(item)}><Trash2 className="h-4 w-4" />Revoke</button></div>
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function EmptyState({ title, detail, href, action }: { title: string; detail: string; href: string; action: string }) {
  return <div className="mx-auto max-w-xl px-5 py-20 text-center"><KeyRound className="mx-auto h-8 w-8 text-[var(--accent)]" /><h1 className="mt-5 text-xl font-semibold text-[var(--ink-strong)]">{title}</h1><p className="mt-2 text-sm leading-6 text-[var(--ink-soft)]">{detail}</p><a className="btn-primary mt-6" href={href}>{action}</a></div>;
}
