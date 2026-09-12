import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2, Sparkles } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import { Config, type SkillConfig, errText } from "@/lib/api";

function blankSkill(): SkillConfig {
  return { id: "", name: "", description: "", content: "", createdAt: "" };
}

export function SkillsPage() {
  const [skills, setSkills] = useState<SkillConfig[]>([]);
  const [editing, setEditing] = useState<SkillConfig | null>(null);

  const reload = useCallback(() => {
    Config.GetConfig().then((c) => setSkills(c.skills ?? []));
  }, []);
  useEffect(reload, [reload]);

  const save = async () => {
    if (!editing) return;
    if (!editing.name.trim()) {
      toast.error("请填写 Skill 名称（建议英文短横线命名，如 weekly-report）");
      return;
    }
    try {
      await Config.SaveSkill(editing);
      toast.success("已保存");
      setEditing(null);
      reload();
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
    }
  };

  const remove = async (s: SkillConfig) => {
    await Config.DeleteSkill(s.id);
    toast.success(`已删除 ${s.name}`);
    reload();
  };

  return (
    <div className="h-full overflow-y-auto p-6">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">Skills</h1>
          <p className="text-sm text-muted-foreground">
            可复用的技能指令包（SKILL.md 规范），可附加到任意 Agent
          </p>
        </div>
        <Button onClick={() => setEditing(blankSkill())}>
          <Plus className="mr-1 h-4 w-4" /> 新增 Skill
        </Button>
      </div>

      <div className="grid gap-3 md:grid-cols-2">
        {skills.map((s) => (
          <Card key={s.id}>
            <CardContent className="p-4">
              <div className="flex items-start justify-between">
                <div className="flex items-center gap-2">
                  <div className="flex h-8 w-8 items-center justify-center rounded-full bg-primary/10">
                    <Sparkles className="h-4 w-4 text-primary" />
                  </div>
                  <div>
                    <div className="font-medium">{s.name}</div>
                    <div className="text-xs text-muted-foreground">{s.description}</div>
                  </div>
                </div>
                <div className="flex items-center gap-1">
                  <Button variant="outline" size="sm" onClick={() => setEditing({ ...s })}>编辑</Button>
                  <Button variant="ghost" size="icon" onClick={() => remove(s)}>
                    <Trash2 className="h-4 w-4 text-muted-foreground" />
                  </Button>
                </div>
              </div>
              <pre className="mt-2 max-h-24 overflow-hidden whitespace-pre-wrap text-xs text-muted-foreground">
                {s.content.length > 200 ? s.content.slice(0, 200) + "…" : s.content}
              </pre>
            </CardContent>
          </Card>
        ))}
      </div>

      <Dialog open={!!editing} onOpenChange={(v) => !v && setEditing(null)}>
        <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>{editing?.id ? "编辑 Skill" : "新增 Skill"}</DialogTitle>
          </DialogHeader>
          {editing && (
            <div className="grid gap-4 py-2">
              <div className="grid grid-cols-2 gap-3">
                <div className="grid gap-1.5">
                  <Label>名称（英文标识）</Label>
                  <Input
                    value={editing.name}
                    onChange={(e) => setEditing({ ...editing, name: e.target.value })}
                    placeholder="weekly-report"
                  />
                </div>
                <div className="grid gap-1.5">
                  <Label>描述</Label>
                  <Input
                    value={editing.description}
                    onChange={(e) => setEditing({ ...editing, description: e.target.value })}
                    placeholder="何时使用该技能"
                  />
                </div>
              </div>
              <div className="grid gap-1.5">
                <Label>技能内容（Markdown）</Label>
                <Textarea
                  rows={12}
                  className="font-mono text-xs"
                  value={editing.content}
                  onChange={(e) => setEditing({ ...editing, content: e.target.value })}
                  placeholder="# 技能说明&#10;&#10;按照以下步骤……"
                />
              </div>
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
