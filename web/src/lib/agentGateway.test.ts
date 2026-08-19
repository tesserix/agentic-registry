import { afterEach, describe, expect, it, vi } from "vitest";
import {
  agentgatewayApi,
  isAgentGatewayHost,
  resourceTypeForKind,
  type AgentgatewayResource,
} from "./agentGateway";

const backend: AgentgatewayResource = {
  apiVersion: "agentgateway.dev/v1alpha1",
  kind: "AgentgatewayBackend",
  metadata: { name: "devai-openai", namespace: "agentgateway-system" },
  spec: { ai: { groups: [] } },
};

describe("AgentGateway host", () => {
  it("selects the dedicated Tesserix admin app only on its public hostname", () => {
    expect(isAgentGatewayHost("agentgateway.tesserix.app")).toBe(true);
    expect(isAgentGatewayHost("AGENTGATEWAY.TESSERIX.APP")).toBe(true);
    expect(isAgentGatewayHost("mcp.tesserix.app")).toBe(false);
  });
});

describe("AgentGateway desired-state API", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("lists the server-authorized resource inventory", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ items: [backend] }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await expect(agentgatewayApi.list()).resolves.toEqual([backend]);
    expect(fetchMock).toHaveBeenCalledWith("/v0/agentgateway/resources", {
      headers: { Accept: "application/json" },
    });
  });

  it("upserts by the kind-derived collection and stable resource name", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(backend), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    await agentgatewayApi.upsert(backend);
    expect(fetchMock).toHaveBeenCalledWith(
      "/v0/agentgateway/backends/devai-openai",
      expect.objectContaining({ method: "PUT", body: JSON.stringify(backend) }),
    );
  });

  it("deletes by kind without accepting arbitrary Kubernetes collections", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);

    await agentgatewayApi.remove("AgentgatewayBackend", "devai-openai");
    expect(fetchMock).toHaveBeenCalledWith(
      "/v0/agentgateway/backends/devai-openai",
      expect.objectContaining({ method: "DELETE" }),
    );
    expect(() => resourceTypeForKind("Secret")).toThrow("Unsupported AgentGateway resource kind");
  });
});
