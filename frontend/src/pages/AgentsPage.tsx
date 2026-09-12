import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2, Bot } from "lucide-react";
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
  Config, type AgentConfig, type ModelProvider, type SkillConfig, type MCPServerConfig, errText } from "@/lib/api";

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

  const reload = useCallback(() => {
    Config.GetConfig().then((c) => {
      setAgents(c.agents ?? []);
      setProviders(c.providers ?? []);
      setSkills(c.skills ?? []);
      setServers(c.mcpServers ?? []);
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
    await Config.DeleteAgent(a.id);
    toast.success(`已删除 ${a.name}`);
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
                        <SelectItem key={m} value={m}>{m}</SelectItem>
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
