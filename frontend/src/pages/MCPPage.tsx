import { useCallback, useEffect, useState } from "react";
import { Events } from "@wailsio/runtime";
import { Plus, Trash2, Play, RefreshCw, Wrench } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import {
  Config, MCP, MCPTransport,
  type MCPServerConfig, type ServerStatus, errText } from "@/lib/api";
import { parseEnvText } from "@/lib/mcpEnv";

function blankServer(): MCPServerConfig {
  return {
    id: "",
    name: "",
    transport: MCPTransport.MCPStdio,
    command: "",
    args: [],
    env: {},
    url: "",
    headers: {},
    disabled: false,
  };
}

const stateLabels: Record<string, { label: string; variant: "secondary" | "default" | "destructive" | "outline" }> = {
  connected: { label: "已连接", variant: "default" },
  stopped: { label: "未连接", variant: "outline" },
  connecting: { label: "连接中", variant: "secondary" },
  error: { label: "错误", variant: "destructive" },
};

export function MCPPage() {
  const [servers, setServers] = useState<MCPServerConfig[]>([]);
  const [statuses, setStatuses] = useState<Record<string, ServerStatus>>({});
  const [editing, setEditing] = useState<MCPServerConfig | null>(null);
  const [argsText, setArgsText] = useState("");
  const [envText, setEnvText] = useState("");
  const [busy, setBusy] = useState<string>("");

  const reload = useCallback(() => {
    Config.GetConfig()
      .then((c) => setServers(c.mcpServers ?? []))
      .catch((e) => toast.error(`加载配置失败: ${errText(e)}`));
    MCP.Status()
      .then((ss) => {
        const map: Record<string, ServerStatus> = {};
        for (const s of ss ?? []) map[s.id] = s;
        setStatuses(map);
        // Lazy connects (a chat turn building the toolset) also raise
        // mcp:auth-pending without a page-side connect() to clear the badge —
        // clear it once that server reached a terminal state.
        setAuthPendingServer((pending) => {
          if (!pending) return null;
          const st = Object.values(map).find((x) => x.name === pending);
          return st && (st.state === "connected" || st.state === "error") ? null : pending;
        });
      })
      .catch((e) => toast.error(`加载状态失败: ${errText(e)}`));
  }, []);
  useEffect(reload, [reload]);

  // OAuth consent: warn the user while an authorization waits in the browser,
  // and mark the server card as awaiting consent.
  const [authPendingServer, setAuthPendingServer] = useState<string | null>(null);
  useEffect(() => {
    const off = Events.On("mcp:auth-pending", (e: any) => {
      const d = Array.isArray(e?.data) ? e.data[0] : e?.data;
      setAuthPendingServer(d?.server ?? null);
      toast.info(`请在浏览器中完成 MCP 授权${d?.server ? `（${d.server}）` : ""}，完成后连接将自动继续`);
    });
    return off;
  }, []);

  const save = async () => {
    if (!editing) return;
    if (!editing.name.trim()) {
      toast.error("请填写服务名称");
      return;
    }
    const s: MCPServerConfig = {
      ...editing,
      args: argsText.split("\n").map((x) => x.trim()).filter(Boolean),
      env: parseEnvText(envText),
    };
    try {
      await Config.SaveMCPServer(s);
      toast.success("已保存");
      setEditing(null);
      reload();
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
    }
  };

  const connect = async (s: MCPServerConfig) => {
    setBusy(s.id);
    try {
      const st = await MCP.Connect(s.id);
      if (st.state === "connected") {
        toast.success(`${s.name}: 发现 ${(st.tools ?? []).length} 个工具`);
      } else {
        toast.error(`${s.name}: ${st.error || st.state}`);
      }
    } catch (e) {
      toast.error(`${s.name}: ${errText(e)}`);
    } finally {
      setBusy("");
      setAuthPendingServer(null);
      reload();
    }
  };

  // 启用开关是 MCP 的主控：开启即保存并立即连接，关闭即保存（后端随
  // changed 判定自动断开缓存连接）。
  const toggleEnabled = async (s: MCPServerConfig, enabled: boolean) => {
    try {
      await Config.SaveMCPServer({ ...s, disabled: !enabled });
      if (enabled) {
        await connect(s);
      } else {
        toast.success(`${s.name} 已停用`);
        reload();
      }
    } catch (e) {
      toast.error(`保存失败: ${errText(e)}`);
      reload();
    }
  };

  const remove = async (s: MCPServerConfig) => {
    if (!window.confirm(`确定删除 MCP 服务器「${s.name}」？`)) return;
    try {
      await Config.DeleteMCPServer(s.id);
      toast.success(`已删除 ${s.name}`);
    } catch (e) {
      toast.error(`删除失败: ${errText(e)}`);
    }
    reload();
  };

  return (
    <div className="h-full overflow-y-auto p-6">
      <div className="mb-4 flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold">MCP 服务</h1>
          <p className="text-sm text-muted-foreground">
            连接 Model Context Protocol 服务器，为 Agent 提供外部工具
          </p>
        </div>
        <Button onClick={() => setEditing(blankServer())}>
          <Plus className="mr-1 h-4 w-4" /> 新增服务
        </Button>
      </div>

      <div className="grid gap-3">
        {servers.length === 0 && (
          <Card>
            <CardContent className="p-8 text-center text-sm text-muted-foreground">
              还没有 MCP 服务。例如添加一个 stdio 服务：command 填 npx，参数填
              <code className="mx-1 rounded bg-muted px-1">-y,@modelcontextprotocol/server-everything</code>
            </CardContent>
          </Card>
        )}
        {servers.map((s) => {
          const st = statuses[s.id];
          const meta = stateLabels[st?.state ?? "stopped"] ?? stateLabels.stopped;
          return (
            <Card key={s.id}>
              <CardContent className="p-4">
                <div className="flex items-start justify-between gap-4">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="font-medium">{s.name}</span>
                      {s.disabled ? (
                        <Badge variant="outline" className="font-normal">已停用</Badge>
                      ) : (
                        <Badge variant={meta.variant}>{meta.label}</Badge>
                      )}
                      <Badge variant="outline" className="font-normal">
                        {s.transport === MCPTransport.MCPStdio ? "stdio" : "streamable-http"}
                      </Badge>
                      {authPendingServer === s.name && (
                        <Badge variant="outline" className="border-amber-500/50 font-normal text-amber-600">
                          等待浏览器授权…
                        </Badge>
                      )}
                    </div>
                    <div className="mt-1 truncate text-xs text-muted-foreground">
                      {s.transport === MCPTransport.MCPStdio
                        ? `${s.command} ${(s.args ?? []).join(" ")}`
                        : s.url}
                    </div>
                    {st?.error && (
                      <div className="mt-1 line-clamp-2 text-xs text-destructive">{st.error}</div>
                    )}
                    {(st?.tools?.length ?? 0) > 0 && (
                      <div className="mt-2">
                        <div className="mb-1 flex items-center gap-1 text-xs text-muted-foreground">
                          <Wrench className="h-3 w-3" /> {(st.tools ?? []).length} 个工具
                        </div>
                        <div className="flex flex-wrap gap-1">
                          {(st.tools ?? []).map((t) => (
                            <Badge key={t.name} variant="secondary" className="font-normal" title={t.description}>
                              {t.name}
                            </Badge>
                          ))}
                        </div>
                      </div>
                    )}
                  </div>
                  <div className="flex shrink-0 items-center gap-2">
                    <label className="flex items-center gap-1.5 text-xs text-muted-foreground">
                      启用
                      <Switch checked={!s.disabled} onCheckedChange={(v) => toggleEnabled(s, v)} />
                    </label>
                    {!s.disabled && st?.state !== "connected" && (
                      <Button variant="outline" size="sm" disabled={busy === s.id} onClick={() => connect(s)}>
                        {busy === s.id
                          ? <RefreshCw className="mr-1 h-3.5 w-3.5 animate-spin" />
                          : <Play className="mr-1 h-3.5 w-3.5" />}
                        连接
                      </Button>
                    )}
                    <Button
                      variant="outline" size="sm"
                      onClick={() => {
                        setEditing({ ...s });
                        setArgsText((s.args ?? []).join("\n"));
                        setEnvText(Object.entries(s.env ?? {}).map(([k, v]) => `${k}=${v}`).join("\n"));
                      }}
                    >
                      编辑
                    </Button>
                    <Button variant="ghost" size="icon" onClick={() => remove(s)}>
                      <Trash2 className="h-4 w-4 text-muted-foreground" />
                    </Button>
                  </div>
                </div>
              </CardContent>
            </Card>
          );
        })}
      </div>

      <Dialog open={!!editing} onOpenChange={(v) => !v && setEditing(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{editing?.id ? "编辑 MCP 服务" : "新增 MCP 服务"}</DialogTitle>
          </DialogHeader>
          {editing && (
            <div className="grid gap-4 py-2">
              <div className="flex items-center justify-between rounded-lg border p-3">
                <div>
                  <div className="text-sm font-medium">启用</div>
                  <div className="text-xs text-muted-foreground">
                    开启后随应用启动自动连接，并注入所有会话
                  </div>
                </div>
                <Switch checked={!editing.disabled} onCheckedChange={(v) => setEditing({ ...editing, disabled: !v })} />
              </div>
              <div className="grid gap-1.5">
                <Label>名称</Label>
                <Input value={editing.name} onChange={(e) => setEditing({ ...editing, name: e.target.value })} placeholder="filesystem" />
              </div>
              <div className="grid gap-1.5">
                <Label>传输方式</Label>
                <Select
                  value={editing.transport}
                  onValueChange={(v) => setEditing({ ...editing, transport: v as MCPTransport })}
                >
                  <SelectTrigger><SelectValue /></SelectTrigger>
                  <SelectContent>
                    <SelectItem value={MCPTransport.MCPStdio}>stdio（本地命令）</SelectItem>
                    <SelectItem value={MCPTransport.MCPStreamableHTTP}>Streamable HTTP（远程）</SelectItem>
                  </SelectContent>
                </Select>
              </div>
              {editing.transport === MCPTransport.MCPStdio ? (
                <>
                  <div className="grid gap-1.5">
                    <Label>命令</Label>
                    <Input value={editing.command} onChange={(e) => setEditing({ ...editing, command: e.target.value })} placeholder="npx / uvx / /path/to/bin" />
                  </div>
                  <div className="grid gap-1.5">
                    <Label>参数（每行一个）</Label>
                    <Textarea rows={3} className="font-mono text-xs" value={argsText} onChange={(e) => setArgsText(e.target.value)} placeholder={"-y\n@modelcontextprotocol/server-everything"} />
                  </div>
                  <div className="grid gap-1.5">
                    <Label>环境变量（每行 KEY=VALUE）</Label>
                    <Textarea rows={2} className="font-mono text-xs" value={envText} onChange={(e) => setEnvText(e.target.value)} placeholder={"GITHUB_PAT=xxx"} />
                  </div>
                </>
              ) : (
                <>
                  <div className="grid gap-1.5">
                    <Label>服务 URL</Label>
                    <Input value={editing.url} onChange={(e) => setEditing({ ...editing, url: e.target.value })} placeholder="https://example.com/mcp" />
                  </div>
                  <div className="grid gap-1.5">
                    <Label>认证方式</Label>
                    <Select
                      value={editing.authType || "none"}
                      onValueChange={(v) => setEditing({ ...editing, authType: v === "none" ? "" : v })}
                    >
                      <SelectTrigger><SelectValue /></SelectTrigger>
                      <SelectContent>
                        <SelectItem value="none">无</SelectItem>
                        <SelectItem value="bearer">Bearer Token</SelectItem>
                        <SelectItem value="api-key">API Key（自定义头）</SelectItem>
                        <SelectItem value="oauth2">OAuth 2.1（浏览器授权 + PKCE）</SelectItem>
                      </SelectContent>
                    </Select>
                  </div>
                  {(editing.authType === "bearer" || editing.authType === "api-key") && (
                    <div className="grid grid-cols-2 gap-3">
                      <div className="grid gap-1.5">
                        <Label>{editing.authType === "bearer" ? "Token" : "Key 值"}</Label>
                        <Input value={editing.authToken} onChange={(e) => setEditing({ ...editing, authToken: e.target.value })} />
                      </div>
                      {editing.authType === "api-key" && (
                        <div className="grid gap-1.5">
                          <Label>请求头名称</Label>
                          <Input value={editing.authHeader} onChange={(e) => setEditing({ ...editing, authHeader: e.target.value })} placeholder="X-API-Key" />
                        </div>
                      )}
                    </div>
                  )}
                  {editing.authType === "oauth2" && (
                    <div className="grid grid-cols-2 gap-3">
                      <div className="grid gap-1.5">
                        <Label>Client ID（预注册）</Label>
                        <Input value={editing.oauthClientId} onChange={(e) => setEditing({ ...editing, oauthClientId: e.target.value })} placeholder="workbuddy-desktop" />
                      </div>
                      <div className="grid gap-1.5">
                        <Label>回调端口（0=自动）</Label>
                        <Input
                          value={String(editing.oauthRedirectPort || "")}
                          onChange={(e) => setEditing({ ...editing, oauthRedirectPort: Number(e.target.value) || 0 })}
                          placeholder="0"
                        />
                      </div>
                      <p className="col-span-2 text-xs text-muted-foreground">
                        连接时自动完成 OAuth 2.1 授权码 + PKCE 流程：打开系统浏览器授权，本地回环地址接收回调，令牌自动刷新。
                      </p>
                    </div>
                  )}
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
