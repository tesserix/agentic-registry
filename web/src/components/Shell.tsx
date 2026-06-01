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
  Search,
} from "lucide-react";
import { api, KINDS, type Health } from "../lib/api";
import CommandPalette from "./CommandPalette";
import Logo from "./Logo";

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
  const [paletteOpen, setPaletteOpen] = useState(false);
  const loc = useLocation();

  useEffect(() => {
    api.health().then(setHealth).catch(() => setHealth(null));
  }, []);

  // Global ⌘K / Ctrl+K toggles the command palette.
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((o) => !o);
      }
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  function toggleTheme() {
    const next = !dark;
    setDark(next);
    document.documentElement.classList.toggle("dark", next);
    localStorage.setItem("ar-theme", next ? "dark" : "light");
  }

  const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

  return (
    <div className="flex min-h-screen">
      <aside
        className="w-64 shrink-0 border-r flex flex-col fixed inset-y-0"
        style={{ borderColor: "var(--border-subtle)", background: "var(--sidebar)" }}
      >
        <NavLink
          to="/"
          className="flex items-center gap-2.5 px-5 h-16 border-b shrink-0"
          style={{ borderColor: "var(--border-subtle)" }}
        >
          <Logo className="w-[20px] h-[20px]" style={{ color: "var(--accent)" }} />
          <span className="font-display text-[15px] font-bold tracking-tight" style={{ color: "var(--ink-strong)" }}>
            Agentic Registry
          </span>
        </NavLink>

        <nav className="flex-1 px-3 py-5 space-y-0.5 overflow-y-auto">
          <div className="label-eyebrow px-2.5 pb-2.5">Catalog</div>
          {KINDS.map((k) => {
            const Icon = ICONS[k.plural] ?? Boxes;
            const active = loc.pathname.startsWith(`/${k.plural}`);
            return (
              <NavLink key={k.plural} to={`/${k.plural}`} className={`nav-item ${active ? "active" : ""}`}>
                <Icon className="w-[17px] h-[17px]" />
                {k.label}
              </NavLink>
            );
          })}
        </nav>

        <div className="px-3 py-3 border-t space-y-1 shrink-0" style={{ borderColor: "var(--border-subtle)" }}>
          <button onClick={toggleTheme} className="btn-ghost w-full justify-start">
            {dark ? <Sun className="w-4 h-4" /> : <Moon className="w-4 h-4" />}
            {dark ? "Light" : "Dark"} mode
          </button>
          <div className="flex items-center gap-2 px-2.5 pt-1.5 text-[11px] font-mono" style={{ color: "var(--ink-muted)" }}>
            <span
              className="w-1.5 h-1.5 rounded-full"
              style={{ background: health?.status === "ok" ? "var(--ok)" : "var(--error)" }}
            />
            {health ? `${health.platform} · ${health.version}` : "offline"}
          </div>
        </div>
      </aside>

      {/* Main column, offset by the fixed sidebar. */}
      <div className="flex-1 min-w-0 ml-64 flex flex-col">
        {/* Top bar with the ⌘K search trigger. */}
        <header
          className="sticky top-0 z-20 h-16 flex items-center gap-3 px-7 border-b backdrop-blur"
          style={{
            borderColor: "var(--border-subtle)",
            background: "color-mix(in srgb, var(--canvas) 82%, transparent)",
          }}
        >
          <button
            onClick={() => setPaletteOpen(true)}
            className="field flex items-center gap-2.5 max-w-md text-left"
            style={{ color: "var(--ink-muted)", cursor: "pointer", padding: "8px 12px" }}
          >
            <Search className="w-4 h-4 shrink-0" />
            <span className="text-[13px] flex-1">Search the registry…</span>
            <span className="flex items-center gap-1">
              <kbd className="kbd">{isMac ? "⌘" : "Ctrl"}</kbd>
              <kbd className="kbd">K</kbd>
            </span>
          </button>
        </header>

        <main className="flex-1">{children}</main>
      </div>

      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
    </div>
  );
}
