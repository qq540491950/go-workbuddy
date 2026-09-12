# WorkBuddy Agent

基于 **Google ADK Go v2**（`google.golang.org/adk/v2`）+ **Wails v3** + **React/TypeScript + shadcn/ui** 构建的类 WorkBuddy Agent 桌面应用。

支持：可配置模型供应商、自定义 Agent、Agent 团队（ADK 子代理委派）、Skills（agentskills.io SKILL.md 规范）、MCP 工具服务器、流式对话、会话持久化（SQLite）。

![tech](https://img.shields.io/badge/ADK-Go%20v2-blue) ![tech](https://img.shields.io/badge/Wails-v3-green) ![tech](https://img.shields.io/badge/ui-shadcn%2Fui-black)

## 功能总览

| 模块 | 说明 |
| --- | --- |
| 模型配置 | 支持 OpenAI 兼容协议（OpenAI / DeepSeek / Kimi / Qwen / Ollama / vLLM…）、Anthropic（Claude）、Google Gemini 三种协议；可配 Base URL、API Key、模型列表、默认供应商，并一键测试连通性 |
| Agents | 自定义名称、描述、系统提示词、Temperature / MaxTokens，挂载 Skills、MCP 工具与内置工具（时间查询、工作区文件读写） |
| Agent 团队 | 多 Agent 协作：自动协调员模式（系统合成协调员分派任务）或指定负责人模式；基于 ADK 的 agent 转移机制（transfer）实现委派 |
| Skills | 可复用技能指令包，保存为标准 SKILL.md（YAML frontmatter + Markdown），由 ADK Skill Toolset 以渐进式披露方式供模型按需加载 |
| MCP 服务 | 通过 stdio（本地命令）或 Streamable HTTP（远程）连接 MCP 服务器，发现并列出工具，挂载到 Agent；连接由 ADK MCP Toolset（go-sdk）管理，支持自动重连 |
| 对话 | 流式输出（SSE）、工具调用/结果实时展示、团队对话中显示各成员输出、随时停止、会话历史持久化（ADK Database Session Service + 纯 Go SQLite 驱动） |
| 设置 | 工作区目录（限制文件工具的读写范围）等全局配置 |

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

- Go ≥ 1.25（ADK Go v2 要求）
- Node.js ≥ 18 + npm
- [Wails v3 CLI](https://v3.wails.io/getting-started/installation/)：

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
```

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
go test ./internal/...
go vet ./internal/... .
```

测试覆盖：

- OpenAI 兼容适配器：SSE 流式文本、函数调用往返（含 `tool` 消息回传）、HTTP 错误
- Anthropic 适配器：SSE 流式文本 + `input_json_delta` 工具参数解析
- 配置存储：默认种子数据、持久化往返、损坏文件自愈
- 工作区路径安全：防目录穿越
- 端到端：ChatService → ADK Runner → llmagent → 假 SSE 模型服务器 → 内置时间工具调用循环 → SQLite 会话持久化

## 使用流程

1. **模型配置** → 新增供应商（例：协议选 OpenAI 兼容，Base URL 填 `https://api.deepseek.com/v1`，填 API Key，模型列表填 `deepseek-chat`），点击「测试」验证连通。
2. **Agents** → 新增 Agent（选择供应商与模型、编写系统提示词、按需挂载 Skills / MCP / 内置工具）。
3. **Skills / MCP 服务** → 维护可复用技能与外部工具（例：stdio 服务 `npx -y @modelcontextprotocol/server-everything`，点「连接」发现工具）。
4. **Agent 团队**（可选）→ 选成员组队，开启自动协调员或指定负责人。
5. **对话** → 新对话选择 Agent 或团队，开始对话；工具调用与团队成员输出会实时展示。

## 视觉测试与体验设计

本项目的 UI 通过「浏览器 + 数据桩」方式做视觉回归：前端以 `wails3 dev` 提供的页面 + 注入的 IPC mock 数据桩，在真实浏览器中逐页走查截图（对话/Agents/团队/Skills/MCP/模型/设置、深浅主题、空态、对话框）。

第一轮视觉测试发现并已修复的缺陷：

| 缺陷 | 修复 |
| --- | --- |
| 助手消息 Markdown 原样显示（`##`/`**`/代码围栏） | 引入 react-markdown + remark-gfm，新增 `MessageMarkdown` 组件：标题/列表/表格/引用全量渲染 |
| 代码块无高亮容器、无法复制 | 代码块渲染为带语言标签和「复制」按钮的面板（对齐 Codex 体验） |
| 绑定调用失败时 toast 只显示 "Error" | `errText()` 统一透传 Wails RuntimeError.message / cause 到所有提示 |
| 页面数据加载失败静默空白 | 会话/配置加载失败均有可见 toast |
| 无消息复制操作 | 用户/最后一条助手消息悬停显示复制按钮 |
| 硬编码深色主题 | 侧栏底部主题切换（浅/深），localStorage 持久化 |
| 首次运行无引导 | 无供应商时对话页显示三步引导卡片（可点击跳转） |
| 会话多时难以查找 | 会话列表新增搜索框；标题完整提示（title） |
| 输入框无快捷键 | ⌘N 新对话、⌘K 聚焦输入框（占位符内提示） |

### 参考 Codex / Claude Desktop / WorkBuddy 的设计对齐

已实施：Markdown + 代码块复制、消息级复制、深浅主题、首次运行引导、会话搜索、快捷键、工具调用折叠面板、流式打字指示器。

### 第五轮迭代（已实施）

| 功能 | 实现 |
| --- | --- |
| 图片附件（视觉模型输入） | 全栈：`Send` 支持 ≤4 张图片附件（单张 6MB，png/jpeg/webp/gif）；OpenAI 兼容适配器转为 content 数组 `image_url`（data URL），Anthropic 转为 base64 image block；前端支持 📎 选择与拖拽投放、缩略图 chips 可移除；用户消息气泡渲染附件缩略图，历史回放同步显示 |
| 团队拓扑可视化 | 团队卡片内渲染层级图：协调员/负责人在顶部，成员以连线展开；自动协调模式与指定负责人模式分别展示 |

多轮测试：适配器图片转换单测 ×2（OpenAI image_url / Anthropic image block）、附件全链路 e2e（模型收到图片 payload、历史回放 data URL、非法类型拒绝）、32 并发/畸形 SSE/取消安全回归、`-race` 通过；浏览器视觉回归（附件缩略图渲染、📎/🎤 按钮、团队拓扑层级图）；打包烟雾通过。

### 第四轮迭代（已实施）

| 功能 | 实现 |
| --- | --- |
| 编辑消息重发 | 用户消息悬停「编辑」→ 输入框进入编辑模式（横幅提示、重发按钮、可取消），发送调用 `EditAndResend` 裁剪最后一轮并重跑新文本 |
| 成本估算 | 供应商可配置输入/输出价格（¥/1M tokens），对话中按单条回复与会话累计 token 自动折算成本（未配置价格则不显示） |
| 运行中可继续输入 | 修复测试中发现的交互问题：回复生成时输入框不再禁用，可提前输入下一条（占位符提示），发送按钮单独禁用 |
| 稳定性测试 | 32 并发配置写入无丢失、畸形 SSE（坏 JSON/超长行/空 choices）不中断且用量不丢、慢响应取消后无残留运行态；services 包 `-race` 通过 |

后续路线图（按优先级）：消息分支树、附件上传（ADK Artifact）、团队拓扑可视化、MCP OAuth、系统托盘、多会话并行、自动更新、配置导入导出。

### 第三轮迭代（已实施）

| 功能 | 实现 |
| --- | --- |
| 语音输入 | 对话输入框内置麦克风按钮（Web Speech API，`SpeechRecognition`，中文 `zh-CN`，支持中间结果实时上屏）；不支持的环境自动禁用并提示 |
| 语音朗读（TTS） | 助手回复悬停「朗读」按钮（`speechSynthesis`，自动剥离 Markdown 标记）；设置页「自动朗读回复」开关，开启后每轮回复完成自动朗读 |
| 工具调用可视化 | `ToolCallCard` 组件：调用中（旋转指示）/ 完成（绿色对勾）/ 失败（红色叉）三态、参数摘要 chips、可展开完整 JSON、运行时实时刷新 |
| 产物预览 | `ArtifactService`（Go）安全读取工作区文件（目录穿越防护、12MB 二进制 / 512KB 文本上限、UTF-8 校验）；工具参数/结果中出现的文件路径自动出现「预览」按钮，`ArtifactPreview` 对话框支持图片、PDF（内嵌渲染）、Markdown（完整渲染）与文本/代码 |
| 夜间模式增强 | 主题模式三选：浅色 / 深色（夜间）/ 跟随系统（监听 OS 外观变化实时切换）；侧栏快捷切换按钮同步持久化 |
| 自定义主题 | 6 种强调色（默认蓝/紫罗兰/祖母绿/暖橙/玫红/青蓝）实时生效（CSS 变量覆盖 primary/ring/sidebar）；正文字号三档（小/中/大）；外观全部持久化到配置文件并在启动时恢复 |

多轮测试记录：① 后端单测全量（含 ArtifactService 4 项新测试：图片 base64、Markdown 读取、目录穿越/二进制拒绝、列表过滤）；② 浏览器视觉回归（工具卡片三态、图片/Markdown 预览、强调色/浅色/字号实时切换、保存、语音按钮存在性）；③ 生产构建打包 + 启动烟雾测试（进程存活 + 配置 schema 自动迁移落盘）。

### 第二轮迭代（已实施）

| 功能 | 实现 |
| --- | --- |
| Token 用量统计 | 两个自研适配器（OpenAI 兼容 `stream_options.include_usage` / Anthropic `message_start+message_delta`）提取用量到 ADK `UsageMetadata`；`ChatStreamEvent` 携带单轮 `usage` 与会话累计 `sessionUsage`；前端在助手消息下方与对话顶栏展示 |
| 重新生成 | `ChatService.Regenerate`：定位最后一条用户消息，通过共享 gorm 连接裁剪其后所有 ADK 事件，再原样重跑；前端在最后一条回复上提供「重新生成」按钮 |
| 会话导出 | `ChatService.ExportSession` 把整段对话导出为 Markdown（含工具调用记录）到工作区 `exports/`；对话顶栏下载按钮一键导出 |
| Agent 模板库 | 内置翻译助手/周报助手/代码审查员/会议纪要 4 套模板，「从模板创建…」一键预填 |

后续路线图（按优先级）：

1. **消息操作**：编辑后重发、分支对话
2. **成本统计**：在 token 用量基础上按模型单价折算成本，按日汇总
3. **对话内附件**：图片/文件拖拽，走 ADK Artifact 服务
4. **团队可视化**：Agent 拓扑图展示委派路径，团队运行时间线
5. **MCP OAuth**：远程 MCP 服务器的授权流（ADK `auth.CredentialProvider`）
6. **系统托盘与全局快捷键**：类 WorkBuddy 的常驻入口与划词唤起
7. **多窗口/多会话并行**：同时运行多个会话的并行流式输出
8. **自动更新与版本通道**：Sparkle/Wails 更新器集成
9. **配置导入导出与云备份**

## 生产使用

### 打包与发布
```bash
wails3 build && wails3 package   # 产物 bin/WorkBuddy Agent.app
```
- 版本号统一维护在 `main.go` 的 `Version` 常量，经 AppInfo 暴露到界面
- 构建使用 `-tags production -trimpath -ldflags "-w -s"`

### 稳定性与自愈
- Agent 运行时 panic 被 recover 并转为界面错误事件，应用不崩溃
- 会话数据库损坏时启动自动备份（`sessions.db.corrupt-<时间戳>`）并重建，保证可启动
- 全部后端路径有单测覆盖，`services` 包通过 `-race`

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

## 数据存储

| 内容 | 位置（macOS） |
| --- | --- |
| 配置（供应商/Agent/团队/Skills/MCP/会话元数据） | `~/Library/Application Support/workbuddy-agent/config.json` |
| Skills 物化目录 | `~/Library/Application Support/workbuddy-agent/skills/` |
| ADK 会话历史（SQLite） | `~/Library/Application Support/workbuddy-agent/sessions.db` |

## 目录结构

```
main.go                    # 应用入口：服务注册、窗口、SQLite 会话服务
internal/
  config/                  # 配置模型与 JSON 存储（含默认 Skills 种子）
  agentkit/                # ADK 集成核心
    llm.go                 #   model.LLM 适配器（OpenAI 兼容 / Anthropic）
    kit.go                 #   Agent/团队构建、内置工具、Skill 物化
  mcpmgr/                  # MCP 服务器管理（连接状态、工具发现）
  services/                # Wails 服务（前端可调用 API）
frontend/
  src/pages/               # 对话 / Agents / 团队 / Skills / MCP / 模型 / 设置
  src/components/ui/       # shadcn/ui 组件
  bindings/                # wails3 生成的 TS 绑定（构建时自动更新）
build/                     # 各平台打包资源（Info.plist / 图标 / Taskfile）
```
