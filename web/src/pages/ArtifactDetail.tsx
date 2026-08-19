import { useEffect, useState, lazy, Suspense } from "react";
import { useParams, useSearchParams, Link } from "react-router-dom";
import { ArrowLeft, Loader2, Tag, ShieldCheck, ShieldAlert, Copy, Check, Fingerprint, Pencil, History } from "lucide-react";
import { api, KINDS, type Artifact, type Revision, visibilityClass, userLabels } from "../lib/api";
import { verifyEd25519 } from "../lib/verify";
import ArtifactEditor from "../components/ArtifactEditor";
import { AdminOnly, useRegistrySession } from "../lib/session";
// React Flow is heavy — load it only when viewing a Workflow/Blueprint.
const FlowCanvas = lazy(() => import("../components/FlowCanvas"));

interface Version {
  tag: string;
  digest?: string;
  updatedAt?: string;
}

export default function ArtifactDetail() {
	const session = useRegistrySession();
  const { plural = "skills", name = "" } = useParams();
  // Artifacts live in a namespace (often not "default" — e.g. "devai"). The
  // catalog list browses every readable namespace and carries the namespace in
  // the link query, so honour it here; fall back to "default" for direct links.
  const [searchParams] = useSearchParams();
  const namespace = searchParams.get("namespace") || "default";
  const meta = KINDS.find((k) => k.plural === plural) ?? KINDS[0];

  const [a, setA] = useState<Artifact | null>(null);
  const [versions, setVersions] = useState<Version[]>([]);
  const [revisions, setRevisions] = useState<Revision[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [editOpen, setEditOpen] = useState(false);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    setError(null);
    setA(null);
    setVersions([]);
    api.get(plural, name, namespace).then(setA).catch((e) => setError(String(e.message ?? e)));
    // Versions table with per-version fingerprints.
    api
      .tags(plural, name, namespace)
      .then(async (t) => {
        const rows = await Promise.all(
          t.tags.map(async (tag) => {
            try {
              const o = await api.getVersion(plural, name, tag, namespace);
              return { tag, digest: o.metadata.digest, updatedAt: o.metadata.updatedAt };
            } catch {
              return { tag };
            }
          }),
        );
        setVersions(rows);
      })
      .catch(() => setVersions([]));
    api.revisions(plural, name, namespace).then(setRevisions).catch(() => setRevisions([]));
  }, [plural, name, namespace, reload]);

  if (error) {
    return (
      <div className="px-4 sm:px-8 py-7 sm:py-9 max-w-5xl mx-auto">
        <Back plural={plural} label={meta.label} />
        <div className="card p-6 mt-4 text-[13px]" style={{ color: "var(--error-ink)" }}>
          {error}
        </div>
      </div>
    );
  }
  if (!a) {
    return (
      <div className="p-7 flex items-center gap-2 justify-center mt-20" style={{ color: "var(--ink-muted)" }}>
        <Loader2 className="w-4 h-4 spin" /> loading…
      </div>
    );
  }

  const m = a.metadata;
  const title = (a.spec?.title as string) || m.name;
  const desc = (a.spec?.description as string) || "";
  const labels = userLabels(m.labels);
  const verified = m.name.includes("/");
  const shortDigest = m.digest ? m.digest.replace(/^sha256:/, "").slice(0, 12) : "";
  const pullCmd = `agentic pull ${m.ref ?? `${plural}/${m.namespace}/${m.name}@${m.tag ?? "latest"}`}`;

  return (
    <div className="px-4 sm:px-8 py-7 sm:py-9 max-w-5xl mx-auto">
      <Back plural={plural} label={meta.label} />

      <div className="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-3 sm:gap-4 mt-4">
        <div className="min-w-0">
          <div className="label-eyebrow">{meta.kind}</div>
          <h1 className="font-serif text-2xl sm:text-3xl font-semibold mt-1 break-words" style={{ color: "var(--ink-strong)" }}>
            {title}
          </h1>
          <div className="flex items-center gap-2 mt-1.5 flex-wrap">
            <span className="font-mono text-[12px]" style={{ color: "var(--ink-muted)" }}>
              {m.name}
            </span>
            <span className="chip font-mono">{m.tag ?? "latest"}</span>
            <span className="chip badge-verified">latest</span>
            {shortDigest && (
              <span className="chip badge-verified font-mono" title={m.digest}>
                <Fingerprint className="w-3 h-3" /> {shortDigest}
              </span>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2 flex-wrap sm:shrink-0">
          {verified && (
            <span className="chip badge-verified">
              <ShieldCheck className="w-3 h-3" /> verified
            </span>
          )}
          <span className={`chip ${visibilityClass(m.visibility)}`}>{m.visibility}</span>
          <AdminOnly>
            <button className="btn-secondary" onClick={() => setEditOpen(true)}>
              <Pencil className="w-3.5 h-3.5" /> Edit
            </button>
          </AdminOnly>
        </div>
      </div>

      {desc && (
        <p className="text-[15px] mt-4 max-w-2xl" style={{ color: "var(--ink-soft)" }}>
          {desc}
        </p>
      )}

      {/* Pull command */}
      <CopyLine className="mt-5" value={pullCmd} prefix="$ " />

      {/* Visual flow editor for Workflow / Blueprint kinds */}
      {(meta.kind === "Workflow" || meta.kind === "Blueprint") && (
        <div className="mt-6">
          <Suspense fallback={<div className="card flex items-center justify-center" style={{ height: 480, color: "var(--ink-muted)" }}><Loader2 className="w-4 h-4 spin" /></div>}>
            <FlowCanvas key={m.digest} artifact={a} plural={plural} kind={meta.kind} readOnly={!session.admin} onSaved={() => setReload((n) => n + 1)} />
          </Suspense>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-3 gap-4 mt-6">
        <section className="lg:col-span-2 space-y-4">
          {/* Identity & provenance — the artifact-repository surface */}
          <div className="card p-5">
            <div className="label-eyebrow mb-3">Identity &amp; provenance</div>
            <div className="space-y-2.5">
              <CopyField label="ARN" value={m.arn} />
              <CopyField label="Digest" value={m.digest} />
              <CopyField label="Reference" value={m.ref} />
              <CopyField label="Digest reference" value={m.digestRef} />
              <CopyField label="UID" value={m.uid} />
              <SignaturePanel digest={m.digest} signature={m.signature} signedBy={m.signedBy} />
            </div>
          </div>

          {/* Spec */}
          <div className="card p-5">
            <div className="label-eyebrow mb-3">Spec</div>
            <pre
              className="font-mono text-[12px] overflow-auto rounded-md p-3"
              style={{ background: "var(--surface-muted)", color: "var(--ink-soft)", maxHeight: 360 }}
            >
              {JSON.stringify(a.spec ?? {}, null, 2)}
            </pre>
          </div>

          {/* Append-only audit timeline */}
          <div className="card p-5">
            <div className="flex items-center gap-2 mb-4">
              <History className="w-4 h-4" style={{ color: "var(--ink-muted)" }} />
              <span className="label-eyebrow">History</span>
              <span className="font-mono text-[11px] ml-auto" style={{ color: "var(--ink-muted)" }}>
                {revisions.length} revision{revisions.length === 1 ? "" : "s"}
              </span>
            </div>
            {revisions.length === 0 ? (
              <p className="text-[12px]" style={{ color: "var(--ink-muted)" }}>No recorded revisions yet.</p>
            ) : (
              <ol className="relative">
                {revisions.map((rev, i) => (
                  <li key={`${rev.tag}-${rev.revision}`} className="flex gap-3 pb-4 last:pb-0">
                    {/* timeline rail */}
                    <div className="flex flex-col items-center shrink-0">
                      <span
                        className="w-2.5 h-2.5 rounded-full mt-1"
                        style={{ background: i === 0 ? "var(--accent)" : "var(--border-strong)" }}
                      />
                      {i < revisions.length - 1 && <span className="w-px flex-1 mt-1" style={{ background: "var(--border-subtle)" }} />}
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2 flex-wrap">
                        <span className="font-mono text-[12px]" style={{ color: "var(--ink)" }}>
                          {rev.tag}
                        </span>
                        <span className="chip" style={{ padding: "1px 6px" }}>rev {rev.revision}</span>
                        {i === 0 && <span className="chip badge-public" style={{ padding: "1px 6px" }}>current</span>}
                        <span className="font-mono text-[11px] ml-auto" style={{ color: "var(--ink-muted)" }}>
                          {rev.createdAt?.slice(0, 19).replace("T", " ")}
                        </span>
                      </div>
                      <div className="font-mono text-[10.5px] truncate mt-0.5" style={{ color: "var(--ink-muted)" }} title={rev.digest}>
                        {rev.digest}
                      </div>
                    </div>
                  </li>
                ))}
              </ol>
            )}
          </div>
        </section>

        <aside className="space-y-4">
          {/* Versions with fingerprints */}
          <div className="card p-5">
            <div className="label-eyebrow mb-3">Versions</div>
            <div className="space-y-2">
              {(versions.length ? versions : [{ tag: m.tag ?? "latest", digest: m.digest, updatedAt: m.updatedAt }]).map((v) => {
                const current = v.tag === (m.tag ?? "latest");
                return (
                  <div key={v.tag} className="flex flex-col gap-0.5 pb-2 border-b last:border-0" style={{ borderColor: "var(--border-subtle)" }}>
                    <div className="flex items-center gap-2 font-mono text-[12px]" style={{ color: "var(--ink)" }}>
                      <Tag className="w-3.5 h-3.5" style={{ color: "var(--ink-muted)" }} /> {v.tag}
                      {v.tag !== "latest" && (
                        <span className="chip badge-public" style={{ padding: "1px 6px" }}>immutable</span>
                      )}
                      {current && <span className="chip" style={{ padding: "1px 6px" }}>current</span>}
                    </div>
                    {v.digest && (
                      <span className="font-mono text-[10.5px] truncate" style={{ color: "var(--ink-muted)" }} title={v.digest}>
                        {v.digest.slice(0, 23)}…
                      </span>
                    )}
                  </div>
                );
              })}
            </div>
          </div>

          {/* Labels */}
          <div className="card p-5">
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

          {/* Scope */}
          <div className="card p-5 text-[12px] space-y-1.5" style={{ color: "var(--ink-soft)" }}>
            <div className="label-eyebrow mb-2">Scope</div>
            <Row k="tenant" v={m.tenantId} />
            {m.orgId && <Row k="org" v={m.orgId} />}
            <Row k="namespace" v={m.namespace} />
            <Row k="created" v={m.createdAt?.slice(0, 19).replace("T", " ")} />
            <Row k="updated" v={m.updatedAt?.slice(0, 19).replace("T", " ")} />
          </div>
        </aside>
      </div>

      <AdminOnly>
        <ArtifactEditor
          kind={meta.kind}
          plural={plural}
          open={editOpen}
          initial={{
            apiVersion: a.apiVersion,
            kind: a.kind,
            metadata: {
              name: m.name,
              namespace: m.namespace,
              // Blank so saving auto-increments to the next version (a released
              // version is immutable). User can type one to pin.
              tag: "",
              visibility: m.visibility,
              labels: userLabels(m.labels),
            },
            spec: a.spec ?? {},
          }}
          onClose={() => setEditOpen(false)}
          onCreated={() => {
            setEditOpen(false);
            setReload((n) => n + 1);
          }}
        />
      </AdminOnly>
    </div>
  );
}

function SignaturePanel({ digest, signature, signedBy }: { digest?: string; signature?: string; signedBy?: string }) {
  const [state, setState] = useState<"idle" | "verifying" | "ok" | "fail" | "error">("idle");
  if (!signature) return null;

  async function verify() {
    setState("verifying");
    try {
      const key = await api.signingKey();
      if (!key.enabled || !key.publicKey || !digest) {
        setState("error");
        return;
      }
      setState((await verifyEd25519(key.publicKey, signature!, digest)) ? "ok" : "fail");
    } catch {
      setState("error");
    }
  }

  const icon =
    state === "verifying" ? <Loader2 className="w-3.5 h-3.5 spin" /> :
    state === "ok" ? <ShieldCheck className="w-3.5 h-3.5" style={{ color: "var(--ok)" }} /> :
    state === "fail" || state === "error" ? <ShieldAlert className="w-3.5 h-3.5" style={{ color: "var(--error)" }} /> :
    <ShieldCheck className="w-3.5 h-3.5" />;
  const label = state === "ok" ? "Verified" : state === "fail" ? "Invalid" : state === "error" ? "Can’t verify" : "Verify";

  return (
    <div className="pt-1">
      <div className="text-[11px] mb-0.5" style={{ color: "var(--ink-muted)" }}>
        Signature (ed25519){signedBy && <span className="font-mono"> · key {signedBy}</span>}
      </div>
      <div className="flex items-center gap-2">
        <div
          className="flex-1 min-w-0 font-mono text-[11px] truncate rounded-md px-2.5 py-1.5"
          style={{ background: "var(--surface-muted)", border: "1px solid var(--border-subtle)", color: "var(--ink-soft)" }}
          title={signature}
        >
          {signature}
        </div>
        <button className="btn-secondary shrink-0" style={{ padding: "6px 10px" }} onClick={verify}>
          {icon}
          {label}
        </button>
      </div>
    </div>
  );
}

function CopyField({ label, value }: { label: string; value?: string }) {
  const [copied, setCopied] = useState(false);
  if (!value) return null;
  return (
    <div>
      <div className="text-[11px] mb-0.5" style={{ color: "var(--ink-muted)" }}>
        {label}
      </div>
      <button
        onClick={() => {
          navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1200);
        }}
        className="w-full flex items-center gap-2 rounded-md px-2.5 py-1.5 text-left"
        style={{ background: "var(--surface-muted)", border: "1px solid var(--border-subtle)" }}
      >
        <span className="font-mono text-[12px] truncate flex-1" style={{ color: "var(--ink)" }}>
          {value}
        </span>
        {copied ? <Check className="w-3.5 h-3.5 shrink-0" style={{ color: "var(--ok)" }} /> : <Copy className="w-3.5 h-3.5 shrink-0" style={{ color: "var(--ink-muted)" }} />}
      </button>
    </div>
  );
}

function CopyLine({ value, prefix = "", className = "" }: { value: string; prefix?: string; className?: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <div className={`card p-3 flex items-center justify-between font-mono text-[13px] ${className}`}>
      <span className="truncate" style={{ color: "var(--ink-soft)" }}>
        {prefix}
        {value}
      </span>
      <button
        onClick={() => {
          navigator.clipboard.writeText(value);
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }}
        className="btn-ghost shrink-0"
        style={{ padding: "4px 8px" }}
      >
        {copied ? <Check className="w-4 h-4" /> : <Copy className="w-4 h-4" />}
      </button>
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
