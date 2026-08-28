import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, describe, expect, it, vi } from "vitest";
import { RegistrySessionProvider } from "../lib/session";
import AgentGatewayShell from "./AgentGatewayShell";

describe("AgentGatewayShell", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("offers administrators a local OAuth sign-out action", () => {
    vi.stubGlobal("document", {
      documentElement: { classList: { contains: () => true } },
    });

    const markup = renderToStaticMarkup(
      <RegistrySessionProvider
        initialSession={{
          authenticated: true,
          email: "samyak.rout@gmail.com",
          tenant_id: "tesserix",
          onboarding_required: false,
          admin: true,
        }}
      >
        <AgentGatewayShell><div>Desired state</div></AgentGatewayShell>
      </RegistrySessionProvider>,
    );

    expect(markup).toContain('href="/oauth2/sign_out?rd=%2F"');
    expect(markup).toContain("Sign out");
  });
});
