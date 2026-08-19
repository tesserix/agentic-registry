import { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import {
  ArrowLeft,
  Check,
  CheckCircle2,
  Clipboard,
  Code2,
  ExternalLink,
  Loader2,
  MessageSquareText,
  Package,
  ShieldCheck,
  TriangleAlert,
  Wrench,
} from "lucide-react";
import { api, type ResolvedMCPServer } from "../lib/api";
import {
  gatewayEndpoint,
  installConfig,
  MCP_GATEWAY_ORIGIN,
  MCP_CLIENTS,
  serverDisplayName,
  type McpClient,
} from "../lib/mcpGateway";

function text(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function listLength(value: unknown): number {
  return Array.isArray(value) ? value.length : 0;
}

async function copy(value: string, onCopied: () => void) {
  await navigator.clipboard.writeText(value);
  onCopied();
}

export default function MCPGatewayServer() {
  const { name = "" } = useParams();
  const [searchParams] = useSearchParams();
  const namespace = searchParams.get("namespace") || "default";
  const [resolved, setResolved] = useState<ResolvedMCPServer | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [client, setClient] = useState<McpClient>("codex");
  const [copied, setCopied] = useState<"endpoint" | "config" | null>(null);

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    api
      .resolvedMcp(name, namespace)
      .then((result) => {
        if (!cancelled) setResolved(result);
      })
      .catch((cause) => {
        if (!cancelled) setError(cause instanceof Error ? cause.message : String(cause));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [name, namespace]);

  if (loading) {
    return (
      <div className="flex items-center justify-center gap-2 py-32 text-[13px] text-[var(--ink-muted)]">
        <Loader2 className="h-4 w-4 spin" /> Resolving server capabilities…
      </div>
    );
  }

  if (error || !resolved) {
    return (
      <div className="mx-auto max-w-4xl px-5 py-12">
        <Link to="/" className="btn-ghost mb-5 -ml-3">
          <ArrowLeft className="h-4 w-4" /> Back to servers
        </Link>
        <div className="panel p-7 text-[13px] text-[var(--error-ink)]">
          Could not resolve this MCP server: {error ?? "not found"}
        </div>
      </div>
    );
  }

  const server = resolved.mcpServer;
  const displayName = serverDisplayName(server.metadata.name);
  const description =
    text(server.spec?.description) ||
    "A managed Model Context Protocol server available through the Tesserix gateway.";
  const endpoint = gatewayEndpoint(MCP_GATEWAY_ORIGIN, server.metadata.name);
  const config = installConfig(client, server.metadata.name, MCP_GATEWAY_ORIGIN);
  const condition = resolved.conditions.find((item) => item.type === "Resolved");
  const healthy = condition?.status !== "False";
  const promptCount = listLength(server.spec?.prompts);
  const resourceCount = listLength(server.spec?.resources);

  return (
    <div className="mx-auto max-w-[1280px] px-4 py-8 sm:px-8 sm:py-10">
      <Link to="/" className="btn-ghost -ml-3 mb-5">
        <ArrowLeft className="h-4 w-4" /> MCP servers
      </Link>

      <section className="mcp-detail-hero rounded-[22px] border p-6 sm:p-8">
        <div className="flex flex-col gap-6 lg:flex-row lg:items-start lg:justify-between">
          <div className="flex min-w-0 items-start gap-4">
            <span className="mcp-server-icon grid h-16 w-16 shrink-0 place-items-center rounded-2xl text-[24px] font-bold">
              {displayName.charAt(0) || "M"}
            </span>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h1 className="text-[27px] font-bold tracking-[-0.025em] text-[var(--ink-strong)]">
                  {displayName}
                </h1>
                <span className="chip badge-verified">
                  <ShieldCheck className="h-3 w-3" /> Managed
                </span>
              </div>
              <p className="mt-1 break-all font-mono text-[11px] text-[var(--ink-muted)]">
                {server.metadata.name}
              </p>
              <p className="mt-4 max-w-2xl text-[13px] leading-6 text-[var(--ink-soft)]">
                {description}
              </p>
            </div>
          </div>
          <div
            className={`flex shrink-0 items-center gap-2 rounded-xl border px-3.5 py-2 text-[12px] ${
              healthy
                ? "border-[var(--ok-soft-bd)] bg-[var(--ok-soft-bg)] text-[var(--ok-ink)]"
                : "border-[var(--warn-soft-bd)] bg-[var(--warn-soft-bg)] text-[var(--warn-ink)]"
            }`}
          >
            {healthy ? (
              <CheckCircle2 className="h-4 w-4" />
            ) : (
              <TriangleAlert className="h-4 w-4" />
            )}
            {condition?.message ?? "Server resolved"}
          </div>
        </div>

        <div className="mt-7 rounded-xl border border-[var(--border)] bg-[var(--surface-muted)] p-3 sm:flex sm:items-center sm:gap-3">
          <div className="mb-2 flex items-center gap-2 text-[11px] font-semibold text-[var(--ink-soft)] sm:mb-0">
            <Code2 className="h-4 w-4" /> MCP endpoint
          </div>
          <code className="block min-w-0 flex-1 overflow-x-auto whitespace-nowrap font-mono text-[11px] text-[var(--ink-strong)]">
            {endpoint}
          </code>
          <button
            className="btn-secondary mt-3 w-full sm:mt-0 sm:w-auto"
            onClick={() => copy(endpoint, () => setCopied("endpoint"))}
          >
            {copied === "endpoint" ? (
              <Check className="h-4 w-4" />
            ) : (
              <Clipboard className="h-4 w-4" />
            )}
            {copied === "endpoint" ? "Copied" : "Copy"}
          </button>
        </div>
      </section>

      <div className="mt-6 grid gap-6 lg:grid-cols-[minmax(0,1fr)_360px]">
        <div className="space-y-6">
          <section className="panel overflow-hidden">
            <div className="border-b border-[var(--border-subtle)] px-6 py-5">
              <div className="label-eyebrow">Capabilities</div>
              <h2 className="mt-1 text-[18px] font-semibold text-[var(--ink-strong)]">
                Available tools
              </h2>
            </div>
            {resolved.tools.length === 0 ? (
              <div className="p-8 text-center text-[13px] text-[var(--ink-muted)]">
                No registry tools are currently resolved for this server.
              </div>
            ) : (
              <div className="divide-y divide-[var(--border-subtle)]">
                {resolved.tools.map((tool) => (
                  <div
                    key={`${tool.metadata.namespace}/${tool.metadata.name}`}
                    className="flex gap-3 px-6 py-4"
                  >
                    <span className="mt-0.5 grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-[var(--accent-soft-bg)] text-[var(--accent)]">
                      <Wrench className="h-4 w-4" />
                    </span>
                    <div className="min-w-0">
                      <div className="text-[13px] font-semibold text-[var(--ink-strong)]">
                        {text(tool.spec?.title) || tool.metadata.name}
                      </div>
                      <div className="mt-0.5 font-mono text-[10px] text-[var(--ink-muted)]">
                        {tool.metadata.name}
                      </div>
                      {text(tool.spec?.description) && (
                        <p className="mt-2 text-[12px] leading-5 text-[var(--ink-soft)]">
                          {text(tool.spec?.description)}
                        </p>
                      )}
                    </div>
                  </div>
                ))}
              </div>
            )}
          </section>

          <section className="panel overflow-hidden">
            <div className="border-b border-[var(--border-subtle)] px-6 py-5">
              <div className="label-eyebrow">Connect</div>
              <h2 className="mt-1 text-[18px] font-semibold text-[var(--ink-strong)]">
                Install in your agent
              </h2>
              <p className="mt-1 text-[12px] text-[var(--ink-muted)]">
                Generate a short-lived bearer token first, then use the exact
                registered endpoint below.
              </p>
            </div>
            <div className="flex gap-1 overflow-x-auto border-b border-[var(--border-subtle)] px-4 pt-3">
              {MCP_CLIENTS.map((item) => (
                <button
                  key={item.id}
                  onClick={() => {
                    setClient(item.id);
                    setCopied(null);
                  }}
                  className={`mcp-client-tab ${client === item.id ? "active" : ""}`}
                >
                  {item.label}
                </button>
              ))}
            </div>
            <div className="p-5">
              <div className="relative overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--code-bg)]">
                <pre className="overflow-x-auto p-5 pr-16 text-[11px] leading-5 text-[var(--code-ink)]">
                  <code>{config}</code>
                </pre>
                <button
                  className="absolute right-3 top-3 rounded-lg border border-white/10 bg-white/10 p-2 text-white/70 hover:bg-white/15 hover:text-white"
                  onClick={() => copy(config, () => setCopied("config"))}
                  aria-label="Copy client configuration"
                >
                  {copied === "config" ? (
                    <Check className="h-4 w-4" />
                  ) : (
                    <Clipboard className="h-4 w-4" />
                  )}
                </button>
              </div>
              <Link to="/access" className="btn-secondary mt-4">
                <ShieldCheck className="h-4 w-4" /> Get OAuth access
              </Link>
            </div>
          </section>
        </div>

        <aside className="space-y-6">
          <section className="panel p-5">
            <div className="label-eyebrow">Server summary</div>
            <dl className="mt-4 space-y-4">
              <div className="flex items-center justify-between gap-3">
                <dt className="flex items-center gap-2 text-[12px] text-[var(--ink-soft)]">
                  <Wrench className="h-4 w-4" /> Tools
                </dt>
                <dd className="font-mono text-[12px] text-[var(--ink-strong)]">
                  {resolved.toolCount}
                </dd>
              </div>
              <div className="flex items-center justify-between gap-3">
                <dt className="flex items-center gap-2 text-[12px] text-[var(--ink-soft)]">
                  <MessageSquareText className="h-4 w-4" /> Prompts
                </dt>
                <dd className="font-mono text-[12px] text-[var(--ink-strong)]">
                  {promptCount}
                </dd>
              </div>
              <div className="flex items-center justify-between gap-3">
                <dt className="flex items-center gap-2 text-[12px] text-[var(--ink-soft)]">
                  <Package className="h-4 w-4" /> Resources
                </dt>
                <dd className="font-mono text-[12px] text-[var(--ink-strong)]">
                  {resourceCount}
                </dd>
              </div>
            </dl>
          </section>

          <section className="panel p-5">
            <div className="flex items-center gap-2 text-[13px] font-semibold text-[var(--ink-strong)]">
              <ShieldCheck className="h-4 w-4 text-[var(--ok)]" /> Security boundary
            </div>
            <p className="mt-3 text-[12px] leading-5 text-[var(--ink-soft)]">
              Every request requires a Zitadel access token with the correct
              issuer, audience, and <code>agentgateway.mcp</code> role. Limits
              are isolated per OAuth subject.
            </p>
          </section>

          <a
            href="https://modelcontextprotocol.io/docs"
            target="_blank"
            rel="noreferrer"
            className="btn-secondary w-full"
          >
            MCP protocol docs <ExternalLink className="h-4 w-4" />
          </a>
        </aside>
      </div>
    </div>
  );
}
