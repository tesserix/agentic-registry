import { useCallback, useEffect, useMemo, useState } from "react";
import {
  ReactFlow,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  Panel,
  Handle,
  Position,
  ConnectionMode,
  addEdge,
  useNodesState,
  useEdgesState,
  MarkerType,
  type Node,
  type Edge,
  type Connection,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Plus, Save, Trash2, Loader2, Check, Workflow as WorkflowIcon } from "lucide-react";
import { api, userLabels, pluralForKind, type Artifact } from "../lib/api";
import { toReactFlow, toSpec, nodeKind, defaultNodeType, typeOptions, FLOW_EDGE_COLOR } from "../lib/flow";

// Registry kinds a flow node can link to. "" = not linked (free-text step).
const LINKABLE_KINDS = ["", "Skill", "Tool", "MCPServer", "Prompt", "Agent", "Workflow"];

// Custom node: a labelled "step" coloured by its kind. Handles on all four
// sides + loose connection mode let you wire any direction (incl. top↔bottom).
function StepNode({ data, selected }: NodeProps) {
  const d = data as { label?: string; kind?: string; ref?: string; refKind?: string };
  const k = nodeKind(d.kind);
  return (
    <div
      className="flow-node"
      data-selected={selected ? "true" : "false"}
      style={{ borderColor: k.color, background: `color-mix(in srgb, var(--surface) 82%, ${k.color})` }}
    >
      <Handle id="t" type="target" position={Position.Top} className="flow-handle" />
      <Handle id="l" type="target" position={Position.Left} className="flow-handle" />
      <span className="flow-node-dot" style={{ background: k.color }} />
      <span className="flow-node-label">{d.label || "untitled"}</span>
      <span className="flow-node-kind" style={{ color: k.color }}>
        {d.ref ? `${d.refKind}: ${d.ref}` : d.kind ?? "task"}
      </span>
      <Handle id="r" type="source" position={Position.Right} className="flow-handle" />
      <Handle id="b" type="source" position={Position.Bottom} className="flow-handle" />
    </div>
  );
}

export default function FlowCanvas({
  artifact,
  plural,
  kind,
  onSaved,
  readOnly = false,
}: {
  artifact: Artifact;
  plural: string;
  kind: string;
  onSaved?: () => void;
  readOnly?: boolean;
}) {
  const init = useMemo(() => toReactFlow(artifact.spec), [artifact]);
  const [nodes, setNodes, onNodesChange] = useNodesState(init.nodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(init.edges);
  const [selected, setSelected] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  // Catalog options for the linked-resource picker, fetched per kind on demand
  // and cached. Keyed by registry Kind ("Skill" → ["code-review", …]).
  const [refOptions, setRefOptions] = useState<Record<string, string[]>>({});
  const ns = artifact.metadata.namespace;

  const dark = typeof document !== "undefined" && document.documentElement.classList.contains("dark");
  const nodeTypes = useMemo(() => ({ step: StepNode }), []);
  const touch = () => {
    setDirty(true);
    setSaved(false);
  };

  const onConnect = useCallback(
    (c: Connection) => {
      setEdges((es) =>
        addEdge(
          { ...c, animated: false, style: { stroke: FLOW_EDGE_COLOR, strokeWidth: 2 }, markerEnd: { type: MarkerType.ArrowClosed, color: FLOW_EDGE_COLOR } },
          es,
        ),
      );
      touch();
    },
    [setEdges],
  );

  function addStep() {
    const id = `step-${Math.round(performance.now()).toString(36)}-${nodes.length}`;
    const x = 60 + (nodes.length % 4) * 60;
    const y = 60 + nodes.length * 24;
    setNodes((ns) => [...ns, { id, type: "step", position: { x, y }, data: { label: "New step", kind: defaultNodeType(kind) } }]);
    setSelected(id);
    touch();
  }

  function patchSelected(patch: Record<string, unknown>) {
    setNodes((ns) => ns.map((n) => (n.id === selected ? { ...n, data: { ...n.data, ...patch } } : n)));
    touch();
  }
  function deleteSelected() {
    if (!selected) return;
    setNodes((ns) => ns.filter((n) => n.id !== selected));
    setEdges((es) => es.filter((e) => e.source !== selected && e.target !== selected));
    setSelected(null);
    touch();
  }

  async function save() {
    setSaving(true);
    setErr(null);
    const flow = toSpec(nodes as Node[], edges as Edge[]);
    const m = artifact.metadata;
    const doc = {
      apiVersion: artifact.apiVersion,
      kind: artifact.kind,
      // Blank tag => the registry auto-increments to the next version, so each
      // saved flow is a new immutable revision rather than mutating in place.
      metadata: { name: m.name, namespace: m.namespace, tag: "", visibility: m.visibility, labels: userLabels(m.labels) },
      spec: { ...(artifact.spec ?? {}), nodes: flow.nodes, edges: flow.edges },
    };
    try {
      await api.create(plural, doc);
      setDirty(false);
      setSaved(true);
      onSaved?.();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSaving(false);
    }
  }

  const selNode = nodes.find((n) => n.id === selected);
  const selData = selNode?.data as { label?: string; kind?: string; ref?: string; refKind?: string } | undefined;

  // Lazily fetch the catalog names for whichever Kind the selected node links
  // to, so the ref combobox can suggest real artifacts.
  const selRefKind = selData?.refKind ?? "";
  useEffect(() => {
    if (!selRefKind || refOptions[selRefKind]) return;
    let live = true;
    api
      .list(pluralForKind(selRefKind), { namespace: ns })
      .then((items) => {
        if (live) setRefOptions((m) => ({ ...m, [selRefKind]: items.map((a) => a.metadata.name) }));
      })
      .catch(() => {
        if (live) setRefOptions((m) => ({ ...m, [selRefKind]: [] }));
      });
    return () => {
      live = false;
    };
  }, [selRefKind, ns, refOptions]);

  return (
    <div className="card flow-theme overflow-hidden" style={{ height: 480, padding: 0 }}>
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={readOnly ? undefined : (c) => {
          onNodesChange(c);
          touch();
        }}
        onEdgesChange={readOnly ? undefined : (c) => {
          onEdgesChange(c);
          touch();
        }}
        onConnect={readOnly ? undefined : onConnect}
        onNodeClick={readOnly ? undefined : (_, n) => setSelected(n.id)}
        onPaneClick={() => setSelected(null)}
        colorMode={dark ? "dark" : "light"}
        connectionMode={ConnectionMode.Loose}
        connectionLineStyle={{ stroke: FLOW_EDGE_COLOR, strokeWidth: 2 }}
        connectionRadius={32}
        nodesDraggable={!readOnly}
        nodesConnectable={!readOnly}
        elementsSelectable={!readOnly}
        deleteKeyCode={readOnly ? null : ["Backspace", "Delete"]}
        fitView
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={18} size={1} />
        <Controls showInteractive={false} />
        <MiniMap pannable zoomable nodeColor={(n) => nodeKind((n.data as { kind?: string })?.kind).color} />

        {/* Toolbar */}
        <Panel position="top-left">
          <div className="flow-toolbar">
            <WorkflowIcon className="w-4 h-4" style={{ color: "var(--flow-accent)" }} />
            <span className="flow-toolbar-title">{kind} flow</span>
            {!readOnly && (
              <>
                <button className="flow-btn" onClick={addStep}>
                  <Plus className="w-3.5 h-3.5" /> Add step
                </button>
                <button className="flow-btn flow-btn-primary" onClick={save} disabled={saving || !dirty}>
                  {saving ? <Loader2 className="w-3.5 h-3.5 spin" /> : saved ? <Check className="w-3.5 h-3.5" /> : <Save className="w-3.5 h-3.5" />}
                  {saved && !dirty ? "Saved" : "Save"}
                </button>
              </>
            )}
          </div>
          {err && <div className="flow-error">{err}</div>}
        </Panel>

        {/* Inspector for the selected step (human intervention) */}
        {!readOnly && selNode && (
          <Panel position="top-right">
            <div className="flow-inspector">
              <div className="label-eyebrow mb-2">Step</div>
              <label className="flow-field-label">Label</label>
              <input className="field" value={selData?.label ?? ""} onChange={(e) => patchSelected({ label: e.target.value })} />
              <label className="flow-field-label mt-2">Type</label>
              <select className="field" value={selData?.kind ?? "task"} onChange={(e) => patchSelected({ kind: e.target.value })}>
                {typeOptions(kind).map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>

              {/* Link this step to a real catalog artifact (optional). */}
              <label className="flow-field-label mt-2">Linked resource</label>
              <select
                className="field"
                value={selData?.refKind ?? ""}
                onChange={(e) => patchSelected({ refKind: e.target.value, ...(e.target.value ? {} : { ref: undefined }) })}
              >
                {LINKABLE_KINDS.map((k) => (
                  <option key={k || "none"} value={k}>
                    {k || "— none —"}
                  </option>
                ))}
              </select>
              {selData?.refKind && (
                <>
                  <input
                    className="field mt-2"
                    list="flow-ref-options"
                    placeholder={`${selData.refKind} name`}
                    value={selData?.ref ?? ""}
                    onChange={(e) => patchSelected({ ref: e.target.value })}
                  />
                  <datalist id="flow-ref-options">
                    {(refOptions[selData.refKind] ?? []).map((n) => (
                      <option key={n} value={n} />
                    ))}
                  </datalist>
                  {selData?.ref && !(refOptions[selData.refKind] ?? []).includes(selData.ref) && (
                    <div className="flow-field-label" style={{ color: "#dc2626" }}>not in catalog</div>
                  )}
                </>
              )}

              <button className="flow-btn flow-btn-danger mt-3 w-full justify-center" onClick={deleteSelected}>
                <Trash2 className="w-3.5 h-3.5" /> Delete step
              </button>
            </div>
          </Panel>
        )}

        {nodes.length === 0 && (
          <Panel position="top-center">
            <div className="flow-empty">Empty flow — click <strong>Add step</strong>, then drag between handles to connect.</div>
          </Panel>
        )}

        {/* Legend */}
        <Panel position="bottom-center">
          <div className="flow-legend">
            {typeOptions(kind).map((t) => {
              const k = nodeKind(t);
              return (
                <span key={t} className="flow-legend-item">
                  <span className="flow-legend-dot" style={{ background: k.color }} />
                  {t}
                </span>
              );
            })}
            <span className="flow-legend-sep" />
            <span className="flow-legend-item"><span className="flow-legend-line" /> sequence</span>
            <span className="flow-legend-item"><span className="flow-legend-line dotted" /> conditional</span>
          </div>
        </Panel>
      </ReactFlow>
    </div>
  );
}
