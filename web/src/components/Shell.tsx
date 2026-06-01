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
  Menu,
  X,
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
  // Mobile nav drawer. On md+ the sidebar is always visible; below md it slides
  // in over a backdrop and this toggles it.
  const [navOpen, setNavOpen] = useState(false);
  const loc = useLocation();

  useEffect(() => {
    api.health().then(setHealth).catch(() => setHealth(null));
  }, []);

  // Close the mobile drawer whenever the route changes (i.e. a nav link tap).
  useEffect(() => {
    setNavOpen(false);
  }, [loc.pathname]);

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
      {/* Backdrop behind the mobile drawer (md: never shown). */}
      {navOpen && (
        <div
          className="fixed inset-0 z-30 md:hidden"
          style={{ background: "rgba(10, 11, 14, 0.45)", backdropFilter: "blur(2px)" }}
          onClick={() => setNavOpen(false)}
          aria-hidden
        />
      )}

      <aside
        className={`w-64 shrink-0 border-r flex flex-col fixed inset-y-0 z-40 transition-transform duration-200 ease-out md:translate-x-0 ${
          navOpen ? "translate-x-0" : "-translate-x-full"
        }`}
        style={{ borderColor: "var(--border-subtle)", background: "var(--sidebar)" }}
      >
        <div className="flex items-center justify-between px-5 h-16 border-b shrink-0" style={{ borderColor: "var(--border-subtle)" }}>
          <NavLink to="/" className="flex items-center gap-2.5 min-w-0">
            <Logo className="w-[20px] h-[20px] shrink-0" style={{ color: "var(--accent)" }} />
            <span className="font-display text-[15px] font-bold tracking-tight truncate" style={{ color: "var(--ink-strong)" }}>
              Agentic Registry
            </span>
          </NavLink>
          {/* Close the drawer (mobile only). */}
          <button
            onClick={() => setNavOpen(false)}
            className="btn-ghost md:hidden -mr-2 p-2"
            aria-label="Close menu"
          >
            <X className="w-[18px] h-[18px]" />
          </button>
        </div>

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
            {health ? [health.platform, health.version].filter(Boolean).join(" · ") || health.status : "offline"}
          </div>
        </div>
      </aside>

      {/* Main column. Offset by the fixed sidebar on md+, full-width below. */}
      <div className="flex-1 min-w-0 md:ml-64 flex flex-col">
        {/* Top bar: hamburger (mobile) + ⌘K search trigger. */}
        <header
          className="sticky top-0 z-20 h-16 flex items-center gap-2.5 px-4 md:px-7 border-b backdrop-blur"
          style={{
            borderColor: "var(--border-subtle)",
            background: "color-mix(in srgb, var(--canvas) 82%, transparent)",
          }}
        >
          <button
            onClick={() => setNavOpen(true)}
            className="btn-ghost md:hidden -ml-1.5 p-2 shrink-0"
            aria-label="Open menu"
          >
            <Menu className="w-5 h-5" />
          </button>
          <button
            onClick={() => setPaletteOpen(true)}
            className="field flex items-center gap-2.5 md:max-w-md text-left"
            style={{ color: "var(--ink-muted)", cursor: "pointer", padding: "8px 12px" }}
          >
            <Search className="w-4 h-4 shrink-0" />
            <span className="text-[13px] flex-1 truncate">Search the registry…</span>
            {/* Keyboard hint only where there's a keyboard. */}
            <span className="hidden sm:flex items-center gap-1">
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
