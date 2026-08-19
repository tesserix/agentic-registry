import { useEffect, useState } from "react";
import {
  Activity,
  BookOpen,
  Boxes,
  ExternalLink,
  LogOut,
  Menu,
  Moon,
  ShieldCheck,
  Sun,
  UserRound,
  X,
} from "lucide-react";
import { loadMcpGatewayProfile, type McpGatewayProfile } from "../lib/mcpGateway";
import { useRegistrySession } from "../lib/session";
import Logo from "./Logo";

export default function AgentGatewayShell({ children }: { children: React.ReactNode }) {
  const session = useRegistrySession();
  const [dark, setDark] = useState(document.documentElement.classList.contains("dark"));
  const [navOpen, setNavOpen] = useState(false);
  const [profile, setProfile] = useState<McpGatewayProfile | null>(null);

  useEffect(() => {
    document.title = "Tesserix AgentGateway";
    loadMcpGatewayProfile().then(setProfile).catch(() => setProfile(null));
  }, []);

  function toggleTheme() {
    const next = !dark;
    setDark(next);
    document.documentElement.classList.toggle("dark", next);
    localStorage.setItem("ar-theme", next ? "dark" : "light");
  }

  const email = profile?.email || session.email;
  const initials = profile?.initials || email.charAt(0).toUpperCase();

  return (
    <div className="mcp-app min-h-screen">
      {navOpen && (
        <button className="fixed inset-0 z-30 bg-black/40 md:hidden" onClick={() => setNavOpen(false)} aria-label="Close navigation" />
      )}
      <aside className={`mcp-sidebar fixed inset-y-0 left-0 z-40 flex w-[252px] flex-col border-r transition-transform md:translate-x-0 ${navOpen ? "translate-x-0" : "-translate-x-full"}`}>
        <div className="flex h-[72px] items-center justify-between border-b px-5">
          <a href="/" className="flex min-w-0 items-center gap-3">
            <span className="mcp-brand-mark grid h-9 w-9 shrink-0 place-items-center rounded-xl"><Logo className="h-5 w-5" /></span>
            <span><span className="block font-display text-[15px] font-bold text-[var(--ink-strong)]">Tesserix</span><span className="block text-[11px] text-[var(--ink-muted)]">AgentGateway</span></span>
          </a>
          <button className="btn-ghost p-2 md:hidden" onClick={() => setNavOpen(false)} aria-label="Close menu"><X className="h-5 w-5" /></button>
        </div>
        <nav className="flex-1 px-3 py-5">
          <div className="label-eyebrow px-3 pb-3">Control plane</div>
          <a className="nav-item active" href="/"><Boxes className="h-[17px] w-[17px]" />Desired state</a>
          <a className="nav-item" href="/ui/traffic/routes"><Activity className="h-[17px] w-[17px]" />Runtime traffic<ExternalLink className="ml-auto h-3 w-3" /></a>
          <a className="nav-item" href="https://mcp.tesserix.app" target="_blank" rel="noreferrer"><ShieldCheck className="h-[17px] w-[17px]" />MCP Gateway<ExternalLink className="ml-auto h-3 w-3" /></a>
          <div className="label-eyebrow px-3 pb-3 pt-8">Resources</div>
          <a className="nav-item" href="https://agentgateway.dev/docs" target="_blank" rel="noreferrer"><BookOpen className="h-[17px] w-[17px]" />AgentGateway docs</a>
        </nav>
        <div className="border-t p-3">
          <div className="mb-2 flex items-center gap-2 rounded-xl border border-[var(--ok-soft-bd)] bg-[var(--ok-soft-bg)] px-3 py-2.5 text-[12px] text-[var(--ok-ink)]"><ShieldCheck className="h-4 w-4" />Zitadel protected<span className="ml-auto h-2 w-2 rounded-full bg-[var(--ok)]" /></div>
          <button onClick={toggleTheme} className="btn-ghost w-full justify-start">{dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}{dark ? "Light" : "Dark"} mode</button>
        </div>
      </aside>
      <div className="min-w-0 md:ml-[252px]">
        <header className="mcp-topbar sticky top-0 z-20 flex h-[72px] items-center gap-3 border-b px-4 sm:px-7">
          <button className="btn-ghost -ml-2 p-2 md:hidden" onClick={() => setNavOpen(true)} aria-label="Open menu"><Menu className="h-5 w-5" /></button>
          <div><div className="text-[13px] font-semibold text-[var(--ink-strong)]">Agent routing control plane</div><div className="text-[11px] text-[var(--ink-muted)]">Registry desired state · XDS runtime</div></div>
          <div className="ml-auto hidden items-center gap-2 rounded-full border border-[var(--border)] bg-[var(--surface)] px-3 py-1.5 font-mono text-[11px] text-[var(--ink-soft)] sm:flex"><span className="h-1.5 w-1.5 rounded-full bg-[var(--ok)]" />agentgateway.tesserix.app</div>
          <details className="group relative">
            <summary className="flex cursor-pointer list-none items-center gap-2 rounded-xl border border-[var(--border)] bg-[var(--surface)] p-1.5 pr-2.5 [&::-webkit-details-marker]:hidden" aria-label="Open user profile">
              <span className="grid h-8 w-8 place-items-center rounded-lg bg-[var(--accent-soft)] text-[11px] font-bold text-[var(--accent)]">{initials || <UserRound className="h-4 w-4" />}</span>
              <span className="hidden sm:block"><span className="block max-w-44 truncate text-[12px] font-semibold text-[var(--ink-strong)]">{profile?.displayName || email || "Signed-in user"}</span><span className="block text-[10px] text-[var(--ink-muted)]">Administrator</span></span>
            </summary>
            <div className="absolute right-0 z-30 mt-2 w-72 rounded-2xl border border-[var(--border)] bg-[var(--surface)] p-4 shadow-xl">
              <div className="truncate text-sm font-semibold text-[var(--ink-strong)]">{profile?.displayName || "Signed-in user"}</div>
              <div className="mt-1 truncate text-[11px] text-[var(--ink-muted)]">{email || "Verified by Zitadel"}</div>
              <div className="mt-4 flex items-center justify-between rounded-xl border border-[var(--ok-soft-bd)] bg-[var(--ok-soft-bg)] px-3 py-2.5 text-[11px] text-[var(--ok-ink)]"><span>Platform role</span><strong>Administrator</strong></div>
              <a className="btn-ghost mt-3 w-full justify-start" href="/oauth2/sign_out?rd=%2F"><LogOut className="h-4 w-4" />Sign out</a>
            </div>
          </details>
        </header>
        <main>{children}</main>
      </div>
    </div>
  );
}
