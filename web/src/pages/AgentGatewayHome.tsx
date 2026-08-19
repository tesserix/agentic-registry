import { useEffect, useMemo, useState } from "react";
import { Activity, Bot, CirclePlus, Loader2, Pencil, Route, ShieldCheck, Trash2, Wrench, X } from "lucide-react";
import { api, type Artifact } from "../lib/api";
import { agentgatewayApi, type AgentgatewayResource, type AgentgatewayResourceKind } from "../lib/agentGateway";
import { useRegistrySession } from "../lib/session";

type Tab = "providers" | "routes" | "policies" | "mcp";

const tabKind: Partial<Record<Tab, AgentgatewayResourceKind>> = {
  providers: "AgentgatewayBackend",
  routes: "HTTPRoute",
  policies: "AgentgatewayPolicy",
};

function starter(kind: AgentgatewayResourceKind): AgentgatewayResource {
  if (kind === "HTTPRoute") {
    return { apiVersion: "gateway.networking.k8s.io/v1", kind, metadata: { name: "new-route", namespace: "agentgateway-system" }, spec: { parentRefs: [{ name: "ai-gateway" }], rules: [] } };
  }
  if (kind === "AgentgatewayPolicy") {
    return { apiVersion: "agentgateway.dev/v1alpha1", kind, metadata: { name: "new-policy", namespace: "agentgateway-system" }, spec: { targetRefs: [{ group: "gateway.networking.k8s.io", kind: "Gateway", name: "ai-gateway" }], frontend: { accessLog: {} } } };
  }
  return {
    apiVersion: "agentgateway.dev/v1alpha1",
    kind,
    metadata: { name: "new-provider", namespace: "agentgateway-system" },
    spec: {
      ai: {
        groups: [{
          providers: [{
            name: "openai",
            openai: {},
            policies: { auth: { passthrough: {} }, tls: {} },
          }],
        }],
      },
    },
  };
}

function ResourceEditor({ initial, onClose, onSaved }: { initial: AgentgatewayResource; onClose: () => void; onSaved: (resource: AgentgatewayResource) => void }) {
  const [text, setText] = useState(JSON.stringify(initial, null, 2));
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function save() {
    setSaving(true);
    setError(null);
    try {
      const resource = JSON.parse(text) as AgentgatewayResource;
      if (!resource.metadata?.name || !resource.kind || !resource.apiVersion || !resource.spec) throw new Error("apiVersion, kind, metadata.name, and spec are required");
      onSaved(await agentgatewayApi.upsert(resource));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/50 p-4" role="dialog" aria-modal="true" aria-label="Edit AgentGateway resource">
      <div className="w-full max-w-3xl rounded-2xl border border-[var(--border)] bg-[var(--surface)] shadow-2xl">
        <div className="flex items-center border-b border-[var(--border)] px-5 py-4"><div><div className="text-sm font-semibold text-[var(--ink-strong)]">AgentGateway desired state</div><div className="mt-1 text-[11px] text-[var(--ink-muted)]">Secrets are rejected. Use workload identity or GitOps-owned secret references.</div></div><button className="btn-ghost ml-auto p-2" onClick={onClose} aria-label="Close editor"><X className="h-4 w-4" /></button></div>
        <div className="p-5"><textarea className="field min-h-[420px] resize-y font-mono text-[12px] leading-5" value={text} onChange={(event) => setText(event.target.value)} spellCheck={false} />{error && <div className="mt-3 rounded-xl border border-[var(--error-soft-bd)] bg-[var(--error-soft-bg)] p-3 text-[12px] text-[var(--error-ink)]">{error}</div>}</div>
        <div className="flex justify-end gap-2 border-t border-[var(--border)] px-5 py-4"><button className="btn-ghost" onClick={onClose}>Cancel</button><button className="btn-primary" onClick={save} disabled={saving}>{saving && <Loader2 className="h-4 w-4 spin" />}Save desired state</button></div>
      </div>
    </div>
  );
}

export default function AgentGatewayHome() {
  const session = useRegistrySession();
  const [resources, setResources] = useState<AgentgatewayResource[]>([]);
  const [mcpServers, setMcpServers] = useState<Artifact[]>([]);
  const [tab, setTab] = useState<Tab>("providers");
  const [editor, setEditor] = useState<AgentgatewayResource | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!session.admin) return;
    let active = true;
    Promise.all([agentgatewayApi.list(), api.list("mcpservers")])
      .then(([desired, mcps]) => { if (active) { setResources(desired); setMcpServers(mcps); } })
      .catch((cause) => { if (active) setError(cause instanceof Error ? cause.message : String(cause)); })
      .finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [session.admin]);

  const counts = useMemo(() => ({
    providers: resources.filter((item) => item.kind === "AgentgatewayBackend").length,
    routes: resources.filter((item) => item.kind === "HTTPRoute").length,
    policies: resources.filter((item) => item.kind === "AgentgatewayPolicy").length,
    mcp: mcpServers.length,
  }), [mcpServers.length, resources]);

  const visible = resources.filter((item) => item.kind === tabKind[tab]);

  function saved(resource: AgentgatewayResource) {
    setResources((current) => [...current.filter((item) => !(item.kind === resource.kind && item.metadata.name === resource.metadata.name)), resource]);
    setEditor(null);
  }

  async function remove(resource: AgentgatewayResource) {
    if (!window.confirm(`Delete ${resource.kind} ${resource.metadata.name}? The data plane keeps serving until reconciliation.`)) return;
    try {
      await agentgatewayApi.remove(resource.kind, resource.metadata.name);
      setResources((current) => current.filter((item) => !(item.kind === resource.kind && item.metadata.name === resource.metadata.name)));
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : String(cause));
    }
  }

  if (!session.admin) {
    return <div className="mx-auto max-w-3xl p-8"><div className="panel p-8"><ShieldCheck className="h-7 w-7 text-[var(--error)]" /><h1 className="mt-4 text-xl font-semibold text-[var(--ink-strong)]">Administrator permission required</h1><p className="mt-2 text-sm text-[var(--ink-soft)]">This session is authenticated but is not one of the two server-authorized AgentGateway administrators.</p></div></div>;
  }

  return (
    <div className="mx-auto max-w-[1440px] px-4 py-8 sm:px-8 sm:py-10">
      <section className="mcp-hero rounded-[24px] border px-6 py-8 sm:px-9">
        <div className="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between"><div><div className="mb-3 inline-flex items-center gap-2 rounded-full border border-[var(--ok-soft-bd)] bg-[var(--ok-soft-bg)] px-3 py-1 text-[11px] font-semibold text-[var(--ok-ink)]"><Activity className="h-3.5 w-3.5" />XDS serving · Registry managed</div><h1 className="text-[30px] font-bold tracking-[-0.035em] text-[var(--ink-strong)] sm:text-[40px]">AgentGateway administration</h1><p className="mt-3 max-w-2xl text-[14px] leading-6 text-[var(--ink-soft)]">Manage model providers, routes, policies, and MCP inventory from one desired-state control plane. Runtime traffic continues from the last reconciled snapshot if this UI is unavailable.</p></div><a className="btn-secondary" href="/ui/traffic/routes"><Activity className="h-4 w-4" />Open runtime traffic</a></div>
        <div className="mt-7 grid gap-3 sm:grid-cols-2 xl:grid-cols-4">{([{ key: "providers", label: "Providers", icon: Bot }, { key: "routes", label: "Routes", icon: Route }, { key: "policies", label: "Policies", icon: ShieldCheck }, { key: "mcp", label: "MCP servers", icon: Wrench }] as const).map(({ key, label, icon: Icon }) => <button key={key} className={`mcp-stat text-left ${tab === key ? "ring-2 ring-[var(--accent)]" : ""}`} onClick={() => setTab(key)}><Icon className="h-4 w-4" /><strong>{counts[key]}</strong><span>{label}</span></button>)}</div>
      </section>

      <section className="mt-8">
        <div className="flex items-center justify-between"><div><div className="label-eyebrow">Desired state</div><h2 className="mt-2 text-[23px] font-semibold capitalize text-[var(--ink-strong)]">{tab}</h2></div>{tab !== "mcp" && <button className="btn-primary" onClick={() => setEditor(starter(tabKind[tab]!))}><CirclePlus className="h-4 w-4" />Add {tab === "providers" ? "provider" : tab.slice(0, -1)}</button>}</div>
        {error && <div className="mt-5 rounded-xl border border-[var(--error-soft-bd)] bg-[var(--error-soft-bg)] p-4 text-[12px] text-[var(--error-ink)]">{error}</div>}
        {loading ? <div className="flex justify-center gap-2 py-20 text-[13px] text-[var(--ink-muted)]"><Loader2 className="h-4 w-4 spin" />Loading desired state…</div> : tab === "mcp" ? (
          <div className="mt-5 grid gap-4 md:grid-cols-2 xl:grid-cols-3">{mcpServers.map((server) => <a key={`${server.metadata.namespace}/${server.metadata.name}`} className="panel p-5 transition-colors hover:bg-[var(--surface-raised)]" href={`https://mcp.tesserix.app/servers/${encodeURIComponent(server.metadata.name)}?namespace=${encodeURIComponent(server.metadata.namespace || "default")}`} target="_blank" rel="noreferrer"><div className="font-semibold text-[var(--ink-strong)]">{server.metadata.name}</div><div className="mt-2 text-[11px] text-[var(--ink-muted)]">Managed in MCP Gateway · open inventory</div></a>)}</div>
        ) : visible.length === 0 ? <div className="panel mt-5 p-12 text-center text-[13px] text-[var(--ink-muted)]">No {tab} are registered.</div> : (
          <div className="mt-5 overflow-hidden rounded-2xl border border-[var(--border)] bg-[var(--surface)]"><table className="w-full text-left text-[12px]"><thead className="border-b border-[var(--border)] bg-[var(--surface-raised)] text-[10px] uppercase tracking-[0.08em] text-[var(--ink-muted)]"><tr><th className="px-5 py-3">Name</th><th className="hidden px-5 py-3 sm:table-cell">Kind</th><th className="px-5 py-3 text-right">Actions</th></tr></thead><tbody>{visible.sort((a, b) => a.metadata.name.localeCompare(b.metadata.name)).map((resource) => <tr key={`${resource.kind}/${resource.metadata.name}`} className="border-b border-[var(--border-subtle)] last:border-0"><td className="px-5 py-4"><div className="font-mono font-semibold text-[var(--ink-strong)]">{resource.metadata.name}</div><div className="mt-1 text-[10px] text-[var(--ink-muted)]">agentgateway-system</div></td><td className="hidden px-5 py-4 text-[var(--ink-soft)] sm:table-cell">{resource.kind}</td><td className="px-5 py-4"><div className="flex justify-end gap-2"><button className="btn-ghost p-2" onClick={() => setEditor(resource)} aria-label={`Edit ${resource.metadata.name}`}><Pencil className="h-4 w-4" /></button><button className="btn-ghost p-2 text-[var(--error)]" onClick={() => remove(resource)} aria-label={`Delete ${resource.metadata.name}`}><Trash2 className="h-4 w-4" /></button></div></td></tr>)}</tbody></table></div>
        )}
      </section>
      {editor && <ResourceEditor initial={editor} onClose={() => setEditor(null)} onSaved={saved} />}
    </div>
  );
}
