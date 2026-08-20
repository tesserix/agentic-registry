import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import {
  ArrowRight,
  Boxes,
  Loader2,
  PackageOpen,
  Search,
  Server,
  ShieldCheck,
  Sparkles,
  Wrench,
} from "lucide-react";
import { api, type Artifact } from "../lib/api";
import { serverDisplayName, serverTenant } from "../lib/mcpGateway";
import ProbeBadge from "../components/ProbeBadge";

function labelsFor(server: Artifact): string[] {
  return Object.values(server.metadata.labels ?? {}).filter(Boolean);
}

function categoryFor(server: Artifact): string {
  const labels = server.metadata.labels ?? {};
  return labels.category ?? labels.provider ?? labels.domain ?? "General";
}

function declaredToolCount(server: Artifact): number | null {
  return Array.isArray(server.spec?.tools) ? server.spec.tools.length : null;
}

function ServerCard({ server }: { server: Artifact }) {
  const displayName = serverDisplayName(server.metadata.name);
  const description =
    typeof server.spec?.description === "string"
      ? server.spec.description
      : "A managed Model Context Protocol server available through the Tesserix gateway.";
  const toolCount = declaredToolCount(server);
  const namespace = server.metadata.namespace ?? "default";

  return (
    <Link
      to={`/servers/${encodeURIComponent(server.metadata.name)}?namespace=${encodeURIComponent(namespace)}`}
      className="mcp-server-card group flex min-h-[246px] flex-col rounded-2xl border p-5"
    >
      <div className="flex items-start justify-between gap-3">
        <span className="mcp-server-icon grid h-12 w-12 place-items-center rounded-xl text-[18px] font-bold">
          {displayName.charAt(0) || "M"}
        </span>
        <ProbeBadge server={server} />
      </div>

      <div className="mt-4">
        <h2 className="text-[16px] font-semibold text-[var(--ink-strong)]">
          {displayName}
        </h2>
        <p className="mt-1 font-mono text-[10px] text-[var(--ink-muted)]">
          {server.metadata.name}
        </p>
        <p className="mt-3 line-clamp-3 text-[12.5px] leading-5 text-[var(--ink-soft)]">
          {description}
        </p>
      </div>

      <div className="mt-auto flex items-center gap-2 border-t border-[var(--border-subtle)] pt-4">
        <span className="chip">
          <Wrench className="h-3 w-3" />
          {toolCount == null
            ? "Tools"
            : `${toolCount} ${toolCount === 1 ? "tool" : "tools"}`}
        </span>
        <span className="chip capitalize">{categoryFor(server)}</span>
        <span className="chip font-mono lowercase">{serverTenant(server)}</span>
        <ArrowRight className="ml-auto h-4 w-4 text-[var(--ink-muted)] transition-transform group-hover:translate-x-1" />
      </div>
    </Link>
  );
}

export default function MCPGatewayHome() {
  const [servers, setServers] = useState<Artifact[]>([]);
  const [search, setSearch] = useState("");
  const [category, setCategory] = useState("All");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    api
      .list("mcpservers")
      .then((items) => {
        if (!cancelled) setServers(items);
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
  }, []);

  const categories = useMemo(
    () => [
      "All",
      ...Array.from(new Set(servers.map(categoryFor))).sort((a, b) =>
        a.localeCompare(b),
      ),
    ],
    [servers],
  );

  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase();
    return servers
      .filter((server) => category === "All" || categoryFor(server) === category)
      .filter((server) => {
        if (!query) return true;
        const description =
          typeof server.spec?.description === "string"
            ? server.spec.description
            : "";
        return [
          server.metadata.name,
          serverDisplayName(server.metadata.name),
          description,
          ...labelsFor(server),
        ].some((value) => value.toLowerCase().includes(query));
      })
      .sort((a, b) =>
        serverDisplayName(a.metadata.name).localeCompare(
          serverDisplayName(b.metadata.name),
        ),
      );
  }, [category, search, servers]);

  return (
    <div className="mx-auto max-w-[1440px] px-4 py-8 sm:px-8 sm:py-10">
      <section className="mcp-hero overflow-hidden rounded-[24px] border px-6 py-8 sm:px-9 sm:py-10">
        <div className="max-w-2xl">
          <div className="mb-4 inline-flex items-center gap-2 rounded-full border border-[var(--accent-soft-bd)] bg-[var(--accent-soft-bg)] px-3 py-1 text-[11px] font-semibold text-[var(--accent-soft-ink)]">
            <Sparkles className="h-3.5 w-3.5" />
            Tesserix Agent Infrastructure
          </div>
          <h1 className="max-w-xl text-[30px] font-bold leading-[1.12] tracking-[-0.035em] text-[var(--ink-strong)] sm:text-[40px]">
            One secure gateway for every agent tool
          </h1>
          <p className="mt-4 max-w-xl text-[14px] leading-6 text-[var(--ink-soft)]">
            Discover approved MCP servers, inspect their capabilities, and connect
            Codex, Claude Code, Cursor, VS Code, or any OAuth-capable agent.
          </p>
        </div>
        <div className="mt-8 grid max-w-2xl grid-cols-1 gap-3 sm:grid-cols-3">
          <div className="mcp-stat">
            <Server className="h-4 w-4" />
            <strong>{servers.length}</strong>
            <span>Managed servers</span>
          </div>
          <div className="mcp-stat">
            <ShieldCheck className="h-4 w-4" />
            <strong>OAuth 2.0</strong>
            <span>Default-deny access</span>
          </div>
          <div className="mcp-stat">
            <Boxes className="h-4 w-4" />
            <strong>1 catalog</strong>
            <span>Registry-backed</span>
          </div>
        </div>
      </section>

      <section className="mt-9">
        <div className="flex flex-col gap-5 lg:flex-row lg:items-end lg:justify-between">
          <div>
            <div className="label-eyebrow">Server directory</div>
            <h2 className="mt-2 text-[23px] font-semibold text-[var(--ink-strong)]">
              Available MCP servers
            </h2>
            <p className="mt-1 text-[12px] text-[var(--ink-muted)]">
              {filtered.length} of {servers.length} approved integrations
            </p>
          </div>
          <div className="relative w-full lg:max-w-md">
            <label htmlFor="mcp-server-search" className="sr-only">
              Search MCP servers
            </label>
            <Search className="absolute left-3.5 top-1/2 h-4 w-4 -translate-y-1/2 text-[var(--ink-muted)]" />
            <input
              id="mcp-server-search"
              className="field"
              style={{ paddingLeft: 39 }}
              placeholder="Search servers, tools, or providers…"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
          </div>
        </div>

        <div className="mt-5 flex gap-2 overflow-x-auto pb-2">
          {categories.map((value) => (
            <button
              key={value}
              onClick={() => setCategory(value)}
              className={`mcp-filter ${category === value ? "active" : ""}`}
            >
              {value}
            </button>
          ))}
        </div>

        <div className="mt-5">
          {loading ? (
            <div className="flex items-center justify-center gap-2 py-24 text-[13px] text-[var(--ink-muted)]">
              <Loader2 className="h-4 w-4 spin" /> Loading MCP servers…
            </div>
          ) : error ? (
            <div className="panel p-7 text-[13px] text-[var(--error-ink)]">
              Could not load the registry: {error}
            </div>
          ) : filtered.length === 0 ? (
            <div className="panel flex flex-col items-center gap-3 p-14 text-center text-[var(--ink-muted)]">
              <PackageOpen className="h-7 w-7" />
              <p>No MCP servers match this search.</p>
            </div>
          ) : (
            <div className="grid grid-cols-1 gap-5 md:grid-cols-2 xl:grid-cols-3">
              {filtered.map((server) => (
                <ServerCard
                  key={`${server.metadata.namespace}/${server.metadata.name}`}
                  server={server}
                />
              ))}
            </div>
          )}
        </div>
      </section>
    </div>
  );
}
