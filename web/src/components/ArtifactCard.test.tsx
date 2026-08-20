import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";
import type { Artifact } from "../lib/api";
import ArtifactCard from "./ArtifactCard";

function artifact(kind: string, status?: Record<string, unknown>): Artifact {
  return {
    apiVersion: "registry.tesserix.app/v1alpha1",
    kind,
    metadata: { name: "devai-mcp", namespace: "devai", visibility: "internal" },
    spec: { description: "Platform tools" },
    status,
  };
}

function markup(a: Artifact) {
  return renderToStaticMarkup(
    <MemoryRouter>
      <ArtifactCard plural="mcpservers" a={a} />
    </MemoryRouter>,
  );
}

describe("ArtifactCard", () => {
  it("shows an MCP server's probe state, the same one the gateway surface shows", () => {
    expect(
      markup(
        artifact("MCPServer", {
          conditions: [{ type: "Unreachable", status: "True", message: "http 502" }],
        }),
      ),
    ).toContain("Unreachable");
  });

  it("does not invent a probe state for artifacts that are never probed", () => {
    expect(markup(artifact("Skill"))).not.toContain("Unprobed");
  });
});
