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
      models: [
        { id: "gpt-5-mini", multimodal: true, contextWindow: 400000, priceIn: 2, priceOut: 8 },
        { id: "gpt-5-max", multimodal: true, contextWindow: 400000, priceIn: 10, priceOut: 40 },
      ],
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
  projects: [
    { id: "proj_alpha", name: "proj-alpha", rootDir: "/tmp/proj-alpha", createdAt: "2026-10-01T00:00:00Z" },
    { id: "proj_beta", name: "go-workbuddy", rootDir: "D:/projects/go-workbuddy", createdAt: "2026-10-20T00:00:00Z" },
  ],
  sessions: [
    { id: "sess_confirm", title: "需要确认的写入", targetType: "agent", targetId: "agent_1", createdAt: "2026-09-18T01:00:00Z", updatedAt: "2026-09-18T01:05:00Z", modelOverride: "gpt-5-mini", workspaceDir: "/tmp/workbuddy-workspace/tasks/sess_confirm" },
    { id: "sess_plain", title: "普通对话", targetType: "team", targetId: "team_1", createdAt: "2026-09-17T09:00:00Z", updatedAt: "2026-09-17T09:10:00Z" },
    { id: "sess_direct", title: "我如何选择模型呢？", targetType: "", targetId: "", createdAt: "2026-11-01T00:45:00Z", updatedAt: "2026-11-01T00:48:00Z" },
    // Sidebar-grouping fixtures: dates relative to load time so the time
    // buckets (置顶/今天/昨天/7天内/更早) always have members to show.
    { id: "sess_pinned", title: "置顶：本周迭代计划", targetType: "", targetId: "", createdAt: "2026-10-20T09:00:00Z", updatedAt: "2026-10-20T10:00:00Z", pinned: true },
    { id: "sess_yesterday", title: "整理会议纪要", targetType: "", targetId: "", createdAt: iso(-1, 15), updatedAt: iso(-1, 16) },
    { id: "sess_week", title: "分析导出数据", targetType: "agent", targetId: "agent_1", createdAt: iso(-3, 10), updatedAt: iso(-3, 11) },
    { id: "sess_earlier", title: "重构登录流程", targetType: "", targetId: "", createdAt: iso(-15, 9), updatedAt: iso(-15, 10) },
    { id: "sess_archived", title: "旧版方案讨论", targetType: "", targetId: "", createdAt: iso(-30, 9), updatedAt: iso(-30, 10), archived: true },
    { id: "sess_project", title: "跨目录：独立项目任务", targetType: "agent", targetId: "agent_1", createdAt: iso(0, 8), updatedAt: iso(0, 9), workspaceDir: "/tmp/proj-alpha" },
    { id: "sess_projchat", title: "项目内对话：绑定 proj-beta", targetType: "", targetId: "", createdAt: iso(0, 7), updatedAt: iso(0, 7), projectId: "proj_beta" },
    { id: "sess_adhoc", title: "临时目录：未注册工作区", targetType: "", targetId: "", createdAt: iso(0, 6), updatedAt: iso(0, 6), workspaceDir: "/tmp/independent-tools" },
  ],
};

// iso(dayOffset, hour) builds a local-time ISO stamp relative to today.
function iso(dayOffset: number, hour: number): string {
  const d = new Date();
  d.setDate(d.getDate() + dayOffset);
  d.setHours(hour, 0, 0, 0);
  return d.toISOString();
}

const histories: Record<string, any[]> = {
  sess_project: [
    { id: "p1", kind: "user", author: "user", text: "帮我检查这个独立项目的构建产物", timestamp: iso(0, 8) },
    { id: "p2", kind: "assistant", author: "写手助手", text: "这是独立工作区的演示会话（项目分组）。", timestamp: iso(0, 9) },
  ],
  sess_confirm: [
    { id: "m1", kind: "user", author: "user", text: "把周报写到 notes/weekly.md", timestamp: "2026-09-18T01:01:00Z" },
    { id: "m1-plan", kind: "tool_result", author: "写手助手", text: "", toolName: "plan_todos", toolResp: { ok: true, todos: [
      { content: "收集本周素材", status: "completed" },
      { content: "起草周报正文", status: "in_progress" },
      { content: "审阅并写入 weekly.md", status: "pending" },
    ] }, timestamp: "2026-09-18T01:01:05Z" },
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
  sess_direct: [
    { id: "d1", kind: "user", author: "user", text: "我如何选择模型呢？", timestamp: "2026-11-01T00:46:00Z" },
    {
      id: "d2", kind: "assistant", author: "助手", timestamp: "2026-11-01T00:46:20Z",
      text: [
        "选择 AI 模型时，不要只看参数规模，建议按下面几步：",
        "## 1. 先明确任务类型",
        "- **复杂推理、研究、复杂写作**：选能力最强的通用模型。",
        "- **日常问答、分类、摘要、批量处理**：选速度快、价格低的小模型。",
        "- **编程、代码审查**：选代码能力强的模型。",
        "- **图片、语音、视频理解**：选对应的多模态模型。",
        "- **敏感数据或离线使用**：优先考虑可私有部署的本地模型。",
        "## 2. 比较关键指标",
        "1. **效果**：准确性、指令遵循、是否容易出错",
        "2. **速度**：首字响应和完整响应时间",
        "3. **成本**：按 Token 计费时尤其重要",
        "4. **上下文长度**：长文档处理需要更大窗口",
        "5. **工具调用**：Agent 工作流需要稳定的函数/工具调用能力",
        "6. **隐私与合规**：数据是否会被保存或用于训练",
        "7. **稳定性与可用性**：服务限制、速率、版本兼容",
        "## 3. 用自己的测试题验证",
        "准备 20～50 个最接近真实工作的例子，同时比较：",
        "- 答案正确率",
        "- 完成率",
        "- 响应时间",
        "- 单次成本",
        "- 格式和风格是否符合要求",
        "## 4. 实用的选择原则",
        "> 简单任务用便宜快速模型，困难任务用强模型。",
        "例如采用“模型路由”：",
        "- 分类、改写、提取信息 → 小模型",
        "- 复杂分析、方案设计、疑难排错 → 大模型",
        "- 先由小模型处理，检测到困难后再升级",
        "如果你告诉我具体用途，例如写作、编程、客服、图片识别或批量数据处理，我可以直接给你一个模型选择方案。",
      ].join("\n"),
    },
  ],
};

let pendingAnswer = 0;
type TaskSnapshotMode = "success" | "empty" | "error" | "truncated" | "slow";
// Browser walks can request empty/error/truncated/slow states with ?taskMode=.
const stubParams = new URLSearchParams(window.location.search);
const requestedTaskSnapshotMode = stubParams.get("taskMode");
let taskSnapshotMode: TaskSnapshotMode = (["success", "empty", "error", "truncated", "slow"] as const).includes(
  requestedTaskSnapshotMode as TaskSnapshotMode,
) ? requestedTaskSnapshotMode as TaskSnapshotMode : "success";
const workspaceMockMode = stubParams.get("workspaceMode") === "conflict"
  ? "conflict"
  : stubParams.get("workspaceMode") === "error" ? "error" : "success";
const dialogMode = stubParams.get("dialogMode") === "cancel" ? "cancel" : "select";

const handlers: Record<string, (args: any[]) => any> = {
  // GetConfig
  "2830209267": () => seedConfig,
  // ListSessions
  "2944653350": () => {
    (window as any).__lastList = JSON.parse(JSON.stringify(seedConfig.sessions));
    return seedConfig.sessions;
  },
  // GetSessionMessages(sessionID)
  "252791171": (args) => histories[args[0]] ?? [],
  // IsRunning(sessionID)
  "2185013938": () => false,
  // SessionStats(sessionID)
  "4163807688": () => ({ messages: 1, toolCalls: 1, toolFailures: 0, assistantTurns: 0, tokensIn: 120, tokensOut: 80, tokensTotal: 200 }),
  // ArchiveSession(sessionID, archived)
  "2286215193": (args) => {
    const sess = seedConfig.sessions.find((s) => s.id === args[0]);
    if (!sess) throw new Error("会话不存在");
    (sess as any).archived = !!args[1];
    return null;
  },
  // PinSession(sessionID, pinned)
  "3144528906": (args) => {
    const sess = seedConfig.sessions.find((s) => s.id === args[0]);
    if (!sess) throw new Error("会话不存在");
    (sess as any).pinned = !!args[1];
    return null;
  },
  // DeleteSession(sessionID)
  "2756846968": (args) => {
    seedConfig.sessions = seedConfig.sessions.filter((s) => s.id !== args[0]);
    delete histories[args[0]];
    return null;
  },
  // SetSessionModel(sessionID, modelID)
  "359717012": (args) => {
    for (const sess of seedConfig.sessions) {
      if (sess.id === args[0]) sess.modelOverride = args[1] || "";
    }
    return null;
  },
  // NewSessionWithWorkspace(targetType, targetID, title, workspaceDir)
  "1384618270": (args) => {
    if (workspaceMockMode === "conflict") throw new Error("工作区已被任务「需要确认的写入」使用：归档或删除该任务即可释放目录，也可以选择其他目录");
    if (workspaceMockMode === "error") throw new Error("工作区不可写");
    const id = `sess_stub_${seedConfig.sessions.length + 1}`;
    const sess = {
      id,
      title: "新对话",
      targetType: args[0],
      targetId: args[1],
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
      workspaceDir: args[3] || `/tmp/workbuddy-workspace/tasks/${id}`,
    };
    seedConfig.sessions.unshift(sess);
    histories[id] = [];
    return sess;
  },
  // SetSessionWorkspace(sessionID, workspaceDir)
  "185468012": (args) => {
    if (workspaceMockMode === "conflict") throw new Error("工作区已被任务「需要确认的写入」使用：归档或删除该任务即可释放目录，也可以选择其他目录");
    if (workspaceMockMode === "error") throw new Error("工作区不可写");
    const sess = seedConfig.sessions.find((s) => s.id === args[0]);
    if (sess) sess.workspaceDir = args[1];
    return null;
  },
  // SessionWorkspace(sessionID)
  "3713305274": (args) => {
    const sess = seedConfig.sessions.find((s) => s.id === args[0]);
    if (!sess) throw new Error("会话不存在");
    const project = seedConfig.projects.find((p: any) => p.id === (sess as any).projectId);
    return sess.workspaceDir || project?.rootDir || seedConfig.settings.workspaceDir;
  },
  // ListProjects
  "2203540805": () => seedConfig.projects,
  // SaveProject(id, name, rootDir)
  "2762009531": (args) => {
    const [id, name, rootDir] = args;
    if (!id) {
      const proj = {
        id: `proj_stub_${seedConfig.projects.length + 1}`,
        name: name || rootDir.split("/").pop(),
        rootDir,
        createdAt: new Date().toISOString(),
      };
      seedConfig.projects.push(proj);
      return proj;
    }
    const proj = seedConfig.projects.find((p: any) => p.id === id);
    if (!proj) throw new Error("项目不存在");
    if (name) proj.name = name;
    if (rootDir) proj.rootDir = rootDir;
    return proj;
  },
  // DeleteProject(id)
  "911459121": (args) => {
    seedConfig.projects = seedConfig.projects.filter((p: any) => p.id !== args[0]);
    for (const sess of seedConfig.sessions) {
      if ((sess as any).projectId === args[0]) delete (sess as any).projectId;
    }
    return null;
  },
  // NewSessionWithProject(targetType, targetID, title, projectID)
  "3614387014": (args) => {
    const id = `sess_stub_${seedConfig.sessions.length + 1}`;
    const sess = {
      id,
      title: "新对话",
      targetType: args[0],
      targetId: args[1],
      createdAt: new Date().toISOString(),
      updatedAt: new Date().toISOString(),
      projectId: args[3],
    };
    seedConfig.sessions.unshift(sess);
    histories[id] = [];
    return sess;
  },
  // SetSessionProject(sessionID, projectID)
  "3134520676": (args) => {
    const sess = seedConfig.sessions.find((s) => s.id === args[0]);
    if (!sess) throw new Error("会话不存在");
    (sess as any).projectId = args[1] || undefined;
    delete sess.workspaceDir;
    return null;
  },
  // ArtifactService.TaskWorkspaceSnapshot(sessionID)
  "57605569": async (args) => {
    if (taskSnapshotMode === "slow") {
      await new Promise((resolve) => setTimeout(resolve, 700));
    }
    if (taskSnapshotMode === "error") throw new Error("任务工作区不可读");
    const sessions = seedConfig.sessions as Array<{ id: string; workspaceDir?: string }>;
    const workspace = sessions.find((s) => s.id === args[0])?.workspaceDir || seedConfig.settings.workspaceDir;
    const base = {
      sessionId: args[0],
      workspace,
      discovered: 0,
      added: 0,
      modified: 0,
      deleted: 0,
      truncated: false,
      baselineAt: "2026-09-18T01:00:00Z",
      refreshedAt: new Date().toISOString(),
    };
    if (taskSnapshotMode === "empty") {
      return { ...base, files: [], artifacts: [], changes: [] };
    }
    if (taskSnapshotMode === "truncated") {
      return {
        ...base,
        files: [
          { name: "huge.log", path: "logs/huge.log", kind: "text", size: 532480, modTime: "2026-09-18T01:08:00Z", previewable: true },
        ],
        artifacts: [],
        changes: [],
        discovered: 5001,
        truncated: true,
      };
    }
    return {
      ...base,
      files: [
      { name: "report.md", path: "output/report.md", kind: "markdown", size: 7168, modTime: "2026-09-18T01:07:00Z", previewable: true },
      { name: "weekly.md", path: "notes/weekly.md", kind: "markdown", size: 2048, modTime: "2026-09-18T01:06:00Z", previewable: true },
      { name: "chart.png", path: "assets/chart.png", kind: "image", size: 40960, modTime: "2026-09-18T01:05:00Z", previewable: true },
      { name: "archive.zip", path: "archive.zip", kind: "binary", size: 524288, modTime: "2026-09-17T09:00:00Z", previewable: false },
      ],
      artifacts: [
      { name: "report.md", path: "output/report.md", kind: "markdown", size: 7168, modTime: "2026-09-18T01:07:00Z", previewable: true, status: "added" },
      { name: "weekly.md", path: "notes/weekly.md", kind: "markdown", size: 2048, modTime: "2026-09-18T01:06:00Z", previewable: true, status: "modified" },
      ],
      changes: [
      { name: "report.md", path: "output/report.md", kind: "markdown", size: 7168, modTime: "2026-09-18T01:07:00Z", previewable: true, status: "added" },
      { name: "weekly.md", path: "notes/weekly.md", kind: "markdown", size: 2048, modTime: "2026-09-18T01:06:00Z", previewable: true, status: "modified" },
      { name: "old.txt", path: "notes/old.txt", kind: "text", size: 512, modTime: "2026-09-17T09:00:00Z", previewable: true, status: "deleted" },
      ],
      discovered: 4,
      added: 1,
      modified: 1,
      deleted: 1,
    };
  },
  // ArtifactService.OpenForSession(sessionID, path)
  "3670798310": (args) => ({
    name: String(args[1]).split("/").pop(),
    path: args[1],
    kind: args[1].endsWith(".png") ? "image" : "markdown",
    mime: args[1].endsWith(".png") ? "image/png" : "text/markdown",
    size: 2048,
    text: args[1].endsWith(".png") ? undefined : "# 周报\n\n任务文件预览。",
  }),
  // AppInfo
  "2925845021": () => ({ name: "WorkBuddy Agent", version: "stub", adk: "google adk-go v2" }),
  // ConfirmToolCall(sessionID, callID, approved)
  "3242111371": (args) => {
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
  "729587208": () => ["gpt-5-mini", "gpt-5"],
  // ConfigService.KnowledgeIndexStats
  "2250008310": () => ({ files: 3, chunks: 12, model: "text-embedding-3-small" }),
  // ConfigService.ListKnowledgeFiles
  "3858202904": () => [
    { name: "k8s.md", sizeBytes: 4096, modTime: "2026-09-18 01:00" },
    { name: "weekly.md", sizeBytes: 2048, modTime: "2026-09-17 18:30" },
  ],
  // AppService.StorageStats
  "1091222926": () => ({ sessionsDbBytes: 524288, knowledgeDbBytes: 65536 }),
  // AppService.MemoryStats
  "1364444048": () => ({ count: 7 }),
};

(window as any).__lastCalls = [];
(window as any).__seedConfig = seedConfig;
setTransport({
  call: async (_objectID, _method, _windowName, descriptor) => {
    if (_objectID === 5 && _method === 4) {
      return dialogMode === "cancel" ? "" : "/tmp/workbuddy-picked";
    }
    (window as any).__lastCalls.push(descriptor);
    // The descriptor {call-id, methodID, args} arrives as the args slot.
    const opt = (descriptor && typeof descriptor === "object"
      ? descriptor
      : { methodID: descriptor, args: [] }) as { methodID: number | string; args?: any[] };
    const h = handlers[String(opt.methodID)];
    if (!h) return null;
    const list = Array.isArray(opt.args) ? opt.args : opt.args == null ? [] : [opt.args];
    // Deep-copy results like real IPC serialization does: returning shared
    // references lets in-place stub mutations slip past React state updates
    // (same-reference setSessions bails out) and shows stale lists.
    const result = h(list);
    return result == null ? result : structuredClone(result);
  },
});

// Expose a helper so tests can inject backend events (chat:stream ...).
(window as any).__emit = (name: string, data: unknown) => {
  (window as any)._wails?.dispatchWailsEvent?.({ name, data });
};
(window as any).__setTaskSnapshotMode = (mode: TaskSnapshotMode) => {
  taskSnapshotMode = mode;
};

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
