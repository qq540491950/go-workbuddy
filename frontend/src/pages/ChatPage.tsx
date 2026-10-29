import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import {
  Plus, Send, Square, Trash2, Pencil, MessageSquare, Bot, Users, Loader2,
  Copy, Check, Search, ArrowRight, RefreshCw, Download, Mic, MicOff, Volume2, Square as SquareStop,
  Paperclip, X, BarChart3, Brain, ShieldAlert, Network, GitFork, Pin, PinOff, FileCode2, Archive, ListTodo,
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
  type AgentConfig, type ChatSession, type TeamConfig, type RemoteAgentConfig, type UsageInfo,
  type ModelProvider, type ModelInfo, type AttachmentIn, type AttachmentOut, type SessionStats,
} from "@/lib/api";

type UIMessage = {
  id: string;
  kind: "user" | "assistant" | "tool_call" | "tool_result" | "confirm" | "error";
  author: string;
  text: string;
  toolName?: string;
  toolArgs?: { [k: string]: any } | null;
  toolResp?: { [k: string]: any } | null;
  callId?: string;
  hint?: string;
  confirmStatus?: "pending" | "approved" | "rejected";
  timestamp?: string;
  durationMs?: number;
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

const SLASH_COMMANDS = [
  { cmd: "/export", desc: "导出会话为 Markdown" },
  { cmd: "/exporthtml", desc: "导出会话为 HTML" },
  { cmd: "/stats", desc: "查看会话统计" },
  { cmd: "/todos", desc: "查看当前任务计划" },
  { cmd: "/new", desc: "新建对话" },
];

/** fmtTime renders an ISO timestamp as local HH:MM for hover display. */
function fmtTime(iso?: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  return isNaN(d.getTime()) ? "" : d.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
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
  // Per-session input drafts: switching sessions never loses what the user
  // was typing (pairs with parallel background sessions).
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const input = activeID ? (drafts[activeID] ?? "") : "";
  const setInput = useCallback(
    (v: string | ((prev: string) => string)) => {
      if (!activeID) return;
      setDrafts((prev) => {
        const cur = prev[activeID] ?? "";
        const next = typeof v === "function" ? v(cur) : v;
        return { ...prev, [activeID]: next };
      });
    },
    [activeID],
  );
  const [runningMap, setRunningMap] = useState<Record<string, boolean>>({});
  const running = !!activeID && !!runningMap[activeID];
  const [agents, setAgents] = useState<AgentConfig[]>([]);
  const [teams, setTeams] = useState<TeamConfig[]>([]);
  const [remotes, setRemotes] = useState<RemoteAgentConfig[]>([]);
  const [providerCount, setProviderCount] = useState(0);
  const [providerList, setProviderList] = useState<ModelProvider[]>([]);
  const [search, setSearch] = useState("");
  const [newChatOpen, setNewChatOpen] = useState(false);
  const [newType, setNewType] = useState<"agent" | "team" | "remote">("agent");
  const [newTarget, setNewTarget] = useState("");
  const [renaming, setRenaming] = useState<ChatSession | null>(null);
  const [renameText, setRenameText] = useState("");
  const [sessionUsage, setSessionUsage] = useState<UsageInfo | null>(null);
  const [statsOpen, setStatsOpen] = useState(false);
  const [stats, setStats] = useState<SessionStats | null>(null);
  const [autoSpeak, setAutoSpeak] = useState(false);
  const [listening, setListening] = useState(false);
  const [editing, setEditing] = useState(false);
  const [ctxBannerDismissed, setCtxBannerDismissed] = useState(false);
  const [pendingAtts, setPendingAtts] = useState<AttachmentIn[]>([]);
  const fileRef = useRef<HTMLInputElement>(null);
  const bottomRef = useRef<HTMLDivElement>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const scrollFrame = useRef<number | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const messagesRef = useRef<UIMessage[]>([]);
  useEffect(() => { messagesRef.current = messages; }, [messages]);
  const sessionsRef = useRef<ChatSession[]>([]);
  useEffect(() => { sessionsRef.current = sessions; }, [sessions]);
  const autoSpeakRef = useRef(autoSpeak);
  useEffect(() => { autoSpeakRef.current = autoSpeak; }, [autoSpeak]);
  // Set when the stream emitted an inline error, so send()'s catch does not
  // also toast the same failure.
  const streamedErrorRef = useRef(false);
  // Set when the current turn actually started (a user bubble arrived). The
  // backend refuses turns past the token budget with error+done and a resolved
  // promise — without this flag the typed text would vanish without a trace.
  const turnStartedRef = useRef(false);

  // markRunning tracks in-flight turns PER SESSION so several sessions can
  // stream in parallel without blocking each other.
  const markRunning = useCallback((id: string, on: boolean) => {
    setRunningMap((prev) => {
      if (!!prev[id] === on) return prev;
      const next = { ...prev };
      if (on) next[id] = true;
      else delete next[id];
      return next;
    });
  }, []);

  const active = sessions.find((s) => s.id === activeID);

  // fireQueued pops the session's queued message and auto-sends it (used both
  // when a turn settles and when the user returns to a settled session).
  const fireQueued = useCallback((sessionID: string) => {
    const q = queuedRef.current[sessionID];
    if (!q) return;
    setQueued((prev) => {
      const next = { ...prev };
      delete next[sessionID];
      return next;
    });
    setTimeout(() => sendRef.current(q), 80);
  }, []);

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
        setRemotes(c.remoteAgents ?? []);
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
    // Guards against A->B fast switches: a slow response for the previous
    // session must not paint its messages under the new one.
    let cancelled = false;
    setSessionUsage(null);
    setCtxBannerDismissed(false);
    Chat.GetSessionMessages(activeID)
      .then((ms) => {
        if (cancelled) return;
        // Pair confirm_request with its confirm_result so pending approvals
        // stay interactive (e.g. after an app restart).
        const answered = new Map<string, boolean>();
        for (const m of ms ?? []) {
          if (m.kind === "confirm_result" && m.callId) {
            answered.set(m.callId, m.toolResp?.confirmed === true);
          }
        }
        setMessages(
          (ms ?? [])
            .filter((m) => m.kind !== "confirm_result")
            .map((m) => ({
              id: m.id,
              kind: (m.kind === "user" ? "user" : m.kind === "tool_call" ? "tool_call" : m.kind === "tool_result" ? "tool_result" : m.kind === "confirm_request" ? "confirm" : "assistant") as UIMessage["kind"],
              author: m.author,
              text: m.text,
              toolName: m.toolName,
              toolArgs: m.toolArgs,
              toolResp: m.toolResp,
              callId: m.callId,
              hint: m.hint,
              confirmStatus: m.kind === "confirm_request" ? (answered.has(m.callId!) ? (answered.get(m.callId!) ? "approved" : "rejected") : "pending") as UIMessage["confirmStatus"] : undefined,
              timestamp: m.timestamp,
              attachments: m.attachments ?? null,
            })),
        );
        // Entering a session always lands on its newest message — the shared
        // ScrollArea keeps the previous session's scrollTop otherwise, which
        // could open a long history at its oldest message.
        requestAnimationFrame(() => bottomRef.current?.scrollIntoView({ block: "end" }));
      })
      .catch((e) => {
        if (!cancelled) toast.error(`加载历史失败: ${errText(e)}`);
      });
    Chat.IsRunning(activeID).then((r) => {
      if (cancelled) return;
      markRunning(activeID, r);
      // A queued message stranded by a session switch (its turn finished in
      // the background) fires now: the user is back and nothing is running.
      if (!r && queuedRef.current[activeID]) {
        fireQueued(activeID);
      }
    }).catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [activeID]);

  // The backend nudges this event when it changes session metadata out of
  // band (async model-generated titles), so the sidebar picks it up.
  useEffect(() => {
    const off = Events.On("sessions:updated", () => reloadSessions());
    return off;
  }, [reloadSessions]);

  // Stream events.
  useEffect(() => {
    const off = Events.On("chat:stream", (e: any) => {
      const ev = Array.isArray(e?.data) ? e.data[0] : e?.data;
      if (!ev) return;
      // Completion bookkeeping runs for EVERY session: background runs must
      // clear their spinner and toast even while the user is in another chat.
      // (This block used to sit after the active-session filter below, which
      // made it unreachable for non-active sessions.)
      if (ev.kind === "done" || ev.kind === "error") {
        if (ev.kind === "error" && ev.sessionId === activeID) streamedErrorRef.current = true;
        markRunning(ev.sessionId, false);
        reloadSessions();
        // A queued message fires as soon as the turn settles — including
        // after an error, so the user's follow-up is never stranded.
        if (queuedRef.current[ev.sessionId] && ev.sessionId === activeID) {
          fireQueued(ev.sessionId);
        }
        if (ev.sessionId !== activeID) {
          const title = sessionsRef.current.find((x: { id: string; title: string }) => x.id === ev.sessionId)?.title ?? "后台会话";
          if (ev.kind === "done") toast.success(`「${title}」已完成回复`);
          else toast.error(`「${title}」本轮出错`);
        }
      }
      if (ev.sessionId !== activeID) return;
      if (ev.kind === "user") turnStartedRef.current = true;
      setMessages((prev) => {
        switch (ev.kind) {
          case "user":
            return [...prev, { id: nextId(), kind: "user", author: "user", text: ev.text, timestamp: new Date().toISOString(), attachments: ev.attachments ?? null }];
          case "delta": {
            const last = prev[prev.length - 1];
            if (last && last.streaming && last.author === ev.author) {
              const copy = [...prev];
              copy[copy.length - 1] = { ...last, text: last.text + ev.text };
              return copy;
            }
            return [...prev, { id: nextId(), kind: "assistant", author: ev.author, text: ev.text, streaming: true, timestamp: new Date().toISOString() }];
          }
          case "message": {
            const idx = [...prev].reverse().findIndex((m) => m.streaming && m.author === ev.author);
            if (idx >= 0) {
              const real = prev.length - 1 - idx;
              const copy = [...prev];
              copy[real] = { ...copy[real], text: ev.text, streaming: false, usage: ev.usage ?? null };
              return copy;
            }
            return [...prev, { id: nextId(), kind: "assistant", author: ev.author, text: ev.text, usage: ev.usage ?? null, timestamp: new Date().toISOString() }];
          }
          case "tool_call":
            return [...prev, { id: nextId(), kind: "tool_call", author: ev.author, text: "", toolName: ev.toolName, toolArgs: ev.toolArgs, timestamp: new Date().toISOString() }];
          case "tool_result":
            return [...prev, { id: nextId(), kind: "tool_result", author: ev.author, text: "", toolName: ev.toolName, toolResp: ev.toolResp, timestamp: new Date().toISOString() }];
          case "confirm_request":
            return [...prev, {
              id: nextId(), kind: "confirm", author: ev.author, text: "",
              toolName: ev.toolName, toolArgs: ev.toolArgs,
              callId: ev.callId, hint: ev.hint, confirmStatus: "pending",
              timestamp: new Date().toISOString(),
            } as UIMessage];
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
        // Read the latest flag through a ref: this subscription is created
        // once per session and would otherwise capture the startup value.
        if (autoSpeakRef.current) {
          const lastAssistant = [...messagesRef.current].reverse().find((m) => m.kind === "assistant" && m.text);
          if (lastAssistant) speak(lastAssistant.text);
        }
      }
    });
    return off;
  }, [activeID, reloadSessions, markRunning, speak]);

  // Auto-scroll only when the reader is already at the bottom, and coalesce
  // per animation frame: scrolling smoothly on every token fights the user
  // and queues animations faster than they finish.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 120;
    if (!nearBottom) return;
    if (scrollFrame.current != null) return;
    scrollFrame.current = requestAnimationFrame(() => {
      scrollFrame.current = null;
      bottomRef.current?.scrollIntoView({ behavior: "auto", block: "end" });
    });
  }, [messages]);

  useEffect(() => () => {
    if (scrollFrame.current != null) cancelAnimationFrame(scrollFrame.current);
  }, []);

  // Cmd/Ctrl+N opens the new-chat dialog; Cmd/Ctrl+K focuses the composer.
  // The keydown listener lives in App (the help panel documents them on every
  // page); ChatPage reacts to the dispatched custom events.
  useEffect(() => {
    const onNew = () => setNewChatOpen(true);
    const onFocus = () => inputRef.current?.focus();
    window.addEventListener("wb-new-chat", onNew);
    window.addEventListener("wb-focus-composer", onFocus);
    return () => {
      window.removeEventListener("wb-new-chat", onNew);
      window.removeEventListener("wb-focus-composer", onFocus);
    };
  }, []);

  // Window-level drag guard: dropping a file outside the composer must not
  // let the webview navigate away from the app.
  useEffect(() => {
    const prevent = (e: DragEvent) => e.preventDefault();
    window.addEventListener("dragover", prevent);
    window.addEventListener("drop", prevent);
    return () => {
      window.removeEventListener("dragover", prevent);
      window.removeEventListener("drop", prevent);
    };
  }, []);

  // Per-session message queue: sending while a reply streams parks the text
  // and auto-sends it when the turn settles (Codex-style queueing).
  const [queued, setQueued] = useState<Record<string, string>>({});
  const queuedRef = useRef(queued);
  useEffect(() => { queuedRef.current = queued; }, [queued]);

  const send = async (override?: string) => {
    const text = (override ?? input).trim();
    if (!text || !activeID) return;
    if (running && !override) {
      setQueued((prev) => ({ ...prev, [activeID]: text }));
      setInput("");
      toast("已排队：当前回复完成后自动发送");
      return;
    }
    // Slash commands run locally without a model round-trip.
    if (text.startsWith("/")) {
      const cmd = text.split(/\s+/)[0].toLowerCase();
      switch (cmd) {
        case "/export":
          setInput("");
          exportSession();
          return;
        case "/exporthtml":
          setInput("");
          exportHTML();
          return;
        case "/stats":
          setInput("");
          openStats();
          return;
        case "/todos": {
          setInput("");
          for (let i = messages.length - 1; i >= 0; i--) {
            const m = messages[i];
            const todos = m.toolResp?.todos;
            if (m.kind === "tool_result" && m.toolName === "plan_todos" && Array.isArray(todos) && todos.length > 0) {
              const done = todos.filter((t: TodoItem) => t.status === "completed").length;
              const lines = todos.map((t: TodoItem) =>
                `${t.status === "completed" ? "✓" : t.status === "in_progress" ? "▶" : "○"} ${t.content}`,
              );
              toast(`${done}/${todos.length} 完成`, { description: lines.join("\n") });
              return;
            }
          }
          toast.info("当前会话还没有任务计划（Agent 挂载「任务计划」内置工具后可用）");
          return;
        }
        case "/new":
          setInput("");
          setNewChatOpen(true);
          return;
        default:
          toast.error(`未知命令 ${cmd}。可用：/export /exporthtml /stats /todos /new`);
          return;
      }
    }
    if (running) return;
    setInput("");
    markRunning(activeID, true);
    const wasEditing = editing;
    setEditing(false);
    const atts = pendingAtts;
    setPendingAtts([]);
    turnStartedRef.current = false;
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
      // A resolved promise without a started turn means the backend refused
      // the round (e.g. token budget exhausted): restore what the user typed
      // instead of silently swallowing it.
      if (!turnStartedRef.current) {
        setInput((cur) => (cur === "" ? text : cur));
        setPendingAtts((prev) => [...atts, ...prev]);
      }
    } catch (e) {
      // The stream already emitted an inline error bubble for run failures;
      // Chat.Send returns the same error so the promise settles, but only
      // surface a toast when nothing was streamed (e.g. validation failure).
      const msg = errText(e);
      if (!streamedErrorRef.current) toast.error(`发送失败: ${msg}`);
      streamedErrorRef.current = false;
      markRunning(activeID, false);
      // Restore the draft and attachments (merging, not clobbering: the user
      // may have started typing again while the request was in flight).
      setPendingAtts((prev) => [...atts, ...prev]);
      setInput((cur) => (cur === "" ? text : cur));
    }
  };

  // Latest send for the stream subscription: the subscription is created
  // once per session and must not fire a stale closure.
  const sendRef = useRef(send);
  useEffect(() => { sendRef.current = send; });

  const cancel = () => {
    if (activeID) Chat.Cancel(activeID);
  };

  // answerConfirm resolves a HITL tool-confirmation card. The status flips
  // optimistically; a failed resume reverts the card to pending.
  const answerConfirm = useCallback(async (msgId: string, callId: string | undefined, approved: boolean) => {
    if (!activeID || !callId || running) return;
    markRunning(activeID, true);
    setMessages((prev) => prev.map((m) => (m.id === msgId ? { ...m, confirmStatus: approved ? "approved" : "rejected" } : m)));
    try {
      await Chat.ConfirmToolCall(activeID, callId, approved);
    } catch (e) {
      if (!streamedErrorRef.current) toast.error(`确认失败: ${errText(e)}`);
      streamedErrorRef.current = false;
      setMessages((prev) => prev.map((m) => (m.id === msgId && m.kind === "confirm" ? { ...m, confirmStatus: "pending" } : m)));
      markRunning(activeID, false);
    }
  }, [activeID, running, markRunning]);

  const MAX_ATT_BYTES = 6 * 1024 * 1024;
  const allowedMIME = ["image/png", "image/jpeg", "image/webp", "image/gif"];

  const addFiles = async (files: FileList | File[]) => {
    let added = 0;
    for (const f of Array.from(files)) {
      if (!allowedMIME.includes(f.type)) {
        toast.error(`不支持的类型 ${f.type || f.name}（仅图片）`);
        continue;
      }
      if (f.size > MAX_ATT_BYTES) {
        toast.error(`${f.name} 超过 6MB`);
        continue;
      }
      let binary = "";
      try {
        const buf = await f.arrayBuffer();
        const bytes = new Uint8Array(buf);
        const chunk = 0x8000;
        for (let i = 0; i < bytes.length; i += chunk) {
          binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
        }
      } catch {
        toast.error(`读取 ${f.name} 失败`);
        continue;
      }
      if (pendingAtts.length + added >= 4) {
        toast.warning("最多附带 4 张图片");
        break;
      }
      added++;
      setPendingAtts((prev) => (prev.length >= 4 ? prev : [...prev, { name: f.name, mime: f.type, data: btoa(binary) }]));
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

  const regenerate = useCallback(async () => {
    if (!activeID || running) return;
    markRunning(activeID, true);
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
      if (!streamedErrorRef.current) toast.error(`重新生成失败: ${errText(e)}`);
      streamedErrorRef.current = false;
      markRunning(activeID, false);
    }
  }, [activeID, running, markRunning]);

  /** speakMessage is stable per call site so memoized bubbles can skip it. */
  const speakMessage = useCallback((text: string) => speak(text), []);

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

  const exportHTML = async () => {
    if (!activeID) return;
    try {
      const path = await Chat.ExportSessionHTML(activeID);
      toast.success(`已导出 HTML: ${path}`);
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

  const forkSession = async (s: ChatSession) => {
    try {
      const fork = await Chat.ForkSession(s.id);
      toast.success(`已分叉: ${fork.title}`);
      reloadSessions();
      setActiveID(fork.id);
      setMessages([]);
    } catch (e) {
      toast.error(`分叉失败: ${errText(e)}`);
    }
  };

  const removeSession = async (s: ChatSession) => {
    if (!window.confirm(`确定删除会话「${s.title}」？该操作不可恢复。`)) return;
    await Chat.DeleteSession(s.id).catch((e) => toast.error(`删除失败: ${errText(e)}`));
    if (s.id === activeID) {
      setActiveID("");
      setMessages([]);
    }
    reloadSessions();
  };

    const [contentMatches, setContentMatches] = useState<Set<string> | null>(null);

  // Debounced content search: with a query present, the sidebar lists sessions
  // whose title OR stored history matches (backend LIKE over event payloads).
  // A monotonic sequence drops stale responses: a slow reply for an earlier
  // query must not overwrite the result of the latest one.
  const searchSeqRef = useRef(0);
  useEffect(() => {
    const q = search.trim();
    if (!q) {
      setContentMatches(null);
      return;
    }
    const t = setTimeout(() => {
      const seq = ++searchSeqRef.current;
      Chat.SearchSessions(q)
        .then((ss) => {
          if (seq === searchSeqRef.current) setContentMatches(new Set((ss ?? []).map((x) => x.id)));
        })
        .catch(() => {
          if (seq === searchSeqRef.current) setContentMatches(null);
        });
    }, 250);
    return () => clearTimeout(t);
  }, [search]);

  const [showArchived, setShowArchived] = useState(false);
  const archivedCount = useMemo(() => sessions.filter((x) => x.archived).length, [sessions]);

  const filteredSessions = useMemo(() => {
    const q = search.trim().toLowerCase();
    const matches = (s: ChatSession) => {
      if (!q) return true;
      if (s.title.toLowerCase().includes(q)) return true;
      return contentMatches ? contentMatches.has(s.id) : false;
    };
    const withQuery = sessions.filter((s) => !s.archived && matches(s));
    if (showArchived) {
      // Archived entries obey the same query filter as the main list.
      return [...withQuery, ...sessions.filter((s) => s.archived && matches(s))];
    }
    return withQuery;
  }, [sessions, search, contentMatches, showArchived]);

  // Provider + model of the active target (agent → its model; team →
  // coordinator when auto, lead agent otherwise) for cost estimation and
  // multimodal gating.
  const target = useMemo(() => {
    if (!active) return { provider: null as ModelProvider | null, model: null as ModelInfo | null };
    let providerId = "";
    let modelId = "";
    if (active.targetType === "team") {
      const team = teams.find((t) => t.id === active.targetId);
      if (team?.autoCoordinate) {
        providerId = team.providerId;
        modelId = team.model;
      } else {
        const lead = agents.find((a) => a.id === team?.leadAgentId);
        providerId = lead?.providerId ?? "";
        modelId = lead?.model ?? "";
      }
    } else {
      const agent = agents.find((a) => a.id === active.targetId);
      providerId = agent?.providerId ?? "";
      modelId = agent?.model ?? "";
    }
    const provider = providerList.find((p) => p.id === providerId) ?? null;
    const model = provider?.models?.find((m) => m.id === modelId) ?? null;
    return { provider, model };
  }, [active, agents, teams, providerList]);

  /** modelMultimodal gates the image attachment entry (see ModelInfo). */
  const modelMultimodal = !!target.model?.multimodal;

  /** costOf computes ¥ cost from token usage at model rates (per 1M). */
  const costOf = useCallback((u?: UsageInfo | null): string => {
    if (!u || !target.model) return "";
    const pin = target.model.priceIn ?? 0;
    const pout = target.model.priceOut ?? 0;
    if (pin <= 0 && pout <= 0) return "";
    const cost = (u.promptTokens / 1e6) * pin + (u.completionTokens / 1e6) * pout;
    if (cost <= 0) return "";
    return cost < 0.01 ? "<¥0.01" : `¥${cost.toFixed(2)}`;
  }, [target.model]);

  const startEdit = useCallback((text: string) => {
    if (running || !activeID) return;
    setInput(text);
    setEditing(true);
    inputRef.current?.focus();
  }, [running, activeID, setInput]);

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

  // Per-session model picker (agent sessions only): mirrors the per-chat
  // model selector of Claude/ChatGPT desktops.
  const sessionModels = useMemo(() => {
    if (!active || active.targetType !== "agent") return [];
    const agent = agents.find((a) => a.id === active.targetId);
    return providerList.find((p) => p.id === agent?.providerId)?.models ?? [];
  }, [active, agents, providerList]);

  const setSessionModel = async (modelID: string) => {
    if (!activeID) return;
    try {
      await Chat.SetSessionModel(activeID, modelID === "__default" ? "" : modelID);
      reloadSessions();
      toast.success(modelID === "__default" ? "已恢复 Agent 默认模型" : `本会话已切换到 ${modelID}`);
    } catch (e) {
      toast.error(`切换模型失败: ${errText(e)}`);
    }
  };

  // ⌘F (dispatched by App) opens the in-conversation filter.
  useEffect(() => {
    const onToggle = () => setFilterOpen(true);
    window.addEventListener("wb-toggle-filter", onToggle);
    return () => window.removeEventListener("wb-toggle-filter", onToggle);
  }, []);

  const toolDurations = useMemo(() => computeToolDurations(messages), [messages]);
  const runningToolsSet = useMemo(() => computeRunningTools(messages), [messages]);
  // Precompute once per message change instead of scanning the tail for every
  // rendered row (that was O(n^2) per token).
  const editFlags = useMemo(() => computeEditFlags(messages), [messages]);
  // Latest session plan (plan_todos result): pinned above the composer so
  // multi-step progress stays visible after later messages scroll by.
  const activePlan = useMemo(() => {
    for (let i = messages.length - 1; i >= 0; i--) {
      const m = messages[i];
      const todos = m.toolResp?.todos;
      if (m.kind === "tool_result" && m.toolName === "plan_todos" && Array.isArray(todos) && todos.length > 0) {
        return todos as TodoItem[];
      }
    }
    return null;
  }, [messages]);
  // In-conversation message filter (⌘F / the header search icon): shows only
  // the messages matching the query, like the desktop clients' chat search.
  const [filterOpen, setFilterOpen] = useState(false);
  const [msgFilter, setMsgFilter] = useState("");
  const filterInputRef = useRef<HTMLInputElement>(null);
  const filterQ = msgFilter.trim().toLowerCase();
  const filteredMessages = useMemo(() => {
    if (!filterQ) return messages;
    return messages.filter((m) => {
      if (m.text && m.text.toLowerCase().includes(filterQ)) return true;
      if (m.hint && m.hint.toLowerCase().includes(filterQ)) return true;
      if (m.toolName && m.toolName.toLowerCase().includes(filterQ)) return true;
      if (m.toolResp && JSON.stringify(m.toolResp).toLowerCase().includes(filterQ)) return true;
      if (m.toolArgs && JSON.stringify(m.toolArgs).toLowerCase().includes(filterQ)) return true;
      return false;
    });
  }, [messages, filterQ]);
  const visibleMessages = useMemo(
    () => (filterQ ? filteredMessages.slice(-RENDER_LIMIT) : messages.slice(-RENDER_LIMIT)),
    [filterQ, filteredMessages, messages],
  );

  const toggleFilter = useCallback(() => {
    setFilterOpen((v) => {
      const next = !v;
      if (next) {
        setTimeout(() => filterInputRef.current?.focus(), 50);
      } else {
        setMsgFilter("");
      }
      return next;
    });
  }, []);
  const slashSuggestions = useMemo(() => {
    if (!input.startsWith("/")) return [];
    const q = input.toLowerCase();
    return SLASH_COMMANDS.filter((c) => c.cmd.startsWith(q));
  }, [input]);

  // contextUsage: how much of the target model's context window the session
  // has consumed so far (best-effort, based on streamed usage totals).
  const contextUsage = useMemo(() => {
    const win = target.model?.contextWindow ?? 0;
    if (win <= 0 || !sessionUsage || sessionUsage.totalTokens <= 0) return null;
    const pct = Math.min(100, (sessionUsage.totalTokens / win) * 100);
    const color = pct >= 80 ? "bg-destructive" : pct >= 50 ? "bg-amber-500" : "bg-emerald-500";
    return { pct, color, win };
  }, [target.model, sessionUsage]);

  // Whole-session cost estimate for the stats dialog (label/value order must
  // match the other stat cells: value muted on top, label bold below).
  const statsCost = useMemo(
    () => (stats ? costOf({ promptTokens: stats.tokensIn, completionTokens: stats.tokensOut, totalTokens: stats.tokensTotal }) : ""),
    [stats, costOf],
  );

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
            {archivedCount > 0 && (
              <button
                className="mb-1 w-full rounded-md px-2 py-1 text-left text-xs text-muted-foreground hover:bg-accent/50"
                onClick={() => setShowArchived((v) => !v)}
              >
                {showArchived ? "隐藏" : "显示"}已归档会话（{archivedCount}）
              </button>
            )}
            {filteredSessions.map((s) => (
              <div
                key={s.id}
                role="button"
                tabIndex={0}
                title={s.title}
                className={cn(
                  "group flex cursor-pointer items-center gap-2 rounded-md px-2.5 py-2 text-sm",
                  s.id === activeID ? "bg-accent text-accent-foreground" : "hover:bg-accent/50",
                )}
                onClick={() => setActiveID(s.id)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" || e.key === " ") {
                    e.preventDefault();
                    setActiveID(s.id);
                  }
                }}
              >
                {s.targetType === "team" ? <Users className="h-3.5 w-3.5 shrink-0 text-primary" /> : s.targetType === "remote" ? <Network className="h-3.5 w-3.5 shrink-0 text-primary" /> : <Bot className="h-3.5 w-3.5 shrink-0 text-primary" />}
                <span className="min-w-0 flex-1">
                  <span className="block truncate" title={s.forkedFrom ? `分叉自「${sessions.find((x) => x.id === s.forkedFrom)?.title ?? s.forkedFrom}」` : s.title}>
                    {s.forkedFrom && <GitFork className="mr-1 inline h-3 w-3 text-muted-foreground" />}
                    {s.title}
                  </span>
                  {s.preview && <span className="block truncate text-[11px] text-muted-foreground">{s.preview}</span>}
                </span>
                {s.pinned && <Pin className="h-3 w-3 shrink-0 text-primary" aria-label="已置顶" />}
                {s.archived && <Badge variant="outline" className="shrink-0 px-1 py-0 text-[10px] text-muted-foreground">归档</Badge>}
                {runningMap[s.id] && <Loader2 className="h-3 w-3 shrink-0 animate-spin text-primary" aria-label="生成中" />}
                <span className="hidden shrink-0 items-center gap-0.5 group-hover:flex">
                  <button
                    className="rounded p-0.5 hover:bg-background"
                    title={s.archived ? "取消归档" : "归档此会话"}
                    onClick={(e) => {
                      e.stopPropagation();
                      Chat.ArchiveSession(s.id, !s.archived).then(reloadSessions).catch((e2) => toast.error(`归档失败: ${errText(e2)}`));
                    }}
                  >
                    <Archive className="h-3 w-3" />
                  </button>
                  <button
                    className="rounded p-0.5 hover:bg-background"
                    title={s.pinned ? "取消置顶" : "置顶此会话"}
                    onClick={(e) => {
                      e.stopPropagation();
                      Chat.PinSession(s.id, !s.pinned).then(reloadSessions).catch((e2) => toast.error(`置顶失败: ${errText(e2)}`));
                    }}
                  >
                    {s.pinned ? <PinOff className="h-3 w-3" /> : <Pin className="h-3 w-3" />}
                  </button>
                  <button
                    className="rounded p-0.5 hover:bg-background"
                    title="分叉此会话（复制完整历史为独立新会话）"
                    onClick={(e) => { e.stopPropagation(); forkSession(s); }}
                  >
                    <GitFork className="h-3 w-3" />
                  </button>
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
            {active && (
              <button
                className={cn(
                  "rounded p-1 text-muted-foreground transition-opacity hover:text-foreground",
                  filterOpen ? "opacity-100 text-primary" : "opacity-60 hover:opacity-100",
                )}
                title="在会话内搜索消息（⌘F）"
                onClick={toggleFilter}
              >
                <Search className="h-3.5 w-3.5" />
              </button>
            )}
            {active && (
              <button
                className="rounded p-1 text-muted-foreground opacity-60 transition-opacity hover:text-foreground hover:opacity-100"
                title="重命名会话"
                onClick={() => { setRenaming(active); setRenameText(active.title); }}
              >
                <Pencil className="h-3.5 w-3.5" />
              </button>
            )}
            {active && sessionModels.length > 0 && (
              <Select
                value={active.modelOverride || "__default"}
                onValueChange={(v) => setSessionModel(v)}
              >
                <SelectTrigger
                  className="h-7 w-auto gap-1 border-none bg-muted/50 px-2.5 text-xs font-normal"
                  title="本会话使用的模型（仅影响当前对话）"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__default">Agent 默认模型</SelectItem>
                  {sessionModels.map((m) => (
                    <SelectItem key={m.id} value={m.id}>{m.id}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
          </div>
          <div className="flex items-center gap-2">
            {sessionUsage && sessionUsage.totalTokens > 0 && (
              <Badge variant="outline" className="font-normal" title={`输入 ${sessionUsage.promptTokens} + 输出 ${sessionUsage.completionTokens} tokens`}>
                {sessionUsage.totalTokens.toLocaleString()} tokens{costOf(sessionUsage) && ` · ${costOf(sessionUsage)}`}
              </Badge>
            )}
            {contextUsage && (
              <div
                className="flex items-center gap-1.5"
                title={`上下文窗口：已用 ${sessionUsage?.totalTokens.toLocaleString()} / ${contextUsage.win.toLocaleString()} tokens（${contextUsage.pct.toFixed(1)}%）`}
              >
                <div className="h-1.5 w-20 overflow-hidden rounded-full bg-muted">
                  <div className={`h-full rounded-full transition-all ${contextUsage.color}`} style={{ width: `${contextUsage.pct}%` }} />
                </div>
                <span className="text-[11px] text-muted-foreground">{contextUsage.pct.toFixed(0)}%</span>
              </div>
            )}
            {contextUsage && contextUsage.pct >= 80 && (
              <Badge variant="outline" className="border-destructive/50 font-normal text-destructive">
                上下文即将耗尽（{contextUsage.pct.toFixed(0)}%）
              </Badge>
            )}
            {active && (
              <>
                <Button
                  variant="ghost" size="icon" title="导出为自包含 HTML（样式化渲染）"
                  onClick={exportHTML}
                >
                  <FileCode2 className="h-4 w-4" />
                </Button>
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

        {filterOpen && (
          <div className="flex items-center gap-2 border-b px-4 py-1.5">
            <Search className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
            <input
              ref={filterInputRef}
              value={msgFilter}
              onChange={(e) => setMsgFilter(e.target.value)}
              placeholder="过滤当前会话的消息（匹配文本/工具名/参数/结果）"
              className="h-7 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            />
            {filterQ && (
              <span className="shrink-0 text-xs text-muted-foreground">{filteredMessages.length}/{messages.length} 条</span>
            )}
            <button
              className="shrink-0 rounded p-1 text-muted-foreground hover:text-foreground"
              title="清除并关闭"
              onClick={toggleFilter}
            >
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        )}
        <ScrollArea className="flex-1" viewportRef={scrollRef}>
          <div className="mx-auto max-w-3xl space-y-3 p-4">
            {messages.length > RENDER_LIMIT && (
              <div className="rounded-lg border bg-muted/40 px-3 py-2 text-center text-xs text-muted-foreground">
                已省略较早的 {messages.length - RENDER_LIMIT} 条消息（完整历史已保存，可导出查看）
              </div>
            )}
            {contextUsage && contextUsage.pct >= 80 && !ctxBannerDismissed && (
              <div className="flex items-center justify-between rounded-lg border border-amber-500/50 bg-amber-500/10 px-4 py-2.5 text-sm">
                <span>上下文窗口已使用 {contextUsage.pct.toFixed(0)}%，模型可能开始遗忘早期内容。建议导出会话后开启新对话。</span>
                <div className="flex shrink-0 gap-2">
                  <Button size="sm" variant="outline" onClick={exportSession}>导出 Markdown</Button>
                  <Button size="sm" variant="ghost" onClick={() => setCtxBannerDismissed(true)}>知道了</Button>
                </div>
              </div>
            )}
            {messages.length === 0 && !active && providerCount === 0 && <Onboarding />}
            {messages.length === 0 && !active && providerCount > 0 && (
              // Providers exist but no chat is selected: previously this
              // rendered an empty pane with no hint about what to do.
              <div className="py-24 text-center text-sm text-muted-foreground">
                <div className="mb-2 text-4xl">💬</div>
                还没有对话。点击左上角「新对话」选择 Agent 开始。
              </div>
            )}
            {messages.length === 0 && active && (
              <div className="py-24 text-center text-sm text-muted-foreground">
                <div className="mb-2 text-4xl">💬</div>
                向 <span className="font-medium text-foreground">{targetLabel}</span> 发送第一条消息
              </div>
            )}
            {visibleMessages.map((m, i, arr) => {
              return (
                <MessageBubble
                  key={m.id}
                  m={m}
                  isLastAssistant={m.kind === "assistant" && i === arr.length - 1}
                  onRegenerate={regenerate}
                  canRegenerate={!running && !!activeID}
                  toolRunning={m.kind === "tool_call" ? runningToolsSet.has(m.id) : false}
                  durationMs={toolDurations.get(m.id)}
                  onSpeak={speakMessage}
                  costOf={costOf}
                  onEdit={startEdit}
                  canEdit={editFlags.get(m.id) === true && !running && !!activeID}
                  onConfirm={answerConfirm}
                />
              );
            })}
            <div ref={bottomRef} />
          </div>
        </ScrollArea>

        <div className="border-t p-3">
          {activePlan && (
            <div className="mx-auto mb-2 max-w-3xl">
              <PlanStrip todos={activePlan} />
            </div>
          )}
          {activeID && queued[activeID] && (
            <div className="mx-auto mb-2 flex max-w-3xl items-center gap-2 rounded-lg border border-primary/30 bg-primary/5 px-3 py-1.5 text-xs">
              <span className="min-w-0 flex-1 truncate">已排队：{queued[activeID]}</span>
              <button
                className="shrink-0 underline underline-offset-2 hover:text-foreground"
                onClick={() => setQueued((prev) => {
                  const next = { ...prev };
                  delete next[activeID];
                  return next;
                })}
              >
                取消
              </button>
            </div>
          )}
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
              {slashSuggestions.length > 0 && (
                <div className="absolute bottom-full left-0 right-0 mb-2 flex flex-wrap gap-1.5">
                  {slashSuggestions.map((c) => (
                    <button
                      key={c.cmd}
                      className="rounded-lg border bg-background px-2.5 py-1 text-left text-xs hover:bg-accent"
                      onClick={() => { setInput(c.cmd + " "); inputRef.current?.focus(); }}
                    >
                      <span className="font-mono">{c.cmd}</span>
                      <span className="ml-1.5 text-muted-foreground">{c.desc}</span>
                    </button>
                  ))}
                </div>
              )}
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
                onPaste={(e) => {
                  if (!active || !modelMultimodal) return;
                  const files = Array.from(e.clipboardData.files || []);
                  if (files.length === 0) return;
                  e.preventDefault();
                  addFiles(files);
                }}
                placeholder={active ? "输入消息，Enter 发送，Shift+Enter 换行（⌘K 聚焦）" : "请先创建对话（⌘N）"}
                // Typing stays available while a reply streams (matching the
                // desktops this app mirrors); send() guards the running state.
                disabled={!active}
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
                title={modelMultimodal ? "添加图片附件" : "当前模型未开启多模态（在模型配置中开启）"}
                disabled={!active || running || !modelMultimodal}
                onClick={() => fileRef.current?.click()}
                className={cn(
                  "absolute bottom-2 right-9 flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
                  (!active || running || !modelMultimodal) && "opacity-40",
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
              <Button onClick={() => send()} disabled={!active || (!input.trim() && !running)}>
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
            <>
            <div className="grid grid-cols-2 gap-2 text-sm">
              {[
                ["用户消息", stats.messages],
                ["助手轮次", stats.assistantTurns],
                ["工具调用", stats.toolCalls],
                ["工具失败", stats.toolFailures],
                ["输入 tokens", stats.tokensIn.toLocaleString()],
                ["输出 tokens", stats.tokensOut.toLocaleString()],
                ["总 tokens", stats.tokensTotal.toLocaleString()],
                [statsCost ? "估算成本" : "未配置价格", statsCost || "—"],
              ].map(([k, v], i) => (
                <div key={i} className="rounded-lg border p-2.5">
                  <div className="text-xs text-muted-foreground">{v}</div>
                  <div className="text-sm font-medium">{k}</div>
                </div>
              ))}
            </div>
            {(stats.authors?.length ?? 0) > 1 && (
              <div className="rounded-lg border p-2.5">
                <div className="mb-1 text-xs text-muted-foreground">成员时间线</div>
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-muted-foreground">
                      <th className="text-left font-normal">成员</th>
                      <th className="text-right font-normal">轮次</th>
                      <th className="text-right font-normal">工具调用</th>
                      <th className="text-right font-normal">tokens</th>
                    </tr>
                  </thead>
                  <tbody>
                    {stats.authors!.map((a) => (
                      <tr key={a.author}>
                        <td className="py-0.5">{a.author}</td>
                        <td className="text-right">{a.turns}</td>
                        <td className="text-right">{a.toolCalls}</td>
                        <td className="text-right">{a.tokens.toLocaleString()}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {(stats.daily?.length ?? 0) > 0 && (
              <div className="rounded-lg border p-2.5">
                <div className="mb-1 text-xs text-muted-foreground">按日明细（成本按当前模型价格估算）</div>
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-muted-foreground">
                      <th className="text-left font-normal">日期</th>
                      <th className="text-right font-normal">输入</th>
                      <th className="text-right font-normal">输出</th>
                      <th className="text-right font-normal">估算成本</th>
                    </tr>
                  </thead>
                  <tbody>
                    {stats.daily!.map((d) => (
                      <tr key={d.date}>
                        <td className="py-0.5">{d.date}</td>
                        <td className="text-right">{d.promptTokens.toLocaleString()}</td>
                        <td className="text-right">{d.completionTokens.toLocaleString()}</td>
                        <td className="text-right">¥{d.estimatedCost.toFixed(4)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            </>
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
              <Select value={newType} onValueChange={(v) => { setNewType(v as "agent" | "team" | "remote"); setNewTarget(""); }}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  <SelectItem value="agent">单个 Agent</SelectItem>
                  <SelectItem value="team">Agent 团队</SelectItem>
                  <SelectItem value="remote">远程 Agent（A2A）</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-1.5">
              <label className="text-sm">
                {newType === "agent" ? "选择 Agent" : newType === "team" ? "选择团队" : "选择远程 Agent"}
              </label>
              <Select value={newTarget || undefined} onValueChange={setNewTarget}>
                <SelectTrigger><SelectValue placeholder="请选择" /></SelectTrigger>
                <SelectContent>
                  {newType === "agent"
                    ? agents.map((a) => <SelectItem key={a.id} value={a.id}>{a.name}</SelectItem>)
                    : newType === "team"
                      ? teams.map((t) => <SelectItem key={t.id} value={t.id}>{t.name}</SelectItem>)
                      : remotes.map((r) => <SelectItem key={r.id} value={r.id}>{r.name}</SelectItem>)}
                </SelectContent>
              </Select>
              {((newType === "agent" && agents.length === 0) ||
                (newType === "team" && teams.length === 0) ||
                (newType === "remote" && remotes.length === 0)) && (
                <p className="text-xs text-muted-foreground">
                  没有可选目标，请先在侧边栏创建 {newType === "agent" ? "Agent" : newType === "team" ? "团队" : "远程 Agent（Agents 页）"}。
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

/** TodoItem mirrors the plan_todos tool payload. */
type TodoItem = { content: string; status: string };

/** TodoListCard renders the model's session plan (plan_todos result) as a
 * checklist so multi-step work is legible at a glance. */
function TodoListCard({ resp }: { resp: { [k: string]: any } | null }) {
  const todos: TodoItem[] = Array.isArray(resp?.todos) ? resp.todos : [];
  if (todos.length === 0) return null;
  const done = todos.filter((t) => t.status === "completed").length;
  return (
    <div className="rounded-lg border px-4 py-3">
      <div className="mb-2 flex items-center gap-2">
        <ListTodo className="h-4 w-4 text-primary" />
        <span className="text-sm font-medium">任务计划</span>
        <Badge variant="outline" className="font-normal text-[11px]">
          {done}/{todos.length} 完成
        </Badge>
      </div>
      <ul className="space-y-1.5">
        {todos.map((t, i) => (
          <li key={i} className="flex items-start gap-2 text-sm">
            {t.status === "completed" ? (
              <Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-600" />
            ) : t.status === "in_progress" ? (
              <Loader2 className="mt-0.5 h-3.5 w-3.5 shrink-0 animate-spin text-primary" />
            ) : (
              <span className="mt-0.5 inline-block h-3.5 w-3.5 shrink-0 rounded-full border border-muted-foreground/50" />
            )}
            <span className={cn(
              "break-words",
              t.status === "completed" && "text-muted-foreground line-through",
              t.status === "in_progress" && "font-medium",
            )}>
              {t.content}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** PlanStrip is the compact pinned view of the model's session plan. */
function PlanStrip({ todos }: { todos: TodoItem[] }) {
  const done = todos.filter((t) => t.status === "completed").length;
  const current = todos.find((t) => t.status === "in_progress");
  return (
    <div className="rounded-lg border bg-muted/30 px-3 py-2 text-xs">
      <div className="flex items-center gap-2">
        <ListTodo className="h-3.5 w-3.5 shrink-0 text-primary" />
        <span className="shrink-0 font-medium">任务计划</span>
        <span className="shrink-0 text-muted-foreground">{done}/{todos.length} 完成</span>
        {current && <span className="min-w-0 flex-1 truncate text-muted-foreground">· 当前：{current.content}</span>}
        <div className="ml-auto h-1 w-16 shrink-0 overflow-hidden rounded-full bg-muted">
          <div className="h-full rounded-full bg-primary transition-all" style={{ width: `${(done / todos.length) * 100}%` }} />
        </div>
      </div>
    </div>
  );
}

/** ConfirmCard renders a HITL tool-confirmation request with approve/reject. */
function ConfirmCard({ m, onConfirm }: {
  m: UIMessage;
  onConfirm: (msgId: string, callId: string | undefined, approved: boolean) => void;
  durationMs?: number;
}) {
  const status = m.confirmStatus ?? "pending";
  return (
    <div className={cn(
      "rounded-lg border px-4 py-3",
      status === "pending" && "border-amber-500/50 bg-amber-500/10",
      status === "approved" && "border-emerald-500/40 bg-emerald-500/5",
      status === "rejected" && "border-destructive/40 bg-destructive/5",
    )}>
      <div className="flex items-center gap-2">
        <ShieldAlert className={cn(
          "h-4 w-4 shrink-0",
          status === "pending" && "text-amber-600",
          status === "approved" && "text-emerald-600",
          status === "rejected" && "text-destructive",
        )} />
        <span className="text-sm font-medium">
          {status === "pending" ? "工具执行确认" : status === "approved" ? "已批准执行" : "已拒绝"}
        </span>
        <Badge variant="outline" className="font-mono text-[11px]">{m.toolName}</Badge>
      </div>
      {m.hint && <div className="mt-1.5 text-xs text-muted-foreground">{m.hint}</div>}
      {m.toolArgs && Object.keys(m.toolArgs).length > 0 && (
        <pre className="mt-2 max-h-32 overflow-auto rounded bg-muted/60 p-2 font-mono text-[11px] leading-relaxed text-muted-foreground">
          {JSON.stringify(m.toolArgs, null, 2)}
        </pre>
      )}
      {status === "pending" ? (
        <div className="mt-2.5 flex gap-2">
          <Button size="sm" className="h-7 px-3 text-xs" onClick={() => onConfirm(m.id, m.callId, true)}>
            <Check className="mr-1 h-3 w-3" /> 允许执行
          </Button>
          <Button size="sm" variant="outline" className="h-7 px-3 text-xs" onClick={() => onConfirm(m.id, m.callId, false)}>
            <X className="mr-1 h-3 w-3" /> 拒绝
          </Button>
        </div>
      ) : (
        <div className={cn(
          "mt-2 text-xs",
          status === "approved" ? "text-emerald-600" : "text-destructive",
        )}>
          {status === "approved" ? "模型已继续执行该工具调用" : "模型已收到拒绝结果"}
        </div>
      )}
    </div>
  );
}

/**
 * computeToolDurations pairs tool_result with its call to derive durations.
 * A HITL confirmation parks the run between the call and the result, so the
 * confirmed call's clock restarts at the confirmation card: otherwise the
 * displayed time is how long the human took to click, not how long the tool ran.
 */
function computeToolDurations(messages: UIMessage[]): Map<string, number> {
  const map = new Map<string, number>();
  const pending: { id: string; toolName: string; ts: number }[] = [];
  for (const m of messages) {
    const ts = m.timestamp ? Date.parse(m.timestamp) : Date.now();
    if (m.kind === "confirm") {
      // The paused tool is the most recent call of the same name.
      if (m.timestamp) {
        const idx = [...pending].reverse().findIndex((p) => p.toolName === m.toolName);
        if (idx >= 0) pending[pending.length - 1 - idx].ts = ts;
      }
      continue;
    }
    if (m.kind !== "tool_call" && m.kind !== "tool_result") continue;
    if (m.kind === "tool_call") {
      pending.push({ id: m.id, toolName: m.toolName ?? "", ts });
    } else {
      const idx = [...pending].reverse().findIndex((p) => p.toolName === m.toolName);
      if (idx >= 0) {
        const call = pending.splice(pending.length - 1 - idx, 1)[0];
        map.set(m.id, Math.max(0, ts - call.ts));
      }
    }
  }
  return map;
}

/** computeRunningTools marks tool_call messages that have no matching result yet.
 * Pairing is by tool name (same as computeToolDurations): parallel/团队 tool
 * rounds emit call A, call B, result B, result A — a FIFO queue would end A's
 * spinner early and leave B spinning forever. */
function computeRunningTools(messages: UIMessage[]): Set<string> {
  const running = new Set<string>();
  const pending: { id: string; toolName: string }[] = [];
  for (const m of messages) {
    if (m.kind === "tool_call") {
      running.add(m.id);
      pending.push({ id: m.id, toolName: m.toolName ?? "" });
    } else if (m.kind === "tool_result") {
      const idx = [...pending].reverse().findIndex((p) => p.toolName === (m.toolName ?? ""));
      if (idx >= 0) {
        const call = pending.splice(pending.length - 1 - idx, 1)[0];
        running.delete(call.id);
      }
    }
  }
  return running;
}

/**
 * computeEditFlags marks the user messages that may be edited: only the last
 * one, because editing replaces that turn and everything after it. Doing this
 * per row was O(n^2) on every streamed token.
 */
function computeEditFlags(messages: UIMessage[]): Map<string, boolean> {
  const flags = new Map<string, boolean>();
  let lastUserID: string | null = null;
  for (const m of messages) {
    if (m.kind === "user") lastUserID = m.id;
  }
  if (lastUserID) flags.set(lastUserID, true);
  return flags;
}

// MessageBubble is memoized: streaming appends a new message object for the
// last bubble each token, and every other bubble must keep the same props to
// skip re-rendering. Callers therefore pass stable callbacks and precomputed
// flags instead of per-row closures.
const MessageBubble = memo(function MessageBubble({ m, isLastAssistant, onRegenerate, canRegenerate, toolRunning, onSpeak, costOf, onEdit, canEdit, onConfirm, durationMs }: {
  m: UIMessage;
  isLastAssistant: boolean;
  onRegenerate: () => void;
  canRegenerate: boolean;
  toolRunning: boolean;
  onSpeak: (text: string) => void;
  costOf: (u?: UsageInfo | null) => string;
  onEdit: (text: string) => void;
  canEdit: boolean;
  onConfirm: (msgId: string, callId: string | undefined, approved: boolean) => void;
  durationMs?: number;
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
          <div
            className="whitespace-pre-wrap break-words rounded-2xl rounded-br-md bg-primary px-4 py-2.5 text-sm text-primary-foreground"
            title={fmtTime(m.timestamp)}
          >
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
  if (m.kind === "confirm") {
    return <ConfirmCard m={m} onConfirm={onConfirm} />;
  }
  if (m.kind === "tool_result" && m.toolName === "plan_todos") {
    return <TodoListCard resp={m.toolResp ?? null} />;
  }
  if (m.kind === "tool_call" || m.kind === "tool_result") {
    return (
      <ToolCallCard
        name={m.toolName ?? "tool"}
        args={m.toolArgs}
        resp={m.toolResp}
        running={m.kind === "tool_call" && toolRunning}
        // durationMs comes from the parent's pairing pass; m.durationMs is
        // never populated, so reading it silently hid every timing chip.
        durationMs={durationMs}
      />
    );
  }
  return (
    <div className="group flex justify-start">
      <div className="max-w-[85%]">
        <div className="mb-0.5 flex items-center gap-2">
          <span className="text-[11px] text-muted-foreground">
            {m.author}
            {m.timestamp && <span className="ml-1 opacity-70">· {fmtTime(m.timestamp)}</span>}
          </span>
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
                onClick={() => onSpeak(m.text)}
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
            <div className="whitespace-pre-wrap break-words text-sm">{m.text}</div>
          )}
        </div>
      </div>
    </div>
  );
});
