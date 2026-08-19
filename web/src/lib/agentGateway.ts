export type AgentgatewayResourceKind =
  | "AgentgatewayBackend"
  | "AgentgatewayPolicy"
  | "HTTPRoute";

export interface AgentgatewayResource {
  apiVersion: string;
  kind: AgentgatewayResourceKind;
  metadata: {
    name: string;
    namespace?: string;
  };
  spec: Record<string, unknown>;
}

const resourceTypes: Record<AgentgatewayResourceKind, string> = {
  AgentgatewayBackend: "backends",
  AgentgatewayPolicy: "policies",
  HTTPRoute: "routes",
};

export function isAgentGatewayHost(hostname: string): boolean {
  return hostname.toLowerCase() === "agentgateway.tesserix.app";
}

export function resourceTypeForKind(kind: string): string {
  const resourceType = resourceTypes[kind as AgentgatewayResourceKind];
  if (!resourceType) throw new Error(`Unsupported AgentGateway resource kind: ${kind}`);
  return resourceType;
}

async function errorMessage(response: Response): Promise<string> {
  try {
    const body = (await response.json()) as { error?: { message?: string } };
    if (body.error?.message) return body.error.message;
  } catch {
    // The status remains useful when an edge proxy returns a non-JSON error.
  }
  return `${response.status} ${response.statusText}`;
}

export const agentgatewayApi = {
  async list(): Promise<AgentgatewayResource[]> {
    const response = await fetch("/v0/agentgateway/resources", {
      headers: { Accept: "application/json" },
    });
    if (!response.ok) throw new Error(await errorMessage(response));
    const body = (await response.json()) as { items: AgentgatewayResource[] };
    return body.items;
  },

  async upsert(resource: AgentgatewayResource): Promise<AgentgatewayResource> {
    const resourceType = resourceTypeForKind(resource.kind);
    const response = await fetch(
      `/v0/agentgateway/${resourceType}/${encodeURIComponent(resource.metadata.name)}`,
      {
        method: "PUT",
        headers: { Accept: "application/json", "Content-Type": "application/json" },
        body: JSON.stringify(resource),
      },
    );
    if (!response.ok) throw new Error(await errorMessage(response));
    return response.json() as Promise<AgentgatewayResource>;
  },

  async remove(kind: AgentgatewayResourceKind, name: string): Promise<void> {
    const resourceType = resourceTypeForKind(kind);
    const response = await fetch(
      `/v0/agentgateway/${resourceType}/${encodeURIComponent(name)}`,
      { method: "DELETE", headers: { Accept: "application/json" } },
    );
    if (!response.ok) throw new Error(await errorMessage(response));
  },
};
