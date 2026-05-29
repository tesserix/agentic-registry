import { useEffect, useState } from "react";
import { useParams, Link } from "react-router-dom";
import { ArrowLeft, Loader2, Tag, ShieldCheck, Copy, Check } from "lucide-react";
import { api, KINDS, type Artifact, visibilityClass, userLabels } from "../lib/api";

export default function ArtifactDetail() {
  const { plural = "skills", name = "" } = useParams();
  const meta = KINDS.find((k) => k.plural === plural) ?? KINDS[0];

  const [a, setA] = useState<Artifact | null>(null);
  const [tags, setTags] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    setError(null);
    setA(null);
    api.get(plural, name).then(setA).catch((e) => setError(String(e.message ?? e)));
    api.tags(plural, name).then((t) => setTags(t.tags)).catch(() => setTags([]));
  }, [plural, name]);

  const installCmd = `agentic pull ${meta.kind.toLowerCase()} ${name}`;

  function copy() {
    navigator.clipboard.writeText(installCmd);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  }

  if (error) {
    return (
      <div className="p-7 max-w-4xl mx-auto">
        <Back plural={plural} label={meta.label} />
        <div className="panel p-6 mt-4 text-[13px]" style={{ color: "var(--error-ink)" }}>
          {error}
        </div>
      </div>
    );
  }
  if (!a) {
    return (
      <div className="p-7 flex items-center gap-2 justify-center mt-20" style={{ color: "var(--ink-muted)" }}>
        <Loader2 className="w-4 h-4 animate-spin" /> loading…
      </div>
    );
  }

  const title = (a.spec?.title as string) || a.metadata.name;
  const desc = (a.spec?.description as string) || "";
  const labels = userLabels(a.metadata.labels);
  const verified = a.metadata.name.includes("/");

  return (
    <div className="p-7 max-w-4xl mx-auto">
      <Back plural={plural} label={meta.label} />

      <div className="flex items-start justify-between gap-4 mt-4">
        <div>
          <div className="label-eyebrow">{meta.kind}</div>
          <h1 className="font-serif text-3xl font-medium mt-1" style={{ color: "var(--ink-strong)" }}>
            {title}
          </h1>
          <div className="font-mono text-[12px] mt-1" style={{ color: "var(--ink-muted)" }}>
            {a.metadata.name}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <span className={`chip ${visibilityClass(a.metadata.visibility)}`}>{a.metadata.visibility}</span>
          {verified && (
            <span className="chip badge-verified">
              <ShieldCheck className="w-3 h-3" /> verified
            </span>
          )}
        </div>
      </div>

      {desc && (
        <p className="text-[15px] mt-4 max-w-2xl" style={{ color: "var(--ink-soft)" }}>
          {desc}
        </p>
      )}

      <div className="panel p-3 mt-5 flex items-center justify-between font-mono text-[13px]">
        <span style={{ color: "var(--ink-soft)" }}>$ {installCmd}</span>
        <button onClick={copy} className="btn-ghost" style={{ padding: "4px 8px" }}>
          {copied ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
        </button>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4 mt-6">
        <section className="lg:col-span-2 panel p-5">
          <div className="label-eyebrow mb-3">Spec</div>
          <pre
            className="font-mono text-[12px] overflow-auto rounded-md p-3"
            style={{ background: "var(--surface-muted)", color: "var(--ink-soft)", maxHeight: 420 }}
          >
            {JSON.stringify(a.spec ?? {}, null, 2)}
          </pre>
        </section>

        <aside className="space-y-4">
          <div className="panel p-5">
            <div className="label-eyebrow mb-3">Versions</div>
            <div className="space-y-1.5">
              {(tags.length ? tags : [a.metadata.tag ?? "latest"]).map((t) => (
                <div key={t} className="flex items-center gap-2 font-mono text-[12px]" style={{ color: "var(--ink-soft)" }}>
                  <Tag className="w-3.5 h-3.5" /> {t}
                </div>
              ))}
            </div>
          </div>

          <div className="panel p-5">
            <div className="label-eyebrow mb-3">Labels</div>
            <div className="flex flex-wrap gap-1.5">
              {Object.entries(labels).length === 0 && (
                <span className="text-[12px]" style={{ color: "var(--ink-muted)" }}>
                  none
                </span>
              )}
              {Object.entries(labels).map(([k, v]) => (
                <span key={k} className="chip">
                  {k}={v}
                </span>
              ))}
            </div>
          </div>

          <div className="panel p-5 text-[12px] space-y-1.5" style={{ color: "var(--ink-soft)" }}>
            <div className="label-eyebrow mb-2">Scope</div>
            <Row k="tenant" v={a.metadata.tenantId} />
            {a.metadata.orgId && <Row k="org" v={a.metadata.orgId} />}
            <Row k="namespace" v={a.metadata.namespace} />
            <Row k="updated" v={a.metadata.updatedAt?.slice(0, 19).replace("T", " ")} />
          </div>
        </aside>
      </div>
    </div>
  );
}

function Row({ k, v }: { k: string; v?: string }) {
  return (
    <div className="flex justify-between gap-3">
      <span style={{ color: "var(--ink-muted)" }}>{k}</span>
      <span className="font-mono truncate">{v ?? "—"}</span>
    </div>
  );
}

function Back({ plural, label }: { plural: string; label: string }) {
  return (
    <Link to={`/${plural}`} className="btn-ghost" style={{ padding: "4px 8px", marginLeft: -8 }}>
      <ArrowLeft className="w-4 h-4" /> {label}
    </Link>
  );
}
