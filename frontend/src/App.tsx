import { useEffect, useState } from "react";
import { MessageSquare, Bot, Users, Sparkles, Blocks, Boxes, Settings as SettingsIcon, Sun, Moon } from "lucide-react";
import { Toaster } from "@/components/ui/sonner";
import { ChatPage } from "@/pages/ChatPage";
import { AgentsPage } from "@/pages/AgentsPage";
import { TeamsPage } from "@/pages/TeamsPage";
import { SkillsPage } from "@/pages/SkillsPage";
import { MCPPage } from "@/pages/MCPPage";
import { ModelsPage } from "@/pages/ModelsPage";
import { SettingsPage } from "@/pages/SettingsPage";
import { cn } from "@/lib/utils";
import { applyTheme, cachedTheme, onSystemThemeChange, type ThemePrefs } from "@/lib/theme";
import { Config } from "@/lib/api";

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
  const [prefs, setPrefs] = useState<ThemePrefs>(() => cachedTheme());
  const [isDark, setIsDark] = useState(() => applyTheme(cachedTheme()));

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
            <button
              title="切换深色/浅色主题"
              onClick={toggleTheme}
              className="rounded p-1 hover:bg-sidebar-accent/60"
            >
              {isDark ? <Sun className="h-3.5 w-3.5" /> : <Moon className="h-3.5 w-3.5" />}
            </button>
          </div>
          <div>v0.1.0</div>
        </div>
      </aside>

      <main className="min-w-0 flex-1 overflow-hidden">
        {page === "chat" && <ChatPage />}
        {page === "agents" && <AgentsPage />}
        {page === "teams" && <TeamsPage />}
        {page === "skills" && <SkillsPage />}
        {page === "mcp" && <MCPPage />}
        {page === "models" && <ModelsPage />}
        {page === "settings" && <SettingsPage />}
      </main>

      <Toaster position="top-center" richColors />
    </div>
  );
}
