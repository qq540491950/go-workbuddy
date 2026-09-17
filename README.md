# WorkBuddy Agent

基于 **Google ADK Go v2**（`google.golang.org/adk/v2`）+ **Wails v3** + **React/TypeScript + shadcn/ui** 构建的类 WorkBuddy Agent 桌面应用。

支持：可配置模型供应商、自定义 Agent、Agent 团队（ADK 子代理委派）、Skills（agentskills.io SKILL.md 规范）、MCP 工具服务器、流式对话、会话持久化（SQLite）。

![tech](https://img.shields.io/badge/ADK-Go%20v2-blue) ![tech](https://img.shields.io/badge/Wails-v3-green) ![tech](https://img.shields.io/badge/ui-shadcn%2Fui-black)

## 功能总览

| 模块 | 说明 |
| --- | --- |
| 模型配置 | 支持 OpenAI 兼容协议（OpenAI / DeepSeek / Kimi / Qwen / Ollama / vLLM…）、Anthropic（Claude）、Google Gemini 三种协议；可配 Base URL、API Key、模型列表（多模态/上下文窗口/价格元数据，可从 /models 拉取）、默认供应商，并一键测试连通性 |
| Agents | 自定义名称、描述、系统提示词、Temperature / MaxTokens，挂载 Skills、MCP 工具与内置工具（时间查询、工作区文件读写、知识库检索、长期记忆）；安全护栏（密钥脱敏/注入检测）；运行护栏（单轮超时/工具调用上限）；写入文件 HITL 人工确认；单个导出/导入分享 |
| 远程 Agent | A2A 协议（官方 a2a-go）：连接远程 Agent 直接对话（Agent Card 自动发现 + 连通性检查），也可作为团队成员参与协作；本应用亦可作为 A2A 服务端对外发布本地 Agent |
| Agent 团队 | 多 Agent 协作：自动协调员模式（系统合成协调员分派任务）或指定负责人模式；基于 ADK 的 agent 转移机制（transfer）实现委派；支持远程 A2A 成员 |
| Skills | 可复用技能指令包，保存为标准 SKILL.md（YAML frontmatter + Markdown），由 ADK Skill Toolset 以渐进式披露方式供模型按需加载；支持从工作区 skills/ 目录批量导入 |
| MCP 服务 | 通过 stdio（本地命令）或 Streamable HTTP（远程）连接 MCP 服务器，发现并列出工具，挂载到 Agent；远程服务器支持 Bearer / API Key / OAuth 2.1（授权码 + PKCE，令牌持久化静默刷新）认证 |
| 知识库 | 工作区 knowledge/ 目录检索：可配置 Embedding 供应商升级为「向量语义 + 关键词」混合检索（本地增量向量索引，SQLite） |
| 对话 | 流式输出（SSE）、工具调用/结果实时展示、团队对话中显示各成员输出、随时停止、工具执行人工确认（HITL）、会话置顶/分叉/并行运行、后台完成提示、上下文窗口占用指示与高水位提醒、Token 用量与成本（含按日明细）、Markdown 导出、会话历史持久化（ADK Database Session Service + 纯 Go SQLite 驱动） |
| 桌面集成 | 系统托盘（显示/隐藏/退出）、全局快捷键（⌘⇧Space 唤起）、深浅主题 / 强调色 / 字号、语音输入与朗读 |
| 设置与迁移 | 工作区目录、外观、语义检索 Embedding 配置、A2A 服务启停、配置备份导出/导入（版本化、可脱敏）、Agent 分享 |

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

### 第二十八轮迭代：后台会话完成提示（已实施）

多会话并行（第十二轮）的收尾体验：切换到其他会话时，后台会话完成/出错会以 toast 提示「『会话标题』已完成回复 / 本轮出错」，避免用户错过并行任务结果。

验证：tsc/build 零错误 + 全量回归。

### 第二十七轮迭代：Agent 单个导出/导入（分享，已实施）

- **导出**：Agent 卡片一键导出为版本化分享文件（`exports/agents/<名称>.agent.json`），不含任何密钥
- **导入**：顶栏「导入 Agent」读取分享文件，自动分配全新 ID；名称冲突自动加（2）（3）… 后缀；版本校验与解析错误友好提示
- 供应商引用（providerId）随文件携带，导入后需在模型配置中确认对应供应商存在

测试：导出/导入往返（新 ID、冲突后缀递增、未知 Agent/坏 JSON/版本拒绝）e2e + 全量回归。

### 第二十六轮迭代：Skills 工作区批量导入（已实施）

- **扫描导入**：Skills 页「从工作区导入」一键扫描 `<工作区>/skills/*/SKILL.md`（agentskills.io 布局），解析 YAML frontmatter 的 name/description；无 frontmatter 时回退一级标题命名
- **去重幂等**：按名称（大小写不敏感）跳过已存在技能，重复导入零副作用
- 未配置工作区/目录缺失均给出明确提示

测试：导入 e2e（frontmatter 解析/标题回退/大小写去重/非 SKILL 文件忽略/幂等重导入）+ 全量回归。

### 第三十八轮迭代：应用内日志查看器（已实施）

- 设置页「诊断」卡片：一键查看最近 200 行应用日志（logs/app.log 尾部），便于故障自查与反馈
- 日志缺失时给出友好占位；尾部窗口上限 300 行防大文件读取

测试：缺失占位 / 尾部窗口语义 e2e + 全量回归。

### 第三十七轮迭代：HTTP 代理支持（已实施）

- 设置页新增「网络」卡片：全局出站代理（`http://host:port`），应用于模型请求、知识库向量、MCP 远程连接与 A2A（共享 http.DefaultTransport，启动时一次性注入）
- 保存并重启后生效；非法地址仅记日志、回退直连；留空恢复直连

测试：代理注入/回环命中（请求经代理服务器转发）/重置直连/非法地址拒绝 单测 + 全量回归。

### 第三十六轮迭代：系统提示词模板变量（已实施）

- 系统提示词支持 `{{date}}` / `{{time}}` / `{{datetime}}` / `{{workspace}}` 四个变量，每次构建 Agent 时解析（应用长驻也能保持日期新鲜）
- 未匹配的 `{{...}}` 原样保留，不破坏既有提示词

测试：变量解析/残留检查/纯文本不变 单测 + 全量回归。

### 第三十五轮迭代：工具调用审计日志（已实施）

- **JSONL 审计追踪**：设置开启后，每次工具调用/结果（含会话 ID、作者、工具名、参数/结果负载、错误字段）以 JSON 行追加到 `logs/tools-audit.jsonl`（0640）
- 写入失败静默忽略——审计绝不阻断对话；开关默认关闭，按需启用
- 设置页「外观」卡片内新增开关

测试：审计 e2e（call/result 记录完整、会话 ID 关联）+ 全量 `-race` + 打包零错误。

### 第三十四轮迭代：会话 Token 预算护栏（已实施）

第十九轮运行护栏的累计维度补充：

- **MaxSessionTokens**（Agent 级，0=不限制）：会话累计 tokens 超出预算后，当前轮正常完成并提示，**新用户消息被拒绝**（ConfirmToolCall 等待中的确认不受影响，仍可应答）
- Agent 编辑器新增预算输入框

测试：跨预算轮完成 + 警告、后续轮拒发（模型零调用）e2e + 全量回归。

### 第三十三轮迭代：A2A 服务开机自启（已实施）

第十六轮 A2A 服务端「重启需手动启动」的补全：

- 设置新增 `a2aAutoStart` / `a2aAgentId` / `a2aPort`；应用启动时若启用则自动发布所选 Agent（端口 0 自动分配），失败仅记日志不阻塞启动
- 设置页 A2A 卡片新增「开机自动启动」开关，保存当前卡片选择的 Agent 与端口

测试：设置持久化往返 e2e + 全量回归 + 打包零错误。

### 第三十二轮迭代：会话导出统计头（已实施）

导出的 Markdown 头部新增统计行（用户消息 / 回复轮次 / 工具调用 / 总 tokens），来自既存 SessionStats 聚合。

测试：导出内容包含统计头 e2e + 全量回归。

### 第三十一轮迭代：AGENTS.md 工程指南（已实施）

为本仓库沉淀面向编码代理的工程地图：常用命令、目录地图、关键机制（HITL 协议、会话库并发约束、A2A 双向栈、绑定哈希、假模型服务器要求）、测试与迭代记录约定、已知边界 —— 降低后续任何自动化/人工迭代的上下文成本。

### 第三十轮迭代：知识库索引状态可观测（已实施）

设置页语义检索卡片显示索引状态：已索引文件数 / 分块数 / 嵌入模型，便于确认向量检索是否就绪。

测试：索引统计单测（文件/分块/模型计数 + nil 索引零值）+ 全量回归 + 打包零错误。

### 第二十五轮迭代：上下文高水位提醒（已实施）

第二十三轮指示器的行为延伸：

- 上下文占用 ≥80% 时：顶栏出现红色警示徽章；消息区顶部出现可关闭的琥珀色提醒条，附「导出 Markdown」与「知道了」快捷操作
- 切换会话自动重置关闭状态

验证：tsc/build 零错误 + 全量回归。

### 第二十四轮迭代：会话置顶（已实施）

- `ChatSession.Pinned` + `PinSession` RPC：侧栏悬停置顶/取消；置顶会话固定在列表最前（其余按最近活跃排序——顺带把列表从"创建序"升级为"活跃序"）
- 置顶状态以图钉图标常显

测试：置顶排序/取消还原 e2e + 全量回归。

### 第二十三轮迭代：上下文窗口使用指示器（已实施）

利用模型级元数据（contextWindow，见「模型接口拉取」轮）：

- 对话顶栏新增**上下文窗口占用进度条**：已用 tokens / 上下文窗口，百分比着色（<50% 绿、50-80% 琥珀、>80% 红），悬停显示具体数值
- 与 tokens/成本徽章并列展示；未配置窗口大小或无用量时不显示

验证：视觉回归桩注入 done 用量事件（200k/400k）→ 50% 琥珀进度条正确渲染 + tsc/build 零错误。

### 第二十二层迭代：会话统计按日成本汇总（已实施）

- **SessionStats 扩展**：`daily[]` 按本地日历日聚合输入/输出/总 tokens，并用会话目标 Agent **当前模型价格**（¥/1M）估算每日成本；跨模型历史按现价近似（已在 UI 注明）
- 统计弹层新增「按日明细」表（日期 / 输入 / 输出 / 估算成本）

测试：日聚合 + 成本计算 e2e（1M 输入 / 0.5M 输出 → 精确 ¥6.00 校验，误差 <0.0001）+ 全量回归 + 打包零错误。

### 第二十一轮迭代：MCP OAuth 令牌持久化（已实施）

第十八轮 OAuth 的补全——**重启后免重复授权**：

- **令牌缓存**：`Config.MCPTokens`（config.json 0600）按服务器缓存访问/刷新令牌与 token endpoint；OAuth handler 通过官方 `NewTokenSource`/`InitialTokenSource` 钩子读写缓存
- **静默刷新**：缓存含刷新令牌 + token endpoint 时，过期令牌自动静默刷新（无需浏览器）；浏览器授权仅在首次或刷新令牌彻底失效时触发
- **导出安全**：令牌缓存永不导出（含密钥导出也不含——属机器本地秘密，丢失可重新授权恢复）

测试：重启模拟 e2e（首次完整授权 → 断言缓存写入含 TokenURL → 新 transport 免浏览器直连成功，浏览器被调用即测试失败）+ 脱敏导出零泄漏扩展 + 全量 `-race`。

### 第二十轮迭代：分叉关系可视化（已实施）

第十五轮会话分叉的 UX 补全：

- `ChatSession.ForkedFrom` 记录分叉来源会话；侧栏分叉会话标题前显示 GitFork 图标，悬停提示「分叉自『原会话标题』」
- 分叉链路可追溯（多级分叉依次回溯）

测试：分叉元数据断言扩展（ForkedFrom 正确指向原会话）+ 全量回归。

### 第十九轮迭代：Agent 运行护栏（超时与工具调用上限，已实施）

针对 Agent 失控循环/挂起的安全护栏（ADK RunConfig 无此能力，应用层实现）：

- **单轮最长运行秒数**（`maxTurnSeconds`）：超时自动中止本轮并明确提示（区别于手动取消）
- **单轮工具调用上限**（`maxToolCalls`）：超出后停止本轮、保留已产生的结果与统计，防止死循环烧 token
- 两者默认 0 = 不限制（零破坏）；Agent 编辑器新增数值输入；Teams 暂按成员各自配置不叠加（负责人级后续迭代）

测试：上限触发（第 3 次调用被拦 + done 收尾）、超时中止（1s 上限 vs 3s 挂起模型）、零值保持旧行为 ×3 + 全量 `-race` + 打包构建零错误。

### 第十八轮迭代：MCP 远程服务器认证（Bearer / API Key / OAuth 2.1，已实施）

路线图「MCP OAuth」落地，依据 MCP 官方授权规范与 go-sdk v1.7 官方 `auth.AuthorizationCodeHandler`（授权码 + PKCE）：

- **三种认证方式**（Streamable HTTP 服务器，按服务器配置选择）：
  - **Bearer Token**：静态令牌，自动加 `Authorization: Bearer …`
  - **API Key**：自定义请求头（默认 `X-API-Key`）
  - **OAuth 2.1**：预注册 Client ID；连接时按 MCP 规范完成发现（WWW-Authenticate → 受保护资源元数据 → 授权服务器元数据）→ 打开系统浏览器完成授权 → 本地回环地址接收回调（授权码 + PKCE S256）→ 令牌交换，401/403 自动触发授权并重试
- **顺带修复**：配置里的自定义 `headers` 字段此前从未被应用到传输层，现已生效
- **安全**：脱敏导出同时剥离 MCP 认证令牌；回调仅绑定本机回环地址；授权等待 5 分钟超时

测试：进程内假授权服务器 + 受保护真实 go-sdk MCP 服务器的全流程 e2e ×4（Bearer / API Key / OAuth 2.1 完整流 / 无凭据被拒）+ 全量 `-race` + 打包构建零错误。

### 第十七轮迭代：浏览器视觉回归基建（已实施）

为本次会话累计的前端改动建立**可复用的浏览器视觉回归**（`frontend/stub/`，`npm run stub`）：

- **原理**：Wails v3 runtime 官方 `setTransport` 注入点替换 IPC 传输层为数据桩；真实 React UI 在普通浏览器中完整运行，`window.__emit` 可注入 `chat:stream` 事件流
- **验证走查（截图 + DOM）**：工具执行确认卡片（pending → 已批准 → 工具完成 → 模型续答全流程交互）、Agents 页远程 Agent 管理区、团队页混合拓扑（🌐 远程成员）、设置页语义检索/备份迁移/A2A 服务三张新卡片、深色主题渲染
- 修复过程中确认了调用描述符结构（`methodID`/`args` 槽位），桩与真实 IPC 语义一致

### 第十六轮迭代：A2A 服务端模式（已实施）

与第十轮（作为客户端连接远程 Agent）对称——本应用现在也能**对外提供 A2A 服务**：

- **发布本地 Agent**：设置页「A2A 服务」选择 Agent 与端口一键启动（端口 0 自动分配；仅监听本机回环地址）；启动后任意 A2A 客户端可发现 Agent Card 并以 JSON-RPC 调用
- **官方服务端栈**：与 ADK launcher 同一配方（`a2asrv.NewStaticAgentCardHandler + adka2a.NewExecutor`），远程调用各自获得服务端侧会话，经同一 ADK runner 执行
- **运行态管理**：启动/停止/状态查询 RPC；重复启动幂等；停止后端口释放；服务为运行态（重启应用后需手动重新启动，避免意外常驻暴露）

测试：loopback e2e（发布 → 卡片发现 → 本应用远程客户端全栈回环对话 → 幂等启动 → 停止状态清理）+ 全量 `-race` + 打包构建零错误。

### 第十五轮迭代：会话分叉（已实施）

路线图「消息分支树」的务实落地（ADK Branch 字段定位是子代理可见性隔离，非用户会话分支，故采用「分叉即新会话」模型）：

- **ForkSession**：一键把整段会话的 ADK 事件完整复制为独立新会话（标题自动加「（分支）」），原时间线永不变动；分叉后可随意 重新生成/编辑重发/人工确认，两边互不干扰
- **实现稳健性**：事件复制用 `pragma_table_info` 动态列名构建 SQL，ADK 事件表结构演进不受影响；事件主键含 session_id，跨会话复制天然无冲突
- **入口**：侧栏会话悬停「分叉」按钮（GitFork 图标），分叉后直接跳转到新分支

测试：分叉 e2e（历史一致复制、元数据正确、分叉内回复只落在分支、原会话时间线零变动、未知会话报错）+ 全量 `-race` + 打包构建零错误。

### 第十四轮迭代：系统托盘与全局快捷键（已实施）

路线图「系统托盘与全局快捷键：类 WorkBuddy 的常驻入口」落地，依据 Wails v3 官方 API（`SystemTrayManager` / `GlobalShortcutManager`）与官方示例：

- **菜单栏托盘**：常驻 🧭 图标（应用图标嵌入二进制）；左键菜单提供「显示/隐藏 WorkBuddy」「退出」；tooltip 提示应用名
- **全局快捷键**：`⌘⇧Space` 全局唤起/隐藏主窗口（无论应用是否前台）；注册冲突（被其他应用占用）仅记录日志、不阻塞启动
- 关闭窗口后从托盘可再次唤起，配合 `ApplicationShouldTerminateAfterLastWindowClosed` 保持单窗口常驻模式

验证：生产构建零错误；启动烟雾确认托盘/快捷键注册无报错、进程健康；全量测试回归。

### 第十三轮迭代：远程 Agent 进团队（已实施）

第十轮 A2A 的自然延伸——远程 Agent 现在可以作为**团队成员**参与协作：

- **混合团队**：团队编辑器新增「远程成员（A2A）」多选区；协调员可把任务 transfer 给远程成员，远程回复经 A2A 流回本地会话历史
- **负责人始终本地**：远程成员仅作为子代理参与（ADK agent transfer 路由），负责人/协调员由本地 Agent 担任
- 删除远程 Agent 时自动从所有团队摘除；拓扑图以 🌐 标注远程成员
- 导出 `SanitizeAgentName`：子代理规范名可被预测（协调员提示词内列出，模型照抄调用）

测试：混合团队全链路 e2e（自动协调员 fake 模型发起 `transfer_to_agent` → A2A 远程成员执行 → 回复落入本地 SQLite 历史）+ 全量 `-race` + 打包构建零错误。

### 第十二轮迭代：多会话并行（已实施）

路线图「多窗口/多会话并行」落地：

- **运行状态按会话隔离**：前端全局 `running` 布尔改为按会话的运行表——任一会话生成中不再阻塞其他会话发送/重新生成/确认操作；侧栏为运行中的会话显示旋转指示器；切换会话时通过 IsRunning 校准
- **并行会话远程图标**：侧栏区分 Agent / 团队 / 远程 A2A 会话图标
- **关键修复（并发数据库）**：并行写入暴露出会话库 SQLITE_BUSY——WAL 的延迟事务快照冲突会绕过 busy_timeout；会话库改为单连接串行化（桌面场景足够），主库与测试库同步
- 测试：双会话并发 e2e（独立回复、无串扰、完成后无残留运行态）+ 全量 `-race`

### 第十一轮迭代：配置备份与迁移（已实施）

路线图「配置导入导出与云备份」的本地部分落地：

- **导出**：设置页「备份与迁移」一键导出为版本化 JSON（`version` 字段向前兼容），写入工作区 `exports/`；**含密钥备份**（0600 权限，可直接恢复）与**脱敏备份**（剥离全部 API Key，可安全分享）两种
- **导入**：选择备份文件 → 明确提示覆盖范围（供应商/Agent/远程 Agent/团队/Skills/MCP/设置被覆盖，**会话历史保留**）→ 解析校验（版本拒绝、缺字段拒绝、10MB 上限）
- 会话历史（sessions.db）与记忆库永不参与导入覆盖

测试：往返恢复（密钥/Agent/团队/远程/Skills 全量）、脱敏导出零泄漏、非法 JSON/版本/缺字段拒绝、导入后会话保留 ×4 + 全量回归。

### 第十轮迭代：A2A 远程 Agent 互操作（已实施）

调研路线图「A2A 协议互操作（连接远程 Agent）」落地，依据 ADK Go v2.4.0 官方 `agent/remoteagent/v2` + 官方 `a2aproject/a2a-go v2`（Agent-to-Agent 协议标准）：

- **远程 Agent 接入**：Agents 页新增「远程 Agent（A2A 协议）」管理——填写对方 A2A 服务地址即可连接；「检查连接」自动发现 Agent Card（`/.well-known/agent-card.json`），展示名称/版本/描述/流式能力/技能清单
- **直接对话**：新建对话支持「远程 Agent」类型；消息经 JSON-RPC 跨进程往返，远程回复照常流式渲染并入 SQLite 会话历史
- **官方栈实现**：客户端用 ADK `remoteagent.NewA2A`（每次调用重新解析卡片，远端更新无需重启）；测试服务端用 `a2asrv.NewHandler + adka2a.NewExecutor`（与 ADK launcher 同一配方）托管真实 llmagent
- 卡片校验含 `SupportedInterfaces`（JSONRPC 绑定）与来源一致性校验，配置错误会得到明确报错

测试：进程内 A2A 全链路 e2e（本地 ChatService → ADK 远程 Agent → 官方 A2A 服务端 → 托管 llmagent → 假模型 → 双侧 SQLite）+ 卡片发现校验 + 全量回归与 `-race`；打包构建零错误、启动烟雾通过。

### 第九轮迭代：知识库语义检索（已实施）

调研路线图「语义化检索（嵌入向量）替代关键词 RAG-lite」落地，依据 OpenAI Embeddings API 权威规范（`POST {base}/embeddings`、`data[].embedding`、余弦相似度推荐）：

- **混合检索**：设置页配置 Embedding 供应商（OpenAI 兼容协议）+ 模型后，`search_knowledge` 升级为「向量语义 75% + 关键词 25%」混合评分；未配置时保持纯关键词行为（零破坏）
- **本地向量索引**：`knowledge.index.db`（SQLite，与会话库同目录）按文件增量构建（mtime 感知，仅重新嵌入变更文件）；删除文件自动清理；**更换 Embedding 模型自动全量重建**（不同模型的向量不可比）
- **切块策略**：700 字符滑动窗口 + 100 重叠，优先在换行处断开，保证片段可读
- **工程鲁棒性**：Embedding 服务故障优雅回退关键词并附提示；协议契约测试固定请求/响应形状（乱序 `index` 恢复、Bearer 鉴权、批量上限）
- 检索结果直接返回语义片段与「语义相关度 %」，比关键词行摘要信息量更高

测试：agentkit 单测/e2e ×5（排序命中与缓存、增量索引、模型切换重建、双路回退、切块边界）+ 协议契约 + 全链路 e2e（ChatService → search_knowledge → 语义检索结果进入模型上下文）+ 全量 `-race` 通过。

### 第八轮迭代：工具执行 HITL 确认（已实施）

调研路线图「工具执行 HITL 确认」落地，依据 ADK Go v2.4.0 官方实现（`tool/tool.go` confirmationTool 包装、`tool/toolconfirmation` 协议、`cmd/launcher/console/hitl.go` 官方客户端模式）：

- **工具级确认声明**：Agent 新增「写入文件需人工确认」开关（`confirmTools`，默认关）；开启后 `write_workspace_file` 经 ADK `functiontool.Config.RequireConfirmation` 包装，模型每次调用前先暂停请求批准（列表/读取不受影响）
- **确认卡片**：运行暂停时对话流实时出现琥珀色确认卡片（原工具名 + 参数 JSON + 提示语），「允许执行 / 拒绝」一键应答；批准后工具真正执行、模型看到执行结果继续生成，拒绝后模型收到拒绝结果并自行调整
- **恢复引擎统一**：`ChatService` 抽出共享 `runTurn`（Send 与 ConfirmToolCall 共用）；新 API `ConfirmToolCall(sessionID, callID, approved)` 按 ADK 协议回发 `adk_request_confirmation` FunctionResponse，重复应答/未知 ID 会被拒绝
- **重启可恢复**：确认请求随事件持久化（`LongRunningToolIDs`），重启后加载历史时未应答的确认卡片仍可交互应答
- 对模型透明：ADK 在恢复后把确认往返折叠为「原调用 + 结果」，模型只感知工具成功或被拒

测试：e2e ×4（批准全链路——文件真正写入 + 模型续答；拒绝——工作区不被触碰 + 模型收到 error；重启后仍可应答并完成写入；参数防护与默认关闭回归）+ 全量 `-race` 通过；修复上一轮遗留的 `wails3 build` 前端严格模式报错（ModelsPage 空值处理）；打包烟雾通过。

### 第七轮迭代：长期记忆（已实施）

调研路线图第一项落地：

- **持久化记忆服务**：`internal/memory` 实现 ADK `memory.Service`（SQLite `memories` 表，与会话库同库）；`AddSessionToMemory` 按 event 幂等摄取（跳过 partial/thought，工具调用记录为摘要），`SearchMemory` 关键词相关度检索，按用户隔离，附带 Count/Clear
- **模型可自动召回**：runner 接入 MemoryService；Agent 勾选「长期记忆检索」后挂载 ADK `loadmemorytool`，模型可跨会话检索用户历史
- **一键存入**：对话顶栏 🧠 按钮把当前整段对话存入长期记忆（toast 显示累计条数）
- 测试：摄取幂等/检索命中/用户隔离/清空 ×4 + RememberSession e2e（部署事实跨会话可召回、无记忆服务时干净报错）

### 第六轮迭代：行业调研对齐（已实施）

基于对 2026 agent 开发趋势的调研（Google ADK 生态：[Memory](https://adk.dev/sessions/memory/) / [A2A 协议](https://a2a-protocol.org/latest/) / [HITL](https://adk.dev/graphs/human-input/)；分层护栏最佳实践：[输入验证→模型→输出过滤](https://www.kalviumlabs.ai/blog/guardrails-for-llm-applications/)；[Agent 可观测性](https://www.langchain.com/resources/agent-observability)），结合项目差距落地三项：

| 功能 | 实现 | 对应趋势 |
| --- | --- | --- |
| 知识库检索（RAG-lite） | `search_knowledge` 内置工具：`workspace/knowledge/` 下 md/txt/csv/json 全文关键词检索，相关度评分 + 片段摘要，Agent 编辑器勾选挂载 | RAG/知识库 |
| 分层护栏 | 输入+输出双向密钥脱敏（sk-/AKIA/ghp_/xox/AIza/sk-ant/私钥块/通用 hex secret 8 类模式）与提示注入启发式检测告警；`EnableGuardrails` 每 Agent 开关（默认开） | Guardrails 分层防御 |
| 会话统计（Observability-lite） | `SessionStats` 聚合：消息数/助手轮次/工具调用与失败/token 用量/估算成本，顶栏统计弹层展示 | 可观测性 |

新增测试：脱敏 8 类模式、注入正负样本、知识库检索/空库/无关文件过滤、统计聚合、护栏脱敏 e2e（历史不留原始密钥）。

路线图（调研后更新）：长期记忆（ADK memory.Service 或 Mem0 集成）、A2A 协议互操作（连接远程 Agent）、工具执行 HITL 确认（ADK toolconfirmation）、语义化检索（嵌入向量）替代关键词 RAG-lite。

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

## 发布就绪检查（第二十九轮收官验证）

- 后端全量测试（agentkit / config / mcpmgr / memory / services）+ `-race` 全部通过
- 前端 `tsc` 严格类型检查 + 生产构建零错误
- `wails3 build` 生产打包成功，启动烟雾通过（进程存活、窗口创建、托盘/快捷键注册无报错、干净退出）
- 浏览器视觉回归（`frontend/stub/`）覆盖确认卡片、远程 Agent、团队拓扑、设置页等关键界面

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
