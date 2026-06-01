import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { Search, Loader2, CornerDownLeft, ArrowUp, ArrowDown } from "lucide-react";
import { api, KINDS, pluralForKind, type Artifact } from "../lib/api";

// CommandPalette is the global ⌘K / Ctrl+K search. Empty query shows the
// catalog sections as quick jumps; typing runs the cross-kind ranked search
// (pgvector cosine on the server). Fully keyboard-driven: ↑/↓ to move, ↵ to
// open, Esc to close.
export default function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const navigate = useNavigate();
  const inputRef = useRef<HTMLInputElement>(null);
  const [q, setQ] = useState("");
  const [results, setResults] = useState<Artifact[]>([]);
  const [loading, setLoading] = useState(false);
  const [active, setActive] = useState(0);

  // Reset and focus whenever the palette opens.
  useEffect(() => {
    if (open) {
      setQ("");
      setResults([]);
      setActive(0);
      // Focus after the open animation begins.
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [open]);

  // Debounced search. Empty query clears results (the catalog jumps show instead).
  useEffect(() => {
    if (!open) return;
    const query = q.trim();
    if (!query) {
      setResults([]);
      setLoading(false);
      return;
    }
    setLoading(true);
    const t = setTimeout(() => {
      let cancelled = false;
      api
        .search(query, 24)
        .then((r) => !cancelled && setResults(r))
        .catch(() => !cancelled && setResults([]))
        .finally(() => !cancelled && setLoading(false));
      return () => {
        cancelled = true;
      };
    }, 140);
    return () => clearTimeout(t);
  }, [q, open]);

  // Flattened, ordered list of selectable rows (catalog jumps OR search hits).
  const rows = useMemo(() => {
    if (q.trim()) {
      return results.map((a) => ({
        kind: "artifact" as const,
        plural: pluralForKind(a.kind),
        name: a.metadata.name,
        title: (a.spec?.title as string) || a.metadata.name,
        sub: a.kind,
      }));
    }
    return KINDS.map((k) => ({
      kind: "section" as const,
      plural: k.plural,
      name: k.label,
      title: `Browse ${k.label}`,
      sub: "Catalog",
    }));
  }, [q, results]);

  useEffect(() => setActive(0), [rows.length]);

  function go(i: number) {
    const row = rows[i];
    if (!row) return;
    onClose();
    if (row.kind === "section") navigate(`/${row.plural}`);
    else navigate(`/${row.plural}/${encodeURIComponent(row.name)}`);
  }

  function onKeyDown(e: React.KeyboardEvent) {
    if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    } else if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((a) => Math.min(a + 1, rows.length - 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((a) => Math.max(a - 1, 0));
    } else if (e.key === "Enter") {
      e.preventDefault();
      go(active);
    }
  }

  if (!open) return null;

  return (
    <div className="palette-overlay" onMouseDown={onClose}>
      <div className="palette" onMouseDown={(e) => e.stopPropagation()} onKeyDown={onKeyDown}>
        {/* Search input */}
        <div className="flex items-center gap-3 px-4 h-14 border-b" style={{ borderColor: "var(--border-subtle)" }}>
          {loading ? (
            <Loader2 className="w-4 h-4 spin shrink-0" style={{ color: "var(--ink-muted)" }} />
          ) : (
            <Search className="w-4 h-4 shrink-0" style={{ color: "var(--ink-muted)" }} />
          )}
          <input
            ref={inputRef}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder="Search skills, tools, MCP servers, prompts…"
            className="flex-1 bg-transparent outline-none text-[15px]"
            style={{ color: "var(--ink-strong)" }}
          />
          <kbd className="kbd">esc</kbd>
        </div>

        {/* Results */}
        <div className="max-h-[52vh] overflow-y-auto p-2">
          {!q.trim() && <div className="label-eyebrow px-3 pt-2 pb-1">Jump to</div>}
          {q.trim() && !loading && rows.length === 0 && (
            <div className="px-3 py-10 text-center text-[13px]" style={{ color: "var(--ink-muted)" }}>
              No matches for “{q.trim()}”.
            </div>
          )}
          {rows.map((row, i) => (
            <div
              key={`${row.plural}/${row.name}/${i}`}
              data-active={i === active}
              className="palette-row"
              onMouseEnter={() => setActive(i)}
              onClick={() => go(i)}
            >
              <span className="font-serif text-[14px] truncate" style={{ color: "var(--ink-strong)" }}>
                {row.title}
              </span>
              <span className="chip ml-auto shrink-0">{row.sub}</span>
              {row.kind === "artifact" && (
                <span className="font-mono text-[11px] truncate hidden sm:block" style={{ color: "var(--ink-muted)", maxWidth: 180 }}>
                  {row.name}
                </span>
              )}
            </div>
          ))}
        </div>

        {/* Footer hints */}
        <div
          className="flex items-center gap-4 px-4 h-10 border-t text-[11px]"
          style={{ borderColor: "var(--border-subtle)", color: "var(--ink-muted)" }}
        >
          <span className="flex items-center gap-1.5">
            <ArrowUp className="w-3 h-3" />
            <ArrowDown className="w-3 h-3" /> navigate
          </span>
          <span className="flex items-center gap-1.5">
            <CornerDownLeft className="w-3 h-3" /> open
          </span>
          <span className="ml-auto font-mono">vector search</span>
        </div>
      </div>
    </div>
  );
}
