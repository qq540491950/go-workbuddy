import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { ACCENTS, FONT_SCALES, applyTheme, cacheTheme, type ThemePrefs } from "@/lib/theme";
import { App, A2A, Config, type AgentConfig, type A2AStatus, type KnowledgeFile, type KnowledgeIndexStats, type ModelProvider, type Settings, type StorageInfo, type MemoryInfo, errText, Protocol } from "@/lib/api";

export function SettingsPage() {
  const [settings, setSettings] = useState<Settings | null>(null);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [agents, setAgents] = useState<AgentConfig[]>([]);
  const [saving, setSaving] = useState(false);
  const importRef = useRef<HTMLInputElement>(null);
  const [logOpen, setLogOpen] = useState(false);
  const [logText, setLogText] = useState("");
  const [storage, setStorage] = useState<StorageInfo | null>(null);
  const [memoryInfo, setMemoryInfo] = useState<MemoryInfo | null>(null);
  const [a2aStatuses, setA2AStatuses] = useState<A2AStatus[]>([]);
  const [a2aAgent, setA2AAgent] = useState("");
  const [a2aPort, setA2APort] = useState("8080");
  const [indexStats, setIndexStats] = useState<KnowledgeIndexStats | null>(null);
  const [kbTestQuery, setKbTestQuery] = useState("");
  const [kbTestResult, setKbTestResult] = useState("");
  const [kbFiles, setKbFiles] = useState<KnowledgeFile[]>([]);

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
    A2A.Status().then((sts) => setA2AStatuses(sts ?? [])).catch(() => {});
    Config.KnowledgeIndexStats().then(setIndexStats).catch(() => {});
    Config.ListKnowledgeFiles().then((fs) => setKbFiles(fs ?? [])).catch(() => {});
    App.StorageStats().then(setStorage).catch(() => {});
    App.MemoryStats().then(setMemoryInfo).catch(() => {});
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
            <CardTitle className="text-base">网络</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-1.5">
            <Label>HTTP 代理</Label>
            <Input
              value={settings.httpProxy}
              onChange={(e) => setSettings({ ...settings, httpProxy: e.target.value })}
              placeholder="http://127.0.0.1:7890（留空直连）"
            />
            <p className="text-xs text-muted-foreground">
              模型请求、知识库向量与 MCP 远程连接统一走该代理；保存并重启应用后生效。
            </p>
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
              {kbFiles.length > 0 && (
                <div className="rounded-lg border p-2">
                  <div className="mb-1 text-xs text-muted-foreground">knowledge/ 目录文件</div>
                  <div className="grid gap-1">
                    {kbFiles.map((f) => (
                      <div key={f.name} className="flex items-center justify-between gap-2 text-xs">
                        <span className="truncate font-mono">{f.name}</span>
                        <span className="shrink-0 text-muted-foreground">
                          {(f.sizeBytes / 1024).toFixed(1)} KB · {f.modTime}
                        </span>
                        <button
                          className="shrink-0 rounded p-0.5 text-muted-foreground hover:text-destructive"
                          title={`删除 ${f.name}`}
                          onClick={async () => {
                            if (!window.confirm(`删除知识文件「${f.name}」？索引将同步移除该文件。`)) return;
                            try {
                              await Config.DeleteKnowledgeFile(f.name);
                              setKbFiles((prev) => (prev ?? []).filter((x) => x.name !== f.name));
                              toast.success("已删除（下次检索自动从索引移除）");
                            } catch (e) {
                              toast.error(`删除失败: ${errText(e)}`);
                            }
                          }}
                        >
                          ✕
                        </button>
                      </div>
                    ))}
                  </div>
                </div>
              )}
              <div className="grid gap-1.5">
                <div className="flex items-center gap-2">
                  <Input
                    value={kbTestQuery}
                    onChange={(e) => setKbTestQuery(e.target.value)}
                    placeholder="试一条检索词，验证检索效果…"
                  />
                  <Button variant="outline" className="shrink-0" onClick={async () => {
                    try {
                      setKbTestResult(await Config.TestKnowledgeSearch(kbTestQuery));
                    } catch (e) {
                      toast.error(`检索失败: ${errText(e)}`);
                    }
                  }}>
                    测试检索
                  </Button>
                </div>
                {kbTestResult && (
                  <pre className="max-h-40 overflow-auto rounded bg-muted/60 p-2 text-[11px] leading-relaxed text-muted-foreground">{kbTestResult}</pre>
                )}
              </div>
              <div className="flex items-center justify-between gap-2">
                <p className="text-xs text-muted-foreground">
                  配置后，知识库检索（search_knowledge）升级为「向量语义 + 关键词」混合检索；向量索引自动增量构建于本地 SQLite。留空则保持关键词检索。
                  {indexStats && indexStats.files > 0 && ` 已索引 ${indexStats.files} 个文件 / ${indexStats.chunks} 个分块${indexStats.model ? `（模型 ${indexStats.model}）` : ""}。`}
                </p>
                <Button
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={async () => {
                    try {
                      await Config.ResetKnowledgeIndex();
                      const st = await Config.KnowledgeIndexStats();
                      setIndexStats(st);
                      toast.success("索引已清空，下次检索将自动重建");
                    } catch (e) {
                      toast.error(`重置失败: ${errText(e)}`);
                    }
                  }}
                >
                  重建索引
                </Button>
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">长会话上下文压缩</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-1.5">
            <div className="grid grid-cols-2 gap-3">
              <div className="grid gap-1.5">
                <Label>滑动窗口压缩间隔（轮）</Label>
                <Input
                  type="number"
                  min={0}
                  value={settings.compactionInterval || ""}
                  onChange={(e) => setSettings({ ...settings, compactionInterval: Number(e.target.value) || 0 })}
                  placeholder="0（关闭）"
                />
              </div>
              <div className="grid gap-1.5">
                <Label>Token 阈值触发</Label>
                <Input
                  type="number"
                  min={0}
                  value={settings.compactionTokenThreshold || ""}
                  onChange={(e) => setSettings({ ...settings, compactionTokenThreshold: Number(e.target.value) || 0 })}
                  placeholder="0（关闭）"
                />
              </div>
            </div>
            <p className="text-xs text-muted-foreground">
              每 N 轮自动把更早的对话压缩为摘要（ADK 滑动窗口），或当提示 token 超过阈值时保留尾部、压缩其余。两者都填则同时生效；均为 0 表示关闭。远程 A2A 会话不支持。
            </p>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">长期记忆</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-2">
            <div className="flex items-center justify-between rounded-lg border p-2.5">
              <div>
                <div className="text-sm font-medium">自动存入长期记忆</div>
                <div className="text-xs text-muted-foreground">每轮对话结束后自动将该会话存入记忆（含工具调用摘要）</div>
              </div>
              <Switch
                checked={settings.autoRemember}
                onCheckedChange={(v) => {
                  update({ autoRemember: v });
                  Config.SaveSettings({ ...settings, autoRemember: v }).catch((e) => toast.error(`保存失败: ${errText(e)}`));
                }}
              />
            </div>
            <div className="flex items-center justify-between gap-2">
              <p className="text-xs text-muted-foreground">
                {memoryInfo ? `当前存储 ${memoryInfo.count} 条记忆条目（按用户隔离，Agent 可跨会话召回）。` : "加载中…"}
              </p>
              {memoryInfo && memoryInfo.count > 0 && (
                <Button
                  variant="outline"
                  size="sm"
                  className="shrink-0"
                  onClick={async () => {
                    if (!window.confirm("确定清空全部长期记忆？该操作不可恢复。")) return;
                    try {
                      await App.MemoryClear();
                      setMemoryInfo({ count: 0 });
                      toast.success("长期记忆已清空");
                    } catch (e) {
                      toast.error(`清空失败: ${errText(e)}`);
                    }
                  }}
                >
                  清空记忆
                </Button>
              )}
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle className="text-base">诊断</CardTitle>
          </CardHeader>
          <CardContent className="grid gap-2">
            <Button
              variant="outline"
              onClick={async () => {
                try {
                  const lines = (await App.RecentLogs(200)) ?? [];
                  setLogText(lines.join("\n"));
                  setLogOpen(true);
                } catch (e) {
                  toast.error(`读取日志失败: ${errText(e)}`);
                }
              }}
            >
              查看最近日志（200 行）
            </Button>
            <Dialog open={logOpen} onOpenChange={setLogOpen}>
              <DialogContent className="sm:max-w-2xl">
                <DialogHeader>
                  <DialogTitle>应用日志（最近 200 行）</DialogTitle>
                </DialogHeader>
                <pre className="max-h-[50vh] overflow-auto rounded bg-muted/60 p-3 font-mono text-[11px] leading-relaxed">{logText}</pre>
              </DialogContent>
            </Dialog>
            <div className="flex items-center justify-between gap-2">
              <p className="text-xs text-muted-foreground">
                遇到问题时先查看日志（logs/app.log，5MB 自动轮转），反馈问题时可一并附上。
                {storage && storage.sessionsDbBytes >= 0 && (
                  <>
                    会话库 {(storage.sessionsDbBytes / 1024 / 1024).toFixed(1)} MB
                    {storage.knowledgeDbBytes >= 0 && ` · 知识索引 ${(storage.knowledgeDbBytes / 1024).toFixed(0)} KB`}
                  </>
                )}
              </p>
              <Button variant="outline" size="sm" className="shrink-0" onClick={async () => {
                try {
                  await App.VacuumSessions();
                  toast.success("会话库已压缩整理");
                } catch (e) {
                  toast.error(`压缩失败: ${errText(e)}`);
                }
              }}>
                压缩会话库
              </Button>
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
              <Button
                onClick={async () => {
                  if (!a2aAgent) { toast.error("请选择要发布的 Agent"); return; }
                  try {
                    await A2A.Start(a2aAgent, Number(a2aPort) || 0);
                    setA2AStatuses((await A2A.Status()) ?? []);
                    toast.success("A2A 服务已启动");
                  } catch (e) {
                    toast.error(`启动失败: ${errText(e)}`);
                  }
                }}
              >
                发布 Agent
              </Button>
            </div>
            {(a2aStatuses.length ?? 0) > 0 && (
              <div className="grid gap-1.5 rounded-lg border p-2.5">
                {a2aStatuses.map((st) => (
                  <div key={st.agentId} className="flex items-center justify-between gap-2 text-xs">
                    <span className="truncate">
                      <span className="font-medium">{st.agentName}</span>
                      <span className="ml-1.5 font-mono text-muted-foreground">{st.url}</span>
                    </span>
                    <Button variant="ghost" size="sm" className="shrink-0" onClick={async () => {
                      try {
                        await A2A.StopAgent(st.agentId ?? "");
                        setA2AStatuses((await A2A.Status()) ?? []);
                      } catch (e) {
                        toast.error(`停止失败: ${errText(e)}`);
                      }
                    }}>
                      停止
                    </Button>
                  </div>
                ))}
              </div>
            )}
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
