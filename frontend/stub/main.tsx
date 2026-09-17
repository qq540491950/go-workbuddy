// IPC stub harness: renders the REAL app UI in a plain browser by replacing
// the Wails transport with canned data. Used for visual regression walks of
// new UI (confirm cards, fork button, remote agents, settings cards).
import { setTransport } from "@wailsio/runtime";
import React from "react";
import ReactDOM from "react-dom/client";
import App from "../src/App";
import "../src/index.css";

// ---- seed data ------------------------------------------------------------

const seedConfig = {
  settings: {
    workspaceDir: "/tmp/workbuddy-workspace",
    language: "zh-CN",
    themeMode: "dark",
    accent: "blue",
    fontScale: "md",
    autoSpeak: false,
    embeddingProviderId: "prov_1",
    embeddingModel: "text-embedding-3-small",
  },
  providers: [
    {
      id: "prov_1",
      name: "OpenAI",
      protocol: "openai",
      baseUrl: "https://api.openai.com/v1",
      apiKey: "sk-stub",
      models: [{ id: "gpt-5-mini", multimodal: true, contextWindow: 400000, priceIn: 2, priceOut: 8 }],
      isDefault: true,
    },
  ],
  agents: [
    {
      id: "agent_1",
      name: "写手助手",
      description: "负责文件写入的演示 Agent",
      providerId: "prov_1",
      model: "gpt-5-mini",
      systemPrompt: "你是写手。",
      builtinTools: ["time", "files", "knowledge"],
      confirmTools: ["files"],
      skillIds: [],
      mcpServerIds: [],
    },
  ],
  remoteAgents: [
    {
      id: "ra_1",
      name: "远程研究助手",
      description: "A2A 远程成员演示",
      cardUrl: "http://127.0.0.1:8080",
      createdAt: "2026-09-18T01:00:00Z",
    },
  ],
  teams: [
    {
      id: "team_1",
      name: "混合团队",
      description: "本地 + 远程成员",
      leadAgentId: "",
      autoCoordinate: true,
      memberAgentIds: ["agent_1"],
      remoteMemberIds: ["ra_1"],
      providerId: "prov_1",
      model: "gpt-5-mini",
      systemPrompt: "",
      createdAt: "2026-09-18T01:00:00Z",
    },
  ],
  skills: [],
  mcpServers: [],
  sessions: [
    { id: "sess_confirm", title: "需要确认的写入", targetType: "agent", targetId: "agent_1", createdAt: "2026-09-18T01:00:00Z", updatedAt: "2026-09-18T01:05:00Z" },
    { id: "sess_plain", title: "普通对话", targetType: "team", targetId: "team_1", createdAt: "2026-09-17T09:00:00Z", updatedAt: "2026-09-17T09:10:00Z" },
  ],
};

const histories: Record<string, any[]> = {
  sess_confirm: [
    { id: "m1", kind: "user", author: "user", text: "把周报写到 notes/weekly.md", timestamp: "2026-09-18T01:01:00Z" },
    { id: "m2", kind: "tool_call", author: "写手助手", text: "", toolName: "write_workspace_file", toolArgs: { path: "notes/weekly.md", content: "# 周报" }, timestamp: "2026-09-18T01:01:10Z" },
    {
      id: "m3", kind: "confirm_request", author: "写手助手", text: "",
      toolName: "write_workspace_file", toolArgs: { path: "notes/weekly.md", content: "# 周报" },
      callId: "call-confirm-1", hint: "即将写入工作区文件 notes/weekly.md，请确认。",
      timestamp: "2026-09-18T01:01:20Z",
    },
  ],
  sess_plain: [
    { id: "n1", kind: "user", author: "user", text: "介绍下你自己", timestamp: "2026-09-17T09:00:00Z" },
    { id: "n2", kind: "assistant", author: "混合团队", text: "我们是本地 + 远程成员组成的团队，可以协作完成写作与研究任务。", timestamp: "2026-09-17T09:01:00Z" },
  ],
};

let pendingAnswer = 0;

const handlers: Record<string, (args: any[]) => any> = {
  // GetConfig
  "362021961": () => seedConfig,
  // ListSessions
  "1516353476": () => seedConfig.sessions,
  // GetSessionMessages(sessionID)
  "3662319581": (args) => histories[args[0]] ?? [],
  // IsRunning(sessionID)
  "3574706456": () => false,
  // SessionStats(sessionID)
  "568481502": () => ({ messages: 1, toolCalls: 1, toolFailures: 0, assistantTurns: 0, tokensIn: 120, tokensOut: 80, tokensTotal: 200 }),
  // AppInfo
  "2350821288": () => ({ name: "WorkBuddy Agent", version: "stub", adk: "google adk-go v2" }),
  // ConfirmToolCall(sessionID, callID, approved)
  "3936177345": (args) => {
    pendingAnswer++;
    const approved = args[2] === true;
    histories[args[0]] = [
      ...(histories[args[0]] ?? []),
      { id: `m-answer-${pendingAnswer}`, kind: "confirm_result", author: "user", text: "", callId: args[1], toolResp: { confirmed: approved }, timestamp: "2026-09-18T01:06:00Z" },
      ...(approved
        ? [{ id: `m-result-${pendingAnswer}`, kind: "tool_result", author: "写手助手", text: "", toolName: "write_workspace_file", toolResp: { ok: true, bytes: 6 }, timestamp: "2026-09-18T01:06:01Z" },
           { id: `m-final-${pendingAnswer}`, kind: "assistant", author: "写手助手", text: "已确认并写入 notes/weekly.md。", timestamp: "2026-09-18T01:06:02Z" }]
        : [{ id: `m-reject-${pendingAnswer}`, kind: "assistant", author: "写手助手", text: "好的，已取消写入。", timestamp: "2026-09-18T01:06:02Z" }]),
    ];
    // Stream the confirmation resume flow back into the UI.
    queueMicrotask(() => {
      window.__emit("chat:stream", { sessionId: args[0], kind: "tool_result", author: "写手助手", toolName: "write_workspace_file", toolResp: { ok: true, bytes: 6 } });
      window.__emit("chat:stream", { sessionId: args[0], kind: "message", author: "写手助手", text: approved ? "已确认并写入 notes/weekly.md。" : "好的，已取消写入。" });
      window.__emit("chat:stream", { sessionId: args[0], kind: "done" });
    });
    return null;
  },
  // ListProviderModels
  "2350675726": () => ["gpt-5-mini", "gpt-5"],
  // ConfigService.KnowledgeIndexStats
  "777366651": () => ({ files: 3, chunks: 12, model: "text-embedding-3-small" }),
  // ConfigService.ListKnowledgeFiles
  "1150714691": () => [
    { name: "k8s.md", sizeBytes: 4096, modTime: "2026-09-18 01:00" },
    { name: "weekly.md", sizeBytes: 2048, modTime: "2026-09-17 18:30" },
  ],
  // AppService.StorageStats
  "3266666296": () => ({ sessionsDbBytes: 524288, knowledgeDbBytes: 65536 }),
  // AppService.MemoryStats
  "2169855046": () => ({ count: 7 }),
};

(window as any).__lastCalls = [];
setTransport({
  call: async (_objectID, _method, _windowName, descriptor) => {
    (window as any).__lastCalls.push(descriptor);
    // The descriptor {call-id, methodID, args} arrives as the args slot.
    const opt = (descriptor && typeof descriptor === "object"
      ? descriptor
      : { methodID: descriptor, args: [] }) as { methodID: number | string; args?: any[] };
    const h = handlers[String(opt.methodID)];
    if (!h) return null;
    const list = Array.isArray(opt.args) ? opt.args : opt.args == null ? [] : [opt.args];
    return h(list);
  },
});

// Expose a helper so tests can inject backend events (chat:stream ...).
(window as any).__emit = (name: string, data: unknown) => {
  (window as any)._wails?.dispatchWailsEvent?.({ name, data });
};

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
