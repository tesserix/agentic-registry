import { Link } from "react-router-dom";
import { ShieldCheck } from "lucide-react";
import { type Artifact, visibilityClass, userLabels } from "../lib/api";

export default function ArtifactCard({ plural, a }: { plural: string; a: Artifact }) {
  const title = (a.spec?.title as string) || a.metadata.name;
  const desc = (a.spec?.description as string) || "";
  const labels = userLabels(a.metadata.labels);
  const verified = a.metadata.name.includes("/") || Boolean(a.metadata.annotations?.["verifiedPublisher"]);

  return (
    <Link
      to={`/${plural}/${encodeURIComponent(a.metadata.name)}`}
      className="panel panel-hover p-4 flex flex-col gap-2 transition"
      style={{ textDecoration: "none" }}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="font-serif text-[15px] font-medium truncate" style={{ color: "var(--ink-strong)" }}>
            {title}
          </div>
          <div className="font-mono text-[11px] truncate" style={{ color: "var(--ink-muted)" }}>
            {a.metadata.name}
          </div>
        </div>
        <span className={`chip ${visibilityClass(a.metadata.visibility)}`}>{a.metadata.visibility ?? "private"}</span>
      </div>

      {desc && (
        <p className="text-[13px] line-clamp-2" style={{ color: "var(--ink-soft)" }}>
          {desc}
        </p>
      )}

      <div className="flex items-center gap-1.5 flex-wrap mt-auto pt-1">
        <span className="chip font-mono">{a.metadata.tag ?? "latest"}</span>
        {verified && (
          <span className="chip badge-verified">
            <ShieldCheck className="w-3 h-3" /> verified
          </span>
        )}
        {Object.entries(labels)
          .slice(0, 3)
          .map(([k, v]) => (
            <span key={k} className="chip">
              {k}={v}
            </span>
          ))}
      </div>
    </Link>
  );
}
