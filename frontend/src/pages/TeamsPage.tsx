import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2, Users } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
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
  Config, type TeamConfig, type AgentConfig, type ModelProvider, errText } from "@/lib/api";

/** TeamTopology renders a lead→members hierarchy for a team. */
function TeamTopology({ team, agentName }: { team: TeamConfig; agentName: (id: string) => string }) {
  const isAuto = team.autoCoordinate;
  const members = (team.memberAgentIds ?? []).filter((id) => isAuto || id !== team.leadAgentId);
  return (
    <div className="rounded-lg border bg-muted/20 p-3">
      <div className="flex flex-col items-center gap-2">
        <div className="rounded-lg border border-primary/40 bg-primary/10 px-3 py-1.5 text-center">
          <div className="text-xs font-semibold text-primary">
            {isAuto ? "🧭 自动协调员" : `👑 ${agentName(team.leadAgentId)}`}
          </div>
          <div className="text-[10px] text-muted-foreground">
            {isAuto ? team.model || "分派任务 · 汇总结果" : "调度成员 · 汇总结果"}
          </div>
        </div>
        {members.length > 0 && (
          <>
            <div className="h-3 w-px bg-border" />
            <div className="flex flex-wrap items-start justify-center gap-x-4 gap-y-2">
              {members.map((id) => {
                const isLead = !isAuto && id === team.leadAgentId;
                return (
                  <div key={id} className="flex flex-col items-center">
                    <div className="h-2 w-px bg-border" />
                    <div className="rounded-lg border bg-background px-2.5 py-1.5 text-center">
                      <div className="text-xs font-medium">
                        {isLead && "👑 "}{agentName(id)}
                      </div>
                      <div className="text-[10px] text-muted-foreground">
                        {isLead ? "负责人" : "成员"}
                      </div>
                    </div>
                  </div>
                );
              })}
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function blankTeam(): TeamConfig {
  return {
    id: "",
    name: "",
    description: "",
    leadAgentId: "",
    autoCoordinate: true,
    memberAgentIds: [],
    providerId: "",
    model: "",
    systemPrompt: "",
    createdAt: "",
  };
}

export function TeamsPage() {
  const [teams, setTeams] = useState<TeamConfig[]>([]);
  const [agents, setAgents] = useState<AgentConfig[]>([]);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [editing, setEditing] = useState<TeamConfig | null>(null);

  const reload = useCallback(() => {
    Config.GetConfig().then((c) => {
      setTeams(c.teams ?? []);
      setAgents(c.agents ?? []);
      setProviders(c.providers ?? []);
    });
  }, []);
  useEffect(reload, [reload]);

  const providerOf = (id: string) => providers.find((p) => p.id === id);
  const agentName = (id: string) => agents.find((a) => a.id === id)?.name ?? "?";

  const save = async () => {
    if (!editing) return;
    if (!editing.name.trim()) {
      toast.error("请填写团队名称");
      return;
    }
    if ((editing.memberAgentIds ?? []).length === 0) {
      toast.error("请至少选择一名成员 Agent");
      return;
    }
    try {
      await Config.SaveTeam(editing);
      toast.success("已保存");
      setEditing(null);
      reload();
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
    }
  };

  const remove = async (t: TeamConfig) => {
    await Config.DeleteTeam(t.id);
    toast.success(`已删除 ${t.name}`);
    reload();
  };

  const toggleMember = (id: string) => {
    if (!editing) return;
    const list = editing.memberAgentIds ?? [];
    setEditing({
      ...editing,
      memberAgentIds: list.includes(id) ? list.filter((x) => x !== id) : [...list, id],
    });
  };

  return (
    <div className="h-full overflow-y-auto p-6">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">Agent 团队</h1>
          <p className="text-sm text-muted-foreground">将多个 Agent 组队协作：负责人调度成员，共同完成任务</p>
        </div>
        <Button onClick={() => setEditing(blankTeam())} disabled={agents.length < 1}>
          <Plus className="mr-1 h-4 w-4" /> 新增团队
        </Button>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        {teams.length === 0 && (
          <Card className="md:col-span-2">
            <CardContent className="p-8 text-center text-sm text-muted-foreground">
              还没有团队。创建团队前请先在「Agents」页创建成员 Agent。
            </CardContent>
          </Card>
        )}
        {teams.map((t) => (
          <Card key={t.id}>
            <CardContent className="p-4">
              <div className="flex items-start justify-between">
                <div className="flex items-center gap-2">
                  <div className="flex h-8 w-8 items-center justify-center rounded-full bg-primary/10">
                    <Users className="h-4 w-4 text-primary" />
                  </div>
                  <div>
                    <div className="font-medium">{t.name}</div>
                    <div className="text-xs text-muted-foreground">
                      {t.autoCoordinate
                        ? `自动协调员 · ${t.model || "?"}`
                        : `负责人: ${agentName(t.leadAgentId)}`}
                    </div>
                  </div>
                </div>
                <div className="flex items-center gap-1">
                  <Button
                    variant="outline" size="sm"
                    onClick={() => setEditing({ ...t, memberAgentIds: t.memberAgentIds ?? [] })}
                  >
                    编辑
                  </Button>
                  <Button variant="ghost" size="icon" onClick={() => remove(t)}>
                    <Trash2 className="h-4 w-4 text-muted-foreground" />
                  </Button>
                </div>
              </div>
              {t.description && (
                <p className="mt-2 line-clamp-2 text-sm text-muted-foreground">{t.description}</p>
              )}
              <div className="mt-3">
                <TeamTopology team={t} agentName={agentName} />
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      <Dialog open={!!editing} onOpenChange={(v) => !v && setEditing(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{editing?.id ? "编辑团队" : "新增团队"}</DialogTitle>
          </DialogHeader>
          {editing && (
            <div className="grid gap-4 py-2">
              <div className="grid gap-1.5">
                <Label>团队名称</Label>
                <Input value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} placeholder="例如 内容创作团队" />
              </div>
              <div className="grid gap-1.5">
                <Label>描述</Label>
                <Input value={editing.description} onChange={(e) => setEditing({ ...editing, description: e.target.value })} />
              </div>
              <div className="grid gap-1.5">
                <Label>成员 Agent（可多选）</Label>
                <div className="grid gap-2 rounded-lg border p-3">
                  {agents.map((a) => (
                    <label key={a.id} className="flex items-center gap-2 text-sm">
                      <Checkbox
                        checked={(editing.memberAgentIds ?? []).includes(a.id)}
                        onCheckedChange={() => toggleMember(a.id)}
                      />
                      <span className="font-medium">{a.name}</span>
                      <span className="truncate text-muted-foreground">{a.description}</span>
                    </label>
                  ))}
                  {agents.length === 0 && (
                    <div className="text-sm text-muted-foreground">请先创建 Agent</div>
                  )}
                </div>
              </div>
              <div className="flex items-center justify-between rounded-lg border p-3">
                <div>
                  <div className="text-sm font-medium">自动协调员模式</div>
                  <div className="text-xs text-muted-foreground">
                    开启后由系统合成一位协调员负责分派任务；关闭则指定某成员担任负责人
                  </div>
                </div>
                <Switch
                  checked={editing.autoCoordinate}
                  onCheckedChange={(v) => setEditing({ ...editing, autoCoordinate: v })}
                />
              </div>
              {!editing.autoCoordinate && (
                <div className="grid gap-1.5">
                  <Label>负责人</Label>
                  <Select
                    value={editing.leadAgentId || undefined}
                    onValueChange={(v) => setEditing({ ...editing, leadAgentId: v })}
                  >
                    <SelectTrigger><SelectValue placeholder="选择负责人" /></SelectTrigger>
                    <SelectContent>
                      {(editing.memberAgentIds ?? []).map((id) => (
                        <SelectItem key={id} value={id}>{agentName(id)}</SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </div>
              )}
              {editing.autoCoordinate && (
                <>
                  <div className="grid grid-cols-2 gap-3">
                    <div className="grid gap-1.5">
                      <Label>协调员供应商</Label>
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
                      <Label>协调员模型</Label>
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
                    <Label>协调员指令</Label>
                    <Textarea
                      rows={3}
                      value={editing.systemPrompt}
                      onChange={(e) => setEditing({ ...editing, systemPrompt: e.target.value })}
                      placeholder="留空使用默认协调策略"
                    />
                  </div>
                </>
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
