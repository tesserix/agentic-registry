import { useState } from "react";
import {
  Bot,
  Check,
  Clipboard,
  Clock3,
  KeyRound,
  LockKeyhole,
  RefreshCw,
  ShieldCheck,
  UserCheck,
} from "lucide-react";
import { AGENTGATEWAY_PROJECT_ID, tokenRequestCommand } from "../lib/mcpGateway";

export default function MCPGatewayAccess() {
  const [copied, setCopied] = useState(false);
  const command = tokenRequestCommand();

  async function copyCommand() {
    await navigator.clipboard.writeText(command);
    setCopied(true);
  }

  return (
    <div className="mx-auto max-w-[1120px] px-4 py-8 sm:px-8 sm:py-10">
      <div className="label-eyebrow">Identity & access</div>
      <h1 className="mt-2 text-[28px] font-bold tracking-[-0.025em] text-[var(--ink-strong)]">
        Connect agents without static gateway keys
      </h1>
      <p className="mt-3 max-w-2xl text-[13px] leading-6 text-[var(--ink-soft)]">
        Each external agent receives an isolated Zitadel OAuth client and
        exchanges its credential for a short-lived bearer token. Tokens are
        scoped, rate-limited by subject, and revocable without changing the
        gateway.
      </p>

      <div className="mt-8 grid gap-4 md:grid-cols-3">
        <div className="mcp-access-feature">
          <span><Bot className="h-5 w-5" /></span>
          <h2>One client per agent</h2>
          <p>A compromised workload can be revoked without interrupting every other agent.</p>
        </div>
        <div className="mcp-access-feature">
          <span><Clock3 className="h-5 w-5" /></span>
          <h2>Short-lived tokens</h2>
          <p>Agents exchange credentials at runtime; access tokens are never committed or copied into UI storage.</p>
        </div>
        <div className="mcp-access-feature">
          <span><ShieldCheck className="h-5 w-5" /></span>
          <h2>Role-scoped access</h2>
          <p>Grant only MCP, model gateway, or both capabilities to each machine identity.</p>
        </div>
      </div>

      <div className="mt-7 grid gap-6 lg:grid-cols-[minmax(0,1fr)_330px]">
        <section className="panel overflow-hidden">
          <div className="border-b border-[var(--border-subtle)] px-6 py-5">
            <div className="flex items-center gap-2 text-[14px] font-semibold text-[var(--ink-strong)]">
              <KeyRound className="h-4 w-4 text-[var(--accent)]" />
              Request an MCP access token
            </div>
            <p className="mt-2 text-[12px] text-[var(--ink-muted)]">
              Retrieve the client ID and secret from your workload secret store,
              then run this exchange from the agent runtime.
            </p>
          </div>

          <div className="p-6">
            <ol className="space-y-4">
              <li className="flex gap-3">
                <span className="mcp-step">1</span>
                <div>
                  <h3 className="text-[13px] font-semibold text-[var(--ink-strong)]">
                    Export your workload credentials
                  </h3>
                  <p className="mt-1 text-[12px] text-[var(--ink-soft)]">
                    Use <code>TESSERIX_MCP_CLIENT_ID</code> and <code>TESSERIX_MCP_CLIENT_SECRET</code>. Never put their values in Git or browser storage.
                  </p>
                </div>
              </li>
              <li className="flex gap-3">
                <span className="mcp-step">2</span>
                <div className="min-w-0 flex-1">
                  <h3 className="text-[13px] font-semibold text-[var(--ink-strong)]">
                    Exchange for a bearer token
                  </h3>
                  <div className="relative mt-3 overflow-hidden rounded-xl border border-[var(--border)] bg-[var(--code-bg)]">
                    <pre className="overflow-x-auto p-5 pr-16 text-[11px] leading-5 text-[var(--code-ink)]">
                      <code>{command}</code>
                    </pre>
                    <button
                      className="absolute right-3 top-3 rounded-lg border border-white/10 bg-white/10 p-2 text-white/70 hover:bg-white/15 hover:text-white"
                      onClick={copyCommand}
                      aria-label="Copy token request"
                    >
                      {copied ? <Check className="h-4 w-4" /> : <Clipboard className="h-4 w-4" />}
                    </button>
                  </div>
                </div>
              </li>
              <li className="flex gap-3">
                <span className="mcp-step">3</span>
                <div>
                  <h3 className="text-[13px] font-semibold text-[var(--ink-strong)]">
                    Configure your agent client
                  </h3>
                  <p className="mt-1 text-[12px] text-[var(--ink-soft)]">
                    Open any server in the directory and copy its Codex, Claude Code, Cursor, VS Code, or LibreChat configuration.
                  </p>
                </div>
              </li>
            </ol>
          </div>
        </section>

        <aside className="space-y-5">
          <section className="panel p-5">
            <div className="flex items-center gap-2 text-[13px] font-semibold text-[var(--ink-strong)]">
              <LockKeyhole className="h-4 w-4 text-[var(--accent)]" />
              OAuth policy
            </div>
            <dl className="mt-4 space-y-4 text-[12px]">
              <div>
                <dt className="text-[var(--ink-muted)]">Issuer</dt>
                <dd className="mt-1 break-all font-mono text-[11px] text-[var(--ink-strong)]">https://auth.tesserix.app</dd>
              </div>
              <div>
                <dt className="text-[var(--ink-muted)]">Audience</dt>
                <dd className="mt-1 break-all font-mono text-[11px] text-[var(--ink-strong)]">{AGENTGATEWAY_PROJECT_ID}</dd>
              </div>
              <div>
                <dt className="text-[var(--ink-muted)]">MCP role</dt>
                <dd className="mt-1 font-mono text-[11px] text-[var(--ink-strong)]">agentgateway.mcp</dd>
              </div>
            </dl>
          </section>

          <section className="panel p-5">
            <div className="flex items-center gap-2 text-[13px] font-semibold text-[var(--ink-strong)]">
              <UserCheck className="h-4 w-4 text-[var(--ok)]" />
              Browser access
            </div>
            <p className="mt-3 text-[12px] leading-5 text-[var(--ink-soft)]">
              This management UI is restricted to the approved Tesserix operators:
            </p>
            <ul className="mt-3 space-y-2 font-mono text-[10.5px] text-[var(--ink-strong)]">
              <li>samyak.rout@gmail.com</li>
              <li>mahesh.sangawar@gmail.com</li>
            </ul>
          </section>

          <section className="rounded-xl border border-[var(--warn-soft-bd)] bg-[var(--warn-soft-bg)] p-4 text-[12px] leading-5 text-[var(--warn-ink)]">
            <div className="flex items-center gap-2 font-semibold">
              <RefreshCw className="h-4 w-4" /> No homemade PATs
            </div>
            <p className="mt-2">
              Client creation, role grants, rotation, and revocation remain in Zitadel and Secret Manager—the identity systems of record.
            </p>
          </section>
        </aside>
      </div>
    </div>
  );
}
