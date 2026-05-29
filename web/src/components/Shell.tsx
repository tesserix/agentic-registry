import { useEffect, useState } from "react";
import { NavLink, useLocation } from "react-router-dom";
import {
  Boxes,
  Wrench,
  Server,
  MessageSquareText,
  Workflow as WorkflowIcon,
  LayoutTemplate,
  Bot,
  Moon,
  Sun,
  PackageOpen,
} from "lucide-react";
import { api, KINDS, type Health } from "../lib/api";

const ICONS: Record<string, React.ComponentType<{ className?: string }>> = {
  skills: Boxes,
  tools: Wrench,
  mcpservers: Server,
  prompts: MessageSquareText,
  workflows: WorkflowIcon,
  blueprints: LayoutTemplate,
  agents: Bot,
};

export default function Shell({ children }: { children: React.ReactNode }) {
  const [dark, setDark] = useState(document.documentElement.classList.contains("dark"));
  const [health, setHealth] = useState<Health | null>(null);
  const loc = useLocation();

  useEffect(() => {
    api.health().then(setHealth).catch(() => setHealth(null));
  }, []);

  function toggleTheme() {
    const next = !dark;
    setDark(next);
    document.documentElement.classList.toggle("dark", next);
    localStorage.setItem("ar-theme", next ? "dark" : "light");
  }

  return (
    <div className="flex min-h-screen">
      <aside className="w-60 shrink-0 border-r flex flex-col" style={{ borderColor: "var(--border-subtle)", background: "var(--surface)" }}>
        <NavLink to="/" className="flex items-center gap-2 px-5 h-16 border-b" style={{ borderColor: "var(--border-subtle)" }}>
          <PackageOpen className="w-5 h-5" style={{ color: "var(--accent)" }} />
          <span className="font-display text-sm font-semibold" style={{ color: "var(--ink-strong)" }}>
            Agentic Registry
          </span>
        </NavLink>

        <nav className="flex-1 px-3 py-4 space-y-0.5">
          <div className="label-eyebrow px-2 pb-2">Catalog</div>
          {KINDS.map((k) => {
            const Icon = ICONS[k.plural] ?? Boxes;
            const active = loc.pathname === `/${k.plural}`;
            return (
              <NavLink
                key={k.plural}
                to={`/${k.plural}`}
                className="flex items-center gap-2.5 px-2.5 py-2 rounded-md text-[13px]"
                style={{
                  color: active ? "var(--ink-strong)" : "var(--ink-soft)",
                  background: active ? "var(--accent-soft-bg)" : "transparent",
                  fontWeight: active ? 600 : 500,
                }}
              >
                <Icon className="w-4 h-4" />
                {k.label}
              </NavLink>
            );
          })}
        </nav>

        <div className="px-3 py-3 border-t" style={{ borderColor: "var(--border-subtle)" }}>
          <button onClick={toggleTheme} className="btn-ghost w-full justify-start">
            {dark ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
            {dark ? "Light" : "Dark"} mode
          </button>
          <div className="flex items-center gap-2 px-2.5 pt-2 text-[11px] font-mono" style={{ color: "var(--ink-muted)" }}>
            <span
              className="w-2 h-2 rounded-full"
              style={{ background: health?.status === "ok" ? "var(--ok)" : "var(--error)" }}
            />
            {health ? `${health.platform} · ${health.version}` : "offline"}
          </div>
        </div>
      </aside>

      <main className="flex-1 min-w-0">{children}</main>
    </div>
  );
}
