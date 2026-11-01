import { lazy, Suspense, useEffect, useState } from "react";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Toaster } from "@/components/ui/sonner";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { applyTheme, cachedTheme, onSystemThemeChange, type ThemePrefs } from "@/lib/theme";
import { App as AppService, Config } from "@/lib/api";

// Chat is the landing view and stays eager; the settings hub (which hosts the
// management screens) loads on demand so it never sits in the startup bundle.
import { ChatPage } from "@/pages/ChatPage";
const SettingsPage = lazy(() => import("@/pages/SettingsPage").then((m) => ({ default: m.SettingsPage })));
import type { SettingsTab } from "@/pages/SettingsPage";

export default function App() {
  const [view, setView] = useState<"chat" | "settings">("chat");
  const [settingsTab, setSettingsTab] = useState<SettingsTab | undefined>(undefined);
  const openSettings = (tab?: SettingsTab) => {
    setSettingsTab(tab);
    setView("settings");
  };

  // Global shortcuts (documented in the help panel on every page): ⌘N starts
  // a chat; ⌘K focuses the composer; ⌘F toggles in-session search; ⌘, opens
  // settings; ⌘⇧[ / ⌘⇧] cycle sessions. ChatPage consumes the chat shortcuts
  // via window events so they survive view switches.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;
      if (!mod || e.repeat) return;
      // ⌘⇧[ / ⌘⇧] move through the session list (US layouts report the key
      // as { / }; compare by code so the shortcut survives layouts).
      if (e.shiftKey && e.code === "BracketLeft") {
        e.preventDefault();
        setView("chat");
        setTimeout(() => window.dispatchEvent(new CustomEvent("wb-prev-session")), 50);
        return;
      }
      if (e.shiftKey && e.code === "BracketRight") {
        e.preventDefault();
        setView("chat");
        setTimeout(() => window.dispatchEvent(new CustomEvent("wb-next-session")), 50);
        return;
      }
      const key = e.key.toLowerCase();
      if (key !== "n" && key !== "k" && key !== "f" && key !== "," && key !== "b") return;
      e.preventDefault();
      if (key === "b") {
        setView("chat");
        // Deferred like ⌘N: when leaving the settings view, ChatPage mounts
        // on the next render and only then registers the listener.
        setTimeout(() => window.dispatchEvent(new CustomEvent("wb-toggle-sidebar")), 50);
        return;
      }
      if (key === ",") {
        openSettings();
        return;
      }
      setView("chat");
      // Deferred: when switching from another page, ChatPage mounts on the
      // next render — a synchronous dispatch would fire before its listener
      // exists and the action would be lost.
      const ev = new CustomEvent(key === "k" ? "wb-focus-composer" : key === "f" ? "wb-toggle-filter" : "wb-new-chat");
      setTimeout(() => window.dispatchEvent(ev), 50);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
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
      <main className="min-w-0 flex-1 overflow-hidden">
        {/* key={view} resets the boundary on navigation: one crashed view must
            not keep the other behind the error screen. */}
        <ErrorBoundary key={view}>
          <Suspense fallback={<div className="p-6 text-sm text-muted-foreground">加载中…</div>}>
            {view === "chat" ? (
              <ChatPage
                isDark={isDark}
                onToggleTheme={toggleTheme}
                onOpenHelp={() => setHelpOpen(true)}
                onOpenSettings={openSettings}
              />
            ) : (
              <SettingsPage initialTab={settingsTab} onClose={() => setView("chat")} version={version} />
            )}
          </Suspense>
        </ErrorBoundary>
      </main>

      <Dialog open={helpOpen} onOpenChange={setHelpOpen}>
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle className="text-base">快捷键与命令</DialogTitle>
            </DialogHeader>
            <div className="grid gap-2 text-sm">
              {[
                ["Enter", "发送消息"],
                ["Shift + Enter", "换行"],
                ["⌘/Ctrl + N", "新对话（全局）"],
                ["⌘/Ctrl + K", "聚焦输入框（全局）"],
                ["⌘/Ctrl + F", "会话内搜索（全局）"],
                ["⌘/Ctrl + ,", "打开设置（全局）"],
                ["⌘/Ctrl + B", "折叠 / 展开对话列表"],
                ["⌘/Ctrl + ⇧[ / ⇧]", "上一个 / 下一个会话"],
                ["↑", "编辑最后一条消息（输入框为空时）"],
                ["Esc", "取消编辑 → 停止生成 → 关闭搜索"],
                ["/export", "导出会话 Markdown"],
                ["/exporthtml", "导出会话 HTML"],
                ["/stats", "会话统计"],
                ["/todos", "查看当前任务计划"],
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

      <Toaster position="top-center" richColors />
    </div>
  );
}
