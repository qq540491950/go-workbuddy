import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import {
  Plus, Send, Square, Trash2, Pencil, MessageSquare, Bot, Users, Loader2,
  Copy, Check, Search, ArrowRight, RefreshCw, Download, Mic, MicOff, Volume2, Square as SquareStop,
  Paperclip, X, BarChart3, Brain,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import { ScrollArea } from "@/components/ui/scroll-area";
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { MessageMarkdown, CopyButton } from "@/components/MessageMarkdown";
import { ToolCallCard } from "@/components/ToolCallCard";
import {
  Chat, Config, errText,
  type AgentConfig, type ChatSession, type TeamConfig, type UsageInfo, type ModelProvider,
  type AttachmentIn, type AttachmentOut, type SessionStats,
} from "@/lib/api";

type UIMessage = {
  id: string;
  kind: "user" | "assistant" | "tool_call" | "tool_result" | "error";
  author: string;
  text: string;
  toolName?: string;
  toolArgs?: { [k: string]: any } | null;
  toolResp?: { [k: string]: any } | null;
  streaming?: boolean;
  usage?: UsageInfo | null;
  attachments?: AttachmentOut[] | null;
};

let mid = 0;
const nextId = () => `m${++mid}`;

// RENDER_LIMIT caps how many messages render at once for smooth long sessions.
const RENDER_LIMIT = 200;

/** mdToSpeech converts markdown to a speakable plain string. */
function mdToSpeech(md: string): string {
  return md
    .replace(/```[\s\S]*?```/g, "（代码块已省略）")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/!\[[^\]]*\]\([^)]*\)/g, "")
    .replace(/\[([^\]]+)\]\([^)]*\)/g, "$1")
    .replace(/^#{1,6}\s+/gm, "")
    .replace(/[*_~>|]/g, "")
    .replace(/\s+\n/g, "\n")
    .trim();
}

/** speechSupported reports whether speech recognition is available. */
function speechSupported(): boolean {
  const w = window as any;
  return !!(w.SpeechRecognition || w.webkitSpeechRecognition);
}

/** speak synthesizes speech for text, cancelling previous playback. */
function speak(text: string) {
  if (!("speechSynthesis" in window)) return;
  window.speechSynthesis.cancel();
  const u = new SpeechSynthesisUtterance(mdToSpeech(text));
  u.lang = "zh-CN";
  u.rate = 1.05;
  window.speechSynthesis.speak(u);
}

export function ChatPage() {
  const [sessions, setSessions] = useState<ChatSession[]>([]);
  const [activeID, setActiveID] = useState("");
  const [messages, setMessages] = useState<UIMessage[]>([]);
  const [input, setInput] = useState("");
  const [running, setRunning] = useState(false);
  const [agents, setAgents] = useState<AgentConfig[]>([]);
  const [teams, setTeams] = useState<TeamConfig[]>([]);
  const [providerCount, setProviderCount] = useState(0);
  const [providerList, setProviderList] = useState<ModelProvider[]>([]);
  const [search, setSearch] = useState("");
  const [newChatOpen, setNewChatOpen] = useState(false);
  const [newType, setNewType] = useState<"agent" | "team">("agent");
  const [newTarget, setNewTarget] = useState("");
  const [renaming, setRenaming] = useState<ChatSession | null>(null);
  const [renameText, setRenameText] = useState("");
  const [sessionUsage, setSessionUsage] = useState<UsageInfo | null>(null);
  const [statsOpen, setStatsOpen] = useState(false);
  const [stats, setStats] = useState<SessionStats | null>(null);
  const [autoSpeak, setAutoSpeak] = useState(false);
  const [listening, setListening] = useState(false);
  const [editing, setEditing] = useState(false);
  const [pendingAtts, setPendingAtts] = useState<AttachmentIn[]>([]);
  const fileRef = useRef<HTMLInputElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const messagesRef = useRef<UIMessage[]>([]);
  useEffect(() => { messagesRef.current = messages; }, [messages]);

  const active = sessions.find((s) => s.id === activeID);

  const reloadSessions = useCallback(() => {
    Chat.ListSessions()
      .then((ss) => setSessions(ss ?? []))
      .catch((e) => toast.error(`加载会话失败: ${errText(e)}`));
  }, []);
  useEffect(() => {
    reloadSessions();
    Config.GetConfig()
      .then((c) => {
        setAgents(c.agents ?? []);
        setTeams(c.teams ?? []);
        setProviderCount((c.providers ?? []).length);
        setProviderList(c.providers ?? []);
        setAutoSpeak(!!c.settings?.autoSpeak);
      })
      .catch((e) => toast.error(`加载配置失败: ${errText(e)}`));
  }, [reloadSessions]);

  // Open the newest session on first load.
  useEffect(() => {
    if (!activeID && sessions.length > 0) {
      setActiveID(sessions[0].id);
    }
  }, [sessions, activeID]);

  // Load history when switching sessions.
  useEffect(() => {
    if (!activeID) return;
    setSessionUsage(null);
    Chat.GetSessionMessages(activeID)
      .then((ms) => {
        setMessages(
          (ms ?? []).map((m) => ({
            id: m.id,
            kind: (m.kind === "user" ? "user" : m.kind === "tool_call" ? "tool_call" : m.kind === "tool_result" ? "tool_result" : "assistant") as UIMessage["kind"],
            author: m.author,
            text: m.text,
            toolName: m.toolName,
            toolArgs: m.toolArgs,
            toolResp: m.toolResp,
            attachments: m.attachments ?? null,
          })),
        );
      })
      .catch((e) => toast.error(`加载历史失败: ${errText(e)}`));
    Chat.IsRunning(activeID).then(setRunning).catch(() => {});
  }, [activeID]);

  // Stream events.
  useEffect(() => {
    const off = Events.On("chat:stream", (e: any) => {
      const ev = Array.isArray(e?.data) ? e.data[0] : e?.data;
      if (!ev || ev.sessionId !== activeID) return;
      setMessages((prev) => {
        switch (ev.kind) {
          case "user":
            return [...prev, { id: nextId(), kind: "user", author: "user", text: ev.text }];
          case "delta": {
            const last = prev[prev.length - 1];
            if (last && last.streaming && last.author === ev.author) {
              const copy = [...prev];
              copy[copy.length - 1] = { ...last, text: last.text + ev.text };
              return copy;
            }
            return [...prev, { id: nextId(), kind: "assistant", author: ev.author, text: ev.text, streaming: true }];
          }
          case "message": {
            const idx = [...prev].reverse().findIndex((m) => m.streaming && m.author === ev.author);
            if (idx >= 0) {
              const real = prev.length - 1 - idx;
              const copy = [...prev];
              copy[real] = { ...copy[real], text: ev.text, streaming: false, usage: ev.usage ?? null };
              return copy;
            }
            return [...prev, { id: nextId(), kind: "assistant", author: ev.author, text: ev.text, usage: ev.usage ?? null }];
          }
          case "tool_call":
            return [...prev, { id: nextId(), kind: "tool_call", author: ev.author, text: "", toolName: ev.toolName, toolArgs: ev.toolArgs }];
          case "tool_result":
            return [...prev, { id: nextId(), kind: "tool_result", author: ev.author, text: "", toolName: ev.toolName, toolResp: ev.toolResp }];
          case "error":
            return [...prev, { id: nextId(), kind: "error", author: "system", text: ev.text }];
          case "done":
            return prev.map((m) => (m.streaming ? { ...m, streaming: false } : m));
          default:
            return prev;
        }
      });
      if (ev.kind === "done") {
        setSessionUsage(ev.sessionUsage ?? null);
        if (autoSpeak) {
          const lastAssistant = [...messagesRef.current].reverse().find((m) => m.kind === "assistant" && m.text);
          if (lastAssistant) speak(lastAssistant.text);
        }
      }
      if (ev.kind === "done" || ev.kind === "error") {
        setRunning(false);
        reloadSessions();
      }
    });
    return off;
  }, [activeID, reloadSessions]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  // Cmd/Ctrl+N opens the new-chat dialog; Cmd/Ctrl+K focuses the composer.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const mod = e.metaKey || e.ctrlKey;
      if (mod && (e.key === "n" || e.key === "N")) {
        e.preventDefault();
        setNewChatOpen(true);
      } else if (mod && (e.key === "k" || e.key === "K")) {
        e.preventDefault();
        inputRef.current?.focus();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  const send = async () => {
    const text = input.trim();
    if (!text || !activeID || running) return;
    setInput("");
    setRunning(true);
    const wasEditing = editing;
    setEditing(false);
    const atts = pendingAtts;
    setPendingAtts([]);
    if (wasEditing) {
      setMessages((prev) => {
        const out = [...prev];
        while (out.length > 0 && (out[out.length - 1].kind === "assistant" || out[out.length - 1].kind === "tool_call" || out[out.length - 1].kind === "tool_result")) {
          out.pop();
        }
        return out;
      });
    }
    try {
      if (wasEditing) {
        await Chat.EditAndResend(activeID, text);
      } else {
        await Chat.Send(activeID, text, atts.length > 0 ? atts : null);
      }
    } catch (e) {
      toast.error(`发送失败: ${errText(e)}`);
      setRunning(false);
      setPendingAtts(atts); // restore on failure
    }
  };

  const cancel = () => {
    if (activeID) Chat.Cancel(activeID);
  };

  const MAX_ATT_BYTES = 6 * 1024 * 1024;
  const allowedMIME = ["image/png", "image/jpeg", "image/webp", "image/gif"];

  const addFiles = async (files: FileList | File[]) => {
    for (const f of Array.from(files)) {
      if (!allowedMIME.includes(f.type)) {
        toast.error(`不支持的类型 ${f.type || f.name}（仅图片）`);
        continue;
      }
      if (f.size > MAX_ATT_BYTES) {
        toast.error(`${f.name} 超过 6MB`);
        continue;
      }
      const buf = await f.arrayBuffer();
      let binary = "";
      const bytes = new Uint8Array(buf);
      const chunk = 0x8000;
      for (let i = 0; i < bytes.length; i += chunk) {
        binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
      }
      setPendingAtts((prev) =>
        prev.length >= 4 ? prev : [...prev, { name: f.name, mime: f.type, data: btoa(binary) }],
      );
    }
  };

  const toggleVoiceInput = () => {
    if (listening) {
      (window as any).__wbRecognition?.stop();
      return;
    }
    const w = window as any;
    const SR = w.SpeechRecognition || w.webkitSpeechRecognition;
    if (!SR) {
      toast.error("当前环境不支持语音识别");
      return;
    }
    const rec = new SR();
    rec.lang = "zh-CN";
    rec.interimResults = true;
    rec.continuous = false;
    const base = input;
    rec.onresult = (e: any) => {
      let text = "";
      for (let i = e.resultIndex; i < e.results.length; i++) {
        text += e.results[i][0].transcript;
      }
      setInput((base + " " + text).trim());
    };
    rec.onend = () => setListening(false);
    rec.onerror = () => setListening(false);
    w.__wbRecognition = rec;
    setListening(true);
    rec.start();
  };

  const regenerate = async () => {
    if (!activeID || running) return;
    setRunning(true);
    setSessionUsage(null);
    setMessages((prev) => {
      const out = [...prev];
      while (out.length > 0 && (out[out.length - 1].kind === "assistant" || out[out.length - 1].kind === "tool_call" || out[out.length - 1].kind === "tool_result")) {
        out.pop();
      }
      return out;
    });
    try {
      await Chat.Regenerate(activeID);
    } catch (e) {
      toast.error(`重新生成失败: ${errText(e)}`);
      setRunning(false);
    }
  };

  const openStats = async () => {
    if (!activeID) return;
    try {
      setStats(await Chat.SessionStats(activeID));
      setStatsOpen(true);
    } catch (e) {
      toast.error(`加载统计失败: ${errText(e)}`);
    }
  };

  const exportSession = async () => {
    if (!activeID) return;
    try {
      const path = await Chat.ExportSession(activeID);
      toast.success(`已导出: ${path}`);
    } catch (e) {
      toast.error(`导出失败: ${errText(e)}`);
    }
  };

  const createSession = async () => {
    if (!newTarget) {
      toast.error("请选择目标");
      return;
    }
    try {
      const s = await Chat.NewSession(newType, newTarget, "");
      setNewChatOpen(false);
      setNewTarget("");
      reloadSessions();
      setActiveID(s.id);
      setMessages([]);
    } catch (e) {
      toast.error(`创建失败: ${errText(e)}`);
    }
  };

  const removeSession = async (s: ChatSession) => {
    await Chat.DeleteSession(s.id).catch((e) => toast.error(`删除失败: ${errText(e)}`));
    if (s.id === activeID) {
      setActiveID("");
      setMessages([]);
    }
    reloadSessions();
  };

  const filteredSessions = useMemo(() => {
    const q = search.trim().toLowerCase();
    if (!q) return sessions;
    return sessions.filter((s) => s.title.toLowerCase().includes(q));
  }, [sessions, search]);

  // Provider pricing for cost estimation (agent → provider; team coordinator).
  const provider = useMemo(() => {
    if (!active) return null;
    let providerId = "";
    if (active.targetType === "team") {
      const team = teams.find((t) => t.id === active.targetId);
      if (team?.autoCoordinate) providerId = team.providerId;
      else {
        const lead = agents.find((a) => a.id === team?.leadAgentId);
        providerId = lead?.providerId ?? "";
      }
    } else {
      providerId = agents.find((a) => a.id === active.targetId)?.providerId ?? "";
    }
    return providerList.find((p) => p.id === providerId) ?? null;
  }, [active, agents, teams, providerList]);

  /** costOf computes ¥ cost from token usage at provider rates (per 1M). */
  const costOf = (u?: UsageInfo | null): string => {
    if (!u || !provider) return "";
    const pin = (provider as any).priceIn ?? 0;
    const pout = (provider as any).priceOut ?? 0;
    if (pin <= 0 && pout <= 0) return "";
    const cost = (u.promptTokens / 1e6) * pin + (u.completionTokens / 1e6) * pout;
    if (cost <= 0) return "";
    return cost < 0.01 ? "<¥0.01" : `¥${cost.toFixed(2)}`;
  };

  const startEdit = (text: string) => {
    if (running || !activeID) return;
    setInput(text);
    setEditing(true);
    inputRef.current?.focus();
  };

  const cancelEdit = () => {
    setEditing(false);
    setInput("");
  };

  const targetLabel = useMemo(() => {
    if (!active) return "";
    if (active.targetType === "team") {
      return teams.find((t) => t.id === active.targetId)?.name ?? "团队";
    }
    return agents.find((a) => a.id === active.targetId)?.name ?? "Agent";
  }, [active, agents, teams]);

  return (
    <div className="flex h-full">
      {/* Sessions */}
      <div className="flex w-60 shrink-0 flex-col border-r">
        <div className="grid gap-2 p-3">
          <Button className="w-full" size="sm" onClick={() => setNewChatOpen(true)}>
            <Plus className="mr-1 h-4 w-4" /> 新对话
          </Button>
          <div className="relative">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="h-8 pl-8 text-xs"
              placeholder="搜索对话"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
        </div>
        <ScrollArea className="flex-1 px-2">
          <div className="grid gap-1 pb-3">
            {filteredSessions.map((s) => (
              <div
                key={s.id}
                title={s.title}
                className={cn(
                  "group flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm",
                  s.id === activeID ? "bg-accent text-accent-foreground" : "hover:bg-accent/50",
                )}
                onClick={() => setActiveID(s.id)}
              >
                {s.targetType === "team" ? <Users className="h-3.5 w-3.5 shrink-0 text-primary" /> : <Bot className="h-3.5 w-3.5 shrink-0 text-primary" />}
                <span className="min-w-0 flex-1 truncate">{s.title}</span>
                <span className="hidden shrink-0 items-center gap-0.5 group-hover:flex">
                  <button
                    className="rounded p-0.5 hover:bg-background"
                    onClick={(e) => { e.stopPropagation(); setRenaming(s); setRenameText(s.title); }}
                  >
                    <Pencil className="h-3 w-3" />
                  </button>
                  <button
                    className="rounded p-0.5 hover:bg-background"
                    onClick={(e) => { e.stopPropagation(); removeSession(s); }}
                  >
                    <Trash2 className="h-3 w-3" />
                  </button>
                </span>
              </div>
            ))}
            {sessions.length > 0 && filteredSessions.length === 0 && (
              <div className="px-2 py-6 text-center text-xs text-muted-foreground">没有匹配的对话</div>
            )}
            {sessions.length === 0 && (
              <div className="px-2 py-8 text-center text-xs text-muted-foreground">
                还没有对话，点击上方「新对话」开始
              </div>
            )}
          </div>
        </ScrollArea>
      </div>

      {/* Chat area */}
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex items-center justify-between border-b px-4 py-2.5">
          <div className="flex items-center gap-2 text-sm">
            <MessageSquare className="h-4 w-4 text-muted-foreground" />
            <span className="max-w-[320px] truncate font-medium">{active ? active.title : "WorkBuddy"}</span>
            {active && (
              <Badge variant="outline" className="font-normal">
                {active.targetType === "team" ? "团队" : "Agent"}: {targetLabel}
              </Badge>
            )}
          </div>
          <div className="flex items-center gap-2">
            {sessionUsage && sessionUsage.totalTokens > 0 && (
              <Badge variant="outline" className="font-normal" title={`输入 ${sessionUsage.promptTokens} + 输出 ${sessionUsage.completionTokens} tokens`}>
                {sessionUsage.totalTokens.toLocaleString()} tokens{costOf(sessionUsage) && ` · ${costOf(sessionUsage)}`}
              </Badge>
            )}
            {active && (
              <>
                <Button
                  variant="ghost" size="icon" title="存入长期记忆（供其他对话召回）"
                  onClick={async () => {
                    try {
                      const n = await Chat.RememberSession(activeID);
                      toast.success(`已存入长期记忆（共 ${n} 条）`);
                    } catch (e) {
                      toast.error(`存入失败: ${errText(e)}`);
                    }
                  }}
                >
                  <Brain className="h-4 w-4" />
                </Button>
                <Button variant="ghost" size="icon" title="会话统计" onClick={openStats}>
                  <BarChart3 className="h-4 w-4" />
                </Button>
                <Button variant="ghost" size="icon" title="导出为 Markdown" onClick={exportSession}>
                  <Download className="h-4 w-4" />
                </Button>
              </>
            )}
            {running && (
              <Badge variant="secondary" className="gap-1">
                <Loader2 className="h-3 w-3 animate-spin" /> 运行中
              </Badge>
            )}
          </div>
        </div>

        <ScrollArea className="flex-1">
          <div className="mx-auto max-w-3xl space-y-3 p-4">
            {messages.length > RENDER_LIMIT && (
              <div className="rounded-lg border bg-muted/40 px-3 py-2 text-center text-xs text-muted-foreground">
                已省略较早的 {messages.length - RENDER_LIMIT} 条消息（完整历史已保存，可导出查看）
              </div>
            )}
            {messages.length === 0 && !active && providerCount === 0 && <Onboarding />}
            {messages.length === 0 && active && (
              <div className="py-24 text-center text-sm text-muted-foreground">
                <div className="mb-2 text-4xl">💬</div>
                向 <span className="font-medium text-foreground">{targetLabel}</span> 发送第一条消息
              </div>
            )}
            {messages.slice(-RENDER_LIMIT).map((m, i, arr) => {
              const runningTools = computeRunningTools(messages);
              return (
                <MessageBubble
                  key={m.id}
                  m={m}
                  isLastAssistant={m.kind === "assistant" && i === arr.length - 1}
                  onRegenerate={regenerate}
                  canRegenerate={!running && !!activeID}
                  toolRunning={m.kind === "tool_call" ? runningTools.has(m.id) : false}
                  onSpeak={() => speak(m.text)}
                  costOf={costOf}
                  onEdit={startEdit}
                  canEdit={!running && !!activeID && m.kind === "user" && !messages.slice(i + 1).some((x) => x.kind === "user")}
                />
              );
            })}
            <div ref={bottomRef} />
          </div>
        </ScrollArea>

        <div className="border-t p-3">
          <div className="mx-auto max-w-3xl">
            {editing && (
              <div className="mb-2 flex items-center justify-between rounded-md border border-primary/40 bg-primary/5 px-3 py-1.5 text-xs text-muted-foreground">
                <span>正在编辑最后一条消息，发送后将替换该轮对话</span>
                <button className="underline underline-offset-2 hover:text-foreground" onClick={cancelEdit}>
                  取消编辑
                </button>
              </div>
            )}
          </div>
          <div className="mx-auto max-w-3xl">
            {pendingAtts.length > 0 && (
              <div className="mb-2 flex flex-wrap gap-2">
                {pendingAtts.map((a, i) => (
                  <span key={i} className="group/att relative flex items-center gap-1.5 rounded-md border bg-muted/40 py-1 pl-1 pr-5 text-xs">
                    <img src={`data:${a.mime};base64,${a.data}`} alt={a.name} className="h-8 w-8 rounded object-cover" />
                    <span className="max-w-[120px] truncate">{a.name}</span>
                    <button
                      className="absolute right-1 top-1/2 -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:text-foreground"
                      onClick={() => setPendingAtts((prev) => prev.filter((_, j) => j !== i))}
                    >
                      <X className="h-3 w-3" />
                    </button>
                  </span>
                ))}
              </div>
            )}
          </div>
          <div
            className="mx-auto flex max-w-3xl items-end gap-2"
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => {
              e.preventDefault();
              if (e.dataTransfer.files.length > 0) addFiles(e.dataTransfer.files);
            }}
          >
            <div className="relative flex-1">
              <Textarea
                ref={inputRef}
                rows={2}
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                    e.preventDefault();
                    send();
                  }
                }}
                placeholder={active ? "输入消息，Enter 发送，Shift+Enter 换行（⌘K 聚焦）" : "请先创建对话（⌘N）"}
                disabled={!active || running}
                className={cn(listening && "border-primary/60 ring-1 ring-primary/40")}
              />
              <input
                ref={fileRef}
                type="file"
                accept="image/png,image/jpeg,image/webp,image/gif"
                multiple
                className="hidden"
                onChange={(e) => {
                  if (e.target.files) addFiles(e.target.files);
                  e.target.value = "";
                }}
              />
              <button
                title="添加图片附件"
                disabled={!active || running}
                onClick={() => fileRef.current?.click()}
                className={cn(
                  "absolute bottom-2 right-9 flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                  (!active || running) && "opacity-40",
                )}
              >
                <Paperclip className="h-3.5 w-3.5" />
              </button>
              <button
                title={speechSupported() ? (listening ? "停止语音输入" : "语音输入") : "当前环境不支持语音识别"}
                disabled={!active || running || !speechSupported()}
                onClick={toggleVoiceInput}
                className={cn(
                  "absolute bottom-2 right-2 flex h-7 w-7 items-center justify-center rounded-md transition-colors",
                  listening
                    ? "bg-destructive/15 text-destructive"
                    : "text-muted-foreground hover:bg-accent hover:text-foreground",
                  (!active || running || !speechSupported()) && "opacity-40",
                )}
              >
                {speechSupported() ? (listening ? <SquareStop className="h-3.5 w-3.5" /> : <Mic className="h-3.5 w-3.5" />) : <MicOff className="h-3.5 w-3.5" />}
              </button>
            </div>
            {running ? (
              <Button variant="destructive" onClick={cancel}>
                <Square className="mr-1 h-4 w-4" /> 停止
              </Button>
            ) : (
              <Button onClick={send} disabled={!active || !input.trim()}>
                <Send className="mr-1 h-4 w-4" /> {editing ? "重发" : "发送"}
              </Button>
            )}
          </div>
        </div>
      </div>

      {/* Session stats dialog */}
      <Dialog open={statsOpen} onOpenChange={setStatsOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle className="text-base">会话统计</DialogTitle>
          </DialogHeader>
          {stats && (
            <div className="grid grid-cols-2 gap-2 text-sm">
              {[
                ["用户消息", stats.messages],
                ["助手轮次", stats.assistantTurns],
                ["工具调用", stats.toolCalls],
                ["工具失败", stats.toolFailures],
                ["输入 tokens", stats.tokensIn.toLocaleString()],
                ["输出 tokens", stats.tokensOut.toLocaleString()],
                ["总 tokens", stats.tokensTotal.toLocaleString()],
                [costOf({ promptTokens: stats.tokensIn, completionTokens: stats.tokensOut, totalTokens: stats.tokensTotal }) || "—", costOf({ promptTokens: stats.tokensIn, completionTokens: stats.tokensOut, totalTokens: stats.tokensTotal }) ? "估算成本" : "未配置价格"],
              ].map(([k, v], i) => (
                <div key={i} className="rounded-lg border p-2.5">
                  <div className="text-xs text-muted-foreground">{v}</div>
                  <div className="text-sm font-medium">{k}</div>
                </div>
              ))}
            </div>
          )}
        </DialogContent>
      </Dialog>

      {/* New chat dialog */}
      <Dialog open={newChatOpen} onOpenChange={setNewChatOpen}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>新对话</DialogTitle>
          </DialogHeader>
          <div className="grid gap-4 py-2">
            <div className="grid gap-1.5">
              <label className="text-sm">类型</label>
              <Select value={newType} onValueChange={(v) => { setNewType(v as "agent" | "team"); setNewTarget(""); }}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="agent">单个 Agent</SelectItem>
                  <SelectItem value="team">Agent 团队</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-1.5">
              <label className="text-sm">{newType === "agent" ? "选择 Agent" : "选择团队"}</label>
              <Select value={newTarget || undefined} onValueChange={setNewTarget}>
                <SelectTrigger><SelectValue placeholder="请选择" /></SelectTrigger>
                <SelectContent>
                  {newType === "agent"
                    ? agents.map((a) => <SelectItem key={a.id} value={a.id}>{a.name}</SelectItem>)
                    : teams.map((t) => <SelectItem key={t.id} value={t.id}>{t.name}</SelectItem>)}
                </SelectContent>
              </Select>
              {((newType === "agent" && agents.length === 0) || (newType === "team" && teams.length === 0)) && (
                <p className="text-xs text-muted-foreground">
                  没有可选目标，请先在侧边栏创建 {newType === "agent" ? "Agent" : "团队"}。
                </p>
              )}
            </div>
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setNewChatOpen(false)}>取消</Button>
            <Button onClick={createSession} disabled={!newTarget}>开始对话</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Rename dialog */}
      <Dialog open={!!renaming} onOpenChange={(v) => !v && setRenaming(null)}>
        <DialogContent className="sm:max-w-sm">
          <DialogHeader>
            <DialogTitle>重命名对话</DialogTitle>
          </DialogHeader>
          <Input value={renameText} onChange={(e) => setRenameText(e.target.value)} />
          <DialogFooter>
            <Button variant="outline" onClick={() => setRenaming(null)}>取消</Button>
            <Button
              onClick={async () => {
                if (renaming) {
                  await Chat.RenameSession(renaming.id, renameText).catch((e) => toast.error(errText(e)));
                  setRenaming(null);
                  reloadSessions();
                }
              }}
            >
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

/** Onboarding guides first-run users like Codex/Claude desktops do. */
function Onboarding() {
  const steps = [
    { n: 1, title: "配置模型供应商", desc: "添加 OpenAI 兼容 / Anthropic / Gemini 接口", page: "模型配置" },
    { n: 2, title: "创建 Agent", desc: "定义系统提示词，挂载 Skills 与 MCP 工具", page: "Agents" },
    { n: 3, title: "开始对话", desc: "新建对话，单个 Agent 或团队协作", page: "对话" },
  ];
  const go = (label: string) => {
    const btns = Array.from(document.querySelectorAll("aside button"));
    const target = btns.find((b) => b.textContent?.includes(label)) as HTMLElement | undefined;
    target?.click();
  };
  return (
    <div className="mx-auto mt-10 max-w-md rounded-xl border bg-card p-5 shadow-sm">
      <div className="mb-1 text-lg font-semibold">欢迎使用 WorkBuddy Agent 👋</div>
      <p className="mb-4 text-sm text-muted-foreground">基于 Google ADK 的本地 Agent 工作台，三步开始：</p>
      <div className="space-y-3">
        {steps.map((s) => (
          <button
            key={s.n}
            onClick={() => go(s.page)}
            className="flex w-full items-center gap-3 rounded-lg border p-3 text-left transition-colors hover:border-primary/50 hover:bg-accent/40"
          >
            <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-primary/10 text-sm font-semibold text-primary">
              {s.n}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-sm font-medium">{s.title}</span>
              <span className="block text-xs text-muted-foreground">{s.desc}</span>
            </span>
            <ArrowRight className="h-4 w-4 shrink-0 text-muted-foreground" />
          </button>
        ))}
      </div>
    </div>
  );
}

/** computeRunningTools marks tool_call messages that have no matching result yet. */
function computeRunningTools(messages: UIMessage[]): Set<string> {
  const running = new Set<string>();
  const pending: string[] = []; // tool_call ids awaiting result, per tool name order
  for (const m of messages) {
    if (m.kind === "tool_call") {
      running.add(m.id);
      pending.push(m.id);
    } else if (m.kind === "tool_result" && pending.length > 0) {
      const id = pending.shift();
      running.delete(id!);
    }
  }
  return running;
}

function MessageBubble({ m, isLastAssistant, onRegenerate, canRegenerate, toolRunning, onSpeak, costOf, onEdit, canEdit }: {
  m: UIMessage;
  isLastAssistant: boolean;
  onRegenerate: () => void;
  canRegenerate: boolean;
  toolRunning: boolean;
  onSpeak: () => void;
  costOf: (u?: UsageInfo | null) => string;
  onEdit: (text: string) => void;
  canEdit: boolean;
}) {
  const [copied, setCopied] = useState(false);
  const copyMsg = async () => {
    try {
      await navigator.clipboard.writeText(m.text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch { /* ignore */ }
  };
  if (m.kind === "user") {
    return (
      <div className="group flex justify-end">
        <div className="flex max-w-[80%] flex-col items-end gap-1.5">
          {(m.attachments?.length ?? 0) > 0 && (
            <div className="flex flex-wrap justify-end gap-1.5">
              {m.attachments!.map((a, i) => (
                <img key={i} src={a.dataUrl} alt={a.name} className="h-20 rounded-lg border object-cover" />
              ))}
            </div>
          )}
          {m.text && (
          <div className="flex items-end gap-1.5">
          {canEdit && (
            <button
              className="mb-1 inline-flex items-center gap-0.5 rounded p-0.5 text-[11px] text-muted-foreground opacity-0 transition-opacity hover:text-foreground group-hover:opacity-100"
              title="编辑并重发这条消息"
              onClick={() => onEdit(m.text)}
            >
              <Pencil className="h-3 w-3" />
              编辑
            </button>
          )}
          <CopyButton text={m.text} className="mb-1 opacity-0 transition-opacity group-hover:opacity-100" />
          <div className="whitespace-pre-wrap rounded-2xl rounded-br-md bg-primary px-4 py-2.5 text-sm text-primary-foreground">
            {m.text}
          </div>
          </div>
          )}
        </div>
      </div>
    );
  }
  if (m.kind === "error") {
    return (
      <div className="rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive">
        {m.text}
      </div>
    );
  }
  if (m.kind === "tool_call" || m.kind === "tool_result") {
    return (
      <ToolCallCard
        name={m.toolName ?? "tool"}
        args={m.toolArgs}
        resp={m.toolResp}
        running={m.kind === "tool_call" && toolRunning}
      />
    );
  }
  return (
    <div className="group flex justify-start">
      <div className="max-w-[85%]">
        <div className="mb-0.5 flex items-center gap-2">
          <span className="text-[11px] text-muted-foreground">{m.author}</span>
          {isLastAssistant && !m.streaming && m.text && (
            <span className="flex items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100">
              <button
                className="inline-flex items-center gap-0.5 rounded p-0.5 text-[11px] text-muted-foreground hover:text-foreground"
                onClick={copyMsg}
              >
                {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
                {copied ? "已复制" : "复制"}
              </button>
              {canRegenerate && (
                <button
                  className="inline-flex items-center gap-0.5 rounded p-0.5 text-[11px] text-muted-foreground hover:text-foreground"
                  title="重新生成最后一条回复"
                  onClick={onRegenerate}
                >
                  <RefreshCw className="h-3 w-3" />
                  重新生成
                </button>
              )}
              <button
                className="inline-flex items-center gap-0.5 rounded p-0.5 text-[11px] text-muted-foreground hover:text-foreground"
                title="朗读这条回复"
                onClick={onSpeak}
              >
                <Volume2 className="h-3 w-3" />
                朗读
              </button>
              {m.usage && m.usage.totalTokens > 0 && (
                <span className="text-[11px] text-muted-foreground/70" title={`输入 ${m.usage.promptTokens} + 输出 ${m.usage.completionTokens}`}>
                  · {m.usage.totalTokens.toLocaleString()} tokens{costOf(m.usage) && ` · ${costOf(m.usage)}`}
                </span>
              )}
            </span>
          )}
        </div>
        <div className="rounded-2xl rounded-bl-md bg-muted px-4 py-2.5">
          {m.streaming && !m.text ? (
            <span className="inline-flex items-center gap-1 text-sm text-muted-foreground">
              <Loader2 className="h-3 w-3 animate-spin" /> 思考中…
            </span>
          ) : m.kind === "assistant" ? (
            <>
              <MessageMarkdown content={m.text} />
              {m.streaming && <span className="ml-0.5 inline-block h-3.5 w-1.5 animate-pulse bg-foreground/60 align-middle" />}
            </>
          ) : (
            <div className="whitespace-pre-wrap text-sm">{m.text}</div>
          )}
        </div>
      </div>
    </div>
  );
}
