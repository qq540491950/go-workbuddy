# WorkBuddy Agent

基于 **Google ADK Go v2**（`google.golang.org/adk/v2`）+ **Wails v3** + **React/TypeScript + shadcn/ui** 构建的类 WorkBuddy Agent 桌面应用。

支持：可配置模型供应商、自定义 Agent、Agent 团队（ADK 子代理委派）、Skills（agentskills.io SKILL.md 规范）、MCP 工具服务器、流式对话、会话持久化（SQLite）。

![tech](https://img.shields.io/badge/ADK-Go%20v2-blue) ![tech](https://img.shields.io/badge/Wails-v3-green) ![tech](https://img.shields.io/badge/ui-shadcn%2Fui-black)

## 功能总览

| 模块 | 说明 |
| --- | --- |
| 模型配置 | 支持 OpenAI 兼容协议（OpenAI / DeepSeek / Kimi / Qwen / Ollama / vLLM…）、Anthropic（Claude）、Google Gemini 三种协议；可配 Base URL、API Key、模型列表（多模态/上下文窗口/价格元数据，可从 /models 拉取）、默认供应商，并一键测试连通性 |
| Agents | 自定义名称、描述、系统提示词、Temperature / MaxTokens / 停止序列，挂载 Skills、MCP 工具与内置工具（时间查询、工作区文件读写、知识库检索、长期记忆、任务计划 plan_todos）；安全护栏（密钥脱敏/注入检测）；运行护栏（单轮超时/工具调用上限）；写入文件 HITL 人工确认；单个导出/导入分享 |
| 远程 Agent | A2A 协议（官方 a2a-go）：连接远程 Agent 直接对话（Agent Card 自动发现 + 连通性检查），也可作为团队成员参与协作；本应用亦可作为 A2A 服务端对外发布本地 Agent |
| Agent 团队 | 多 Agent 协作：自动协调员模式（系统合成协调员分派任务）或指定负责人模式；基于 ADK 的 agent 转移机制（transfer）实现委派；支持远程 A2A 成员 |
| Skills | 可复用技能指令包，保存为标准 SKILL.md（YAML frontmatter + Markdown），由 ADK Skill Toolset 以渐进式披露方式供模型按需加载；支持从工作区 skills/ 目录批量导入 |
| MCP 服务 | 通过 stdio（本地命令）或 Streamable HTTP（远程）连接 MCP 服务器，发现并列出工具，挂载到 Agent；远程服务器支持 Bearer / API Key / OAuth 2.1（授权码 + PKCE，令牌持久化静默刷新）认证 |
| 知识库 | 工作区 knowledge/ 目录检索：可配置 Embedding 供应商升级为「向量语义 + 关键词」混合检索（本地增量向量索引，SQLite） |
| 对话 | 流式输出（SSE）、工具调用/结果实时展示、团队对话中显示各成员输出、随时停止、工具执行人工确认（HITL）、会话置顶/分叉/并行运行、后台完成提示、上下文窗口占用指示与高水位提醒、Token 用量与成本（含按日明细）、任务计划清单卡与置顶进度条、会话级模型选择、生成期间排队发送、会话内消息过滤（⌘F）、图片粘贴/拖放/重生成保留附件、Markdown 导出、会话历史持久化（ADK Database Session Service + 纯 Go SQLite 驱动） |
| 桌面集成 | 系统托盘（显示/隐藏/退出）、全局快捷键（⌘⇧Space 唤起）、深浅主题 / 强调色 / 字号、语音输入与朗读 |
| 设置与迁移 | 工作区目录、外观、语义检索 Embedding 配置、智能会话命名（可选）、A2A 服务启停、配置备份导出/导入（版本化、可脱敏）、Agent 分享 |

## 架构

```
┌────────────────────────── macOS / Windows / Linux ──────────────────────────┐
│  Wails v3 桌面壳                                                            │
│  ┌────────────── React + TS + shadcn/ui (Tailwind v4) ───────────────────┐  │
│  │  对话 / Agents / 团队 / Skills / MCP / 模型配置 / 设置                  │  │
│  └───────────────▲───────────────────────────────────────────────────────┘  │
│                  │ 绑定调用 (wails3 generate bindings) + 事件流 (chat:stream) │
│  ┌───────────────┴──────────────── Go 后端 ──────────────────────────────┐  │
│  │  services: ConfigService / ChatService / MCPService / AppService      │  │
│  │  agentkit: model.LLM 适配器(OpenAI 兼容 / Anthropic / Gemini)          │  │
│  │            llmagent 构建、内置 FunctionTool、Skill Toolset             │  │
│  │  mcpmgr:   MCP 服务器生命周期与状态（ADK mcptoolset + go-sdk）          │  │
│  │  config:   JSON 配置存储；ADK session/database + SQLite 会话持久化      │  │
│  └───────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────┘
```

关键点：

- **模型可配置**：ADK 的 `model.LLM` 是接口，本项目自行实现了 OpenAI Chat Completions（流式 + 函数调用）与 Anthropic Messages（流式 + tool use）两个适配器，Gemini 使用 ADK 官方 `model/gemini`，因此任意兼容供应商都能接入。
- **Agent 团队**：团队成员是标准 `llmagent`，由 ADK 注入 `transfer_to_agent` 能力实现相互委派；自动协调员模式额外合成一位协调员作为根节点。
- **Skills**：保存时物化为 `<配置目录>/skills/<name>/SKILL.md`，构建 Agent 时通过 `skilltoolset` 挂载。
- **会话持久化**：ADK 事件（含工具调用）完整存入 SQLite（`sessions.db`），重启后可回看历史。

## 快速开始

### 环境要求

- Go 1.26+（以 `go.mod` 为准）
- Node.js ≥ 20.19 + npm（Vite 8 与 `@vitejs/plugin-react` 要求）
- [Wails v3 CLI](https://v3.wails.io/getting-started/installation/)，与 Go SDK 保持同一版本：

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.20
```

macOS 需要 Xcode Command Line Tools；Linux 需要 GTK / WebKit 开发包。

### 开发运行

```bash
wails3 dev
```

### 构建与打包

```bash
# 生产构建（生成绑定 → 构建前端 → 编译 Go）
wails3 build          # 产物: bin/go-wails-react-tpl

# 打包 macOS .app
wails3 package        # 产物: bin/go-wails-react-tpl.app（重命名为 WorkBuddy Agent.app）
```

### 自测

```bash
go test ./internal/... .
go vet ./internal/... .
cd frontend && npm test        # vitest（前端纯函数单测）
```

测试覆盖：

- OpenAI 兼容适配器：SSE 流式文本、函数调用往返（含 `tool` 消息回传）、HTTP 错误
- Anthropic 适配器：SSE 流式文本 + `input_json_delta` 工具参数解析
- 配置存储：默认种子数据、持久化往返、损坏文件自愈
- 工作区路径安全：防目录穿越
- 端到端：ChatService → ADK Runner → llmagent → 假 SSE 模型服务器 → 内置时间工具调用循环 → SQLite 会话持久化

## 使用流程

1. **模型配置** → 新增供应商（例：协议选 OpenAI 兼容，Base URL 填 `https://api.deepseek.com/v1`，填 API Key，可点「拉取」获取模型列表），点击「测试」验证连通（显示延迟）。
2. **Agents** → 新增 Agent（选择供应商与模型、编写系统提示词（支持 `{{date}}` 等变量）、按需挂载 Skills / MCP / 内置工具；可开启「写入文件需人工确认」与运行护栏）；也可「导入 Agent」分享文件。
3. **远程 Agent（可选）** → Agents 页「远程 Agent（A2A 协议）」填对方服务地址，「检查连接」自动发现 Agent Card。
4. **Skills / MCP 服务** → 维护可复用技能（可从工作区 skills/ 批量导入）与外部工具（stdio 或 Streamable HTTP，远程服务器支持 Bearer / API Key / OAuth 2.1 认证）。
5. **Agent 团队**（可选）→ 选成员组队（可含远程成员），开启自动协调员或指定负责人。
6. **对话** → 新对话选择 Agent / 团队 / 远程 Agent，开始对话；支持斜杠命令（`/export` `/stats` `/todos` `/new`…）、消息时间戳、工具耗时、HITL 确认卡片、上下文占用指示；会话级模型切换（头部下拉）、生成期间可继续输入并排队、⌘F 会话内搜索；多个会话可并行运行，后台完成会提示（点击「查看」直达），侧栏显示未读点。
7. **设置** → 外观/工作区/语义检索（Embedding）/A2A 服务发布/备份迁移/诊断日志。


## 视觉测试与体验设计

UI 通过「浏览器 + 数据桩」做视觉回归：前端以 `wails3 dev` 提供的页面 + 注入的 IPC mock 数据桩，在真实浏览器中逐页走查（对话/Agents/团队/Skills/MCP/模型/设置、深浅主题、空态、对话框）。

逐轮的迭代依据、实现要点与测试证据记录在 [docs/iteration-log.md](docs/iteration-log.md)。

## 质量与验证

```bash
./scripts/verify.sh      # 前端构建 → go vet → 测试 → -race → wails3 build
```

CI（`.github/workflows/ci.yml`）在每次 push / PR 上跑后端 `vet + test + -race` 与前端 `tsc + build`。注意 workflow 会先构建前端：`main.go` 用 `//go:embed all:frontend/dist` 嵌入产物，干净检出时该目录不存在。

测试覆盖（`go test ./internal/... .`，156 个用例，`-race` 全绿）：

- 两个模型适配器：SSE 流式文本、函数调用往返（含 `tool` 消息回传）、HTTP 错误与重试
- 配置存储：深拷贝隔离、默认种子、持久化往返、损坏文件自愈、脱敏导出不污染运行态
- 端到端：ChatService → ADK Runner → llmagent → 假 SSE 模型服务器 → 内置工具调用循环 → SQLite 持久化
- 稳定性：并发起停与取消、跨轮次取消登记、知识索引增量与过期行清理、路径穿越与符号链接逃逸、技能物化隔离与原子写、配置更新原子提交、记忆并发去重、任务计划跨轮回读
- 能力：plan_todos 任务计划（清单卡 + 置顶进度条）、停止序列透传、429/Retry-After 重试、消息排队、附件即时渲染与重生保留

## 发布就绪检查（第 29/42/63 轮多轮收官验证，持续维护）

- 后端全量测试（agentkit / config / mcpmgr / memory / services）+ `-race` 全部通过
- 前端 `tsc` 严格类型检查 + 生产构建零错误
- `wails3 build` 生产打包成功，启动烟雾通过（进程存活、窗口创建、托盘/快捷键注册无报错、干净退出）
- 浏览器视觉回归（`frontend/stub/`）覆盖确认卡片交互、远程 Agent 管理、团队拓扑、设置页全部新卡片与深色主题
- 双子代理代码审查（Standards + Spec）已执行并修复全部硬性发现
- 一键复检：`./scripts/verify.sh` → `VERIFY: ALL GREEN`

## 生产使用

### 打包与发布
```bash
wails3 build && wails3 package   # 产物 bin/WorkBuddy Agent.app
```
- 版本号统一维护在 `main.go` 的 `Version` 常量，经 AppInfo 暴露到界面
- 构建使用 `-tags production -trimpath -ldflags "-w -s"`

### 稳定性与自愈
- 单轮默认 10 分钟超时兜底（Agent 可自定义 `MaxTurnSeconds`），供应商无响应不会永久挂起
- 运行期 panic 被 recover 并转为界面错误事件，应用不崩溃
- 会话数据库损坏时启动自动备份（`sessions.db.corrupt-<时间戳>`）并重建，保证可启动
- 全部后端路径有单测覆盖，`internal/...` 通过 `-race`

### 安全
- `config.json`（含 API Key）权限 0600；API Key 不出本机（仅直连所配置的供应商）
- 模型输出经 react-markdown 渲染，默认不执行原始 HTML；链接新窗口打开
- 产物预览严格限制在工作区目录内（目录穿越防护、大小上限）

### 运维与诊断
- 日志：`~/Library/Application Support/workbuddy-agent/logs/app.log`（5MB 自动轮转）
- 故障排查：先看 app.log；会话库异常时检查 `sessions.db.corrupt-*` 备份
- 数据备份：直接复制 `~/Library/Application Support/workbuddy-agent/`（配置 + 会话 + Skills）

### 已知边界
- 语音识别依赖 Web Speech API（macOS WKWebView 可用性随系统版本变化，不支持时按钮自动禁用）
- 长会话渲染上限 200 条（完整历史已持久化，可导出 Markdown 查看）
- MCP OAuth 令牌缓存在本机 config.json（0600），不随任何导出携带；丢失后重新授权即可
- 团队负责人必须为本地 Agent（远程 A2A 成员仅作为可转移的子代理参与）
- HTTP 代理设置在启动时注入，修改后需重启应用生效

## 数据存储

| 内容 | 位置（macOS） |
| --- | --- |
| 配置（供应商/Agent/团队/Skills/MCP/会话元数据） | `~/Library/Application Support/workbuddy-agent/config.json` |
| Skills 物化目录 | `~/Library/Application Support/workbuddy-agent/skills/` |
| ADK 会话历史（SQLite） | `~/Library/Application Support/workbuddy-agent/sessions.db` |

## 目录结构

```
main.go                    # 应用入口：服务注册、窗口、托盘+全局快捷键、SQLite 会话库、
                           # 知识索引、代理注入、A2A 自启动
internal/
  config/                  # 配置模型与 JSON 存储（0600；含 MCP OAuth 令牌缓存、默认 Skills 种子）
  agentkit/                # ADK 集成核心
    llm.go                 #   model.LLM 适配器（OpenAI 兼容 / Anthropic）
    kit.go                 #   Agent/团队构建、内置工具装配、提示词模板变量、SanitizeAgentName
    semantic.go            #   知识库向量索引（SQLite 增量）+ OpenAI 兼容 /embeddings 客户端
    knowledge.go           #   search_knowledge 工具（语义+关键词混合检索）
    remote.go              #   A2A 远程 Agent（官方 a2a-go）+ Agent Card 解析
    guardrail.go           #   密钥脱敏 / 提示注入检测
  mcpmgr/                  # MCP 服务器生命周期（连接状态、工具发现）
    auth.go                #   Bearer / API Key / OAuth 2.1 授权码+PKCE 传输层
  memory/                  # 长期记忆（ADK memory.Service，SQLite）
  services/                # Wails 服务（前端可调用 RPC）
    chatservice.go         #   会话/对话核心：runTurn 共享引擎、HITL 确认、分叉、并行、护栏
    a2aservice.go          #   A2A 服务端（对外发布本地 Agent）
    app.go                 #   AppInfo、配置备份导出/导入、Agent 分享、日志诊断
frontend/
  src/pages/               # 对话 / Agents(含远程) / 团队 / Skills / MCP / 模型 / 设置
  src/components/ui/       # shadcn/ui 组件
  src/components/ToolCallCard.tsx  # 工具卡片 + 产物预览 + 路径提取
  stub/                    # 浏览器视觉回归桩（setTransport IPC 数据桩，npm run stub）
  bindings/                # wails3 生成的 TS 绑定（构建时自动再生成，勿手改）
build/                     # 各平台打包资源（Info.plist / 图标 / Taskfile）
AGENTS.md                  # 面向编码代理的工程指南（命令/机制/约定）
```
