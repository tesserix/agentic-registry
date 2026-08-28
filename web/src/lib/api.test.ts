import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";

describe("MCP gateway API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("loads resolved server capabilities without changing registry identity", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          mcpServer: { metadata: { name: "catalog-atlassian-mcp" } },
          tools: [],
          toolCount: 0,
          conditions: [],
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const result = await api.resolvedMcp(
      "catalog-atlassian-mcp",
      "platform",
    );

    expect(result.mcpServer.metadata.name).toBe("catalog-atlassian-mcp");
    expect(fetchMock).toHaveBeenCalledWith(
      "/v0/mcpservers/catalog-atlassian-mcp/resolved?namespace=platform",
      { headers: { Accept: "application/json" } },
    );
  });
});

describe("registry session API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("loads the server-authorized administration capability", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          authenticated: true,
          email: "samyak.rout@gmail.com",
          admin: true,
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const session = await api.session();

    expect(session.admin).toBe(true);
    expect(fetchMock).toHaveBeenCalledWith("/v0/session", {
      headers: { Accept: "application/json" },
    });
  });
});

describe("API credential management", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("creates a short-lived credential with an idempotency key", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          id: "cred-1",
          name: "ci",
          client_id: "client-1",
          client_secret: "shown-once",
          status: "active",
          scopes: ["registry:read", "registry:publish"],
        }),
        { status: 201, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("crypto", { randomUUID: () => "idem-42" });

    const result = await api.createCredential({
      name: "ci",
      scopes: ["registry:read", "registry:publish"],
      lifetime_days: 30,
      namespaces: ["agents-team"],
      kinds: ["Agent", "Tool"],
    });

    expect(result.client_secret).toBe("shown-once");
    expect(fetchMock).toHaveBeenCalledWith("/v0/settings/api-credentials", {
      method: "POST",
      headers: {
        Accept: "application/json",
        "Content-Type": "application/json",
        "Idempotency-Key": "idem-42",
      },
      body: JSON.stringify({
        name: "ci",
        scopes: ["registry:read", "registry:publish"],
        lifetime_days: 30,
        namespaces: ["agents-team"],
        kinds: ["Agent", "Tool"],
      }),
    });
  });
});
