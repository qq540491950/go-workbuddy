import { lazy, Suspense, useEffect, useState } from "react";
import { MessageSquare, Bot, Users, Sparkles, Blocks, Boxes, Settings as SettingsIcon, Sun, Moon, CircleHelp } from "lucide-react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Toaster } from "@/components/ui/sonner";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { cn } from "@/lib/utils";
import { applyTheme, cachedTheme, onSystemThemeChange, type ThemePrefs } from "@/lib/theme";
import { App as AppService, Config } from "@/lib/api";

// Pages load on demand: Chat is the landing view and stays eager, everything
// else is a settings-style screen that should not sit in the startup bundle.
import { ChatPage } from "@/pages/ChatPage";
const AgentsPage = lazy(() => import("@/pages/AgentsPage").then((m) => ({ default: m.AgentsPage })));
const TeamsPage = lazy(() => import("@/pages/TeamsPage").then((m) => ({ default: m.TeamsPage })));
const SkillsPage = lazy(() => import("@/pages/SkillsPage").then((m) => ({ default: m.SkillsPage })));
const MCPPage = lazy(() => import("@/pages/MCPPage").then((m) => ({ default: m.MCPPage })));
const ModelsPage = lazy(() => import("@/pages/ModelsPage").then((m) => ({ default: m.ModelsPage })));
const SettingsPage = lazy(() => import("@/pages/SettingsPage").then((m) => ({ default: m.SettingsPage })));

const navItems = [
  { key: "chat", label: "对话", icon: MessageSquare },
  { key: "agents", label: "Agents", icon: Bot },
  { key: "teams", label: "Agent 团队", icon: Users },
  { key: "skills", label: "Skills", icon: Sparkles },
  { key: "mcp", label: "MCP 服务", icon: Blocks },
  { key: "models", label: "模型配置", icon: Boxes },
  { key: "settings", label: "设置", icon: SettingsIcon },
] as const;

type NavKey = (typeof navItems)[number]["key"];

export default function App() {
  const [page, setPage] = useState<NavKey>("chat");
  const [helpOpen, setHelpOpen] = useState(false);
  const [prefs, setPrefs] = useState<ThemePrefs>(() => cachedTheme());
  const [isDark, setIsDark] = useState(() => applyTheme(cachedTheme()));
  const [version, setVersion] = useState("");

  // Boot: fetch app metadata for the sidebar version label.
  useEffect(() => {
    AppService.AppInfo().then((info) => setVersion(info?.version ?? "")).catch(() => {});
  }, []);

  // Boot: apply cached theme instantly, then sync from persisted settings.
  useEffect(() => {
    Config.GetConfig()
      .then((cfg) => {
        const s = cfg.settings;
        const next: ThemePrefs = {
          themeMode: s.themeMode || "dark",
          accent: s.accent || "blue",
          fontScale: s.fontScale || "md",
        };
        localStorage.setItem("wb-theme-prefs", JSON.stringify(next));
        setPrefs(next);
        setIsDark(applyTheme(next));
      })
      .catch(() => {});
  }, []);

  // Re-apply when the OS appearance changes (themeMode === "system").
  useEffect(() => onSystemThemeChange(prefs, setIsDark), [prefs]);

  // Quick light/dark toggle from the sidebar (persists to backend).
  const toggleTheme = async () => {
    const mode = isDark ? "light" : "dark";
    const next = { ...prefs, themeMode: mode };
    setPrefs(next);
    setIsDark(applyTheme(next));
    localStorage.setItem("wb-theme-prefs", JSON.stringify(next));
    try {
      const cfg = await Config.GetConfig();
      cfg.settings.themeMode = mode;
      await Config.SaveSettings(cfg.settings);
    } catch { /* offline: cache only */ }
  };

  return (
    <div className="flex h-screen w-screen overflow-hidden bg-background text-foreground">
      <aside className="flex w-52 shrink-0 flex-col border-r bg-sidebar py-4 text-sidebar-foreground">
        <div className="mb-6 flex items-center gap-2 px-4">
          <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-primary text-primary-foreground">
            <Sparkles className="h-4 w-4" />
          </div>
          <div>
            <div className="text-sm font-semibold leading-none">WorkBuddy</div>
            <div className="mt-1 text-[11px] text-muted-foreground">ADK Agent 桌面端</div>
          </div>
        </div>
        <nav className="flex flex-1 flex-col gap-1 px-2">
          {navItems.map(({ key, label, icon: Icon }) => (
            <button
              key={key}
              onClick={() => setPage(key)}
              className={cn(
                "flex items-center gap-2.5 rounded-md px-3 py-2 text-sm transition-colors",
                page === key
                  ? "bg-sidebar-accent font-medium text-sidebar-accent-foreground"
                  : "text-muted-foreground hover:bg-sidebar-accent/50 hover:text-sidebar-accent-foreground",
              )}
            >
              <Icon className="h-4 w-4" />
              {label}
            </button>
          ))}
        </nav>
        <div className="px-4 text-[11px] text-muted-foreground">
          <div className="mb-2 flex items-center justify-between">
            <span>ADK Go v2 · Wails v3</span>
            <span className="flex items-center gap-1">
              <button
                title="快捷键与命令"
                onClick={() => setHelpOpen(true)}
                className="rounded p-1 hover:bg-sidebar-accent/60"
              >
                <CircleHelp className="h-3.5 w-3.5" />
              </button>
              <button
                title="切换深色/浅色主题"
                onClick={toggleTheme}
                className="rounded p-1 hover:bg-sidebar-accent/60"
              >
                {isDark ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
              </button>
            </span>
          </div>
          {version && <div>v{version}</div>}
        </div>

        <Dialog open={helpOpen} onOpenChange={setHelpOpen}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle className="text-base">快捷键与命令</DialogTitle>
            </DialogHeader>
            <div className="grid gap-2 text-sm">
              {[
                ["⌘/Ctrl + N", "新建对话"],
                ["⌘/Ctrl + K", "聚焦输入框"],
                ["Enter", "发送消息"],
                ["Shift + Enter", "换行"],
                ["/export", "导出会话 Markdown"],
                ["/exporthtml", "导出会话 HTML"],
                ["/stats", "会话统计"],
                ["/new", "新建对话"],
              ].map(([k, d]) => (
                <div key={k} className="flex items-center justify-between rounded-lg border px-3 py-1.5">
                  <span className="font-mono text-xs">{k}</span>
                  <span className="text-xs text-muted-foreground">{d}</span>
                </div>
              ))}
            </div>
          </DialogContent>
        </Dialog>
      </aside>

      <main className="min-w-0 flex-1 overflow-hidden">
        {/* key={page} resets the boundary on navigation: one crashed page must
            not keep every other page behind the error screen. */}
        <ErrorBoundary key={page}>
          <Suspense fallback={<div className="p-6 text-sm text-muted-foreground">加载中…</div>}>
            {page === "chat" && <ChatPage />}
            {page === "agents" && <AgentsPage />}
            {page === "teams" && <TeamsPage />}
            {page === "skills" && <SkillsPage />}
            {page === "mcp" && <MCPPage />}
            {page === "models" && <ModelsPage />}
            {page === "settings" && <SettingsPage />}
          </Suspense>
        </ErrorBoundary>
      </main>

      <Toaster position="top-center" richColors />
    </div>
  );
}
