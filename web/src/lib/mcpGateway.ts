import type { Artifact } from "./api";

export type McpClient =
  | "codex"
  | "cursor"
  | "claude-code"
  | "vscode";

export const MCP_CLIENTS = [
  { id: "codex", label: "Codex" },
  { id: "claude-code", label: "Claude Code" },
  { id: "cursor", label: "Cursor" },
  { id: "vscode", label: "VS Code" },
] as const satisfies readonly { id: McpClient; label: string }[];

export const MCP_GATEWAY_ORIGIN = "https://mcp.tesserix.app";
export const AGENTGATEWAY_PROJECT_ID = "386889024519799084";

export interface McpGatewayUserInfo {
  user: string;
  email: string;
  groups?: string[];
  preferredUsername?: string;
}

export interface McpGatewayProfile {
  displayName: string;
  email: string;
  initials: string;
  role: "Administrator";
}

type Requester = (
  input: RequestInfo | URL,
  init?: RequestInit,
) => Promise<Response>;

export function isMcpGatewayHost(hostname: string): boolean {
  return hostname.toLowerCase() === "mcp.tesserix.app";
}

export const TENANT_LABEL = "mcp.tesserix.app/tenant";
export const DEFAULT_TENANT = "default";

export function gatewayEndpoint(
  origin: string,
  tenant: string,
  serverName: string,
): string {
  return `${origin.replace(/\/$/, "")}/mcp/${encodeURIComponent(tenant)}/${encodeURIComponent(serverName)}`;
}

// Mirrors the export adapter's tenant resolution: a route the UI advertises
// has to be the route the gateway serves.
export function serverTenant(server: Artifact): string {
  return (
    server.metadata.labels?.[TENANT_LABEL] ||
    server.metadata.namespace ||
    DEFAULT_TENANT
  );
}

// The per-server scope a caller's token must carry to reach this server.
export function serverScope(server: Artifact): string {
  return `mcp:${serverTenant(server)}:${server.metadata.name}`;
}

export type ProbeState = "ready" | "drifted" | "unreachable" | "unprobed";

export interface ProbeStatus {
  state: ProbeState;
  message?: string;
  tools?: string[];
  lastProbedAt?: string;
}

interface StatusCondition {
  type?: string;
  status?: string;
  reason?: string;
  message?: string;
}

// Reachability is what the capability probe observed, never what the catalog
// row asserts — a server that is listed is not a server that answers.
export function probeStatus(server: Artifact): ProbeStatus {
  const conditions = (server.status?.conditions ?? []) as StatusCondition[];
  if (!Array.isArray(conditions) || conditions.length === 0) {
    return { state: "unprobed" };
  }
  const find = (type: string) =>
    conditions.find((condition) => condition.type === type);
  const observed = server.status?.observedTools;
  const base = {
    tools: Array.isArray(observed) ? (observed as string[]) : undefined,
    lastProbedAt:
      typeof server.status?.lastProbedAt === "string"
        ? server.status.lastProbedAt
        : undefined,
  };

  const unreachable = find("Unreachable");
  if (unreachable?.status === "True") {
    return { ...base, state: "unreachable", message: unreachable.message };
  }
  const drifted = find("Drifted");
  if (drifted?.status === "True") {
    return { ...base, state: "drifted", message: drifted.message };
  }
  const ready = find("Ready");
  if (ready?.status === "True") {
    return { ...base, state: "ready", message: ready.message };
  }
  return { ...base, state: "unreachable", message: ready?.message };
}

export function serverDisplayName(serverName: string): string {
  return serverName
    .replace(/^catalog-/, "")
    .replace(/-mcp$/, "")
    .split("-")
    .filter(Boolean)
    .map((part) => `${part.charAt(0).toUpperCase()}${part.slice(1)}`)
    .join(" ");
}

export function profileFromUserInfo(
  userInfo: McpGatewayUserInfo,
): McpGatewayProfile {
  const email = userInfo.email.trim();
  if (!email) throw new Error("OAuth session did not include an email");

  const displayName = userInfo.preferredUsername?.trim() || email;
  const nameParts = displayName
    .replace(/@.*$/, "")
    .split(/[\s._-]+/)
    .filter(Boolean);
  const initials = nameParts
    .slice(0, 2)
    .map((part) => part.charAt(0).toUpperCase())
    .join("");

  return {
    displayName,
    email,
    initials: initials || email.charAt(0).toUpperCase(),
    role: "Administrator",
  };
}

export async function loadMcpGatewayProfile(
  request: Requester = fetch,
): Promise<McpGatewayProfile> {
  const response = await request("/oauth2/userinfo", {
    headers: { Accept: "application/json" },
  });
  if (!response.ok) throw new Error("Unable to load OAuth session profile");
  return profileFromUserInfo(
    (await response.json()) as McpGatewayUserInfo,
  );
}

export function installConfig(
  client: McpClient,
  tenant: string,
  serverName: string,
  origin: string,
): string {
  const endpoint = gatewayEndpoint(origin, tenant, serverName);

  if (client === "codex") {
    return `[mcp_servers.${serverName}]\nurl = "${endpoint}"\nbearer_token_env_var = "TESSERIX_MCP_TOKEN"`;
  }

  if (client === "claude-code") {
    return `claude mcp add --transport http ${serverName} ${endpoint} --header "Authorization: Bearer \${TESSERIX_MCP_TOKEN}"`;
  }

  const rootKey = client === "vscode" ? "servers" : "mcpServers";

  return JSON.stringify(
    {
      [rootKey]: {
        [serverName]: {
          type: "http",
          url: endpoint,
          headers: {
            Authorization: "Bearer ${env:TESSERIX_MCP_TOKEN}",
          },
        },
      },
    },
    null,
    2,
  );
}

export function tokenRequestCommand(): string {
  return `curl --fail-with-body --user "\${TESSERIX_MCP_CLIENT_ID}:\${TESSERIX_MCP_CLIENT_SECRET}" \\
  --data-urlencode "grant_type=client_credentials" \\
  --data-urlencode "scope=openid urn:zitadel:iam:org:project:id:${AGENTGATEWAY_PROJECT_ID}:aud urn:zitadel:iam:org:projects:roles" \\
  https://auth.tesserix.app/oauth/v2/token`;
}
