import { useEffect, useMemo, useState } from "react";
import { useParams } from "react-router-dom";
import { Search, SlidersHorizontal, Loader2, PackageOpen } from "lucide-react";
import { api, KINDS, type Artifact } from "../lib/api";
import ArtifactCard from "../components/ArtifactCard";

export default function Catalog() {
  const { plural = "skills" } = useParams();
  const meta = KINDS.find((k) => k.plural === plural) ?? KINDS[0];

  const [items, setItems] = useState<Artifact[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [labelSelector, setLabelSelector] = useState("");

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
  }, [plural, search, labelSelector]);

  const count = useMemo(() => items.length, [items]);

  return (
    <div className="p-7 max-w-6xl mx-auto">
      <div className="label-eyebrow">Marketplace</div>
      <div className="flex items-end justify-between gap-4 mt-1">
        <h1 className="font-serif text-2xl font-medium" style={{ color: "var(--ink-strong)" }}>
          {meta.label}
        </h1>
        <span className="font-mono text-[12px]" style={{ color: "var(--ink-muted)" }}>
          {count} {count === 1 ? "artifact" : "artifacts"}
        </span>
      </div>

      <div className="flex gap-3 mt-5 flex-col sm:flex-row">
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

      <div className="mt-6">
        {loading ? (
          <div className="flex items-center gap-2 py-16 justify-center" style={{ color: "var(--ink-muted)" }}>
            <Loader2 className="w-4 h-4 animate-spin" /> loading…
          </div>
        ) : error ? (
          <div className="panel p-6 text-[13px]" style={{ color: "var(--error-ink)" }}>
            {error}
          </div>
        ) : count === 0 ? (
          <div className="panel p-10 flex flex-col items-center text-center gap-2" style={{ color: "var(--ink-muted)" }}>
            <PackageOpen className="w-6 h-6" />
            <p className="text-[13px]">No {meta.label.toLowerCase()} match your query.</p>
            <p className="font-mono text-[12px]">
              Publish one: <code>agentic apply -f {meta.kind.toLowerCase()}.yaml</code>
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
            {items.map((a) => (
              <ArtifactCard key={`${a.metadata.namespace}/${a.metadata.name}`} plural={plural} a={a} />
            ))}
          </div>
        )}
      </div>
    </div>
  );
}
