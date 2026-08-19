import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { AdminOnly, RegistrySessionProvider } from "./session";

describe("AdminOnly", () => {
  it("renders registry mutation controls only for server-authorized admins", () => {
    const admin = renderToStaticMarkup(
      <RegistrySessionProvider initialSession={{ authenticated: true, email: "samyak.rout@gmail.com", admin: true }}>
        <AdminOnly><button>Publish</button></AdminOnly>
      </RegistrySessionProvider>,
    );
    const reader = renderToStaticMarkup(
      <RegistrySessionProvider initialSession={{ authenticated: true, email: "reader@example.com", admin: false }}>
        <AdminOnly><button>Publish</button></AdminOnly>
      </RegistrySessionProvider>,
    );

    expect(admin).toContain("Publish");
    expect(reader).not.toContain("Publish");
  });
});
