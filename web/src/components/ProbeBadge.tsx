import { CheckCircle2, CircleDashed, TriangleAlert, XCircle } from "lucide-react";
import type { Artifact } from "../lib/api";
import { probeStatus, type ProbeState } from "../lib/mcpGateway";

const PRESENTATION: Record<
  ProbeState,
  { label: string; icon: typeof CheckCircle2; tone: string }
> = {
  ready: {
    label: "Active",
    icon: CheckCircle2,
    tone: "border-[var(--ok-soft-bd)] bg-[var(--ok-soft-bg)] text-[var(--ok-ink)]",
  },
  drifted: {
    label: "Drifted",
    icon: TriangleAlert,
    tone: "border-[var(--warn-soft-bd)] bg-[var(--warn-soft-bg)] text-[var(--warn-ink)]",
  },
  unreachable: {
    label: "Unreachable",
    icon: XCircle,
    tone: "border-[var(--error-soft-bd)] bg-[var(--error-soft-bg)] text-[var(--error-ink)]",
  },
  unprobed: {
    label: "Unprobed",
    icon: CircleDashed,
    tone: "border-[var(--border)] bg-[var(--surface-muted)] text-[var(--ink-muted)]",
  },
};

export default function ProbeBadge({
  server,
  showMessage = false,
}: {
  server: Artifact;
  showMessage?: boolean;
}) {
  const status = probeStatus(server);
  const { label, icon: Icon, tone } = PRESENTATION[status.state];

  return (
    <span
      title={status.message ?? undefined}
      className={`flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[10px] font-semibold uppercase tracking-[0.08em] ${tone}`}
    >
      <Icon className="h-3 w-3" /> {label}
      {showMessage && status.message ? (
        <span className="normal-case tracking-normal">— {status.message}</span>
      ) : null}
    </span>
  );
}
