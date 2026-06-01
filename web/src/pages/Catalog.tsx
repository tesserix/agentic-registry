import { useEffect, useMemo, useRef, useState } from "react";
import { useParams } from "react-router-dom";
import { parse as parseYAML } from "yaml";
import { Search, SlidersHorizontal, Loader2, PackageOpen, Plus, Upload } from "lucide-react";
import { api, KINDS, type Artifact } from "../lib/api";
import ArtifactCard from "../components/ArtifactCard";
import ArtifactEditor from "../components/ArtifactEditor";

type Doc = Record<string, unknown>;

// Parse an uploaded manifest: JSON by extension/content, otherwise YAML.
function parseManifest(name: string, text: string): Doc {
  const isJson = name.toLowerCase().endsWith(".json");
  const v = isJson ? JSON.parse(text) : parseYAML(text);
  if (v == null || typeof v !== "object") throw new Error("manifest must be an object");
  return v as Doc;
}

export default function Catalog() {
  const { plural = "skills" } = useParams();
  const meta = KINDS.find((k) => k.plural === plural) ?? KINDS[0];

  const [items, setItems] = useState<Artifact[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [labelSelector, setLabelSelector] = useState("");
  const [editorOpen, setEditorOpen] = useState(false);
  const [seed, setSeed] = useState<Doc | null>(null);
  const [uploadErr, setUploadErr] = useState<string | null>(null);
  const [reload, setReload] = useState(0);
  const fileInput = useRef<HTMLInputElement | null>(null);

  // Direct upload from the header: parse the file, then open the editor
  // pre-filled so the user can review/adjust before publishing.
  async function onUpload(file: File | null | undefined) {
    if (!file) return;
    setUploadErr(null);
    try {
      const doc = parseManifest(file.name, await file.text());
      setSeed(doc);
      setEditorOpen(true);
    } catch (e) {
      setUploadErr(`could not load ${file.name}: ${e instanceof Error ? e.message : String(e)}`);
    } finally {
      if (fileInput.current) fileInput.current.value = "";
    }
  }

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError(null);
    api
      .list(plural, { search, labelSelector: labelSelector.trim() || undefined })
      .then((data) => {
        if (!cancelled) setItems(data);
      })
      .catch((e) => {
        if (!cancelled) setError(String(e.message ?? e));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [plural, search, labelSelector, reload]);

  const count = useMemo(() => items.length, [items]);

  return (
    <div className="px-8 py-9 max-w-7xl mx-auto">
      <div className="label-eyebrow">Marketplace</div>
      <div className="flex items-end justify-between gap-4 mt-2">
        <h1 className="font-serif text-[28px] font-semibold leading-tight" style={{ color: "var(--ink-strong)" }}>
          {meta.label}
        </h1>
        <div className="flex items-center gap-4 pb-1">
          <span className="font-mono text-[12px]" style={{ color: "var(--ink-muted)" }}>
            {count} {count === 1 ? "artifact" : "artifacts"}
          </span>
          <input
            ref={fileInput}
            type="file"
            accept=".yaml,.yml,.json,application/json,application/x-yaml,text/yaml,text/plain"
            className="hidden"
            onChange={(e) => onUpload(e.target.files?.[0])}
          />
          <button
            className="btn-secondary"
            onClick={() => fileInput.current?.click()}
            title={`Upload a ${meta.kind} manifest (YAML or JSON)`}
          >
            <Upload className="w-4 h-4" /> Upload
          </button>
          <button
            className="btn-primary"
            onClick={() => {
              setSeed(null);
              setEditorOpen(true);
            }}
          >
            <Plus className="w-4 h-4" /> New {meta.kind}
          </button>
        </div>
      </div>

      {uploadErr && (
        <div className="card mt-4 px-4 py-2.5 text-[12px]" style={{ color: "var(--error-ink)", background: "var(--error-soft-bg)", borderColor: "var(--error-soft-bd)" }}>
          {uploadErr}
        </div>
      )}

      <div className="flex gap-3 mt-6 flex-col sm:flex-row">
        <div className="relative flex-1">
          <Search className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2" style={{ color: "var(--ink-muted)" }} />
          <input
            className="field"
            style={{ paddingLeft: 34 }}
            placeholder={`Search ${meta.label.toLowerCase()}…`}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
        <div className="relative flex-1">
          <SlidersHorizontal className="w-4 h-4 absolute left-3 top-1/2 -translate-y-1/2" style={{ color: "var(--ink-muted)" }} />
          <input
            className="field font-mono text-[12px]"
            style={{ paddingLeft: 34 }}
            placeholder="labelSelector e.g. language=go,domain in (code-review)"
            value={labelSelector}
            onChange={(e) => setLabelSelector(e.target.value)}
          />
        </div>
      </div>

      <div className="mt-7">
        {loading ? (
          <div className="flex items-center gap-2 py-20 justify-center text-[13px]" style={{ color: "var(--ink-muted)" }}>
            <Loader2 className="w-4 h-4 spin" /> loading…
          </div>
        ) : error ? (
          <div className="card p-6 text-[13px]" style={{ color: "var(--error-ink)" }}>
            {error}
          </div>
        ) : count === 0 ? (
          <div className="card p-14 flex flex-col items-center text-center gap-3" style={{ color: "var(--ink-muted)" }}>
            <PackageOpen className="w-7 h-7" />
            <p className="text-[14px]" style={{ color: "var(--ink-soft)" }}>
              No {meta.label.toLowerCase()} match your query.
            </p>
            <p className="font-mono text-[12px]">
              Publish one: <code>agentic apply -f {meta.kind.toLowerCase()}.yaml</code>
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-5">
            {items.map((a) => (
              <ArtifactCard key={`${a.metadata.namespace}/${a.metadata.name}`} plural={plural} a={a} />
            ))}
          </div>
        )}
      </div>

      <ArtifactEditor
        kind={meta.kind}
        plural={plural}
        open={editorOpen}
        seed={seed}
        onClose={() => {
          setEditorOpen(false);
          setSeed(null);
        }}
        onCreated={() => {
          setEditorOpen(false);
          setSeed(null);
          setReload((n) => n + 1);
        }}
      />
    </div>
  );
}
