import { useCallback, useEffect, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { ACCENTS, FONT_SCALES, applyTheme, cacheTheme, type ThemePrefs } from "@/lib/theme";
import { Config, type Settings, errText } from "@/lib/api";

export function SettingsPage() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [saving, setSaving] = useState(false);

  const reload = useCallback(() => {
    Config.GetConfig().then((c) => setSettings(c.settings)).catch((e) => toast.error(errText(e)));
  }, []);
  useEffect(reload, [reload]);

  const update = (patch: Partial<Settings>) => {
    if (!settings) return;
    const next = { ...settings, ...patch };
    setSettings(next);
    // Live-apply appearance changes without waiting for save.
    if ("themeMode" in patch || "accent" in patch || "fontScale" in patch) {
      const prefs: ThemePrefs = {
        themeMode: next.themeMode, accent: next.accent, fontScale: next.fontScale,
      };
      cacheTheme(next);
      applyTheme(prefs);
    }
  };

  const save = async () => {
    if (!settings) return;
    setSaving(true);
    try {
      await Config.SaveSettings(settings);
      cacheTheme(settings);
      applyTheme({ themeMode: settings.themeMode, accent: settings.accent, fontScale: settings.fontScale });
      toast.success("设置已保存");
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
    } finally {
      setSaving(false);
    }
  };

  if (!settings) return null;

  return (
    <div className="h-full overflow-y-auto p-6">
      <div className="mb-4">
        <h1 className="text-lg font-semibold">设置</h1>
        <p className="text-sm text-muted-foreground">应用全局配置</p>
      </div>

      <div className="max-w-xl space-y-4">
        <Card>
          <CardHeader>
            <CardTitle className="text-base">外观</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-5">
            <div className="grid gap-1.5">
              <Label>主题模式（夜间模式）</Label>
              <Select value={settings.themeMode || "dark"} onValueChange={(v) => update({ themeMode: v })}>
                <SelectTrigger className="w-56"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="light">浅色</SelectItem>
                  <SelectItem value="dark">深色（夜间）</SelectItem>
                  <SelectItem value="system">跟随系统</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div className="grid gap-1.5">
              <Label>强调色</Label>
              <div className="flex flex-wrap gap-2">
                {Object.entries(ACCENTS).map(([key, a]) => (
                  <button
                    key={key}
                    title={a.label}
                    onClick={() => update({ accent: key })}
                    className={cn(
                      "flex h-9 w-9 items-center justify-center rounded-full border-2 transition-transform hover:scale-105",
                      (settings.accent || "blue") === key ? "border-foreground" : "border-transparent",
                    )}
                  >
                    <span
                      className="h-6 w-6 rounded-full border"
                      style={{
                        background: a.color || "var(--primary)",
                      }}
                    />
                  </button>
                ))}
              </div>
            </div>

            <div className="grid gap-1.5">
              <Label>正文字号</Label>
              <div className="flex gap-2">
                {Object.entries(FONT_SCALES).map(([key, f]) => (
                  <Button
                    key={key}
                    variant={(settings.fontScale || "md") === key ? "default" : "outline"}
                    size="sm"
                    className="w-16"
                    onClick={() => update({ fontScale: key })}
                  >
                    {f.label}
                  </Button>
                ))}
              </div>
            </div>

            <div className="flex items-center justify-between rounded-lg border p-3">
              <div>
                <div className="text-sm font-medium">自动朗读回复</div>
                <div className="text-xs text-muted-foreground">助手回复完成后使用语音朗读（TTS）</div>
              </div>
              <Switch
                checked={settings.autoSpeak}
                onCheckedChange={(v) => update({ autoSpeak: v })}
              />
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">工作区</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4">
            <div className="grid gap-1.5">
              <Label>工作区目录</Label>
              <Input
                value={settings.workspaceDir}
                onChange={(e) => setSettings({ ...settings, workspaceDir: e.target.value })}
                placeholder="/Users/you/Documents/WorkBuddyWorkspace"
              />
              <p className="text-xs text-muted-foreground">
                开启「工作区文件读写」内置工具的 Agent 只能访问该目录内的文件；「产物预览」也仅限该目录。
              </p>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">关于</CardTitle>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            <div>WorkBuddy Agent · 基于 Google ADK Go v2 构建</div>
            <div className="mt-1">
              Agent 运行时：ADK Runner / LLMAgent / AgentTool；会话存储：SQLite（ADK Database Session Service）；
              工具：ADK FunctionTool、MCP Toolset（go-sdk）、Skill Toolset（agentskills.io 规范）。
            </div>
          </CardContent>
        </Card>

        <Button onClick={save} disabled={saving}>保存设置</Button>
      </div>
    </div>
  );
}
