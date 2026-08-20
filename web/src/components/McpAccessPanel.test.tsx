import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Artifact } from "../lib/api";
import McpAccessPanel from "./McpAccessPanel";

const server: Artifact = {
  apiVersion: "registry.tesserix.app/v1alpha1",
  kind: "MCPServer",
  metadata: {
    name: "homechef-mcp",
    namespace: "devai",
    labels: { "mcp.tesserix.app/tenant": "homechef" },
  },
  spec: {},
  status: {
    conditions: [{ type: "Ready", status: "True" }],
    observedTools: ["get_order_status", "track_delivery"],
  },
};

describe("McpAccessPanel", () => {
  it("advertises the tenant-scoped route the gateway actually serves", () => {
    expect(renderToStaticMarkup(<McpAccessPanel server={server} />)).toContain(
      "https://mcp.tesserix.app/mcp/homechef/homechef-mcp",
    );
  });

  it("names the scope a caller's token must carry", () => {
    expect(renderToStaticMarkup(<McpAccessPanel server={server} />)).toContain(
      "mcp:homechef:homechef-mcp",
    );
  });

  it("lists what the probe observed, not what the manifest claims", () => {
    const markup = renderToStaticMarkup(<McpAccessPanel server={server} />);
    expect(markup).toContain("get_order_status");
    expect(markup).toContain("track_delivery");
  });

  it("says so plainly when nothing has been observed yet", () => {
    const unprobed = { ...server, status: {} };
    expect(renderToStaticMarkup(<McpAccessPanel server={unprobed} />)).toContain(
      "No probe has run",
    );
  });

  it("never renders a credential, only the reference to one", () => {
    const brokered = {
      ...server,
      spec: { credentialRef: { secretName: "homechef-mcp-upstream", key: "token" } },
    };
    const markup = renderToStaticMarkup(<McpAccessPanel server={brokered} />);
    expect(markup).toContain("homechef-mcp-upstream");
    expect(markup).toContain("brokered by the gateway");
  });
});
