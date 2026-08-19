// Typed client for the Agentic Registry /v0 API. The marketplace UI is a pure
// consumer — it never holds secrets and talks to the same public API any
// gateway or agent would.

export type Visibility = "public" | "internal" | "private";

export interface ObjectMeta {
  name: string;
  namespace?: string;
  tag?: string;
  uid?: string;
  labels?: Record<string, string>;
  annotations?: Record<string, string>;
  visibility?: Visibility;
  tenantId?: string;
  orgId?: string;
  teamId?: string;
  contentHash?: string;
  createdAt?: string;
  updatedAt?: string;
  // Server-computed artifact-repository identity.
  arn?: string;
  digest?: string;     // "sha256:<hex>"
  ref?: string;        // "<plural>/<ns>/<name>@<tag>"
  digestRef?: string;  // "<plural>/<ns>/<name>@sha256:<hex>"
  signature?: string;  // base64 Ed25519 signature over digest
  signedBy?: string;   // signing key id
}

export interface SigningKey {
  enabled: boolean;
  algorithm?: string;
  keyId?: string;
  publicKey?: string; // base64 raw Ed25519 public key
  encoding?: string;
  signs?: string;
}

export interface Artifact {
  apiVersion: string;
  kind: string;
  metadata: ObjectMeta;
  spec?: Record<string, unknown>;
  status?: Record<string, unknown>;
}

export const KINDS = [
  { plural: "skills", kind: "Skill", label: "Skills" },
  { plural: "tools", kind: "Tool", label: "Tools" },
  { plural: "mcpservers", kind: "MCPServer", label: "MCP Servers" },
  { plural: "prompts", kind: "Prompt", label: "Prompts" },
  { plural: "workflows", kind: "Workflow", label: "Workflows" },
  { plural: "blueprints", kind: "Blueprint", label: "Blueprints" },
  { plural: "agents", kind: "Agent", label: "Agents" },
] as const;

// pluralForKind maps an artifact's Kind (e.g. "MCPServer") to its catalog
// route segment (e.g. "mcpservers") so search results can link correctly.
export function pluralForKind(kind: string): string {
  return KINDS.find((k) => k.kind === kind)?.plural ?? "skills";
}

const BASE = import.meta.env.VITE_API_BASE ?? "";

async function getRaw(path: string): Promise<Response> {
  const res = await fetch(`${BASE}${path}`, { headers: { Accept: "application/json" } });
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`;
    try {
      const body = await res.json();
      msg = body?.error?.message ?? msg;
    } catch {
      /* ignore */
    }
    throw new Error(msg);
  }
  return res;
}

async function get<T>(path: string): Promise<T> {
  return (await getRaw(path)).json() as Promise<T>;
}

// A single page of a collection plus the opaque cursor for the next page
// (read from the X-Next-Cursor response header; absent when fully drained).
export interface Page<T> {
  items: T[];
  nextCursor?: string;
}

async function getPage<T>(path: string): Promise<Page<T>> {
  const res = await getRaw(path);
  const items = (await res.json()) as T[];
  return { items, nextCursor: res.headers.get("X-Next-Cursor") || undefined };
}

export interface Health {
  status: string;
  version: string;
  platform: string;
}

export interface Session {
  authenticated: boolean;
  email: string;
  admin: boolean;
}

export interface RegistryCondition {
  type: string;
  status: "True" | "False";
  reason: string;
  message: string;
}

export interface UnresolvedRef {
  kind: string;
  ref: string;
  reason: string;
}

export interface ResolvedMCPServer {
  mcpServer: Artifact;
  tools: Artifact[];
  toolCount: number;
  unresolved?: UnresolvedRef[];
  conditions: RegistryCondition[];
}

export const api = {
  health: () => get<Health>("/healthz"),

  session: () => get<Session>("/v0/session"),

  list: (plural: string, opts: { namespace?: string; labelSelector?: string; search?: string } = {}) => {
    const q = new URLSearchParams();
    if (opts.namespace) q.set("namespace", opts.namespace);
    if (opts.labelSelector) q.set("labelSelector", opts.labelSelector);
    if (opts.search) q.set("search", opts.search);
    const qs = q.toString();
    return get<Artifact[]>(`/v0/${plural}${qs ? `?${qs}` : ""}`);
  },

  // Cursor-paginated variant of list(): returns one page of items plus the
  // cursor for the next page. Pass that cursor back to fetch the following
  // page; an absent cursor means the collection is exhausted. Used by the
  // catalog's infinite scroll.
  listPage: (
    plural: string,
    opts: { namespace?: string; labelSelector?: string; search?: string; limit?: number; cursor?: string } = {},
  ): Promise<Page<Artifact>> => {
    const q = new URLSearchParams();
    if (opts.namespace) q.set("namespace", opts.namespace);
    if (opts.labelSelector) q.set("labelSelector", opts.labelSelector);
    if (opts.search) q.set("search", opts.search);
    if (opts.limit) q.set("limit", String(opts.limit));
    if (opts.cursor) q.set("cursor", opts.cursor);
    const qs = q.toString();
    return getPage<Artifact>(`/v0/${plural}${qs ? `?${qs}` : ""}`);
  },

  // Global, cross-kind ranked search (pgvector cosine when available).
  search: (q: string, limit = 20) =>
    get<Artifact[]>(`/v0/search?q=${encodeURIComponent(q)}&limit=${limit}`),

  // Publish (create/update) one artifact. The server accepts JSON or YAML;
  // we always send JSON. Returns the stored object (with server-stamped meta).
  create: async (plural: string, obj: unknown): Promise<Artifact> => {
    const res = await fetch(`${BASE}/v0/${plural}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json" },
      body: JSON.stringify(obj),
    });
    if (!res.ok) {
      let msg = `${res.status} ${res.statusText}`;
      try {
        const body = await res.json();
        msg = body?.error?.message ?? body?.applied?.[0]?.error ?? msg;
      } catch {
        /* ignore */
      }
      throw new Error(msg);
    }
    return res.json() as Promise<Artifact>;
  },

  get: (plural: string, name: string, namespace = "default") =>
    get<Artifact>(`/v0/${plural}/${encodeURIComponent(name)}?namespace=${namespace}`),

  resolvedMcp: (name: string, namespace = "default") =>
    get<ResolvedMCPServer>(
      `/v0/mcpservers/${encodeURIComponent(name)}/resolved?namespace=${encodeURIComponent(namespace)}`,
    ),

  getVersion: (plural: string, name: string, tag: string, namespace = "default") =>
    get<Artifact>(`/v0/${plural}/${encodeURIComponent(name)}/${encodeURIComponent(tag)}?namespace=${namespace}`),

  tags: (plural: string, name: string, namespace = "default") =>
    get<{ name: string; tags: string[] }>(`/v0/${plural}/${encodeURIComponent(name)}/tags?namespace=${namespace}`),

  // Append-only audit timeline (all tags), newest first.
  revisions: (plural: string, name: string, namespace = "default") =>
    get<Revision[]>(`/v0/${plural}/${encodeURIComponent(name)}/revisions?namespace=${namespace}`),

  // The registry's public signing key (for verifying digest attestations).
  signingKey: () => get<SigningKey>("/v0/signing-key"),
};

export interface Revision {
  tag: string;
  revision: number;
  digest: string; // "sha256:<hex>"
  visibility?: string;
  createdAt: string;
}

export function visibilityClass(v?: Visibility): string {
  switch (v) {
    case "public":
      return "badge-public";
    case "internal":
      return "badge-internal";
    default:
      return "badge-private";
  }
}

// Strip server-managed system labels so the UI shows only user labels.
export function userLabels(labels?: Record<string, string>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(labels ?? {})) {
    if (!k.startsWith("registry.agentic.dev/")) out[k] = v;
  }
  return out;
}
