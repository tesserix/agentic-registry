import { KeyRound, Wrench } from "lucide-react";
import type { Artifact } from "../lib/api";
import {
  gatewayEndpoint,
  MCP_GATEWAY_ORIGIN,
  probeStatus,
  serverScope,
  serverTenant,
} from "../lib/mcpGateway";
import ProbeBadge from "./ProbeBadge";

interface CredentialRef {
  secretName?: string;
  key?: string;
}

export default function McpAccessPanel({ server }: { server: Artifact }) {
  const tenant = serverTenant(server);
  const endpoint = gatewayEndpoint(MCP_GATEWAY_ORIGIN, tenant, server.metadata.name);
  const status = probeStatus(server);
  const credential = server.spec?.credentialRef as CredentialRef | undefined;

  return (
    <div className="card p-5">
      <div className="flex items-center gap-2 mb-3">
        <span className="label-eyebrow">Gateway access</span>
        <span className="ml-auto">
          <ProbeBadge server={server} showMessage />
        </span>
      </div>

      <div className="space-y-2.5 text-[12px]" style={{ color: "var(--ink-soft)" }}>
        <div>
          <div className="label-eyebrow mb-1">Endpoint</div>
          <code className="font-mono text-[11px]">{endpoint}</code>
        </div>
        <div>
          <div className="label-eyebrow mb-1">Required scope</div>
          <code className="font-mono text-[11px]">{serverScope(server)}</code>
        </div>
        {credential?.secretName && (
          <div>
            <div className="label-eyebrow mb-1">Upstream credential</div>
            <span className="chip font-mono">
              <KeyRound className="w-3 h-3" /> {credential.secretName}
              {credential.key ? `#${credential.key}` : ""}
            </span>
            <p className="mt-1.5">
              Injected from the vault and brokered by the gateway — callers never
              hold it.
            </p>
          </div>
        )}
        <div>
          <div className="label-eyebrow mb-1">Observed tools</div>
          {status.tools && status.tools.length > 0 ? (
            <div className="flex flex-wrap gap-1.5">
              {status.tools.map((tool) => (
                <span key={tool} className="chip font-mono">
                  <Wrench className="w-3 h-3" /> {tool}
                </span>
              ))}
            </div>
          ) : (
            <p>No probe has run against this server yet.</p>
          )}
        </div>
      </div>
    </div>
  );
}
