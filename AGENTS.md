# AGENTS.md — WorkBuddy Agent 工程指南

面向在本仓库工作的编码代理（与人类贡献者）。先读 README.md 的功能总览与各轮迭代记录了解能力边界。

## 项目概要

类 WorkBuddy 的本地 AI Agent 桌面应用：**Google ADK Go v2**（agent 运行时）+ **Wails v3**（桌面壳）+ **React/TS + shadcn/ui**（前端）。

## 常用命令

一键全量验证（测试 + race + 前端 + 打包）：`./scripts/verify.sh`

```bash
go test ./internal/...                     # 后端全量测试（提交前必须全绿）
go test -race ./internal/...               # 并发安全回归（改并发/数据库代码后必跑）
go vet ./internal/... .
cd frontend && npx tsc --noEmit && npm run build   # 前端类型检查 + 构建
cd frontend && npm test                            # vitest（lib/chatUtils、lib/mcpEnv 纯函数单测）
wails3 generate bindings -f '-tags production' -clean=true -ts -i   # 再生成 Wails 绑定
wails3 build                               # 生产构建（自动重跑绑定生成 + 前端构建 + Go 编译）
wails3 package                             # 打包 macOS .app
cd frontend && npm run stub                # 浏览器视觉回归桩（端口 9246，/stub/index.html）
```

注意：`wails3 build` 的绑定生成带 `-clean=true`，会**重建**绑定文件 —— 手工改绑定会被覆盖。改 Go 服务后直接跑再生成即可（前端 import 用生成产物）。

- **`wails3 generate bindings` 必须在仓库根目录执行**：在子目录（如 frontend/）执行会扫描到 Go 标准库与依赖包，生成 `bindings/time/`、`bindings/gorm.io/`、`bindings/context/` 等垃圾目录（可删除；正确产物为 `bindings/go-workbuddy/` 与 `bindings/github.com/`——后者含 wails-typed-events 插件必需的 eventcreate/eventdata，勿删）。
- **依赖包绑定产物不入库**：`.gitignore` 已排除 `bindings/{context,database,gorm.io,reflect,strings,sync,time,google.golang.org}`——每次 `wails3 build` 自动重建，git status 不会显示。

## 目录地图

```
main.go                      # 装配：服务注册、窗口、托盘+全局快捷键、SQLite 会话库、知识索引
internal/config/             # 配置模型与 JSON 存储（0600 config.json；含默认 Skills 种子、MCP 令牌缓存）
internal/agentkit/           # ADK 集成核心
  llm.go                     #   OpenAI 兼容 / Anthropic 自研 model.LLM 适配器（SSE 流式 + 函数调用）
  kit.go                     #   Agent/团队构建、内置工具装配、SanitizeAgentName
  semantic.go                #   知识库向量索引（SQLite、增量、混合检索）+ OpenAI 兼容 /embeddings 客户端
  knowledge.go               #   关键词检索与 search_knowledge 工具
  remote.go                  #   A2A 远程 Agent（官方 a2a-go）+ Agent Card 解析
  guardrail.go               #   密钥脱敏 / 提示注入检测
internal/mcpmgr/             # MCP 服务器生命周期；auth.go = Bearer/API-Key/OAuth 2.1 传输层
internal/memory/             # 长期记忆（ADK memory.Service，SQLite）
internal/services/           # Wails 服务（前端可调用 RPC）
  chatservice.go             #   会话/对话核心：runTurn 共享引擎、HITL 确认、分叉、并行
  task_workspace.go          #   任务目录校验、会话级改绑锁与工作区 RPC
  workspace_snapshot.go      #   任务快照基线、产物与文件级变更
  artifactservice.go         #   会话级文件列表/预览与安全扫描
  a2aservice.go              #   A2A 服务端（对外发布本地 Agent）
  app.go                     #   AppInfo、配置备份导出/导入、Agent 分享
frontend/src/pages/          # Chat / Agents / Teams / Skills / MCP / Models / Settings
frontend/src/components/TaskWorkspacePanel.tsx # 右侧任务结果区
frontend/bindings/           # wails3 生成的 TS 绑定（勿手改，会被 -clean 覆盖）
frontend/stub/               # 浏览器视觉回归：setTransport 数据桩 + window.__emit 事件注入
```

## 关键机制（改动前必读）

- **对话引擎**：`ChatService.runTurn` 是 Send / Regenerate / EditAndResend / ConfirmToolCall 的共享引擎。改事件流（`ChatStreamEvent`）时同步改前端 `ChatPage.tsx` 的 switch 与历史渲染 `renderEvent`。
- **HITL 确认协议**：ADK 以 `adk_request_confirmation` FunctionCall（带 `LongRunningToolIDs`）暂停运行；应答 = 发送 `{ID, Name: "adk_request_confirmation", Response: {"confirmed": bool}}` 的 FunctionResponse 作为下一轮消息。ADK 在恢复后对模型隐藏确认往返。
- **会话库并发**：`sessions.db` 使用 WAL + busy_timeout(5000) + **单连接**（`SetMaxOpenConns(1)`）。WAL 下延迟事务的快照冲突会绕过 busy_timeout —— 不要移除单连接限制，否则并行会话写库触发 SQLITE_BUSY。
- **绑定哈希**：方法 ID = FNV-1a32(`go-workbuddy/internal/services.<Service>.<Method>`)（生成器自动处理；手工计算仅用于应急）。
- **全局代理**：`agentkit.ApplyGlobalProxy` 在启动时改写 `http.DefaultTransport.Proxy`——模型适配器、embeddings、MCP、gemini SDK、a2a-go 的出站流量统一生效；新增直连客户端时务必走 DefaultTransport 或在代理测试中钉住覆盖。
- **A2A 双向**：客户端 = `agentkit.BuildRemoteAgent`（remoteagent v2，AgentCardProvider 每次调用重解析）；服务端 = `a2aservice.go` / 测试中 `a2asrv.NewHandler + adka2a(v2).NewExecutor`（与 ADK launcher 同配方）。Agent Card 必须含 `SupportedInterfaces`（JSONRPC 绑定），否则客户端报 "no supported interfaces"。
- **假模型服务器**：OpenAI 兼容桩必须同时处理 `"stream":false`（返回普通 chat.completion JSON）与 SSE（A2A executor 以非流式调用模型）。
- **知识库索引**：`knowledge.index.db` 独立于 sessions.db；按文件 mtime 增量；变更文件重嵌入前先删除其旧行（否则缩小文件会留下过期分块）；换 Embedding 模型全量重建；Embedding 故障优雅回退关键词。
- **配置读取**：`Store.Get()` 返回深拷贝（切片/map 均独立），可以随意修改返回值；`Store.Settings()` 用于只需要全局设置的热路径。`ExportConfig(false)` 依赖这一点——脱敏只能作用在副本上。
- **取消登记**：每轮运行在 `runs[sessionID]` 登记 `*runHandle`，释放时只在指针仍属于自己时删除，避免旧轮次清掉新轮次的取消函数。
- **会话元数据变更**：Rename/SetModel/Archive/Pin 统一走 `mutateSession`（未知会话返回错误，审计仅成功后落笔）；`DeleteSession` 与 `Regenerate/EditAndResend` 先 `Cancel` 在跑轮次再动历史。
- **会话级模型覆盖**：`ChatSession.ModelOverride` 经 `agentkit.EffectiveModel` 统一钳制（覆盖 ID 不在供应商列表则回落默认），运行、统计计费、autoTitle 侧调用三处共用；设置侧的即时开关（autoRemember/AutoTitle/A2A 自启动）从「上次已保存快照」持久化，避免冲洗未保存字段。
- **任务级工作区**：公开 `NewSession` 与 `NewSessionWithWorkspace` 均创建任务目录（前者自动、后者可指定）；首轮事件后锁定，活动任务不能重复占用同一显式目录。`ChatSession.EffectiveWorkspace` 是文件工具、`{{workspace}}` 与预览的唯一后端解析入口，前端通过 `SessionWorkspace` RPC 获取有效路径；旧会话空值回落全局工作区。发送/确认与改绑共用会话级 workspace 锁，避免启动竞态。知识库/Skills/导出仍使用全局工作区。
- **任务产物快照**：基线存于应用数据目录的 `task-snapshots/<sessionID>.json`（版本化、原子写，不进 sessions.db）。结果区按文件存在性/大小/mtime 计算新增、修改、删除；扫描跳过隐藏/版本库/依赖目录与符号链接，5000 文件/200 条展示上限。删除会话清基线但保留任务文件；分叉共享工作区但建立独立基线。
- **plan_todos**：状态存 ADK session state 键 `workbuddy.todos`，全量替换语义（省略参数=只读，空数组=清空）；前端从 tool_result 渲染清单卡与置顶条。
- **autoTitle**：opt-in（`Settings.AutoTitle`），首轮后异步 30s 侧调用生成标题，仅替换自己写下的兜底标题；goroutine 自带 recover。
- **A2A 失效联动**：SaveAgent（替换）/DeleteAgent 通过 `Services.StopPublishedAgent` 下线已发布服务器；OAuth 回调端口惰性重绑（授权流程结束必释放）。
- **单轮超时**：Agent 未配置 `MaxTurnSeconds` 时默认 600 秒兜底；`runTurn` 的 recover 把 panic 转为 `error` + `done` 事件，界面不会卡在运行态。
- **导出脱敏**：`ExportConfig(false)` 剥离供应商 API Key 与 MCP AuthToken；`MCPTokens` 任何导出都不携带。

## 约定

- 测试与被测代码同包同目录（`*_test.go`），e2e 用 `httptest` 假服务器（fake SSE 模型 / 假授权服务器 / 内存 A2A 服务端），不依赖外部网络。
- UI 文案为中文；错误经 `errText()` 透传到 toast。
- 每轮功能迭代在 `docs/iteration-log.md` 顶层「视觉测试与体验设计」章节**顶部**追加「### 第 N 轮迭代：<主题>（已实施）」记录（含依据、实现要点、测试证据）。README 只保留概览与「质量与验证」。
- 提交信息采用约定式前缀（feat / fix / docs / test / chore / refactor）+ scope，如 `feat(chat): 主题 — 要点`；正文列出实现与测试清单。

## 已知边界

- 长会话渲染上限 200 条（完整历史持久化，可导出查看）
- MCP OAuth 令牌缓存在 config.json（0600）；脱敏导出不带令牌
- 团队负责人必须为本地 Agent（远程 A2A 成员仅作为可转移的子代理）
- 任务结果区支持行级 diff（基线内容快照存于 `task-snapshots/content/<sessionID>/`，文本 ≤256KB、每会话 32MB 预算）；仍不支持 Office 在线预览或网页运行预览
