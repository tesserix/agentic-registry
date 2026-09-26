import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { RegistrySessionProvider } from "../lib/session";
import APICredentials from "./APICredentials";

describe("API credentials settings", () => {
  it("does not expose credential controls to an anonymous visitor", () => {
    const html = renderToStaticMarkup(
      <RegistrySessionProvider
        initialSession={{ authenticated: false, email: "", tenant_id: "", onboarding_required: false, admin: false }}
      >
        <APICredentials />
      </RegistrySessionProvider>,
    );

    expect(html).toContain("Sign in to manage credentials");
    expect(html).not.toContain("Create credential");
  });

  it("shows the complete client credentials setup for authenticated publishers", () => {
    const html = renderToStaticMarkup(
      <RegistrySessionProvider
        initialSession={{ authenticated: true, email: "dev@example.com", tenant_id: "tenant-1", onboarding_required: false, admin: false }}
      >
        <APICredentials />
      </RegistrySessionProvider>,
    );

    expect(html).toContain("AGENTIC_CLIENT_ID");
    expect(html).toContain("AGENTIC_CLIENT_SECRET");
    expect(html).toContain("AGENTIC_TOKEN_URL");
    expect(html).toContain("AGENTIC_AUDIENCE");
    expect(html).toContain("GitHub Actions secrets");
  });
});
