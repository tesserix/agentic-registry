import { Link } from "react-router-dom";
import { ShieldCheck, Fingerprint } from "lucide-react";
import { type Artifact, visibilityClass, userLabels } from "../lib/api";
import ProbeBadge from "./ProbeBadge";

export default function ArtifactCard({ plural, a }: { plural: string; a: Artifact }) {
  const title = (a.spec?.title as string) || a.metadata.name;
  const desc = (a.spec?.description as string) || "";
  const labels = userLabels(a.metadata.labels);
  const verified = a.metadata.name.includes("/") || Boolean(a.metadata.annotations?.["verifiedPublisher"]);
  const shortDigest = a.metadata.digest ? a.metadata.digest.replace(/^sha256:/, "").slice(0, 10) : "";

  return (
    <Link
      to={`/${plural}/${encodeURIComponent(a.metadata.name)}${a.metadata.namespace ? `?namespace=${encodeURIComponent(a.metadata.namespace)}` : ""}`}
      className="card card-link p-5 flex flex-col gap-3"
      style={{ textDecoration: "none" }}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="font-serif text-[16px] font-semibold leading-snug truncate" style={{ color: "var(--ink-strong)" }}>
            {title}
          </div>
          <div className="font-mono text-[11px] truncate mt-0.5" style={{ color: "var(--ink-muted)" }}>
            {a.metadata.name}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1.5">
          {a.kind === "MCPServer" && <ProbeBadge server={a} />}
          <span className={`chip ${visibilityClass(a.metadata.visibility)}`}>
            {a.metadata.visibility ?? "private"}
          </span>
        </div>
      </div>

      {desc && (
        <p className="text-[13px] leading-relaxed line-clamp-2" style={{ color: "var(--ink-soft)" }}>
          {desc}
        </p>
      )}

      <div className="flex items-center gap-1.5 flex-wrap mt-auto pt-1">
        <span className="chip font-mono">{a.metadata.tag ?? "latest"}</span>
        <span className="chip badge-verified">latest</span>
        {shortDigest && (
          <span className="chip font-mono" title={a.metadata.digest}>
            <Fingerprint className="w-3 h-3" /> {shortDigest}
          </span>
        )}
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
