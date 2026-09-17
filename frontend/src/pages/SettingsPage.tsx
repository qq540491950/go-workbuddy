import { useCallback, useEffect, useRef, useState } from "react";
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
import { App, A2A, Config, type AgentConfig, type A2AStatus, type KnowledgeIndexStats, type ModelProvider, type Settings, errText, Protocol } from "@/lib/api";

export function SettingsPage() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [agents, setAgents] = useState<AgentConfig[]>([]);
  const [saving, setSaving] = useState(false);
  const importRef = useRef<HTMLInputElement>(null);
  const [a2aStatus, setA2AStatus] = useState<A2AStatus | null>(null);
  const [a2aAgent, setA2AAgent] = useState("");
  const [a2aPort, setA2APort] = useState("8080");
  const [indexStats, setIndexStats] = useState<KnowledgeIndexStats | null>(null);

  const reload = useCallback(() => {
    Config.GetConfig()
      .then((c) => {
        setSettings(c.settings);
        setProviders(c.providers ?? []);
        setAgents(c.agents ?? []);
      })
      .catch((e) => toast.error(errText(e)));
  }, []);
  useEffect(reload, [reload]);
  useEffect(() => {
    A2A.Status().then(setA2AStatus).catch(() => {});
    Config.KnowledgeIndexStats().then(setIndexStats).catch(() => {});
  }, []);

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
            <div className="flex items-center justify-between rounded-lg border p-3">
              <div>
                <div className="text-sm font-medium">工具调用审计日志</div>
                <div className="text-xs text-muted-foreground">将每次工具调用/结果以 JSONL 追加到应用日志目录（tools-audit.jsonl）</div>
              </div>
              <Switch
                checked={settings.toolsAudit}
                onCheckedChange={(v) => update({ toolsAudit: v })}
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
            <CardTitle className="text-base">知识库语义检索</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-4">
            <div className="grid gap-1.5">
              <Label>Embedding 供应商</Label>
              <Select
                value={settings.embeddingProviderId || "none"}
                onValueChange={(v) => update({ embeddingProviderId: v === "none" ? "" : v })}
              >
                <SelectTrigger className="w-72"><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="none">关闭（仅关键词检索）</SelectItem>
                  {providers.filter((p) => (p.protocol ?? Protocol.ProtocolOpenAI) === Protocol.ProtocolOpenAI).map((p) => (
                    <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-1.5">
              <Label>Embedding 模型</Label>
              <Input
                value={settings.embeddingModel}
                onChange={(e) => setSettings({ ...settings, embeddingModel: e.target.value })}
                placeholder="text-embedding-3-small / BAAI/bge-large-zh-v1.5"
              />
              <p className="text-xs text-muted-foreground">
                配置后，知识库检索（search_knowledge）升级为「向量语义 + 关键词」混合检索；向量索引自动增量构建于本地 SQLite。留空则保持关键词检索。
                {indexStats && indexStats.files > 0 && ` 已索引 ${indexStats.files} 个文件 / ${indexStats.chunks} 个分块${indexStats.model ? `（模型 ${indexStats.model}）` : ""}。`}
              </p>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">备份与迁移</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-3">
            <div className="flex flex-wrap gap-2">
              <Button
                variant="outline"
                onClick={async () => {
                  try {
                    const p = await App.ExportConfig(true);
                    toast.success(`已导出（含密钥）: ${p}`);
                  } catch (e) {
                    toast.error(`导出失败: ${errText(e)}`);
                  }
                }}
              >
                导出备份（含密钥）
              </Button>
              <Button
                variant="outline"
                onClick={async () => {
                  try {
                    const p = await App.ExportConfig(false);
                    toast.success(`已导出（脱敏）: ${p}`);
                  } catch (e) {
                    toast.error(`导出失败: ${errText(e)}`);
                  }
                }}
              >
                导出备份（脱敏）
              </Button>
              <Button variant="outline" onClick={() => importRef.current?.click()}>
                导入备份…
              </Button>
              <input
                ref={importRef}
                type="file"
                accept="application/json,.json"
                className="hidden"
                onChange={async (e) => {
                  const f = e.target.files?.[0];
                  e.target.value = "";
                  if (!f) return;
                  if (!window.confirm("导入将覆盖当前供应商/Agent/远程 Agent/团队/Skills/MCP/设置（会话历史保留），确定继续？")) return;
                  try {
                    const text = await f.text();
                    await App.ImportConfig(text);
                    toast.success("导入成功，已应用备份配置");
                    reload();
                  } catch (err) {
                    toast.error(`导入失败: ${errText(err)}`);
                  }
                }}
              />
            </div>
            <p className="text-xs text-muted-foreground">
              备份写入工作区 exports/ 目录。含密钥备份以 0600 权限保存且可直接恢复；脱敏备份不含 API Key，适合分享或迁移。
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">A2A 服务（对外提供远程 Agent）</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-3">
            <div className="grid grid-cols-2 gap-3">
              <div className="grid gap-1.5">
                <Label>发布的 Agent</Label>
                <Select value={a2aAgent || undefined} onValueChange={setA2AAgent}>
                  <SelectTrigger><SelectValue placeholder="选择 Agent" /></SelectTrigger>
                  <SelectContent>
                    {agents.map((a) => (
                      <SelectItem key={a.id} value={a.id}>{a.name}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-1.5">
                <Label>端口</Label>
                <Input value={a2aPort} onChange={(e) => setA2APort(e.target.value)} placeholder="8080" />
              </div>
            </div>
            <div className="flex items-center gap-2">
              {!a2aStatus?.running ? (
                <Button
                  onClick={async () => {
                    if (!a2aAgent) { toast.error("请选择要发布的 Agent"); return; }
                    try {
                      setA2AStatus(await A2A.Start(a2aAgent, Number(a2aPort) || 0));
                      toast.success("A2A 服务已启动");
                    } catch (e) {
                      toast.error(`启动失败: ${errText(e)}`);
                    }
                  }}
                >
                  启动服务
                </Button>
              ) : (
                <Button
                  variant="outline"
                  onClick={async () => {
                    try {
                      await A2A.Stop();
                      setA2AStatus(await A2A.Status());
                      toast.success("A2A 服务已停止");
                    } catch (e) {
                      toast.error(`停止失败: ${errText(e)}`);
                    }
                  }}
                >
                  停止服务
                </Button>
              )}
              {a2aStatus?.running && (
                <span className="text-xs text-muted-foreground">
                  运行中：<span className="font-mono">{a2aStatus.url}</span> · 发布 {a2aStatus.agentName}
                </span>
              )}
            </div>
            <div className="flex items-center justify-between rounded-lg border p-3">
              <div>
                <div className="text-sm font-medium">开机自动启动 A2A 服务</div>
                <div className="text-xs text-muted-foreground">使用上方选择的 Agent 与端口，应用启动时自动发布</div>
              </div>
              <Switch
                checked={settings.a2aAutoStart}
                onCheckedChange={(v) => {
                  const next = {
                    ...settings,
                    a2aAutoStart: v,
                    a2aAgentId: v ? (a2aAgent || settings.a2aAgentId) : settings.a2aAgentId,
                    a2aPort: Number(a2aPort) || 0,
                  };
                  update(next);
                  Config.SaveSettings(next).catch((e) => toast.error(`保存失败: ${errText(e)}`));
                }}
              />
            </div>
            <p className="text-xs text-muted-foreground">
              启动后其他 A2A 客户端可通过该地址发现并调用此 Agent（仅监听本机地址）。
            </p>
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
