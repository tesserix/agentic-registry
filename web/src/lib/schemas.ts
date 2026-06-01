// Per-kind form schemas + starter manifests. Each artifact is a Kubernetes-
// style object (apiVersion/kind/metadata/spec); only the `spec` shape differs
// per kind. The editor renders the `fields` below bound to dot-paths on a
// single canonical doc, and seeds new docs from `starter()`.

export const API_VERSION = "registry.agentic.dev/v1alpha1";

export type FieldType =
  | "text"
  | "textarea"
  | "select"
  | "checkbox"
  | "refList" // multi-select of catalog artifact names (itemKind = the plural)
  | "group" // a nested object rendered from `children`
  | "objectList"; // a list of records, each rendered from `children`

export interface Field {
  path: string; // dot-path into the doc, e.g. "spec.title"
  label: string;
  type: FieldType;
  placeholder?: string;
  help?: string;
  options?: string[];
  required?: boolean;
  mono?: boolean;
  itemKind?: string; // for "refList": the plural to query, e.g. "skills"
  children?: Field[]; // for "group" / "objectList": nested field shapes (paths are RELATIVE)
}

// Common metadata fields shared by every kind.
const META: Field[] = [
  { path: "metadata.name", label: "Name", type: "text", placeholder: "my-artifact", required: true, mono: true, help: "Unique identifier within the collection." },
  { path: "metadata.tag", label: "Version", type: "text", placeholder: "auto · v0.0.1", mono: true, help: "Leave blank to auto-increment (v0.0.1, v0.0.2, …); set one (e.g. v1.2.0) to pin." },
  { path: "metadata.visibility", label: "Visibility", type: "select", options: ["public", "internal", "private"] },
];

// Spec fields per kind.
const SPEC: Record<string, Field[]> = {
  Skill: [
    { path: "spec.title", label: "Title", type: "text", placeholder: "Senior Go Engineer", required: true },
    { path: "spec.description", label: "Description", type: "textarea", placeholder: "What this skill does and when to use it." },
    { path: "spec.source.repository", label: "Source repository", type: "text", placeholder: "https://github.com/org/repo", mono: true },
  ],
  Tool: [
    { path: "spec.title", label: "Title", type: "text", placeholder: "Postgres Query", required: true },
    { path: "spec.description", label: "Description", type: "textarea", placeholder: "What the tool does and its inputs/outputs." },
  ],
  MCPServer: [
    { path: "spec.name", label: "Server name", type: "text", placeholder: "io.github.acme/files", required: true, mono: true },
    { path: "spec.version", label: "Version", type: "text", placeholder: "1.0.0", mono: true },
    { path: "spec.description", label: "Description", type: "textarea", placeholder: "What this MCP server exposes." },
    { path: "spec.packages.0.registryType", label: "Package registry", type: "select", options: ["npm", "pypi", "oci", "nuget"] },
    { path: "spec.packages.0.identifier", label: "Package identifier", type: "text", placeholder: "@acme/files-mcp", mono: true },
    { path: "spec.packages.0.transport.type", label: "Transport", type: "select", options: ["stdio", "sse", "streamableHttp"] },
  ],
  Prompt: [
    { path: "spec.title", label: "Title", type: "text", placeholder: "Summarize Incident", required: true },
    { path: "spec.description", label: "Description", type: "textarea", placeholder: "What this prompt produces." },
    { path: "spec.template", label: "Template", type: "textarea", placeholder: "Summarize the following:\n\n{{input}}", mono: true, help: "Use {{variables}} for runtime values." },
  ],
  Workflow: [
    { path: "spec.title", label: "Title", type: "text", placeholder: "Release Train", required: true },
    { path: "spec.description", label: "Description", type: "textarea", placeholder: "What this workflow orchestrates." },
  ],
  Blueprint: [
    { path: "spec.title", label: "Title", type: "text", placeholder: "RAG Chatbot", required: true },
    { path: "spec.description", label: "Description", type: "textarea", placeholder: "The pattern this blueprint wires together." },
  ],
  Agent: [
    { path: "spec.title", label: "Title", type: "text", placeholder: "On-Call Responder", required: true },
    { path: "spec.description", label: "Description", type: "textarea", placeholder: "What the agent does autonomously." },
    {
      path: "spec.model", label: "Model", type: "group", help: "The LLM this agent reasons with.", children: [
        { path: "provider", label: "Provider", type: "select", options: ["", "anthropic", "openai", "google", "groq"] },
        { path: "name", label: "Model name", type: "text", placeholder: "claude-sonnet-4", mono: true },
        { path: "temperature", label: "Temperature", type: "text", placeholder: "0.3", mono: true, help: "0–2." },
      ],
    },
    { path: "spec.systemPrompt", label: "System prompt", type: "textarea", placeholder: "You are an on-call SRE. …", help: "The agent's base instruction. References to Prompts below are available at runtime." },
    { path: "spec.skills", label: "Skills", type: "refList", itemKind: "skills", help: "Registry Skills this agent can perform." },
    { path: "spec.tools", label: "Tools", type: "refList", itemKind: "tools", help: "Registry Tools the agent may call." },
    { path: "spec.mcpServers", label: "MCP servers", type: "refList", itemKind: "mcpservers", help: "MCP servers the agent connects to for tools." },
    { path: "spec.prompts", label: "Prompts", type: "refList", itemKind: "prompts", help: "Reusable Prompts available to the agent." },
    {
      path: "spec.a2a", label: "A2A (advanced)", type: "group", help: "Agent-to-agent protocol config (optional).", children: [
        { path: "url", label: "Service URL", type: "text", placeholder: "https://…", mono: true, help: "Where consumers reach this agent over A2A." },
        { path: "preferredTransport", label: "Transport", type: "select", options: ["", "JSONRPC", "GRPC"] },
      ],
    },
  ],
};

export function fieldsFor(kind: string): Field[] {
  return [...META, ...(SPEC[kind] ?? SPEC.Skill)];
}

// starter returns a fresh, minimal doc for a kind with sensible defaults so the
// editor and the live YAML are never empty.
export function starter(kind: string): Record<string, unknown> {
  const meta = { name: "", tag: "", visibility: "public", labels: {} as Record<string, string> };
  const specByKind: Record<string, Record<string, unknown>> = {
    Skill: { title: "", description: "", source: { repository: "" } },
    Tool: { title: "", description: "" },
    MCPServer: {
      name: "",
      version: "1.0.0",
      description: "",
      packages: [{ registryType: "npm", identifier: "", transport: { type: "stdio" } }],
    },
    Prompt: { title: "", description: "", template: "" },
    Workflow: { title: "", description: "", nodes: [], edges: [] },
    Blueprint: { title: "", description: "", nodes: [], edges: [] },
    Agent: {
      title: "", description: "",
      model: { provider: "", name: "", temperature: "" },
      systemPrompt: "",
      skills: [], tools: [], mcpServers: [], prompts: [],
      a2a: { url: "", preferredTransport: "" },
    },
  };
  return { apiVersion: API_VERSION, kind, metadata: meta, spec: specByKind[kind] ?? specByKind.Skill };
}

// ── Manifest linting / safety scan ──────────────────────────────────────────
// Uploaded (or hand-edited) manifests are scanned before they can be published:
// they must be structurally valid, match the registry standard for their kind,
// and carry no obviously unsafe content. Errors block publish; warnings inform.

export interface LintIssue {
  level: "error" | "warning";
  message: string;
}

// RFC-1123 style identifier, same shape the server enforces for metadata.name.
const NAME_RE = /^[a-z0-9]([-a-z0-9]*[a-z0-9])?$/;
const MAX_NAME = 253;
const MAX_BYTES = 256 * 1024; // reject absurdly large manifests outright.

const TOP_LEVEL = new Set(["apiVersion", "kind", "metadata", "spec", "status"]);

// Patterns that have no business in a declarative manifest — flagged as unsafe
// so we never store markup/script payloads smuggled through a field.
const UNSAFE: { re: RegExp; what: string }[] = [
  { re: /<script\b/i, what: "embedded <script> markup" },
  { re: /<\/?iframe\b/i, what: "embedded <iframe> markup" },
  { re: /javascript:/i, what: "javascript: URI" },
  { re: /data:text\/html/i, what: "data:text/html URI" },
  { re: /\bon(?:load|error|click|mouseover)\s*=/i, what: "inline event handler" },
];
// Control chars (except tab/newline/carriage-return) — a sign of binary/junk.
// eslint-disable-next-line no-control-regex
const CONTROL_RE = /[\u0000-\u0008\u000B\u000C\u000E-\u001F]/;

function requiredPaths(kind: string): Field[] {
  return fieldsFor(kind).filter((f) => f.required);
}

// lintManifest validates a parsed manifest against the registry standard for
// `expectedKind`. Returns [] when the manifest is clean.
export function lintManifest(doc: unknown, expectedKind: string): LintIssue[] {
  const issues: LintIssue[] = [];
  const err = (message: string) => issues.push({ level: "error", message });
  const warn = (message: string) => issues.push({ level: "warning", message });

  if (doc == null || typeof doc !== "object" || Array.isArray(doc)) {
    return [{ level: "error", message: "manifest must be a single object (apiVersion/kind/metadata/spec)" }];
  }
  const d = doc as Record<string, unknown>;

  // Size & content-safety scan over the raw serialized form.
  let serialized = "";
  try {
    serialized = JSON.stringify(d);
  } catch {
    return [{ level: "error", message: "manifest contains values that cannot be serialized (circular or invalid)" }];
  }
  if (serialized.length > MAX_BYTES) err(`manifest is too large (${Math.round(serialized.length / 1024)}KB; max ${MAX_BYTES / 1024}KB)`);
  if (CONTROL_RE.test(serialized)) err("manifest contains control/binary characters");
  for (const { re, what } of UNSAFE) if (re.test(serialized)) err(`manifest contains unsafe content: ${what}`);

  // apiVersion / kind.
  if (!d.apiVersion) err("missing required field: apiVersion");
  else if (d.apiVersion !== API_VERSION) warn(`apiVersion is "${String(d.apiVersion)}"; expected "${API_VERSION}"`);

  if (!d.kind) err("missing required field: kind");
  else if (typeof d.kind === "string" && d.kind !== expectedKind) err(`kind is "${d.kind}" but this collection only accepts "${expectedKind}"`);

  // metadata + name.
  const meta = d.metadata as Record<string, unknown> | undefined;
  if (!meta || typeof meta !== "object") {
    err("missing required field: metadata");
  } else {
    const name = meta.name;
    if (!name || typeof name !== "string" || !name.trim()) err("missing required field: metadata.name");
    else if (name.length > MAX_NAME) err(`metadata.name is too long (max ${MAX_NAME} chars)`);
    else if (!NAME_RE.test(name)) err(`metadata.name "${name}" is invalid (use lowercase letters, digits and hyphens, e.g. my-skill)`);

    if (meta.visibility != null && !["public", "internal", "private"].includes(String(meta.visibility))) {
      err(`metadata.visibility "${String(meta.visibility)}" is invalid (public | internal | private)`);
    }
    if (meta.labels != null && (typeof meta.labels !== "object" || Array.isArray(meta.labels))) {
      err("metadata.labels must be a string→string map");
    }
  }

  // spec presence + required per-kind fields.
  if (d.spec == null || typeof d.spec !== "object" || Array.isArray(d.spec)) {
    err("missing required field: spec");
  } else {
    for (const f of requiredPaths(expectedKind)) {
      if (f.path.startsWith("metadata.")) continue; // handled above
      const v = getPath(d, f.path);
      if (v == null || (typeof v === "string" && !v.trim())) err(`missing required field: ${f.path} (${f.label})`);
    }
  }

  // Unknown top-level keys → warn (forward-compatible, but worth surfacing).
  for (const k of Object.keys(d)) if (!TOP_LEVEL.has(k)) warn(`unexpected top-level field "${k}" will be ignored`);

  return issues;
}

// getPath / setPath operate on dot-paths, creating intermediate objects/arrays.
export function getPath(obj: unknown, path: string): unknown {
  return path.split(".").reduce<unknown>((acc, key) => {
    if (acc == null) return undefined;
    return (acc as Record<string, unknown>)[key];
  }, obj);
}

export function setPath(obj: Record<string, unknown>, path: string, value: unknown): Record<string, unknown> {
  const keys = path.split(".");
  const clone = structuredClone(obj);
  let cur: Record<string, unknown> = clone;
  for (let i = 0; i < keys.length - 1; i++) {
    const k = keys[i];
    const nextIsIndex = /^\d+$/.test(keys[i + 1]);
    if (cur[k] == null) cur[k] = nextIsIndex ? [] : {};
    cur = cur[k] as Record<string, unknown>;
  }
  cur[keys[keys.length - 1]] = value;
  return clone;
}
