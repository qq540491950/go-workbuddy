import { useCallback, useEffect, useState } from "react";
import { Plus, Trash2, PlugZap, Star, Loader2, RefreshCw, Image as ImageIcon } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import {
  Config, Protocol, type ModelProvider, type ModelInfo, errText } from "@/lib/api";

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
  const [testing, setTesting] = useState(false);
  const [fetching, setFetching] = useState(false);

  const reload = useCallback(() => {
    Config.GetConfig().then((c) => setProviders(c.providers ?? []));
  }, []);
  useEffect(reload, [reload]);

  const openEditor = (p?: ModelProvider) => {
    const item = p ? { ...p, models: (p.models ?? []).map((m) => ({ ...m })) } : blankProvider();
    setEditing(item);
  };

  /** updateModel patches one row of the model list being edited. */
  const updateModel = (index: number, patch: Partial<ModelInfo>) => {
    setEditing((prev) => prev ? {
      ...prev,
      models: (prev.models ?? []).map((m, i) => (i === index ? { ...m, ...patch } : m)),
    } : prev);
  };

  const removeModel = (index: number) => {
    setEditing((prev) => prev ? {
      ...prev,
      models: (prev.models ?? []).filter((_, i) => i !== index),
    } : prev);
  };

  const addModel = () => {
    setEditing((prev) => prev ? { ...prev, models: [...(prev.models ?? []), { id: "" }] } : prev);
  };

  const fetchModels = async () => {
    if (!editing) return;
    setFetching(true);
    try {
      const ids = await Config.ListProviderModels(editing);
      let added = 0;
      setEditing((prev) => {
        if (!prev) return prev;
        const have = new Set((prev.models ?? []).map((m) => m.id));
        const fresh = ids.filter((id) => !have.has(id)).map((id): ModelInfo => ({ id }));
        added = fresh.length;
        return { ...prev, models: [...(prev.models ?? []), ...fresh] };
      });
      toast.success(added > 0 ? `拉取成功，新增 ${added} 个模型` : `拉取成功，无新增模型（共 ${ids.length} 个）`);
    } catch (e) {
      toast.error(`拉取失败: ${errText(e)}，可手动输入`);
    } finally {
      setFetching(false);
    }
  };

  const save = async () => {
    if (!editing) return;
    if (!editing.name.trim()) {
      toast.error("请填写供应商名称");
      return;
    }
    // Trim ids and drop empty / duplicate rows.
    const models: ModelInfo[] = [];
    for (const m of editing.models ?? []) {
      const id = m.id.trim();
      if (!id || models.some((x) => x.id === id)) continue;
      models.push({ ...m, id });
    }
    try {
      await Config.SaveProvider({ ...editing, models });
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
    const model = (p.models ?? [])[0]?.id;
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
                    <Badge
                      key={m.id}
                      variant="outline"
                      className="gap-1 font-normal"
                      title={[m.multimodal ? "多模态" : "", m.contextWindow ? `上下文 ${m.contextWindow}` : ""]
                        .filter(Boolean).join(" · ")}
                    >
                      {m.multimodal && <ImageIcon className="h-3 w-3 text-muted-foreground" />}
                      {m.id}
                    </Badge>
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
        <DialogContent className="sm:max-w-2xl">
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
                        ? "https://api.minimaxi.com/anthropic（留空使用 Anthropic 官方）"
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

              <div className="grid gap-2">
                <div className="flex items-center justify-between">
                  <Label>模型列表</Label>
                  <div className="flex gap-2">
                    <Button variant="outline" size="sm" disabled={fetching} onClick={fetchModels}>
                      {fetching
                        ? <Loader2 className="mr-1 h-3.5 w-3.5 animate-spin" />
                        : <RefreshCw className="mr-1 h-3.5 w-3.5" />}
                      拉取模型
                    </Button>
                    <Button variant="outline" size="sm" onClick={addModel}>
                      <Plus className="mr-1 h-3.5 w-3.5" /> 添加
                    </Button>
                  </div>
                </div>
                {(editing.models ?? []).length === 0 ? (
                  <p className="rounded-lg border border-dashed p-3 text-center text-xs text-muted-foreground">
                    暂无模型。点击「拉取模型」从供应商接口获取，或「添加」手动输入。
                  </p>
                ) : (
                  <div className="grid gap-1.5">
                    <div className="grid grid-cols-[minmax(0,1fr)_64px_104px_84px_84px_28px] items-center gap-2 px-0.5 text-[11px] text-muted-foreground">
                      <span>模型 ID</span>
                      <span className="text-center">多模态</span>
                      <span>上下文 (tokens)</span>
                      <span>¥/1M 入</span>
                      <span>¥/1M 出</span>
                      <span />
                    </div>
                    {(editing.models ?? []).map((m, i) => (
                      <div key={i} className="grid grid-cols-[minmax(0,1fr)_64px_104px_84px_84px_28px] items-center gap-2">
                        <Input
                          value={m.id}
                          onChange={(e) => updateModel(i, { id: e.target.value })}
                          placeholder="model-id"
                          className="h-8"
                        />
                        <Switch
                          checked={!!m.multimodal}
                          onCheckedChange={(v) => updateModel(i, { multimodal: v })}
                          className="justify-self-center"
                          title="支持图片输入"
                        />
                        <Input
                          type="number" min="0"
                          value={m.contextWindow ?? ""}
                          onChange={(e) => updateModel(i, { contextWindow: Number(e.target.value) || 0 })}
                          placeholder="如 128000"
                          className="h-8"
                        />
                        <Input
                          type="number" step="0.1" min="0"
                          value={m.priceIn ?? ""}
                          onChange={(e) => updateModel(i, { priceIn: Number(e.target.value) || 0 })}
                          placeholder="可选"
                          className="h-8"
                        />
                        <Input
                          type="number" step="0.1" min="0"
                          value={m.priceOut ?? ""}
                          onChange={(e) => updateModel(i, { priceOut: Number(e.target.value) || 0 })}
                          placeholder="可选"
                          className="h-8"
                        />
                        <Button
                          variant="ghost" size="icon" className="h-8 w-7"
                          onClick={() => removeModel(i)}
                        >
                          <Trash2 className="h-3.5 w-3.5 text-muted-foreground" />
                        </Button>
                      </div>
                    ))}
                  </div>
                )}
                <p className="text-xs text-muted-foreground">
                  填写价格后，对话中会显示每次回复与累计的成本估算；「多模态」决定聊天页是否允许发送图片。
                </p>
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
