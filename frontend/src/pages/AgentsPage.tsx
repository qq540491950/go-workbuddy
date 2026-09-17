import { useCallback, useEffect, useRef, useState } from "react";
import { Plus, Trash2, Bot, Network, Loader2, FolderDown, Upload } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import {
  Config, type AgentConfig, type ModelProvider, type SkillConfig, type MCPServerConfig,
  type RemoteAgentConfig, type RemoteAgentInfo, App, errText } from "@/lib/api";

const agentTemplates: { label: string; make: () => AgentConfig }[] = [
  {
    label: "翻译助手",
    make: () => ({
      ...blankAgent(),
      name: "翻译助手",
      description: "中英互译，保留格式与术语",
      systemPrompt:
        "你是专业翻译。检测输入语言：中文译成英文，其他语言译成中文。保留 Markdown 格式、代码块与专有名词，译文自然流畅。只输出译文。",
      temperature: 0.3,
    }),
  },
  {
    label: "周报助手",
    make: () => ({
      ...blankAgent(),
      name: "周报助手",
      description: "根据本周工作记录撰写结构化周报",
      systemPrompt:
        "你是资深团队负责人。根据用户提供的工作记录，按「本周完成 / 进行中 / 下周计划 / 需协调」撰写结构化周报，量化成果，语气专业简洁。",
      temperature: 0.7,
      builtinTools: ["time"],
    }),
  },
  {
    label: "代码审查员",
    make: () => ({
      ...blankAgent(),
      name: "代码审查员",
      description: "系统化审查代码正确性、可读性、设计与安全",
      systemPrompt:
        "你是严谨的代码审查专家。按正确性、可读性、设计、安全四个维度审查代码，按 P0/P1/P2 分组输出问题，每条给出位置与修复建议。",
      temperature: 0.2,
    }),
  },
  {
    label: "会议纪要",
    make: () => ({
      ...blankAgent(),
      name: "会议纪要",
      description: "将会议记录整理为决议与行动项",
      systemPrompt:
        "你是高效的会议助理。将口语化的会议记录整理为：会议目标、关键讨论、决议事项、行动项（负责人+截止时间）。缺失信息标注 TODO。",
      temperature: 0.4,
    }),
  },
];

function blankAgent(): AgentConfig {
  return {
    id: "",
    name: "",
    description: "",
    providerId: "",
    model: "",
    systemPrompt: "",
    temperature: 0.7,
    maxTokens: 4096,
    skillIds: [],
    mcpServerIds: [],
    builtinTools: ["time"],
    createdAt: "",
    enableGuardrails: true,
  };
}

function toggle(list: string[], v: string): string[] {
  return list.includes(v) ? list.filter((x) => x !== v) : [...list, v];
}

export function AgentsPage() {
  const [agents, setAgents] = useState<AgentConfig[]>([]);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [skills, setSkills] = useState<SkillConfig[]>([]);
  const [servers, setServers] = useState<MCPServerConfig[]>([]);
  const [editing, setEditing] = useState<AgentConfig | null>(null);
  const [remotes, setRemotes] = useState<RemoteAgentConfig[]>([]);
  const [remoteEditing, setRemoteEditing] = useState<RemoteAgentConfig | null>(null);
  const [remoteCheck, setRemoteCheck] = useState<RemoteAgentInfo | null>(null);
  const [checking, setChecking] = useState(false);
  const agentImportRef = useRef<HTMLInputElement>(null);
  const remoteImportRef = useRef<HTMLInputElement>(null);

  const reload = useCallback(() => {
    Config.GetConfig().then((c) => {
      setAgents(c.agents ?? []);
      setProviders(c.providers ?? []);
      setSkills(c.skills ?? []);
      setServers(c.mcpServers ?? []);
      setRemotes(c.remoteAgents ?? []);
    });
  }, []);
  useEffect(reload, [reload]);

  const providerOf = (id: string) => providers.find((p) => p.id === id);

  const save = async () => {
    if (!editing) return;
    if (!editing.name.trim()) {
      toast.error("请填写 Agent 名称");
      return;
    }
    if (!editing.providerId || !editing.model) {
      toast.error("请选择供应商与模型");
      return;
    }
    try {
      await Config.SaveAgent(editing);
      toast.success("已保存");
      setEditing(null);
      reload();
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
    }
  };

  const remove = async (a: AgentConfig) => {
    if (!window.confirm(`确定删除 Agent「${a.name}」？挂载关系将一并解除。`)) return;
    await Config.DeleteAgent(a.id);
    toast.success(`已删除 ${a.name}`);
    reload();
  };

  const saveRemote = async () => {
    if (!remoteEditing) return;
    if (!remoteEditing.name.trim() || !remoteEditing.cardUrl.trim()) {
      toast.error("请填写名称与 Card URL");
      return;
    }
    try {
      await Config.SaveRemoteAgent(remoteEditing);
      toast.success("远程 Agent 已保存");
      setRemoteEditing(null);
      setRemoteCheck(null);
      reload();
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
    }
  };

  const checkRemote = async () => {
    if (!remoteEditing?.cardUrl.trim()) {
      toast.error("请先填写 Card URL");
      return;
    }
    setChecking(true);
    setRemoteCheck(null);
    try {
      setRemoteCheck(await Config.CheckRemoteAgent(remoteEditing.cardUrl.trim()));
    } catch (e) {
      toast.error(`连接失败: ${errText(e)}`);
    } finally {
      setChecking(false);
    }
  };

  const removeRemote = async (r: RemoteAgentConfig) => {
    if (!window.confirm(`确定删除远程 Agent「${r.name}」？`)) return;
    await Config.DeleteRemoteAgent(r.id);
    toast.success(`已删除 ${r.name}`);
    reload();
  };

  return (
    <div className="h-full overflow-y-auto p-6">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">Agents</h1>
          <p className="text-sm text-muted-foreground">定义具备系统提示词、Skills、MCP 工具的自定义 Agent</p>
        </div>
        <div className="flex items-center gap-2">
          <select
            className="h-9 rounded-md border bg-background px-2 text-sm text-muted-foreground"
            value=""
            onChange={(e) => {
              const t = agentTemplates.find((x) => x.label === e.target.value);
              if (t) setEditing(t.make());
            }}
          >
            <option value="" disabled>
              从模板创建…
            </option>
            {agentTemplates.map((t) => (
              <option key={t.label} value={t.label}>
                {t.label}
              </option>
            ))}
          </select>
          <Button variant="outline" onClick={() => agentImportRef.current?.click()}>
            <FolderDown className="mr-1 h-4 w-4" /> 导入 Agent
          </Button>
          <input
            ref={agentImportRef}
            type="file"
            accept="application/json,.json"
            className="hidden"
            onChange={async (e) => {
              const f = e.target.files?.[0];
              e.target.value = "";
              if (!f) return;
              try {
                const imported = await App.ImportAgent(await f.text());
                toast.success(`已导入 Agent「${imported.name}」`);
                reload();
                // Open the editor so the user picks a provider/model right away.
                setEditing({ ...imported, skillIds: imported.skillIds ?? [], mcpServerIds: imported.mcpServerIds ?? [], builtinTools: imported.builtinTools ?? [] });
              } catch (err) {
                toast.error(`导入失败: ${errText(err)}`);
              }
            }}
          />
          <Button onClick={() => setEditing(blankAgent())}>
            <Plus className="mr-1 h-4 w-4" /> 新增 Agent
          </Button>
        </div>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        {agents.length === 0 && (
          <Card className="md:col-span-2">
            <CardContent className="p-8 text-center text-sm text-muted-foreground">
              还没有 Agent。先到「模型配置」添加供应商，然后创建你的第一个 Agent。
            </CardContent>
          </Card>
        )}
        {agents.map((a) => {
          const p = providerOf(a.providerId);
          return (
            <Card key={a.id}>
              <CardContent className="p-4">
                <div className="flex items-start justify-between">
                  <div className="flex items-center gap-2">
                    <div className="flex h-8 w-8 items-center justify-center rounded-full bg-primary/10">
                      <Bot className="h-4 w-4 text-primary" />
                    </div>
                    <div>
                      <div className="font-medium">{a.name}</div>
                      <div className="text-xs text-muted-foreground">
                        {a.model} · {p?.name ?? "未知供应商"}
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-1">
                    <Button
                      variant="ghost" size="icon"
                      title="导出为分享文件"
                      onClick={async () => {
                        try {
                          const p = await App.ExportAgent(a.id);
                          toast.success(`已导出: ${p}`);
                        } catch (err) {
                          toast.error(`导出失败: ${errText(err)}`);
                        }
                      }}
                    >
                      <Upload className="h-4 w-4 text-muted-foreground" />
                    </Button>
                    <Button variant="outline" size="sm" onClick={() => setEditing({ ...a, skillIds: a.skillIds ?? [], mcpServerIds: a.mcpServerIds ?? [], builtinTools: a.builtinTools ?? [] })}>
                      编辑
                    </Button>
                    <Button variant="ghost" size="icon" onClick={() => remove(a)}>
                      <Trash2 className="h-4 w-4 text-muted-foreground" />
                    </Button>
                  </div>
                </div>
                {a.description && (
                  <p className="mt-2 line-clamp-2 text-sm text-muted-foreground">{a.description}</p>
                )}
                <div className="mt-2 flex flex-wrap gap-1">
                  {(a.builtinTools ?? []).map((t) => (
                    <Badge key={t} variant="outline" className="font-normal">内置:{t}</Badge>
                  ))}
                  {(a.skillIds ?? []).map((id) => {
                    const s = skills.find((x) => x.id === id);
                    return s ? <Badge key={id} variant="secondary" className="font-normal">Skill:{s.name}</Badge> : null;
                  })}
                  {(a.mcpServerIds ?? []).map((id) => {
                    const s = servers.find((x) => x.id === id);
                    return s ? <Badge key={id} variant="secondary" className="font-normal">MCP:{s.name}</Badge> : null;
                  })}
                </div>
              </CardContent>
            </Card>
          );
        })}
      </div>

      <div className="mt-6 mb-3 flex items-center justify-between">
        <div>
          <h2 className="text-base font-semibold flex items-center gap-2">
            <Network className="h-4 w-4 text-primary" /> 远程 Agent（A2A 协议）
          </h2>
          <p className="text-sm text-muted-foreground">连接远程 A2A Agent（Agent Card 自动发现），可直接对话</p>
        </div>
        <Button
          variant="outline"
          onClick={() => remoteImportRef.current?.click()}
        >
          <FolderDown className="mr-1 h-4 w-4" /> 导入
        </Button>
        <input
          ref={remoteImportRef}
          type="file"
          accept="application/json,.json"
          className="hidden"
          onChange={async (e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (!f) return;
            try {
              const imported = await App.ImportRemoteAgent(await f.text());
              toast.success(`已导入远程 Agent「${imported.name}」`);
              reload();
            } catch (err) {
              toast.error(`导入失败: ${errText(err)}`);
            }
          }}
        />
        <Button
          variant="outline"
          onClick={() => {
            setRemoteCheck(null);
            setRemoteEditing({ id: "", name: "", description: "", cardUrl: "", createdAt: "" });
          }}
        >
          <Plus className="mr-1 h-4 w-4" /> 新增远程 Agent
        </Button>
      </div>
      <div className="grid gap-3 md:grid-cols-2">
        {remotes.length === 0 && (
          <Card className="md:col-span-2">
            <CardContent className="p-6 text-center text-sm text-muted-foreground">
              还没有远程 Agent。填写对方的 A2A 服务地址即可连接（例如 http://localhost:8080）。
            </CardContent>
          </Card>
        )}
        {remotes.map((r) => (
          <Card key={r.id}>
            <CardContent className="p-4">
              <div className="flex items-start justify-between">
                <div className="flex items-center gap-2">
                  <div className="flex h-8 w-8 items-center justify-center rounded-full bg-primary/10">
                    <Network className="h-4 w-4 text-primary" />
                  </div>
                  <div>
                    <div className="font-medium">{r.name}</div>
                    <div className="max-w-[240px] truncate text-xs text-muted-foreground" title={r.cardUrl}>{r.cardUrl}</div>
                  </div>
                </div>
                <div className="flex items-center gap-1">
                  <Button
                    variant="ghost" size="icon"
                    title="导出为分享文件"
                    onClick={async () => {
                      try {
                        const p = await App.ExportRemoteAgent(r.id);
                        toast.success(`已导出: ${p}`);
                      } catch (err) {
                        toast.error(`导出失败: ${errText(err)}`);
                      }
                    }}
                  >
                    <Upload className="h-4 w-4 text-muted-foreground" />
                  </Button>
                  <Button variant="outline" size="sm" onClick={() => { setRemoteCheck(null); setRemoteEditing(r); }}>
                    编辑
                  </Button>
                  <Button variant="ghost" size="icon" onClick={() => removeRemote(r)}>
                    <Trash2 className="h-4 w-4 text-muted-foreground" />
                  </Button>
                </div>
              </div>
              {r.description && (
                <p className="mt-2 line-clamp-2 text-sm text-muted-foreground">{r.description}</p>
              )}
            </CardContent>
          </Card>
        ))}
      </div>

      <Dialog open={!!remoteEditing} onOpenChange={(v) => !v && setRemoteEditing(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{remoteEditing?.id ? "编辑远程 Agent" : "新增远程 Agent"}</DialogTitle>
          </DialogHeader>
          {remoteEditing && (
            <div className="grid gap-4 py-2">
              <div className="grid gap-1.5">
                <Label>名称</Label>
                <Input value={remoteEditing.name} onChange={(e) => setRemoteEditing({ ...remoteEditing, name: e.target.value })} placeholder="例如 远程研究助手" />
              </div>
              <div className="grid gap-1.5">
                <Label>描述</Label>
                <Input value={remoteEditing.description} onChange={(e) => setRemoteEditing({ ...remoteEditing, description: e.target.value })} placeholder="可选" />
              </div>
              <div className="grid gap-1.5">
                <Label>A2A 服务地址（Agent Card 基址）</Label>
                <Input
                  value={remoteEditing.cardUrl}
                  onChange={(e) => { setRemoteEditing({ ...remoteEditing, cardUrl: e.target.value }); setRemoteCheck(null); }}
                  placeholder="http://localhost:8080"
                />
                <p className="text-xs text-muted-foreground">从 /.well-known/agent-card.json 自动发现能力，JSON-RPC 调用。</p>
              </div>
              {remoteCheck && (
                <div className="rounded-lg border bg-muted/40 p-3 text-sm">
                  <div className="font-medium">✓ {remoteCheck.name} <span className="text-xs text-muted-foreground">v{remoteCheck.version}</span></div>
                  {remoteCheck.description && <div className="mt-1 text-xs text-muted-foreground">{remoteCheck.description}</div>}
                  <div className="mt-1 text-xs text-muted-foreground">
                    流式：{remoteCheck.streaming ? "支持" : "不支持"}
                    {(remoteCheck.skills?.length ?? 0) > 0 && ` · 技能：${remoteCheck.skills!.join("、")}`}
                  </div>
                </div>
              )}
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={checkRemote} disabled={checking}>
              {checking && <Loader2 className="mr-1 h-3 w-3 animate-spin" />} 检查连接
            </Button>
            <Button onClick={saveRemote}>保存</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!editing} onOpenChange={(v) => !v && setEditing(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{editing?.id ? "编辑 Agent" : "新增 Agent"}</DialogTitle>
          </DialogHeader>
          {editing && (
            <div className="grid gap-4 py-2">
              <div className="grid grid-cols-2 gap-3">
                <div className="grid gap-1.5">
                  <Label>名称</Label>
                  <Input value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} placeholder="例如 周报助手" />
                </div>
                <div className="grid gap-1.5">
                  <Label>描述</Label>
                  <Input value={editing.description} onChange={(e) => setEditing({ ...editing, description: e.target.value })} placeholder="一句话说明能力" />
                </div>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="grid gap-1.5">
                  <Label>供应商</Label>
                  <Select
                    value={editing.providerId || undefined}
                    onValueChange={(v) => setEditing({ ...editing, providerId: v, model: "" })}
                  >
                    <SelectTrigger><SelectValue placeholder="选择供应商" /></SelectTrigger>
                    <SelectContent>
                      {providers.map((p) => (
                        <SelectItem key={p.id} value={p.id}>{p.name}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
                <div className="grid gap-1.5">
                  <Label>模型</Label>
                  <Select
                    value={editing.model || undefined}
                    onValueChange={(v) => setEditing({ ...editing, model: v })}
                    disabled={!editing.providerId}
                  >
                    <SelectTrigger><SelectValue placeholder="选择模型" /></SelectTrigger>
                    <SelectContent>
                      {(providerOf(editing.providerId)?.models ?? []).map((m) => (
                        <SelectItem key={m.id} value={m.id}>{m.id}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              </div>
              <div className="grid gap-1.5">
                <Label>系统提示词</Label>
                <Textarea
                  rows={5}
                  value={editing.systemPrompt}
                  onChange={(e) => setEditing({ ...editing, systemPrompt: e.target.value })}
                  placeholder="你是一个……的助手，擅长……"
                />
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="grid gap-1.5">
                  <Label>Temperature</Label>
                  <Input
                    type="number" step="0.1" min="0" max="2"
                    value={editing.temperature}
                    onChange={(e) => setEditing({ ...editing, temperature: Number(e.target.value) })}
                  />
                </div>
                <div className="grid gap-1.5">
                  <Label>Max Tokens</Label>
                  <Input
                    type="number" step="256" min="256"
                    value={editing.maxTokens}
                    onChange={(e) => setEditing({ ...editing, maxTokens: Number(e.target.value) })}
                  />
                </div>
              </div>
              <div className="grid gap-1.5">
                <Label>内置工具</Label>
                <div className="flex gap-4 rounded-lg border p-3">
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={(editing.builtinTools ?? []).includes("time")}
                      onCheckedChange={() => setEditing({ ...editing, builtinTools: toggle(editing.builtinTools ?? [], "time") })}
                    />
                    时间查询
                  </label>
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={(editing.builtinTools ?? []).includes("files")}
                      onCheckedChange={() => setEditing({ ...editing, builtinTools: toggle(editing.builtinTools ?? [], "files") })}
                    />
                    工作区文件读写
                  </label>
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={(editing.builtinTools ?? []).includes("knowledge")}
                      onCheckedChange={() => setEditing({ ...editing, builtinTools: toggle(editing.builtinTools ?? [], "knowledge") })}
                    />
                    知识库检索（workspace/knowledge/）
                  </label>
                  <label className="flex items-center gap-2 text-sm">
                    <Checkbox
                      checked={(editing.builtinTools ?? []).includes("memory")}
                      onCheckedChange={() => setEditing({ ...editing, builtinTools: toggle(editing.builtinTools ?? [], "memory") })}
                    />
                    长期记忆检索（可跨会话召回用户历史）
                  </label>
                </div>
              </div>
              <div className="flex items-center justify-between rounded-lg border p-3">
                <div>
                  <div className="text-sm font-medium">安全护栏</div>
                  <div className="text-xs text-muted-foreground">
                    自动遮蔽消息中的密钥/凭据，检测提示注入并告警
                  </div>
                </div>
                <Switch
                  checked={editing.enableGuardrails !== false}
                  onCheckedChange={(v) => setEditing({ ...editing, enableGuardrails: v })}
                />
              </div>
              <div className="grid gap-1.5">
                <Label>会话 Token 预算</Label>
                <Input
                  type="number"
                  min={0}
                  value={editing.maxSessionTokens || ""}
                  onChange={(e) => setEditing({ ...editing, maxSessionTokens: Number(e.target.value) || 0 })}
                  placeholder="0（不限制）"
                />
                <p className="text-xs text-muted-foreground">会话累计 tokens 超出后拒绝新消息（当前轮正常完成）</p>
              </div>
              <div className="grid grid-cols-2 gap-3">
                <div className="grid gap-1.5">
                  <Label>单轮最长运行秒数</Label>
                  <Input
                    type="number"
                    min={0}
                    value={editing.maxTurnSeconds || ""}
                    onChange={(e) => setEditing({ ...editing, maxTurnSeconds: Number(e.target.value) || 0 })}
                    placeholder="0（不限制）"
                  />
                  <p className="text-xs text-muted-foreground">超时后本轮停止，防止模型挂起</p>
                </div>
                <div className="grid gap-1.5">
                  <Label>单轮工具调用上限</Label>
                  <Input
                    type="number"
                    min={0}
                    value={editing.maxToolCalls || ""}
                    onChange={(e) => setEditing({ ...editing, maxToolCalls: Number(e.target.value) || 0 })}
                    placeholder="0（不限制）"
                  />
                  <p className="text-xs text-muted-foreground">超出后本轮停止，防止死循环消耗</p>
                </div>
              </div>
              {(editing.builtinTools ?? []).includes("files") && (
                <div className="flex items-center justify-between rounded-lg border p-3">
                  <div>
                    <div className="text-sm font-medium">写入文件需人工确认</div>
                    <div className="text-xs text-muted-foreground">
                      模型调用文件写入前暂停等待你的批准；拒绝时模型收到拒绝结果并自行调整
                    </div>
                  </div>
                  <Switch
                    checked={(editing.confirmTools ?? []).includes("files")}
                    onCheckedChange={(v) => {
                      const next = new Set(editing.confirmTools ?? []);
                      if (v) next.add("files");
                      else next.delete("files");
                      setEditing({ ...editing, confirmTools: [...next] });
                    }}
                  />
                </div>
              )}
              {skills.length > 0 && (
                <div className="grid gap-1.5">
                  <Label>附加 Skills</Label>
                  <div className="grid gap-2 rounded-lg border p-3">
                    {skills.map((s) => (
                      <label key={s.id} className="flex items-center gap-2 text-sm">
                        <Checkbox
                          checked={(editing.skillIds ?? []).includes(s.id)}
                          onCheckedChange={() => setEditing({ ...editing, skillIds: toggle(editing.skillIds ?? [], s.id) })}
                        />
                        <span className="font-medium">{s.name}</span>
                        <span className="truncate text-muted-foreground">{s.description}</span>
                      </label>
                    ))}
                  </div>
                </div>
              )}
              {servers.length > 0 && (
                <div className="grid gap-1.5">
                  <Label>MCP 工具服务</Label>
                  <div className="grid gap-2 rounded-lg border p-3">
                    {servers.map((s) => (
                      <label key={s.id} className="flex items-center gap-2 text-sm">
                        <Checkbox
                          checked={(editing.mcpServerIds ?? []).includes(s.id)}
                          onCheckedChange={() => setEditing({ ...editing, mcpServerIds: toggle(editing.mcpServerIds ?? [], s.id) })}
                        />
                        {s.name}
                      </label>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)}>取消</Button>
            <Button onClick={save}>保存</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
