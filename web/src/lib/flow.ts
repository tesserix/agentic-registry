// Flow model for Workflow and Blueprint artifacts. The graph lives in the
// artifact's spec as `nodes` + `edges`, so the visual editor and the stored
// manifest are the same source of truth.
import { type Node, type Edge, MarkerType } from "@xyflow/react";

export interface FlowNode {
  id: string;
  label: string;
  type?: string; // see NODE_KINDS
  position?: { x: number; y: number };
}
export interface FlowEdge {
  id?: string;
  from: string;
  to: string;
  label?: string;
  // "dotted" = animated (conditional/branch), "solid" = direct sequence.
  // When omitted it's auto-derived: labelled edges are dotted, others solid.
  style?: "dotted" | "solid";
}

// Node palette — a distinct, flow-only colour theme. Semi-transparent fills +
// solid borders read cleanly in both light and dark.
export const NODE_KINDS: Record<string, { label: string; color: string; bg: string }> = {
  start: { label: "Start", color: "#16a34a", bg: "rgba(22,163,74,0.13)" },
  task: { label: "Task", color: "#6366f1", bg: "rgba(99,102,241,0.13)" },
  approval: { label: "Approval", color: "#d97706", bg: "rgba(217,119,6,0.14)" },
  gateway: { label: "Gateway", color: "#9333ea", bg: "rgba(147,51,234,0.14)" },
  end: { label: "End", color: "#dc2626", bg: "rgba(220,38,38,0.13)" },
  // blueprint kinds
  component: { label: "Component", color: "#0891b2", bg: "rgba(8,145,178,0.14)" },
  model: { label: "Model", color: "#0d9488", bg: "rgba(13,148,136,0.14)" },
  store: { label: "Store", color: "#7c3aed", bg: "rgba(124,58,237,0.13)" },
};

export function nodeKind(t?: string) {
  return NODE_KINDS[t ?? "task"] ?? NODE_KINDS.task;
}

// A single indigo edge colour that reads on both the light and dark flow board
// (used for the line, the arrowhead, and the live connection drag line).
export const FLOW_EDGE_COLOR = "#7c83f0";

// isDotted decides an edge's render style: explicit `style`, else auto — a
// labelled edge is a conditional/branch (dotted+animated), an unlabelled edge
// is a direct sequence (solid).
function isDotted(e: FlowEdge): boolean {
  if (e.style) return e.style === "dotted";
  return !!e.label;
}

// Default node type offered first when adding, per artifact kind.
export function defaultNodeType(kind: string): string {
  return kind === "Blueprint" ? "component" : "task";
}
export function typeOptions(kind: string): string[] {
  return kind === "Blueprint" ? ["component", "model", "store", "gateway"] : ["start", "task", "approval", "gateway", "end"];
}

// Simple left-to-right layered layout for nodes that have no saved position.
function autoLayout(nodes: FlowNode[], edges: FlowEdge[]): Record<string, { x: number; y: number }> {
  const indeg: Record<string, number> = {};
  const adj: Record<string, string[]> = {};
  nodes.forEach((n) => {
    indeg[n.id] = 0;
    adj[n.id] = [];
  });
  edges.forEach((e) => {
    if (adj[e.from] && indeg[e.to] !== undefined) {
      adj[e.from].push(e.to);
      indeg[e.to]++;
    }
  });
  // Assign a layer (column) via BFS from roots.
  const layer: Record<string, number> = {};
  let frontier = nodes.filter((n) => indeg[n.id] === 0).map((n) => n.id);
  if (frontier.length === 0 && nodes.length) frontier = [nodes[0].id];
  const seen = new Set<string>();
  let depth = 0;
  while (frontier.length) {
    const next: string[] = [];
    for (const id of frontier) {
      if (seen.has(id)) continue;
      seen.add(id);
      layer[id] = Math.max(layer[id] ?? 0, depth);
      for (const t of adj[id] ?? []) if (!seen.has(t)) next.push(t);
    }
    frontier = next;
    depth++;
  }
  // Any disconnected nodes get the next column.
  nodes.forEach((n) => {
    if (layer[n.id] === undefined) layer[n.id] = depth;
  });
  const perCol: Record<number, number> = {};
  const pos: Record<string, { x: number; y: number }> = {};
  nodes.forEach((n) => {
    const col = layer[n.id];
    const row = perCol[col] ?? 0;
    perCol[col] = row + 1;
    pos[n.id] = { x: col * 240 + 24, y: row * 120 + 24 };
  });
  return pos;
}

export function toReactFlow(spec: Record<string, unknown> | undefined): { nodes: Node[]; edges: Edge[] } {
  const rawNodes = (spec?.nodes as FlowNode[]) ?? [];
  const rawEdges = (spec?.edges as FlowEdge[]) ?? [];
  const layout = autoLayout(rawNodes, rawEdges);
  const nodes: Node[] = rawNodes.map((n) => ({
    id: n.id,
    type: "step",
    position: n.position ?? layout[n.id] ?? { x: 24, y: 24 },
    data: { label: n.label || n.id, kind: n.type ?? "task" },
  }));
  const edges: Edge[] = rawEdges.map((e, i) => {
    const dotted = isDotted(e);
    return {
      id: e.id ?? `${e.from}->${e.to}-${i}`,
      source: e.from,
      target: e.to,
      label: e.label,
      animated: dotted,
      style: { stroke: FLOW_EDGE_COLOR, strokeWidth: 2, ...(dotted ? { strokeDasharray: "7 5" } : {}) },
      markerEnd: { type: MarkerType.ArrowClosed, color: FLOW_EDGE_COLOR, width: 18, height: 18 },
    };
  });
  return { nodes, edges };
}

export function toSpec(nodes: Node[], edges: Edge[]): { nodes: FlowNode[]; edges: FlowEdge[] } {
  return {
    nodes: nodes.map((n) => ({
      id: n.id,
      label: String((n.data as { label?: string })?.label ?? n.id),
      type: String((n.data as { kind?: string })?.kind ?? "task"),
      position: { x: Math.round(n.position.x), y: Math.round(n.position.y) },
    })),
    edges: edges.map((e) => ({
      id: e.id,
      from: e.source,
      to: e.target,
      ...(e.label ? { label: String(e.label) } : {}),
      style: e.animated ? ("dotted" as const) : ("solid" as const),
    })),
  };
}
