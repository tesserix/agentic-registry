import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import type { Artifact } from "../lib/api";
import ProbeBadge from "./ProbeBadge";

function server(status?: Record<string, unknown>): Artifact {
  return {
    apiVersion: "registry.tesserix.app/v1alpha1",
    kind: "MCPServer",
    metadata: { name: "devai-mcp", namespace: "devai" },
    spec: {},
    status,
  };
}

const ready = { conditions: [{ type: "Ready", status: "True" }] };
const drifted = {
  conditions: [
    { type: "Ready", status: "True" },
    { type: "Drifted", status: "True", message: "observed adds: search" },
  ],
};
const unreachable = {
  conditions: [
    { type: "Ready", status: "False" },
    { type: "Unreachable", status: "True", message: "http 502" },
  ],
};

describe("ProbeBadge", () => {
  it("does not claim a catalog row is active before a probe has run", () => {
    expect(renderToStaticMarkup(<ProbeBadge server={server()} />)).toContain(
      "Unprobed",
    );
  });

  it("reports active only when the probe reached the server", () => {
    expect(renderToStaticMarkup(<ProbeBadge server={server(ready)} />)).toContain(
      "Active",
    );
  });

  it("surfaces drift between the declared and observed tool surface", () => {
    const markup = renderToStaticMarkup(<ProbeBadge server={server(drifted)} />);
    expect(markup).toContain("Drifted");
    expect(markup).not.toContain("Active");
  });

  it("surfaces an unreachable server with its probe error", () => {
    const markup = renderToStaticMarkup(
      <ProbeBadge server={server(unreachable)} showMessage />,
    );
    expect(markup).toContain("Unreachable");
    expect(markup).toContain("http 502");
  });
});
