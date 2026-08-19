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
