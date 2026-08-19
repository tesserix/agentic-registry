import { describe, expect, it } from "vitest";
import {
  gatewayEndpoint,
  installConfig,
  isMcpGatewayHost,
  serverDisplayName,
  tokenRequestCommand,
} from "./mcpGateway";

describe("MCP gateway presentation", () => {
  it("enables the product UI only on the MCP gateway host", () => {
    expect(isMcpGatewayHost("mcp.tesserix.app")).toBe(true);
    expect(isMcpGatewayHost("aregistry.tesserix.app")).toBe(false);
    expect(isMcpGatewayHost("localhost")).toBe(false);
  });

  it("keeps the registered route name in the public endpoint", () => {
    expect(
      gatewayEndpoint("https://mcp.tesserix.app/", "catalog-atlassian-mcp"),
    ).toBe("https://mcp.tesserix.app/mcp/catalog-atlassian-mcp");
  });

  it("formats catalog names for people without changing identity", () => {
    expect(serverDisplayName("catalog-atlassian-mcp")).toBe("Atlassian");
    expect(serverDisplayName("gitops-mcp")).toBe("Gitops");
  });

  it("generates client configuration from the exact gateway endpoint", () => {
    expect(
      installConfig(
        "codex",
        "catalog-atlassian-mcp",
        "https://mcp.tesserix.app",
      ),
    ).toContain(
      'url = "https://mcp.tesserix.app/mcp/catalog-atlassian-mcp"',
    );
    expect(
      installConfig(
        "cursor",
        "catalog-atlassian-mcp",
        "https://mcp.tesserix.app",
      ),
    ).toContain('"type": "http"');
  });

  it("generates bearer-token setup for every supported agent client", () => {
    const configs = {
      "claude-code": "claude mcp add --transport http",
      vscode: '"servers"',
      librechat: "mcpServers:",
    } as const;

    for (const [client, marker] of Object.entries(configs)) {
      const config = installConfig(
        client as keyof typeof configs,
        "catalog-atlassian-mcp",
        "https://mcp.tesserix.app",
      );
      expect(config).toContain(marker);
      expect(config).toContain(
        "https://mcp.tesserix.app/mcp/catalog-atlassian-mcp",
      );
      expect(config).toContain("TESSERIX_MCP_TOKEN");
    }
  });

  it("documents short-lived OAuth tokens without embedding credentials", () => {
    const command = tokenRequestCommand();

    expect(command).toContain("https://auth.tesserix.app/oauth/v2/token");
    expect(command).toContain("386889024519799084:aud");
    expect(command).toContain("TESSERIX_MCP_CLIENT_SECRET");
    expect(command).not.toContain("client_secret=");
  });
});
