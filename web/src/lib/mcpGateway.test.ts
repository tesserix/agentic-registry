import { describe, expect, it, vi } from "vitest";
import {
  gatewayEndpoint,
  installConfig,
  isMcpGatewayHost,
  loadMcpGatewayProfile,
  MCP_CLIENTS,
  profileFromUserInfo,
  probeStatus,
  serverDisplayName,
  serverScope,
  serverTenant,
  tokenRequestCommand,
} from "./mcpGateway";

describe("MCP gateway presentation", () => {
  it("offers only supported agent clients", () => {
    expect(MCP_CLIENTS).toEqual([
      { id: "codex", label: "Codex" },
      { id: "claude-code", label: "Claude Code" },
      { id: "cursor", label: "Cursor" },
      { id: "vscode", label: "VS Code" },
    ]);
  });

  it("enables the product UI only on the MCP gateway host", () => {
    expect(isMcpGatewayHost("mcp.tesserix.app")).toBe(true);
    expect(isMcpGatewayHost("aregistry.tesserix.app")).toBe(false);
    expect(isMcpGatewayHost("localhost")).toBe(false);
  });

  it("keeps the registered route name in the tenant-scoped endpoint", () => {
    expect(
      gatewayEndpoint("https://mcp.tesserix.app/", "devai", "catalog-atlassian-mcp"),
    ).toBe("https://mcp.tesserix.app/mcp/devai/catalog-atlassian-mcp");
  });

  it("formats catalog names for people without changing identity", () => {
    expect(serverDisplayName("catalog-atlassian-mcp")).toBe("Atlassian");
    expect(serverDisplayName("gitops-mcp")).toBe("Gitops");
  });

  it("generates client configuration from the exact gateway endpoint", () => {
    expect(
      installConfig(
        "codex",
        "devai",
        "catalog-atlassian-mcp",
        "https://mcp.tesserix.app",
      ),
    ).toContain(
      'url = "https://mcp.tesserix.app/mcp/devai/catalog-atlassian-mcp"',
    );
    expect(
      installConfig(
        "cursor",
        "devai",
        "catalog-atlassian-mcp",
        "https://mcp.tesserix.app",
      ),
    ).toContain('"type": "http"');
  });

  it("generates bearer-token setup for every supported agent client", () => {
    const configs = {
      "claude-code": "claude mcp add --transport http",
      vscode: '"servers"',
    } as const;

    for (const [client, marker] of Object.entries(configs)) {
      const config = installConfig(
        client as keyof typeof configs,
        "devai",
        "catalog-atlassian-mcp",
        "https://mcp.tesserix.app",
      );
      expect(config).toContain(marker);
      expect(config).toContain(
        "https://mcp.tesserix.app/mcp/devai/catalog-atlassian-mcp",
      );
      expect(config).toContain("TESSERIX_MCP_TOKEN");
    }
  });

  it("documents short-lived OAuth tokens without embedding credentials", () => {
    const command = tokenRequestCommand();

    expect(command).toContain("https://auth.tesserix.app/oauth/v2/token");
    expect(command).toContain("387190457387450503:aud");
    expect(command).toContain("TESSERIX_MCP_CLIENT_SECRET");
    expect(command).not.toContain("client_secret=");
  });

  it("maps the verified OAuth session to an administrator profile", () => {
    expect(
      profileFromUserInfo({
        user: "zitadel-user-id",
        email: "samyak.rout@gmail.com",
        preferredUsername: "Samyak Rout",
      }),
    ).toEqual({
      displayName: "Samyak Rout",
      email: "samyak.rout@gmail.com",
      initials: "SR",
      role: "Administrator",
    });
  });

  it("loads profile data only from oauth2-proxy userinfo", async () => {
    const request = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          user: "zitadel-user-id",
          email: "mahesh.sangawar@gmail.com",
        }),
        { status: 200 },
      ),
    );

    await expect(loadMcpGatewayProfile(request)).resolves.toMatchObject({
      email: "mahesh.sangawar@gmail.com",
      role: "Administrator",
    });
    expect(request).toHaveBeenCalledWith("/oauth2/userinfo", {
      headers: { Accept: "application/json" },
    });
  });
});

describe("tenant-scoped MCP endpoints", () => {
  const artifact = (
    overrides: Partial<{
      name: string;
      namespace: string;
      labels: Record<string, string>;
      status: Record<string, unknown>;
    }> = {},
  ) =>
    ({
      apiVersion: "registry.agentic.dev/v1alpha1",
      kind: "MCPServer",
      metadata: {
        name: overrides.name ?? "homechef-mcp",
        namespace: overrides.namespace,
        labels: overrides.labels,
      },
      spec: {},
      status: overrides.status,
    }) as never;

  it("reads the tenant from the label, then the namespace, then falls back", () => {
    expect(
      serverTenant(artifact({ labels: { "mcp.tesserix.app/tenant": "homechef" }, namespace: "devai" })),
    ).toBe("homechef");
    expect(serverTenant(artifact({ namespace: "mark8ly" }))).toBe("mark8ly");
    expect(serverTenant(artifact({}))).toBe("default");
  });

  it("publishes the tenant-scoped path a caller must actually dial", () => {
    expect(gatewayEndpoint("https://mcp.tesserix.app/", "homechef", "homechef-mcp")).toBe(
      "https://mcp.tesserix.app/mcp/homechef/homechef-mcp",
    );
  });

  it("puts the tenant-scoped endpoint into every client configuration", () => {
    for (const client of MCP_CLIENTS) {
      expect(installConfig(client.id, "homechef", "homechef-mcp", "https://mcp.tesserix.app")).toContain(
        "https://mcp.tesserix.app/mcp/homechef/homechef-mcp",
      );
    }
  });

  it("names the scope a caller's token has to carry", () => {
    expect(serverScope(artifact({ labels: { "mcp.tesserix.app/tenant": "homechef" } }))).toBe(
      "mcp:homechef:homechef-mcp",
    );
  });
});

describe("probe status", () => {
  const withStatus = (status?: Record<string, unknown>) =>
    ({
      apiVersion: "registry.agentic.dev/v1alpha1",
      kind: "MCPServer",
      metadata: { name: "homechef-mcp" },
      spec: {},
      status,
    }) as never;

  const conditions = (...items: Record<string, unknown>[]) => ({ conditions: items });

  it("reports a server that was never probed rather than claiming it is active", () => {
    expect(probeStatus(withStatus(undefined)).state).toBe("unprobed");
    expect(probeStatus(withStatus({ status: "active" })).state).toBe("unprobed");
  });

  it("reports ready only when a probe reached the server", () => {
    const status = probeStatus(
      withStatus({
        ...conditions(
          { type: "Ready", status: "True", reason: "Probed" },
          { type: "Drifted", status: "False" },
        ),
        observedTools: ["get_order_status"],
        lastProbedAt: "2026-08-20T10:00:00Z",
      }),
    );
    expect(status.state).toBe("ready");
    expect(status.tools).toEqual(["get_order_status"]);
    expect(status.lastProbedAt).toBe("2026-08-20T10:00:00Z");
  });

  it("surfaces drift ahead of readiness so an undeclared tool is visible", () => {
    const status = probeStatus(
      withStatus(
        conditions(
          { type: "Ready", status: "True" },
          { type: "Drifted", status: "True", message: "undeclared tools: delete_order" },
        ),
      ),
    );
    expect(status.state).toBe("drifted");
    expect(status.message).toBe("undeclared tools: delete_order");
  });

  it("surfaces an unreachable server above everything else", () => {
    const status = probeStatus(
      withStatus(
        conditions(
          { type: "Ready", status: "False", reason: "Unreachable" },
          { type: "Unreachable", status: "True", message: "http 502" },
          { type: "Drifted", status: "True" },
        ),
      ),
    );
    expect(status.state).toBe("unreachable");
    expect(status.message).toBe("http 502");
  });
});
