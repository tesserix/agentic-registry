import { useEffect, useState } from "react";
import { NavLink, useLocation } from "react-router-dom";
import {
  BookOpen,
  KeyRound,
  LayoutGrid,
  Menu,
  Moon,
  ShieldCheck,
  Sun,
  UserRound,
  X,
} from "lucide-react";
import {
  loadMcpGatewayProfile,
  type McpGatewayProfile,
} from "../lib/mcpGateway";
import Logo from "./Logo";

type Props = { children: React.ReactNode };

const NAV = [
  { to: "/", label: "MCP Servers", icon: LayoutGrid },
  { to: "/access", label: "Access & OAuth", icon: KeyRound },
] as const;

export default function MCPGatewayShell({ children }: Props) {
  const [dark, setDark] = useState(
    document.documentElement.classList.contains("dark"),
  );
  const [navOpen, setNavOpen] = useState(false);
  const [profile, setProfile] = useState<McpGatewayProfile | null>(null);
  const location = useLocation();

  useEffect(() => {
    let active = true;
    loadMcpGatewayProfile()
      .then((sessionProfile) => {
        if (active) setProfile(sessionProfile);
      })
      .catch(() => {
        if (active) setProfile(null);
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    setNavOpen(false);
    document.title = "Tesserix MCP Gateway";
  }, [location.pathname]);

  function toggleTheme() {
    const next = !dark;
    setDark(next);
    document.documentElement.classList.toggle("dark", next);
    localStorage.setItem("ar-theme", next ? "dark" : "light");
  }

  return (
    <div className="mcp-app min-h-screen">
      {navOpen && (
        <button
          className="fixed inset-0 z-30 bg-black/40 md:hidden"
          onClick={() => setNavOpen(false)}
          aria-label="Close navigation"
        />
      )}

      <aside
        className={`mcp-sidebar fixed inset-y-0 left-0 z-40 flex w-[252px] flex-col border-r transition-transform md:translate-x-0 ${
          navOpen ? "translate-x-0" : "-translate-x-full"
        }`}
      >
        <div className="flex h-[72px] items-center justify-between border-b px-5">
          <NavLink to="/" className="flex min-w-0 items-center gap-3">
            <span className="mcp-brand-mark grid h-9 w-9 shrink-0 place-items-center rounded-xl">
              <Logo className="h-5 w-5" />
            </span>
            <span className="min-w-0">
              <span className="block truncate font-display text-[15px] font-bold text-[var(--ink-strong)]">
                Tesserix
              </span>
              <span className="block truncate text-[11px] font-medium text-[var(--ink-muted)]">
                MCP Gateway
              </span>
            </span>
          </NavLink>
          <button
            className="btn-ghost p-2 md:hidden"
            onClick={() => setNavOpen(false)}
            aria-label="Close menu"
          >
            <X className="h-5 w-5" />
          </button>
        </div>

        <nav className="flex-1 px-3 py-5">
          <div className="label-eyebrow px-3 pb-3">Gateway</div>
          <div className="space-y-1">
            {NAV.map(({ to, label, icon: Icon }) => {
              const active =
                to === "/"
                  ? location.pathname === "/" ||
                    location.pathname.startsWith("/servers/")
                  : location.pathname.startsWith(to);
              return (
                <NavLink
                  key={to}
                  to={to}
                  className={`nav-item ${active ? "active" : ""}`}
                >
                  <Icon className="h-[17px] w-[17px]" />
                  {label}
                </NavLink>
              );
            })}
          </div>

          <div className="label-eyebrow px-3 pb-3 pt-8">Resources</div>
          <a
            className="nav-item"
            href="https://modelcontextprotocol.io/docs"
            target="_blank"
            rel="noreferrer"
          >
            <BookOpen className="h-[17px] w-[17px]" />
            MCP documentation
          </a>
        </nav>

        <div className="border-t p-3">
          <div className="mb-2 flex items-center gap-2 rounded-xl border border-[var(--ok-soft-bd)] bg-[var(--ok-soft-bg)] px-3 py-2.5 text-[12px] text-[var(--ok-ink)]">
            <ShieldCheck className="h-4 w-4 shrink-0" />
            <span>OAuth protected</span>
            <span className="ml-auto h-2 w-2 rounded-full bg-[var(--ok)]" />
          </div>
          <button
            onClick={toggleTheme}
            className="btn-ghost w-full justify-start"
          >
            {dark ? (
              <Sun className="h-4 w-4" />
            ) : (
              <Moon className="h-4 w-4" />
            )}
            {dark ? "Light" : "Dark"} mode
          </button>
        </div>
      </aside>

      <div className="min-w-0 md:ml-[252px]">
        <header className="mcp-topbar sticky top-0 z-20 flex h-[72px] items-center gap-3 border-b px-4 sm:px-7">
          <button
            className="btn-ghost -ml-2 p-2 md:hidden"
            onClick={() => setNavOpen(true)}
            aria-label="Open menu"
          >
            <Menu className="h-5 w-5" />
          </button>
          <div className="min-w-0">
            <div className="text-[13px] font-semibold text-[var(--ink-strong)]">
              Agent tool control plane
            </div>
            <div className="truncate text-[11px] text-[var(--ink-muted)]">
              Discover and securely connect agents to approved MCP servers
            </div>
          </div>
          <div className="ml-auto hidden items-center gap-2 rounded-full border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 font-mono text-[11px] text-[var(--ink-soft)] sm:flex">
            <span className="h-1.5 w-1.5 rounded-full bg-[var(--ok)]" />
            mcp.tesserix.app
          </div>
          <details className="group relative">
            <summary
              className="flex cursor-pointer list-none items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-1.5 pr-2.5 text-left transition-colors hover:bg-[var(--surface-raised)] [&::-webkit-details-marker]:hidden"
              aria-label="Open user profile"
            >
              <span className="grid h-8 w-8 shrink-0 place-items-center rounded-lg bg-[var(--accent-soft)] text-[11px] font-bold text-[var(--accent)]">
                {profile?.initials || <UserRound className="h-4 w-4" />}
              </span>
              <span className="hidden min-w-0 sm:block">
                <span className="block max-w-44 truncate text-[12px] font-semibold text-[var(--ink-strong)]">
                  {profile?.displayName || "Signed-in user"}
                </span>
                <span className="block text-[10px] text-[var(--ink-muted)]">
                  Administrator
                </span>
              </span>
            </summary>
            <div className="absolute right-0 z-30 mt-2 w-72 rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-xl">
              <div className="flex items-center gap-3">
                <span className="grid h-11 w-11 shrink-0 place-items-center rounded-xl bg-[var(--accent-soft)] text-sm font-bold text-[var(--accent)]">
                  {profile?.initials || <UserRound className="h-5 w-5" />}
                </span>
                <span className="min-w-0">
                  <span className="block truncate text-sm font-semibold text-[var(--ink-strong)]">
                    {profile?.displayName || "Signed-in user"}
                  </span>
                  <span className="block truncate text-[11px] text-[var(--ink-muted)]">
                    {profile?.email || "Verified by Zitadel"}
                  </span>
                </span>
              </div>
              <div className="mt-4 flex items-center justify-between rounded-xl border border-[var(--ok-soft-bd)] bg-[var(--ok-soft-bg)] px-3 py-2.5">
                <span className="text-[11px] font-medium text-[var(--ok-ink)]">
                  Platform role
                </span>
                <span className="text-[11px] font-bold text-[var(--ok-ink)]">
                  Administrator
                </span>
              </div>
              <p className="mt-3 text-[10px] leading-4 text-[var(--ink-muted)]">
                Session identity is verified by Zitadel and protected by the gateway allowlist.
              </p>
            </div>
          </details>
        </header>
        <main>{children}</main>
      </div>
    </div>
  );
}
