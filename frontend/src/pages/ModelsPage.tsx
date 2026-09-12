import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2, PlugZap, Star, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { Switch } from "@/components/ui/switch";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import {
  Config, Protocol, type ModelProvider, errText } from "@/lib/api";

const protocolLabels: Record<string, string> = {
  [Protocol.ProtocolOpenAI]: "OpenAI 兼容 (DeepSeek/Kimi/Ollama...)",
  [Protocol.ProtocolAnthropic]: "Anthropic (Claude)",
  [Protocol.ProtocolGemini]: "Google Gemini",
};

function blankProvider(): ModelProvider {
  return {
    id: "",
    name: "",
    protocol: Protocol.ProtocolOpenAI,
    baseUrl: "",
    apiKey: "",
    models: [],
    isDefault: false,
  };
}

export function ModelsPage() {
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [editing, setEditing] = useState<ModelProvider | null>(null);
  const [modelsText, setModelsText] = useState("");
  const [testing, setTesting] = useState(false);

  const reload = useCallback(() => {
    Config.GetConfig().then((c) => setProviders(c.providers ?? []));
  }, []);
  useEffect(reload, [reload]);

  const openEditor = (p?: ModelProvider) => {
    const item = p ? { ...p } : blankProvider();
    setEditing(item);
    setModelsText((item.models ?? []).join("\n"));
  };

  const save = async () => {
    if (!editing) return;
    if (!editing.name.trim()) {
      toast.error("请填写供应商名称");
      return;
    }
    const p: ModelProvider = {
      ...editing,
      models: modelsText.split("\n").map((s) => s.trim()).filter(Boolean),
    };
    try {
      await Config.SaveProvider(p);
      toast.success("已保存");
      setEditing(null);
      reload();
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
    }
  };

  const remove = async (p: ModelProvider) => {
    await Config.DeleteProvider(p.id);
    toast.success(`已删除 ${p.name}`);
    reload();
  };

  const test = async (p: ModelProvider) => {
    const model = (p.models ?? [])[0];
    if (!model) {
      toast.error("请先在供应商里配置至少一个模型 ID");
      return;
    }
    setTesting(true);
    try {
      await Config.TestProvider(p, model);
      toast.success(`连接成功 (${model})`);
    } catch (e) {
      toast.error(`连接失败: ${errText(e)}`);
    } finally {
      setTesting(false);
    }
  };

  return (
    <div className="h-full overflow-y-auto p-6">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">模型配置</h1>
          <p className="text-sm text-muted-foreground">配置 LLM 供应商与模型，供 Agent 使用</p>
        </div>
        <Button onClick={() => openEditor()}>
          <Plus className="mr-1 h-4 w-4" /> 新增供应商
        </Button>
      </div>

      <div className="grid gap-3">
        {providers.length === 0 && (
          <Card>
            <CardContent className="p-8 text-center text-sm text-muted-foreground">
              还没有配置模型供应商。点击右上角「新增供应商」，配置 OpenAI 兼容 / Anthropic / Gemini 接口。
            </CardContent>
          </Card>
        )}
        {providers.map((p) => (
          <Card key={p.id}>
            <CardContent className="flex items-center justify-between p-4">
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{p.name}</span>
                  {p.isDefault && (
                    <Badge variant="secondary" className="gap-1">
                      <Star className="h-3 w-3" /> 默认
                    </Badge>
                  )}
                </div>
                <div className="mt-1 truncate text-xs text-muted-foreground">
                  {protocolLabels[p.protocol] ?? p.protocol}
                  {p.baseUrl && ` · ${p.baseUrl}`}
                </div>
                <div className="mt-1.5 flex flex-wrap gap-1">
                  {(p.models ?? []).map((m) => (
                    <Badge key={m} variant="outline" className="font-normal">{m}</Badge>
                  ))}
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-2">
                <Button variant="outline" size="sm" disabled={testing} onClick={() => test(p)}>
                  {testing ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" /> : <PlugZap className="mr-1 h-3.5 w-3.5" />}
                  测试
                </Button>
                <Button variant="outline" size="sm" onClick={() => openEditor(p)}>编辑</Button>
                <Button variant="ghost" size="icon" onClick={() => remove(p)}>
                  <Trash2 className="h-4 w-4 text-muted-foreground" />
                </Button>
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      <Dialog open={!!editing} onOpenChange={(v) => !v && setEditing(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing?.id ? "编辑供应商" : "新增供应商"}</DialogTitle>
          </DialogHeader>
          {editing && (
            <div className="grid gap-4 py-2">
              <div className="grid gap-1.5">
                <Label>名称</Label>
                <Input
                  value={editing.name}
                  onChange={(e) => setEditing({ ...editing, name: e.target.value })}
                  placeholder="例如 DeepSeek、OpenAI、本地 Ollama"
                />
              </div>
              <div className="grid gap-1.5">
                <Label>协议</Label>
                <Select
                  value={editing.protocol}
                  onValueChange={(v) => setEditing({ ...editing, protocol: v as Protocol })}
                >
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    {Object.entries(protocolLabels).map(([v, label]) => (
                      <SelectItem key={v} value={v}>{label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="grid gap-1.5">
                <Label>Base URL</Label>
                <Input
                  value={editing.baseUrl}
                  onChange={(e) => setEditing({ ...editing, baseUrl: e.target.value })}
                  placeholder={
                    editing.protocol === Protocol.ProtocolOpenAI
                      ? "https://api.deepseek.com/v1（留空使用 OpenAI 官方）"
                      : editing.protocol === Protocol.ProtocolAnthropic
                        ? "https://api.anthropic.com（留空使用官方）"
                        : "留空使用 Google 官方"
                  }
                />
              </div>
              <div className="grid gap-1.5">
                <Label>API Key</Label>
                <Input
                  type="password"
                  value={editing.apiKey}
                  onChange={(e) => setEditing({ ...editing, apiKey: e.target.value })}
                  placeholder="sk-..."
                />
              </div>
              <div className="grid gap-1.5">
                <Label>模型列表（每行一个）</Label>
                <Textarea
                  rows={4}
                  value={modelsText}
                  onChange={(e) => setModelsText(e.target.value)}
                  placeholder={"deepseek-chat\ndeepseek-reasoner"}
                />
              </div>
              <div className="flex items-center justify-between rounded-lg border p-3">
                <Label>设为默认供应商</Label>
                <Switch
                  checked={editing.isDefault}
                  onCheckedChange={(v) => setEditing({ ...editing, isDefault: v })}
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
