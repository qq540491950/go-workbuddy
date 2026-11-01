# 迭代日志

本文件按轮次记录每次功能迭代的依据、实现要点与测试证据。最新记录在最上方。

## 视觉测试与体验设计

### 第一百五十一轮迭代：模块重命名 changeme → go-workbuddy（已实施）

依据：用户指出 `go.mod` 模块名仍是 Wails 模板默认的 `changeme`，与项目名 go-workbuddy 不符，要求整体审核并执行重命名。审计确认模块名贯穿 go.mod、47 个 Go 文件 import、前端生成绑定目录、`api.ts` 手写 import、视觉桩硬编码方法 ID、`.gitignore`/`AGENTS.md`/`README.md`；另发现第二处模板遗留 `go-wails-react-tpl`（Taskfile `APP_NAME` 与 macOS/iOS/Windows/Linux 打包资产）。

实现要点：

- `go.mod` 模块名改为 `go-workbuddy`，47 个 Go 文件 import 同步替换（含 `debug/mcp_probe`）。
- 重新生成绑定：产物目录由 `frontend/bindings/changeme/` 变为 `frontend/bindings/go-workbuddy/`，全部方法 ID 随模块路径变化；`api.ts` 五处 import 同步。
- 视觉桩 `frontend/stub/main.tsx` 的 17 个硬编码方法 ID 全部换算为新 ID；顺带修复陈旧的 AppInfo 键（旧值 2350821288 不对应任何现存方法，属潜伏死代码，现已指向真实的 `appservice.AppInfo`）。
- 方法 ID 公式确认为 FNV-1a32(`包路径.类型名.方法名`)（wails v3.0.0 `internal/hash/fnv.go` + `internal/generator/collect/service.go`），`AGENTS.md` 同步模块名。
- Taskfile `APP_NAME` 改为 `go-workbuddy`；macOS/iOS plist、Windows NSIS/MSIX、Linux desktop/nfpm 打包资产中的模板名全部替换；`docs/iteration-log.md` 历史轮次记录保留原名不动。

测试证据：rg 全仓 `changeme` 零残留（含生成绑定）；`go vet` + `go test ./internal/... .` 全绿；vitest 16/16、`tsc --noEmit`、`npm run build` 通过；`wails3 build` 端到端成功并产出 `bin/go-workbuddy.exe`。`go test -race` 本机无 C 工具链（CGO）无法执行，由 CI ubuntu 任务覆盖。

### 第一百五十轮迭代：对话优先外壳与设置中心（已实施）

依据：用户提出对齐 ChatGPT / Claude / Codex 桌面端的导航模型——最左列直接是对话列表、对话按目录分组、全部配置收进设置。设计固化为 `docs/specs/chat-first-shell.md` 并拆为六张票据（设置中心骨架、对话优先外壳、侧栏折叠、时间分组、项目分组、快捷入口）全部实施。关键设计澄清：目录分组的键是工作区根而非会话任务目录——每个会话都自动创建独立任务目录，逐目录分组会碎片化。

实现要点：

- **设置中心**：设置页重构为「左标签 + 右内容」九标签（通用 / 模型供应商 / Agents / Agent 团队 / Skills / MCP 服务 / 知识库 / 数据与存储 / 关于），五个管理页组件化迁入对应标签；⌘, 打开、Esc 与返回按钮回对话；关于标签显示版本号。
- **对话优先外壳**：删除主导航栏，视图收敛为「对话 / 设置」；主题切换、帮助、设置入口迁入对话列底部；空状态与 Onboarding 直达设置对应标签；⌘N / ⌘K / ⌘F 语义不变。
- **对话列表分组**：新增 `groupSessionsByTime`（置顶 → 今天 → 昨天 → 7 天内 → 更早 → 已归档，归档默认折叠可展开）与 `groupSessionsByProject`（显式工作区按目录成组并保留原路径大小写、自动任务目录与直聊归「默认项目」、组间按最新成员排序）两个纯函数；列表按组渲染，搜索过滤与分组正交；项目组悬停显示完整路径。
- **折叠与偏好持久化**：设置新增 `sidebarGrouping`（time / project，默认 time，非法值回退）与 `sidebarCollapsed`（默认 false）；⌘B 或按钮折叠 / 展开对话列，折叠后头部出现展开按钮。
- **快捷入口**：@ 候选浮层底部「管理助手与团队…」、模型选择器下拉底部「管理模型供应商…」、会话头部徽标点击直达设置对应标签（直聊徽标保持纯展示）；帮助面板补 ⌘, 与 ⌘B。
- **视觉桩**：新增跨天 / 置顶 / 归档 / 独立工作区会话夹具，日期相对加载日计算，保证分组桶始终有成员。

测试证据：分组纯函数 vitest 表驱动四用例（固定「当前时间」断言桶序与成员、7 天临界、无效时间回落更早、大小写与反斜杠路径归并、组间最新排序）；`internal/config` Go 测试覆盖新字段默认值、持久化与非法分组回退；浏览器桩走查两列外壳、时间 / 项目分组（截图）、归档折叠、侧栏折叠 / 展开三态下长对话 composer 几何（708 ≤ 720，消息区可滚）、设置九标签与管理页嵌入、@ 浮层入口截图与模型选择器入口 DOM 断言；走查中发现并修复「分组切换仅持久化未更新 UI 状态」缺陷。工作区另有并行会话进行中的 MCPPage / config / kit 改动（第一百四十九轮），未纳入本轮提交。

### 第一百四十九轮迭代：MCP 启用开关——默认开启、随启动自连并注入所有会话（已实施）

依据：用户反馈直聊里让助手用 Tavily 查天气，助手回答「当前会话尚未加载 Tavily MCP，无法热加载」（附截图）；并提出 MCP 应「只有开启和关闭」——默认在软件打开时就开启并注入会话，开关控件改用 Switch。代码定位三处断点：`directAgentConfig` 构建的直聊临时助手不带任何 `MCPServerIDs`，直聊会话零 MCP；`MCPServerConfig.AutoStart` 字段自创建起无后端读取、无 UI 控件，纯死配置，启动时也不连接；MCP 页只有「连接/断开」按钮，没有启用开关。

实现要点：

- **语义重定义**：以 `Disabled bool`（`disabled,omitempty`）替代死字段 `AutoStart`——省略即启用，存量配置零迁移自动变为「默认开启」，正好符合用户预期；启用开关是权威控制。
- **注入所有会话**：`kit.buildLLMAgent` 的 MCP 挂载改为「启用的服务全量注入（直聊/Agent/团队统一生效）∪ Agent 显式挂载的补充（可单独启用某停用服务）」；显式挂载已停用服务仍生效（逃逸口），悬空 ID 依旧报错，启用但损坏的服务仍走「报错不静默降级」契约。直聊经 `BuildAgentWithModelAndWorkspace` 走同一装配，无需改 `directAgentConfig`。
- **启动自连**：`main.go` 增加后台 goroutine 顺序预连接所有启用服务并预热工具列表，失败只记日志不阻塞启动；Manager 锁内建连，与页面「连接」共用同一状态缓存。
- **MCP 页 Switch 化**：卡片操作区以「启用」Switch 为主控（开启即保存并立即连接，关闭即保存并自动断开），移除「断开」按钮，「连接」保留为启用状态下的手动重试；停用服务显示「已停用」徽标；编辑对话框顶部新增启用开关（说明：随应用启动自动连接并注入所有会话）；新增服务默认启用。

测试证据：新增 `TestBuildAgentInjectsEnabledMCPServers`（修复前红：启用但损坏的 MCP 不注入、Build 不报错；修复后启用→报错注入、停用→构建通过、显式挂载停用服务→按需启用）并回归 `TestBuildAgentReportsMCPFailures` 保持通过；`go test ./internal/...`、`go vet ./internal/... .` 全绿；绑定再生成（`autoStart`→`disabled`）后 `tsc --noEmit`、vitest 16 用例、生产构建通过。`-race` 因本机缺 gcc 无法运行（环境限制）；启动自连 goroutine 只读 `Store.Get()` 快照且 Manager 自带锁。

### 第一百四十八轮迭代：MCP stdio 命令尾随空白导致连接失败修复（已实施）

依据：用户反馈 MCP 页添加 npx 型 stdio 服务（`npx -y tavily-mcp`）点连接即报 `failed to list MCP tools: failed to init MCP session: exec: "npx ": executable file not found in %PATH%`，而同机其他 Agent 客户端配置同一服务正常。错误引号内的尾随空格暴露根因：命令输入框的值原样入库（前端仅对「参数」逐行 trim，command、后端 `SaveMCPServer` 与 `buildTransport` 全链路无规范化），`exec.Command("npx ")` 的 `LookPath("npx ")` 永远匹配不到 `npx.cmd`——与 PATH 无关，其他客户端的 JSON 配置不引入尾随空白故不受影响。

实现要点：

- **运行时兜底**：`buildTransport` stdio 分支对 Command `TrimSpace`、Args 逐项 trim 并丢弃空白项；streamable-http 的 URL 同步 trim——已入库的脏配置（如现存 `"npx "`）无需重新保存即可恢复连接。
- **保存侧止血**：`SaveMCPServer` 落库前规范化 Name / Command / URL / Args，UI 保存与配置导入两条路径统一；规范化在 changed 判定之前执行，脏配置改存后能正确触发旧连接失效。
- **空 Args 保持 nil**：`Store.Get()` 深拷贝会把空切片还原为 nil，若规范化产出空非 nil 切片，JSON 比较（`[]` vs `null`）每次都判 changed，未变更保存也会误断缓存连接——规范化后空集回落 nil，与新测试共同钉住该语义。前端无需改动。

测试证据：新增 `TestBuildTransportTrimsStdioCommand`（修复前红，报错 `exec: "cmd ": executable file not found in %PATH%` 与用户症状同形；修复后命令解析成功且 Args 保持 `["-y","tavily-mcp"]`）与 `TestSaveMCPServerNormalizesWhitespace`（落库值全部去空白）；回归中抓出空 Args 误判 changed 问题并以 nil 语义修复，`TestSaveMCPServerInvalidatesStaleConnection` 保持通过；`go test ./internal/...` 与 `go vet ./internal/... .` 全绿。

### 第一百四十七轮迭代：聊天页垂直溢出修复——输入框被顶出窗口（已实施）

依据：用户反馈直聊会话「UI 有很大的问题」并附 maximized 截图：长回复底部发送按钮被窗口边缘裁切；实机复现（814px 高窗口）更严重——两轮长对话后整个 composer 被推出窗口外完全不可见，消息区也无法滚动，历史消息低于折叠线的部分永远读不到。桩页几何测量定位根因：shadcn `ScrollArea` Root 自基线起缺失默认的 `overflow-hidden`，在 `h-screen` 列布局中作为 `flex-1` 子项时自动最小尺寸（`min-height: auto`）等于内容高度，flexbox 无法把它收缩到可用空间以内——实测消息区 Root 高 921px（视口仅 720px）、`scrollHeight == clientHeight`（自身不滚），整列被内容撑高后由 App 根的 `overflow-hidden` 裁掉底部。

实现要点：

- **`scroll-area.tsx` Root 恢复 `overflow-hidden`**（shadcn 默认值）：非 visible 的 overflow 使 flex 子项自动最小尺寸归零，`flex-1` 重新生效——消息区收缩到剩余空间、radix Viewport 正确接管内部滚动；会话列表与任务结果区的 ScrollArea 同步受益。
- **composer 防挤占**：输入区容器加 `shrink-0`，textarea 加 `max-h-48 overflow-y-auto`——长文本粘贴时输入框封顶 12rem 并内部滚动，消息区让位而不是把 composer 顶出窗口。
- **视觉桩补直聊场景**：stub 种子新增空目标直聊会话（长 Markdown 回复，与用户实况一致），作为本轮回归的走查夹具。

测试证据：桩页（720px 视口）修复前后对比——修复前消息区 Root `overflow: visible`、高 921px、composer 顶部 748px（窗口外）；修复后 Root `overflow: hidden`、高 578px、composer 底部 708px（窗口内），Viewport `scrollHeight 921 > clientHeight 578` 可滚且能滚回顶部看到日期分隔与首条消息；2100 字符粘贴 textarea 稳定在 192px 并内部滚动。新增 `scroll-area.test.tsx` SSR 断言 Root 必须携带 `overflow-hidden`（防止再次回归）；`vitest` 12 用例、`tsc --noEmit`、生产构建全部通过。

### 第一百四十六轮迭代：直聊会话与 @ 助手路由，滚动条与设置页修复（已实施）

依据：用户反馈对齐 Codex / Claude Code / WorkBuddy 的对话模型——对话不应要求先创建 Agent，助手应通过 `@` 按需唤起；同时指出设置页布局失衡与全站滚动条观感问题。代码审查确认三处根因：新对话对话框在无 Agent 时死锁（必须先建 Agent）；全站无 `::-webkit-scrollbar` 样式，WebView2 深色主题渲染 15px 亮色原生滚动条且与聊天区 radix ScrollArea 风格割裂；设置页为唯一未居中的 `max-w-xl` 窄列（实测 1217px 视口中右侧空置 640px）。另发现既有缺陷：非 ASCII Agent 名经 `sanitizeAgentName` 变为 `agent_<hash>`，聊天气泡与流事件直接显示该哈希。

实现要点：

- **直聊会话**：`NewSessionWithWorkspace` 允许空目标（仅 `""`，非法类型仍拒绝）；`runTurn` 新增直聊分支——不落库地构造「默认供应商默认模型 + 通用提示词」的临时 Agent，会话级模型覆盖与工作区机制照常生效。无供应商时发送报错引导至模型配置页。
- **@ 消息级路由**：`resolveMention` 解析消息开头 `@名称`（Agent/团队/远程，最长名匹配，须跟随空格边界），`turnTargetFor` 仅将该轮构建目标替换为被 @ 者，会话绑定不变；@ 提及保留在消息原文中，Regenerate / EditAndResend / HITL 恢复（从最后一条用户消息回溯解析）都按同一规则重放路由；未知名回落直聊。
- **作者名反查**：新增 `displayAuthor` 将 ADK 事件作者（净化后标识符）映射回配置显示名，应用于流事件与历史渲染（导出经同一渲染受益），中文 Agent 名在气泡中正确显示。
- **前端直聊流**：「新对话」按钮与 ⌘N 直接创建空目标会话（删除旧选择对话框）；无会话时输入框可用，首条消息惰性建会话（`freshInput` 草稿）；composer 输入 `@` 弹出助手/团队/远程候选浮层（键盘上下选择、Enter/Tab 采纳、Esc 关闭）；头部徽标「直聊」、模型选择器列默认供应商全部模型、空态与 Onboarding 改为「配供应商 → 直接开聊（@ 指定助手）」两步。
- **滚动条统一**：全局 10px 细滚动条（thumb 用前景色 25% 混合、圆角、hover 提亮、track 透明）+ `scrollbar-width: thin`，设置/Agents/团队/Skills/MCP/模型页、弹窗与代码块与聊天区视觉一致。
- **设置页布局**：`max-w-5xl` 居中 + `lg:grid-cols-2` 两列卡片网格（窄窗口回落单列），保存按钮改为吸底模糊操作栏（跨双列）。
- **顺带修复**：`Input`/`Textarea` 改 `forwardRef`，修复 ChatPage 传 ref 无效导致的 ⌘K 聚焦失效（React "Function components cannot be given refs" 告警消失）。

测试证据：后端新增 `chatservice_direct_test.go` 四用例（直聊会话目标不变且作者为「助手」、@路由至具名 Agent 且 Regenerate 保持路由、未知名回落直聊、无供应商报错引导），`go test ./internal/...` 与 `go vet ./internal/... .` 全绿；前端 `tsc --noEmit`、vitest 11 用例、生产构建通过；浏览器桩（1440×900）走查确认直聊会话创建、直聊徽标与默认模型选择器、`@写` 弹出候选且 Enter 采纳为 `@写手助手 `、设置页两列居中布局与吸底保存、细滚动条生效。

### 第一百四十五轮迭代：Wails v3 正式版与 Windows 启动告警修复（已实施）

依据：Wails v3.0.0 正式发布后统一升级 Go SDK、CLI 与前端运行时；Windows `wails3 dev` 启动仍出现 `uname` / `tail` 缺失提示，定位为根 Taskfile 无条件加载 Android 全局 shell 变量所致，而非 Windows 构建需要这些工具。

实现要点：

- Wails Go SDK、`@wailsio/runtime` 与本机 CLI 统一为 `v3.0.0`；README 安装命令同步更新，绑定重新生成。
- Android `HOST_ARCH` / `ANDROID_ABI` 改用 Task 内置 `ARCH`，消除 include 阶段的 `uname` 求值；`avdmanager` 查找移入模拟器启动脚本，仅在实际需要时执行 `tail`。

测试证据：`wails3 task --list-all` 无 Unix 工具缺失警告；Android `arm64` / `amd64` dry-run 保留正确 ABI 与交叉编译参数；`wails3 dev` 实测完成 Go 编译、Vite 启动、WebView2 创建和前端连接，启动日志无 `uname` / `tail` 警告。升级后 `go test ./internal/...`、`go vet`、前端 `tsc`、vitest 11 用例、生产构建及 `wails3 build` 均通过。未实际构建或运行 Android（本机无 Android SDK）。

### 第一百四十四轮迭代：任务级工作区与产物验收工作台（已实施）

依据：MCP 调研 WorkBuddy 官方「结果查看 / 快速开始」后确认其核心闭环是任务级工作目录 + 右侧产物验收；结合本会话规格 issue #10 与垂直切片 #11–#15 实施。

实现要点：

- **任务级工作区**：会话持久化 `WorkspaceDir`；新对话默认创建独立 `<全局工作区>/tasks/<sessionID>`，也可通过原生目录选择器绑定已有目录。首轮前可改绑、首轮后锁定；目录需可读写且不能被其他未归档任务重复占用。旧会话继续回落全局工作区。
- **运行时统一**：单 Agent、团队、文件工具、`{{workspace}}` 提示词变量和工具卡预览统一使用会话工作区；知识库、Skills 与导出保持应用级目录。工具卡中的 `search_knowledge` 引用继续走全局预览。
- **右侧结果区**：聊天页新增可收起的任务结果面板，提供「全部文件 / 产物 / 变更」；支持路径搜索、图片/PDF/Markdown/文本预览、未知二进制提示、空/错/截断状态、手动刷新和会话切换防串数据。桌面为第三栏，窄屏为覆盖式面板。
- **产物与变更**：任务创建时原子写入版本化快照基线；结果区计算新增、修改、删除及计数，旧会话首次查询懒建基线，重启后继续累计。HITL 批准写入、工具结果、回合完成与外部 MCP 文件变化都可经重新扫描发现。
- **安全与边界**：扫描只统计普通文件，拒绝目录穿越与符号链接逃逸，跳过隐藏文件、版本库元数据、依赖缓存和虚拟环境；5000 文件扫描上限、200 条展示上限。删除会话清理基线但保留文件；归档不影响结果访问；分叉共享有效工作区但建立独立基线。
- **双轴审查修复**：公开 `NewSession` 也默认创建任务目录；发送与改绑用会话级锁线性化；显式目录强制绝对路径；前端工作区统一经后端 `SessionWorkspace` 解析；快速切会话不再残留旧快照；补齐变更类型筛选、ARIA 标签键盘导航、普通文件判定、错误 toast 与浏览器桩空/错/截断/慢响应状态。
- **复审补强**：归档目录被新任务复用后，解除归档会重新校验占用（同分叉谱系豁免）；目录同一性用 `os.SameFile` 覆盖 macOS 大小写变体；“全部”及标签页变更数使用截断前总数；无扩展名 UTF-8 文件恢复文本预览；ARIA 仅对现存面板建立 `aria-controls`；浏览器桩覆盖目录选择、取消、冲突与失败；新增并行写入与并行快照的 race 场景。

测试证据：后端新增任务隔离、目录校验、首轮锁定、发送/改绑竞态、快照状态/截断、旧会话懒基线、HITL 产物、分叉/删除/归档与并发快照测试；`go test ./internal/... .`、`go vet` 与临时 MinGW 下 `CGO_ENABLED=1 go test -race ./internal/... .` 全绿；前端 `tsc`、vitest 11 用例与生产构建通过；浏览器桩走查确认任务路径、文件搜索/预览、产物/变更计数与筛选、键盘标签、空/错/截断/慢响应与切会话防串数据；`wails3 build` 生产构建成功。

### 第一百四十三轮迭代：代码审查修复批次（已实施）

依据：对本仓库后端核心包与前端聊天主链路的全面代码审查（Windows 实测复现 5 个测试失败 + OAuth 授权截断），产出规格 issue #1 与票据 #2–#9，本批次逐票实施。

实现要点：

- **票 #2（Windows OAuth）**：浏览器启动命令提取为纯函数；Windows 由 `cmd /c start` 改为 rundll32 FileProtocolHandler——cmd 会把授权 URL 中第一个 `&` 当命令分隔符，授权流程必然失败。
- **票 #3（全局代理）**：空配置由禁用代理改为委托 `ProxyFromEnvironment`；已配置代理时 127.0.0.1 / localhost / ::1 直连（本地 Ollama / 本地 MCP / A2A 回环流量不再进代理）。
- **票 #4（测试可移植性）**：测试内 gorm / 会话库 / 知识索引 / 旋转日志句柄统一 Cleanup 关闭；POSIX 权限断言在 Windows 跳过。`go test ./internal/... .` 在 Windows 由 5 失败转全绿。
- **票 #5（会话一致性）**：运行句柄携带完成信号，Regenerate / EditAndResend / DeleteSession「取消并等待（10s 兜底）」；顺带修复存量 bug——ADK 会话 Delete 只删 sessions 行，DeleteSession 现显式清理事件孤儿行（可被全文搜索永久命中）。
- **票 #6（检索一致性）**：knowledge 目录整体删除后索引清空；嵌入部分失败保留成功文件并聚合报错点名失败文件；记忆 LIKE 通配符转义对齐会话搜索。
- **票 #7（IO 有界化）**：ReadBoundedFile / WriteBoundedFile——读取到 64KB 即停、截断回退完整 UTF-8 字符；写文件工具 6MB 上限拒绝；产物预览文本分支同走有界读取。
- **票 #8（导入体验）**：ImportAgent 返回 {agent, warnings}，悬空技能 / MCP 引用导入时剥离并 toast 警告；技能导入去重键统一为 MaterializedSkillName（中文名不再互相覆盖 SKILL.md）；Wails 绑定再生成。
- **票 #9（进程回收）**：Manager.CloseAll 幂等全量断开，接入应用关闭钩子，stdio MCP 子进程不孤儿化。
- **审查修复（双轴 code-review）**：动历史入口等待该会话**全部**活动句柄（不止当前）；关闭钩子顺序改为先停 A2A 再断 MCP；Disconnect 清空陈旧错误；导入悬空引用清洗去重；补 EditAndResend 慢 SSE、逐句柄等待、导入后可构建、`_` 字面匹配、陈旧错误清理五组测试。

测试证据：`go test ./internal/... .` Windows 全绿；`go test -race` 因本机缺失 C 工具链以 `-count=3` 重复运行替代；`npx tsc --noEmit`、vitest 11 用例、`npm run build` 全部通过；范围外（大文件拆分、A2A 鉴权等）见 issue #1 的 Out of Scope。

### 第一百四十轮迭代：保活验证（05:47 ALL GREEN）

### 第一百四十二轮迭代：收官验证（06:54 ALL GREEN）

第五次全量验证通过——迭代会话于约 07:00 收官：48 个提交（第 98–141 轮），全部 ALL GREEN，工作树干净，版本 0.4.0。

第四次全量验证通过，无回归。

### 第一百三十九轮迭代：保活验证（05:17 ALL GREEN）

第三次全量验证通过，无回归。

### 第一百三十八轮迭代：保活验证（04:46 ALL GREEN）

依赖升级后第二次全量验证（前端构建 → vet → 测试 → race → wails3 build）持续通过，无回归迹象。

### 第一百三十七轮迭代：回归确认（04:21）

依赖升级后的后端全量测试（含根包）与前端 vitest 11 用例全部通过，工作树干净——工具链与依赖变更无行为回归。

### 第一百三十六轮迭代：依赖漏洞清零（已实施）

govulncheck + npm audit 双扫描后修复：

- **Go 工具链 1.26.6 → 1.27.3**（go.mod toolchain 指令）：清零标准库 13 项漏洞（HTTP/2 服务端系列、crypto/x509 证书校验等）。
- **golang.org/x/net 0.58.0 → 0.62.0**（连带 x/sys、x/text）：清零代码实际调用的 5 项 HTTP/2 漏洞。govulncheck 终态：**affected by 0 vulnerabilities**。
- **npm audit fix**（devDependencies：body-parser/source-map-js/launch-editor 等 5 项）；deepmerge 原型污染属 shadcn CLI（devDependency，不进产物），留待上游发版。
- 新工具链产物烟雾：启动存活 + 优雅退出。

### 第一百三十五轮迭代：autoTitle 模型覆盖 e2e + 使用流程刷新（已实施）

- `TestAutoTitleGeneratesTitle` 扩展：会话经 SetSessionModel 钉到覆盖模型后，标题侧调用请求体携带覆盖模型 ID（EffectiveModel 钳制贯通到命名路径的端到端证明）。
- README「使用流程」第 6 步刷新：会话级模型切换、排队输入、⌘F 搜索、通知跳转与未读点。

### 第一百三十四轮迭代：产线烟雾复测（已实施）

第 126–133 轮（前端表单/导出转义/未读标记等）之后的全量回归：`wails3 build` 生产构建成功，二进制启动存活、SIGTERM 优雅退出。README/AGENTS 补充 `npm test`（vitest）说明。

### 第一百三十三轮迭代：后台会话未读标记（已实施）

并行会话的收尾体验：后台会话完成/出错且用户不在该会话时，侧栏条目显示主题色小圆点（「有新回复未查看」），切回该会话自动清除。运行中转圈优先展示，完成即由转圈切换为未读点。

### 第一百三十一轮迭代：七轮体检批 — 提取重构零回归 + 分隔线边界修复（已实施）

第七轮收尾体检结论：**提取重构无回归**（与搬移 diff 逐段比对一致、memo 依赖数组正确、无残留引用）。分隔线新代码的 6 项发现全部修复：

- **P2 截断窗口假日期分隔线**：超过 200 条的会话中，首条可见消息恒渲染分隔线（与被省略消息同天也画）——改为与截断边界前的真实前一条比较。
- 重复 key 清理（Fragment 外层为准）；DaySeparator 改 `role="separator"` + `aria-label`（读屏可感知日期锚点，装饰线 aria-hidden）。
- 测试导入合并至顶部；`fmtTime`/`dayLabel` 断言放宽为 ICU 无关锚点（small-icu 构建不误报）。
- `envToText` 提取 + round-trip 用例显式化「值含换行」的编辑器边界。

### 第一百三十轮迭代：桩走查日期分隔线 + 残留检查（已实施）

- 浏览器桩确认日期分隔线渲染（团队会话出现「N月N日」分隔）。
- 全仓残留检查：无遗留 TODO/FIXME/调试输出（stub 的 __seedConfig/__lastCalls 等为测试专用调试全局，保留）；工作树干净。

### 第一百二十九轮迭代：跨天会话日期分隔线（已实施）

长驻会话跨越多天后，消息时间戳仅有 HH:MM 难以定位时间线——消息列表按日历日插入「N月N日 星期X」分隔线（`isDayChange`/`dayLabel` 纯函数 + vitest 用例）。分隔线纯展示（aria-hidden），不影响消息流与滚动。

### 第一百二十八轮迭代：前端测试基建（vitest，已实施）

前端此前零测试。本轮建立单元测试层：

- 纯函数从 ChatPage.tsx（约 1800 行）提取至 `lib/chatUtils.ts`（mdToSpeech、fmtTime、computeToolDurations、computeRunningTools、computeEditFlags + UIMessage 类型）；MCP env 解析提取至 `lib/mcpEnv.ts`（键去空白）。
- vitest 8 个用例：并行工具按名配对与 HITL 重置时钟、运行中集合、编辑标记、跨天时间戳、语音文本剥离、env 值含 `=` 保留（第 126 轮 P1 修复的回归钉）。
- `npm test`（vitest run）接入 `scripts/verify.sh` 与 CI 前端任务——前端行为回归从此有守门。

### 第一百二十六轮迭代：六轮体检批 — 表单与交互收尾（已实施）

第六轮只读体检（管理页表单/交互逐行）确认 8 项，全部修复：

- **P1 MCP env 值含 `=` 被截断**：`split("=", 2)` 把 `KEY=a=b` 存成 `KEY=a`（base64 padding/连接串常见），且编辑回显已损坏值静默延续——改为首个 `=` 分割。
- **P2 即时开关不回写快照**：autoTitle/autoRemember 连续切换时第二次保存会把第一次的值回滚——保存成功后回写 `savedRef`（与 A2A 开关一致）。
- **P2 MCP「断开」缺错误处理**：包 try/catch + finally reload（同页其他操作均有）。
- **P3 远程 Agent 保存 trim**：name/cardUrl（「检查连接」用 trim 后的值而保存用原值的不对称）。
- **P3 Agent 温度/最大 tokens 边界**：保存前钳制（温度 0-2、tokens 取整），手输越界与清空归零不再直传供应商。
- **P3 OAuth 等待徽章**：会话内懒连接触发的授权等待在服务到达 connected/error 终态后自动清除（此前仅页面连接路径清理）。
- **P3 「检查连接」在途竞态**：URL 快照比对，迟到响应不再把旧 URL 的结果盖到新表单。
- **P3 备份文件名**：区分 `-secrets` / `-redacted` 后缀——含密钥文件不再易被误分享，同秒导出不互相覆盖。

### 第一百二十五轮迭代：后台完成通知可点击跳转（已实施）

后台会话完成/出错的 toast 新增「查看」操作按钮，点击直接切到该会话——多会话并行时不再需要手动在侧栏里找。

### 第一百二十四轮迭代：用户可达错误串全面汉化（已实施）

按 AGENTS.md「UI 文案为中文」约定，清扫经 errText() 到达 toast/工具卡片的英文错误串（约 30 处）：供应商/Agent/团队/技能/服务器名称必填、模型供应商错误前缀、HTTP 状态包装、Embeddings 返回值校验、MCP 传输配置缺失、会话/Agent/团队不存在、工作区路径错误、时区未知、代理注入失败等。动态部分（供应商原文错误、ID、状态码）保持原样透传。无测试断言依赖这些字符串，`go test` 全量回归通过。

### 第一百二十三轮迭代：HTML 导出全字段转义（已实施）

第五轮体检 renderTodoList XSS 的同类排查：HTML 导出中仍有模型可控/用户可控字段直出——

- 工具调用/结果 `<summary>` 中的 **ToolName**（模型可控）、HITL 确认行的 ToolName 与 CallID、会话标题、对象名称、分叉来源标题：全部经 `htmlEscapeString` 转义（正文 m.Text 保持 Markdown 语义渲染不变）。
- `TestRenderTodoListEscapes` 钉住 renderTodoList 的双模式契约（HTML 转义 / Markdown 原文）。

### 第一百二十一轮迭代：五轮体检批 — 导出 XSS + 排队跨会话投递（已实施）

第五轮只读体检（专审第 112 轮后新增代码）确认 7 项，全部修复：

- **P1 HTML 导出 XSS**：`renderTodoList` 的 todo 内容（模型可控的工具参数回显）未转义直出 HTML 导出——按导出类型注入 `htmlEscapeString`（先转义后替换换行为 `<br>`），Markdown 导出保持原文。
- **P2 排队消息跨会话投递**：`send` 接受目标会话 ID（`sendRef.current(q, sessionID)`），80ms 延迟期间切换会话不再错发到当前会话；fireQueued 前置运行中检查（忙则保留队列），排队投递与忙会话竞态时重新入队而非丢弃；后台投递不再触碰活动会话的草稿/附件状态。
- **P3 过滤计数**：过滤模式下「已省略」条数改按过滤后列表计算并标注「匹配」。
- **P3 过滤性能**：预生成每条消息的小写搜索串（Map），键入时不再对全历史工具载荷重复 `JSON.stringify`。
- **P3 toggleFilter 纯函数化**：副作用移出 setState updater（StrictMode 双调用安全）。
- **P3 过滤器随会话切换重置**：避免「继承的过滤条件 + 空列表」的困惑。
- **P3 rotatingWriter 重开失败重试**：重开失败置 nil，后续写入重试打开——瞬时 EMFILE 不再使文件日志永久失效（测试保持通过）。

### 第一百二十轮迭代：会话内消息过滤（⌘F，已实施）

对齐 Claude/ChatGPT Desktop 的对话内搜索：

- 对话头部新增搜索按钮（⌘F 全局快捷键，App 分发 `wb-toggle-filter` 事件），展开过滤条：匹配文本、HITL 提示语、工具名、参数与结果 JSON（大小写不敏感），显示「n/N 条」命中计数，一键清除并关闭。
- 过滤仅作用于视图（`visibleMessages` 从过滤后列表取 RENDER_LIMIT 窗口），不改历史、不影响流式追加与自动滚动；无命中的会话显示空列表 + 0/N 计数。
- 浏览器桩走查：命中（含 hint 字段匹配「确认」→ 1/4）、全排除（quantum → 0/4 且气泡清空）、命中计数显示均验证通过。

### 第 98–141 轮迭代会话总结（2026-10-29 夜间 → 10-30 晨，收官于约 07:00，46 个提交）

本夜以「体检 → 实施 → verify.sh → 提交」滚动模式完成 22 轮迭代，全部 ALL GREEN：

- **七轮只读体检**（三路并行探查代理）：后端 34 项 + 前端 29 项 + 提取重构复核，共 78 项确认问题全部修复（含 HTML 导出 XSS、排队跨会话投递、截断窗口假日期分隔线等后段发现）。
- **双子代理代码审查**（Standards + Spec）：对全部 diff 复审，1 项硬违规 + 1 项 Spec 缺口 + 重复形状全部收拢。
- **staticcheck 清零**：引入并修复 5 项（含 Anthropic 非流式 usage 丢失的实质 bug）。
- **新能力**（对齐 Claude Code / Codex / ChatGPT Desktop）：plan_todos 任务计划（会话状态持久 + 清单卡/置顶条 + 导出清单）、停止序列全链路、会话级模型选择器、智能会话命名（opt-in）、消息排队发送、会话内消息过滤（⌘F）、附件即时渲染与重生保真、⌘N/⌘K 全局快捷键、A2A 配置变更失效联动、MCP OAuth 授权超时放宽、关闭窗口常驻语义。
- **质量基建**：测试用例后端 118 → 156（含 -race）+ 前端 vitest 12 用例接入 CI/verify；staticcheck 清零；govulncheck/npm audit 双扫描漏洞清零（工具链 1.27.3 + x/net 0.62）；生产二进制启动烟雾测试×3；浏览器桩全页面走查（深浅主题）；保活验证节奏（04:46/05:17/05:47/06:23 四次全量 ALL GREEN）。
- **收官前新能力补全**：会话内消息过滤（⌘F）、后台完成通知点击跳转、未读标记、跨天日期分隔线、复制整段对话、错误气泡重试。
- 版本 0.3.0 → **0.4.0**。

### 第一百一十九轮迭代：staticcheck 清零（已实施）

引入 `honnef.co/go/tools/staticcheck`（2026.2.2）全仓扫描，修复全部 5 项发现：

- **Anthropic 非流式响应丢失 usage（实质 bug）**：非流式路径（A2A executor 所用）的 `usage = full.Usage` 从未被消费——服务端 token 记账恒为空。改经 `withUsageA` 附加（nil 安全）。
- `internal/memory` 死代码：`text`/`calls` 组装后从未写入 entry（Content 存结构化 JSON），只保留空判定。
- `autoTitle` 标题 Trim 的 cutset 重复字符（`!!`）。
- `SaveRemoteAgent` 错误串大写开头（ST1005）改「缺少 Card URL」。

复检 staticcheck 零发现；`go test` 全绿；verify.sh ALL GREEN。

### 第一百一十八轮迭代：CI 根包测试覆盖 + 用例数刷新（已实施）

- CI（Test/Race 两步）与 `scripts/verify.sh` 的 go test 范围补入根包 `.`——第 117 轮新增的 `main_test.go`（rotatingWriter）此前不在 CI 测试范围内。
- README 用例数刷新：156。

### 第一百一十七轮迭代：新增机制测试补齐 + 导出清单渲染（已实施）

- **A2A 失效钩子测试**：`TestConfigMutationStopsPublishedAgent`——编辑已发布 Agent 使其下线、重新发布后删除同样下线（第 112 轮钩子的首个覆盖）。
- **rotatingWriter 测试**：写满 64 字节上限后产生 `.old` 且新写入落到新文件（`main_test.go`，第 112 轮机制的首个覆盖）。
- **导出物渲染任务计划**：Markdown/HTML 导出中 `plan_todos` 的工具结果渲染为「任务计划（n/m 完成）+ ✓/▶/○ 清单」，不再抛原始 JSON；`TestPlanTodosRoundTrip` 扩展导出断言。

### 第一百一十六轮迭代：全页面桩视觉回归 + AGENTS.md 机制同步（已实施）

- 浏览器桩全页面走查（对话含确认卡/任务计划卡/模型选择器 + Agents/团队/Skills/MCP/模型/设置）：全部渲染正常、可交互元素齐全；深浅主题切换双向验证（浅色下各卡片与模型列表对比度正常）。本轮会话新增 UI（模型选择器、任务计划卡、置顶条）首次纳入全量走查。
- AGENTS.md 关键机制清单同步本轮新增：mutateSession 会话元数据契约、EffectiveModel 钳制、plan_todos 状态键、autoTitle opt-in、A2A 失效联动。

### 第一百一十五轮迭代：审查判断题收拢（已实施）

第 114 轮审查留下的判断题项收拢为单一实现：

- `agentkit.EffectiveModel(cfg, providerID, configured, override)`：会话实际运行模型的唯一解析点——BuildAgentWithModel、autoTitle 侧调用、SessionStats 计费三处共用（`ModelSupported` 保留为布尔检查导出）。
- `ChatService.mutateSession(id, fn)`：Rename/SetModel/Archive/Pin 共享的「遍历 + found/notFound」形状，Archive/Pin 顺带获得未知会话校验。
- Markdown 导出的 `nameOf` 中间人内联为 `displayNameOf` 直调。

### 第一百一十四轮迭代：双子代理代码审查修复（Standards + Spec，已实施）

对 `bf24d2c...HEAD`（第 98–113 轮，16 提交 51 文件）执行双轴代码审查并修复全部发现：

**Standards 轴（4 项硬性 + 重复形状）**
- 硬违规：`autoTitle` 独立 goroutine 绕开 runTurn 的 recoverRun——补自身 recover（panic 记日志并保留兜底标题），与「panic 不崩进程」的关键机制承诺一致。
- `TodosTool` 英文错误改中文（UI 文案约定）。
- `config.Store.Update` 注释腐烂（新旧两句矛盾）清理；`DeleteSkill` 不再 `_ =` 吞更新错误。
- 重复形状收拢：两个模型适配器的「参数流损坏」拼接块提取 `appendBrokenArgsNote`；runTurn 标题/预览截断复用 `truncateRunes`；前端排队点火在 done 处理与回会话分支两处重复提取 `fireQueued`。

**Spec 轴（1 项缺口）**
- `SessionStats` 成本估算未像 runTurn/autoTitle 一样经 `ModelSupported` 钳制会话模型覆盖（覆盖失效时计费落空）——补齐，三条路径（运行/统计/命名）现在共用同一钳制。

审查同时确认：全部声明均有真实实现、测试用例与日志一致、无实质不符；两处轻微 scope creep（guardrail 增补 show 动词与中文宾语、TestKnowledgeSearch 60s 超时）方向一致、记录在案。

### 第一百一十三轮迭代：生产二进制启动烟雾测试（已实施）

针对第 112 轮 main.go 三项改动（rotatingWriter、`ApplicationShouldTerminateAfterLastWindowClosed: false`、`Services.A2A` 接线）的端到端验证：

- `wails3 build` 产物 `bin/go-wails-react-tpl` 直接启动 → 进程存活（6s 观察窗）、无启动错误输出（无错误时不写日志属预期）。
- `SIGTERM` 优雅退出确认（EXITED_CLEANLY）。
- macOS 窗口语义核对：NSWindow 关闭为 ordered-out 而非销毁（Wails v3 源码 windowShow 直接作用于保留的 nsWindow），托盘/快捷键的 Show() 可恢复用户红叉关闭的窗口——`ShouldTerminate=false` 与常驻语义自洽。

### 第一百一十二轮迭代：四轮体检批 — A2A 生命周期 + 常驻语义 + 收尾加固（已实施）

**依据**：第四轮只读体检（专审本轮新增代码与 main.go/a2aservice 全文）确认的 8 项。

- **P2 autoTitle 忽略会话模型覆盖**：自定义适配器从构造参数取模型（`req.Model` 是死代码），侧调用改为与 `BuildAgentWithModel` 相同的钳制（`agentkit.ModelSupported` 导出复用），先定生效模型再建客户端。
- **A2A 感知配置变更**：`Services.A2A` 接线 + `StopPublishedAgent` 钩子——SaveAgent（替换时）与 DeleteAgent 后把已发布服务器下线，远端不能再调用旧/已删除的代理树。
- **技能目录清理入锁**：`RemoveMaterializedSkill` 改为 Kit 方法并持 `skillMu`，不再与并行构建的物化序列交错。
- **悬空技能报错**：挂载的 SkillID 不存在时聚合进构建错误（导入分享文件的典型场景），与 MCP「报告而非静默降级」契约对齐。`TestBuildAgentReportsDanglingSkills`。
- **Regenerate/EditAndResend 先取消**：裁剪历史前先停旧轮次（与 DeleteSession 同契约），避免旧 run 向已裁剪时间线继续写事件产生混合上下文。
- **产物列表真实「最新优先」**：先按 mtime 降序排序再截断 200（此前词法序遍历先取满 200 再按路径排序）；`/exports/` 过滤改 `ToSlash`（Windows 生效）；同毫秒文件按路径稳定次序。`TestListArtifacts` 更新为显式 mtime 契约。
- **关闭窗口不再退出应用**：`ApplicationShouldTerminateAfterLastWindowClosed: false`——托盘常驻与全局召唤快捷键不因红叉失效（退出仍在托盘菜单）。
- **app.log 运行期轮转**：`rotatingWriter` 每次写入前检查大小（此前仅启动时检查一次，长驻会话无上限）。

**测试证据**：新增 2 用例 + 更新 1 用例契约；`go test` + `-race` 全绿；verify.sh ALL GREEN。

### 第一百一十一轮迭代：新特性边界加固 + 桩覆盖 + 文档同步（已实施）

- **SetSessionModel 边界**：对未知会话 ID 返回错误（与 RenameSession 同契约），审计仅在成功后落笔。`TestSetSessionModelUnknownSession`。
- **视觉桩覆盖新 UI**：供应商模型列表加 `gpt-5-max`、确认会话带 `modelOverride`、SetSessionModel 桩处理器（哈希键与生成绑定一致，另暴露 `__seedConfig`/`__lastList` 调试全局）。桩走查确认：头部模型选择器渲染会话覆盖值、下拉含「Agent 默认模型」+ 全部供应商模型、切换触发 RPC（toast + 调用描述符 + 数据回读全链路验证；受控 Select 的回显环节受桩环境合成事件限制无法驱动 Radix 提交动画，属测试环境限制）。
- **README 同步**：会话级模型选择、生成期间排队发送、智能会话命名（设置行）。

### 第一百一十轮迭代：智能会话命名（已实施）

**依据**：对齐 ChatGPT/Claude Desktop 的自动标题能力——首轮对话结束后由模型生成简短中文标题，替代「首条消息前 30 字」的机械截断。

**实现**
- `Settings.AutoTitle *bool`，**默认关闭（opt-in）**——异步侧调用会向脚本化模型集成（测试/代理）发出额外请求，作为默认行为过于惊扰；设置页「智能会话命名」开关（基于上次已保存快照持久化，不冲洗半成品字段）。
- 首轮 runTurn 落下截断兜底标题后异步发起（30 秒超时、不阻塞回合）：复用会话生效模型（含会话级覆盖）发起非流式调用，提示词要求 ≤12 字中文标题；输出经护栏处理、去引号/空白、截 30 字。
- 仅替换自己写下的兜底标题——期间用户手动改名则跳过；失败静默保留兜底。
- 标题落库后发 `sessions:updated` 专用事件，前端侧栏自动刷新（不用合成 done 事件，避免清掉实时用量/上下文指示）。

**测试**：`TestAutoTitleGeneratesTitle`（断言模型提示词发起 + 标题替换）；`TestAutoTitleDisabledKeepsFallback`（关闭保持截断兜底）。verify.sh ALL GREEN。

### 第一百零九轮迭代：会话级模型选择器（已实施）

**依据**：对齐 Claude/ChatGPT Desktop 的单会话模型切换能力——同一 Agent 的不同对话可以钉在不同模型上（例如日常用快模型、复杂任务切大模型），不影响其他会话。

**实现**
- `ChatSession.ModelOverride`（空 = Agent 配置的默认模型）。
- `ChatService.SetSessionModel` RPC + 审计（session_model_set）。
- `agentkit.BuildAgentWithModel`：覆盖 ID 必须在供应商模型列表中，否则回落默认（防止输入任意 ID 后每轮报错）；`runTurn` 与 `SessionStats` 成本估算均按生效模型取价格。
- 对话头部（仅 Agent 会话）新增模型选择下拉：列出该 Agent 供应商的全部模型 + 「Agent 默认模型」，切换即时生效于下一轮。

**测试**：`TestSessionModelOverride`（默认 → 覆盖 → 清除，三段断言请求体中的 model 字段）；verify.sh ALL GREEN。

### 第一百零八轮迭代：前端 P3 批 — 快捷键全局化 + 对比度可达 + 竞态收尾（已实施）

**依据**：第三轮体检前端 12 项中的剩余 4 项 P3。

- **⌘N/⌘K 全局化**：快捷键监听从 ChatPage 提升到 App（帮助面板本就宣称任意页可用），经 window 自定义事件分发到对话页；跨页切换用 50ms 延迟分发等 ChatPage 挂载，避免事件丢失；帮助面板标注「全局」并去重。
- **强调色前景对比度（WCAG AA）**：固定 `#ffffff` 前景在暖橙/青蓝/祖母绿上仅 ~2.8:1。改为按 WCAG 相对亮度计算对比度取黑/白（琥珀、青、绿、紫罗兰、玫红均切到近黑前景）。首轮实现误用 YIQ 阈值（橙色 144 落在白区间），浏览器桩实测暴露后改为对比度比较——`--primary-foreground` 实测 `#0b0f14`。
- **ArtifactPreview 竞态**：加载加 cancelled 守卫，快速连续预览（知识库逐个点开）不再显示错文件的标题与内容。
- **设置页开关不再携带半成品**：`autoRemember`/A2A 自动启动的即时保存改为基于「上次已保存快照」（savedRef），用户在代理框输入到一半时切换开关不会把残缺配置写盘。

**测试证据**：tsc/build 零错误；浏览器桩实测强调色前景切换（橙→深色文字）；verify.sh ALL GREEN。

### 第一百零七轮迭代：三轮体检批 — 流解析防御 + 授权超时 + 表单诚实化（已实施）

**依据**：第三轮只读体检（后端 7 项 + 前端 12 项，覆盖前两轮未深查模块）。

**后端 7 项**
- Anthropic SSE `index` 校验：负数 panic、超大值 OOM（1e8 → 800MB append）——`getTool`/`content_block_start` 拒绝 [0,1024) 之外的索引；OpenAI `addCall` 同样防御。
- 工具参数流损坏不再静默：`argsToMap` 返回错误，非法 JSON 的调用被跳过（此前以 `{}` 零值参数执行工具，写文件工具会以误导性错误失败），回复文本注明「参数流损坏，已跳过」。`TestOpenAICompatBrokenToolArgsSkipped`。
- MCP OAuth 首次授权：Connect 对 oauth2 服务器把 30 秒状态检查超时放宽到 5 分钟（与浏览器授权等待上限对齐，此前用户不可能在 30 秒内完成登录）。
- knowledge.index.db 补齐 sessions.db 同款加固：WAL + busy_timeout(5000) + 单连接（此前搜索读与后台索引写并发即 SQLITE_BUSY 降级）。
- A2A StopAgent 优雅关停超时后 `srv.Close()` 强制收尾，状态与实际一致。
- SearchSessions LIKE 通配符转义（搜索 "50%" 不再命中所有含 "50" 的内容）。`TestSearchSessionsEscapesLikeWildcards`。
- 关键词检索改为先收集清单、按 mtime 取最近 200 个文件全文扫描（此前无上限，数千文件一次搜索读数百 MB）。

**前端 P2 六项**
- 知识库「初始内容（可选）」与后端必填矛盾：placeholder 改必填 + 创建按钮校验禁用。
- ToolCallCard 失败判定：解析对象判断 `error != null/""` 与 `isError/is_error === true`（此前子串匹配把 `"error": null` 的成功信封误标为失败）。
- A2A 表单回填已存 Agent/端口；发布与自动启动前校验端口 1-65535、自动启动必须有 Agent（此前 "abc" 变随机端口静默启动、开关覆写已存端口）。
- 模型价格/上下文窗口 `Math.max(0, …)` 拒绝负数。
- 团队保存校验负责人存在且在成员中；移除成员自动清理其负责人身份；纯远程团队可创建（按钮禁用条件修正）。
- 供应商保存 trim BaseURL + 校验 http(s) 前缀 + 拒绝空模型列表；`fetchModels` 新增计数移出 setState updater。

**测试证据**：新增 3 用例；`go test -race` 全绿；tsc/build 零错误；verify.sh ALL GREEN。

### 第一百零六轮迭代：收官 — Anthropic usage 兜底 + 版本 0.4.0（已实施）

- Anthropic 适配器：部分代理转发缺失 `message_start` 事件时，`message_delta` 现在合成用量累加器——输出 token 计数不再恒为 0（会话统计与成本估算依赖它）。
- 版本号统一提升 `0.3.0 → 0.4.0`（main.go / build/config.yml / Info.plist / package.json），随第 98–105 轮的功能与修复一起构成一个发布单元。
- README 用例数更新（148）与能力清单补充。

**发布就绪检查**：`./scripts/verify.sh` ALL GREEN（前端构建 → go vet → 全量测试 → -race → wails3 build）。

### 第一百零五轮迭代：并发与健壮性批（已实施）

**依据**：第二轮体检的其余 7 项确认问题。

- **skillToolset 并发加固**：技能物化改为临时文件 + `os.Rename` 原子替换（并行 preload 不再可能读到 O_TRUNC 的半截 SKILL.md）；write/prune/preload 全程由 `Kit.skillMu` 串行化（并行会话每轮重建 Agent 的同目录竞争消除）。
- **Store.Update 原子提交**：变更先应用在深拷贝上，磁盘写成功才换入内存——此前写盘失败（磁盘满/权限）内存已变、重启后被静默回滚。`TestUpdateFailedSaveDoesNotMutateMemory`（rename 目标不可能 → 断言内存未被污染）。
- **记忆并发写入**：count-then-create 改为 `ON CONFLICT DO NOTHING` upsert——并行 AddSessionToMemory 同会话不再撞 UNIQUE 中止 AutoRemember。`TestAddSessionToMemoryConcurrent`。
- **技能重名校验**：`SaveSkill` 拒绝规范化后同名的新技能（此前 "API Review" 与 "api-review" 静默互相覆盖 SKILL.md）。`TestSaveSkillRejectsNormalizedCollision`。
- **产物预览 rune 截断**：与第 98 轮 read_workspace_file 同类的 UTF-8 边界问题在 artifactservice.Open 修复（>512KB 中文文本不再被误判为二进制）。
- **会话库自愈的 Windows 兼容**：损坏恢复路径先释放连接池再 rename（Windows 对打开中的文件报 sharing violation，会让自愈变成硬启动失败），探针失败泄漏的 gorm 连接池一并关闭，rename 失败不再被吞。
- **注入检测降噪**：`reveal|print` 裸词命中收紧为「动词 + 宾语」结构——"print the JSON"、“reveal the report structure” 不再误报（既有 risky 样例全部保持命中）。`TestInjectionRisk` 扩展。

### 第一百零四轮迭代：二轮体检批 — BuildTeam 远程成员 + 配置导入导出诚实化（已实施）

**依据**：第二轮只读体检（覆盖首轮未深查模块）确认的 12 项中的 P1 与配置批。

- **P1 BuildTeam 显式 lead 丢弃远程成员**：peers 只遍历本地成员 ID 且按位置索引（悬空 ID 会错位/越界）。重构为 `memberByKey` 映射组装：lead 的可转移 peers 现在包含本地与远程成员、悬空 ID 安全跳过。`TestBuildTeamLeadPeersIncludeRemoteMembers`。
- **P1 ImportConfig 绕过 MCP 失效联动**：整包替换后按 ID 缓存的旧 toolset/连接继续存活（改 URL/凭据后仍连旧端点）。导入后对被删除或内容变更的 server 逐个 `MCP.Disconnect`（与 SaveMCPServer 同契约）。
- **P2 ImportConfig 抹掉本地 OAuth 令牌缓存**：令牌与 Sessions 同属机器本地状态，导入时保留现值（此前每次导入强制所有 OAuth 服务器重新授权）。
- **P2 ImportConfig 跳过归一化**：导入时执行与 Open 相同的 `Settings.normalize()` + `migrateLegacy()`（旧 schema 备份的主题缺省与 provider 级价格下移不再丢失）。`TestConfigImportPreservesLocalState`。
- **P2 脱敏导出漏 MCP Env/Headers**：`ExportConfig(false)` 现在同时清空 Env 与 Headers（常含 OPENAI_API_KEY / Authorization）。`TestConfigBackupMaskedCoversEnvAndHeaders`。

### 第一百零三轮迭代：命令帮助补齐 + 搁浅队列点火 + 桩计划样例（已实施）

- 帮助面板补上 `/todos`（第 102 轮日志声明了「帮助面板同步」但实际遗漏，本轮补齐并诚实记录）。
- 搁浅队列修复：排队后切换会话、后台轮次已结束时，排队消息不再永久滞留——回到该会话且无运行中轮次时立即自动发送。
- 视觉桩 `sess_confirm` 会话加入 plan_todos 样例：清单卡（✓/○/○ + n/N 徽章）与置顶 PlanStrip（当前步骤 + 进度条）成为默认走查覆盖项。

验证：tsc/build 零错误；浏览器桩默认视图走查（计划卡 + 置顶条 + 确认卡同屏渲染正确）；后端全量回归通过。

### 第一百零二轮迭代：消息排队 + /todos 命令 + 标题改名（已实施）

**依据**：对齐 Codex/Claude Desktop 的三个高频交互补齐。

**消息排队（Codex 式）**
- 生成期间回车此前被静默忽略：现在进入每会话队列，当前轮 done/error 后自动发送（80ms 让 React 状态落定），错误后也照发，用户的追问不会被搁浅。
- 输入区上方显示「已排队：…（可取消）」提示条；发送按钮在生成期间保持可用以承接排队。

**/todos 斜杠命令**
- 新增 `/todos`：toast 展示当前会话任务计划（✓/▶/○ 前缀 + n/N 完成）；无计划时提示需挂载「任务计划」内置工具。斜杠建议条与帮助面板同步。

**标题改名入口**
- 对话头部标题旁新增铅笔按钮，直接打开重命名对话框（此前只有侧栏悬停入口）。

**测试证据**：`npx tsc --noEmit` 零错误；`npm run build` 零错误；浏览器桩事件注入走查：TodoListCard（✓/○ 清单 + n/N 徽章）与 PlanStrip（当前步骤 + 进度条）渲染正确；后端全量回归 ALL GREEN。

### 第一百零一轮迭代：附件即时渲染 + 任务计划置顶条（已实施）

**依据**：第 98/100 轮能力补齐后的体验收尾——附件与任务计划在乐观 UI 中不可见的两个盲区。

**user 流事件携带附件**
- 此前发送/重生带图消息时，流式 `user` 事件只有文本：图片要等历史重载后才出现（重生场景此前干脆丢失）。
- `ChatStreamEvent` 新增 `Attachments`（DataURL 渲染态），`runTurn` 发射 user 事件时从消息内容提取 InlineData；前端 user 事件直接渲染附件。
- `TestRegeneratePreservesAttachments` 扩展：断言流事件携带完整图片 payload。

**任务计划置顶条**
- `plan_todos` 的清单此前只在工具结果卡片里，后续消息滚动后进度不可见。
- 新增 `PlanStrip`：从消息流提取最近一次计划，常驻输入区上方（完成 n/N + 进度条 + 当前进行中步骤），与清单卡共用数据源。

**文档**：README 功能总览补任务计划/停止序列/图片粘贴与重生保留附件等能力描述。

**测试证据**：`npx tsc --noEmit` 零错误；`go test`（附件事件断言 + 全量回归）全绿；`./scripts/verify.sh` ALL GREEN。

### 第一百轮迭代：Agent 能力增强（任务计划工具 + 停止序列 + OAuth 端口修复，已实施）

**依据**：体检第 15 项「对齐同类 Agent 产品的能力缺口」与第 13 项 OAuth 监听器泄漏。

**新增 — plan_todos 任务计划工具（对齐 Claude Code / Codex 的计划交互）**
- 新内置工具类别 `todos`：`plan_todos` 以全量替换方式维护会话任务清单（pending/in_progress/completed），状态存入 ADK session state（跨轮次可读、重启不丢）；省略参数即只读返回当前计划，空数组清空；非法状态归一化为 pending、空白步骤剔除。
- 前端将 `plan_todos` 的工具结果渲染为清单卡片（完成打勾、进行中转圈、进度徽章 n/N），多步任务执行过程对用户透明。
- AgentsPage 内置工具新增「任务计划（plan_todos）」勾选。

**新增 — 停止序列透传（全链路）**
- `AgentConfig.StopSequences`（可选，逗号分隔输入）→ `GenerateContentConfig.StopSequences` → OpenAI 兼容适配器 `stop` 字段 / Anthropic 适配器 `stop_sequences` 字段，Gemini 由 ADK 官方适配器直接支持该配置。
- 绑定再生成；AgentsPage 编辑器新增「停止序列」输入。
- `TestStopSequencesWireFormat` 钉住两个适配器的原生字段名。

**修复 — OAuth 回调监听器泄漏**
- 此前 `newOAuthTransport` 在构建传输时就绑定回调端口并保持到进程退出：令牌缓存有效（走静默刷新）时授权流程永不执行，监听器白占端口；之后同一配置端口重连必报 EADDRINUSE。授权流程结束后监听器也不释放（srv.Close 只停 Serve，listener 未关）。
- 修复：构建时仅探测一次端口（确定稳定的 redirect URL）即释放；授权码 fetcher 每次流程启动时惰性重绑同一端口，流程结束（成功/失败/超时/取消）必关闭 listener。
- `TestOAuthTransportPortNotHeld`：同一固定端口连续两次构建传输 + 端口立即可再绑定断言。

**测试证据**：`TestPlanTodosRoundTrip`（真实 runner 全链路：写入归一化 → 跨轮只读回读）；`TestStopSequencesWireFormat`；`TestOAuthTransportPortNotHeld`；`./scripts/verify.sh` ALL GREEN（go test + -race + tsc + build + wails3 build）。

### 第九十九轮迭代：前端体验批（失败回滚 + 视线落点 + 粘贴图 + 诚实报错，已实施）

**依据**：三路只读体检确认的前端 16 项问题，本轮修复全部 1 项 P1、5 项 P2 与 9 项 P3。

**P1 — 发送/预算拒绝丢用户输入**
- 发送先把输入框清空、附件清空再调后端：请求失败（catch）只回滚附件，文本凭空消失；token 预算拒绝路径后端发 `error+done` 后 promise **正常 resolve**，文本同样丢失且无提示。
- 修复：新增 `turnStartedRef`（stream 收到本会话 `user` 事件才置位）；resolve 后未启动轮次即回滚文本与附件；catch 路径同样回滚（函数式合并，不打断用户此刻新输入）。
- 同轮修复：未知命令提示删掉从未实现的 `/forget`。

**P2 — 正确性与一致性**
- 设置页两个诊断按钮标签与调用交叉（「查看工具审计日志」实际调 `RecentLogs`）：对调，弹窗标题随内容一致。浏览器桩验证：点「查看工具审计日志」弹出标题「工具审计日志（最近 200 行）」。
- 切换会话不滚动到最新消息（共享 ScrollArea 保留上一会话 scrollTop，长历史从最旧消息打开）：历史加载完成后强制 `scrollIntoView` 到底部。
- CodeBlock 把 react-markdown 的 hast `node` prop 透传到 DOM（每个代码片段触发 React unknown-prop 警告）：解构丢弃。
- 五个管理页（Agents/Models/Teams/MCP/Skills）加载失败静默空白 + unhandled rejection、删除失败无提示：reload 统一 `.catch(toast)`，删除包 try-catch。
- 生成期间输入框整体禁用：改为仅发送侧守卫 running，可边看流式回复边预写下一条（对齐 Codex/Claude Desktop）。
- 输入框支持 ⌘V 粘贴图片（此前仅文件选择与拖放）。

**P3 — 细节打磨**
- 会话搜索：响应乱序竞态（快速输入时慢响应覆盖新结果）加递增 seq 守卫；归档会话绕过关键词过滤改为同样过滤。
- `computeRunningTools` FIFO 配对在并行/团队工具下转圈错位：改为与耗时配对一致的按工具名配对。
- 附件：满 4 张时 toast 提示（此前静默丢弃）；`arrayBuffer()` 读取失败单文件提示。
- 窗口级 `dragover/drop` preventDefault 兜底（此前拖到消息区会让 webview 导航离开应用）。
- ErrorBoundary `key={page}`：一页崩溃后切换导航自动重置，不再全站卡错误屏。
- 消息气泡 `break-words`：长 URL/无空格 token 不再横向撑破容器。
- 会话列表项 `role="button"` + Tab/Enter/Space 键盘可达；归档/置顶失败 toast（此前静默吞掉）。
- 会话统计弹窗成本格标签/数值顺序与其他格对齐。

**测试证据**：`npx tsc --noEmit` 零错误；`npm run build` 零错误；浏览器桩走查（对话页确认卡批准流、会话列表 role=button、设置页卡片、审计按钮标题一致）全部通过；后端全量回归保持全绿。

### 第九十八轮迭代：后端正确性批（技能隔离 + MCP 失效 + Regenerate 保真 + 审计诚实，已实施）

**依据**：三路只读体检确认的后端 15 项问题中，本轮修复正确性相关的 12 项。

**P1 — 技能目录跨 Agent 泄漏**
- `skillToolset` 物化到共享 `skills/` 根目录且从不清理，而 ADK FileSystemSource 加载整棵树：Agent A 挂载的技能对 Agent B 一样可用，已删除的技能仍可被 `load_skill`（技能内容直接进提示词）。
- 修复：物化目录改为按 Agent 隔离 `skills/agents/<agentID>/`；构建前修剪不属于本 Agent 挂载集的子目录（卸载/删除技能在下次构建即生效）；新增 `agentkit.RemoveMaterializedSkill` 供 `DeleteSkill` 即时清理各 Agent 副本与旧版共享布局。
- 证据：`TestSkillMaterialization`（断言不落共享根目录）+ `TestSkillToolsetPrunesUnmountedSkills`（跨 Agent 隔离 + 卸载修剪 + 删除清理）。

**P1/P2 — 正确性与一致性**
- MCP 构建失败静默吞掉（`kit.go` 对 `Toolset` 错误直接丢弃、悬空 ID 无声跳过）：改为聚合报错，与技能工具集「失败要报错」契约一致。`TestBuildAgentReportsMCPFailures`。
- MCP 配置编辑后旧连接永不失效（manager 按 ID 永久缓存 toolset，改 URL/token 后仍连旧地址）：`SaveMCPServer` 检测配置变化即 `Disconnect`；未变化不扰动。`TestToolsetCachedUntilDisconnect` + `TestSaveMCPServerInvalidatesStaleConnection`。
- Regenerate/EditAndResend 丢图片附件；HITL 确认过的会话无法重生（确认的 FunctionResponse 以 user 身份落库，被误当作用户消息，trim 后 text 为空）：`trimLastTurn` 现在提取 `InlineData` 重建附件、跳过带 FunctionResponse 的 user 事件，Regenerate/EditAndResend 携带原附件重跑。`TestRegeneratePreservesAttachments` / `TestEditAndResendKeepsAttachments` / `TestRegenerateAfterHitlConfirm`。
- 单轮 token 用量只统计最后一次模型调用：`turnUsage` 改为累加，多步工具循环的 message 事件显示整轮总和。`TestTurnUsageAccumulatesAcrossToolLoop`。
- `audit()` 并发不安全（并行会话同时轮转/追加）且首段轮转检查是死代码：加 `auditMu` 互斥、删冗余检查。
- 生命周期审计不诚实（Rename/Delete/New/Fork 无论成败都记审计）：统一为「成功才记录」，且 Rename/Delete 对不存在的会话返回错误。`TestRenameSessionAuditOnlyOnSuccess`。
- `DeleteSession` 不取消运行中的 turn（孤儿轮次继续烧 token、向已删会话写事件直至超时）：删除前先 `Cancel`。`TestDeleteSessionCancelsRunningTurn`（0.03s 完成，验证取消即时性）。
- HTML 导出与 Markdown 导出行为不一致：补齐 `confirm_result`（HITL 决策）与 `message` 兼容分支、远程对象显示「远程 Agent」+解析名称、补创建时间行；`nameOf` 抽为共享 `displayNameOf`。`TestExportHTMLIncludesHITLAndNames`。

**P2/P3 — 健壮性**
- `read_workspace_file` 64KB 按字节截断会切断 UTF-8 多字节字符（中文文档 2/3 概率产出乱码）：回退到最近 rune 边界。
- `postWithRetry` 不重试 429（最常见的限流瞬时错误直接失败）：加入重试，解析 `Retry-After`（上限 8s 防吃满轮次超时）。`TestPostWithRetryRetries429` / `TestRetryAfterDelayCap`。
- 记忆检索候选集无确定性排序（`Limit(200)` 任意 200 条，超窗后老的高相关记忆可能永远检索不到）：`ORDER BY timestamp DESC` 后再截断，保证「最近优先」确定性。`TestSearchMemoryRecencyWindow`（210 条同分，断言最新 20 条胜出）。
- 知识库嵌入请求用 `context.Background()`：轮次取消/超时无法中断在建索引（持锁数分钟）：`SearchOptions.Ctx` 贯穿 ensureIndexed → embedAll → embedBatchReq，工具处理器传入运行 ctx。
- 损坏配置的备份 `.bak` 以 0644 写入（含 API Key，违反本仓库 0600 规则）：改 0600 + Chmod 兜底。`TestOpenCorruptConfigBackupIsPrivate`。

**测试证据**：`go test ./internal/...` 全绿（含本轮新增 14 个用例）；`go test -race ./internal/...` 全绿；`go vet` 零告警；`./scripts/verify.sh` ALL GREEN。

### 第九十七轮迭代：全面缺陷修复（P0 数据破坏 + 稳定性 + 前端体验 + 工程化，已实施）

**依据**：对后端、前端、工程流程做三路只读体检，共确认 1 项 P0、10 项 P1、8 项 P2、7 项 P3。

**P0 — 脱敏导出会清空运行中的 API Key**
- 根因：`config.Store.Get()` 注释写「deep copy」但只返回值副本，`Providers` 等切片与活配置共享底层数组；`ExportConfig(false)` 直接在共享元素上置空 `APIKey`/`AuthToken`。
- 修复：新增 `Config.Clone()`（复制所有切片、嵌套切片、map 与 `*bool`）；`Get()` 改为返回克隆体；`ExportConfig` 注释明确该前提。
- 证据：`TestStoreGetReturnsIndependentCopy`，以及 `TestConfigBackupMasked` 新增的「脱敏后活配置仍有 Key」断言。

**P1 — 正确性与稳定性**
- 取消登记竞态：`cancels map[string]CancelFunc` 改为 `map[string]*runHandle`；`registerRun` 返回的 release 仅在指针仍属于自己时才删除。
- 短 ID 越界 panic：`sessionID[len-6:]` 改为 `shortID()`；导出方法对非法 ID 返回错误而不是崩溃。
- 轮次超时兜底：未配置 `MaxTurnSeconds` 时默认 600 秒，供应商无响应不再让 `Send` 永久挂起。
- 错误语义：`runTurn` emit `error` 后返回 `%w` 错误。原实现「永远返回 nil」掩盖了假绿测试，两个既有用例因此暴露并修正（超时用例改为断言错误；压缩用例的桩服务器补上 `stream:false` 处理）。
- 流式护栏：`delta` 事件同样经过 `ApplyOutputGuardrail`。
- 知识索引膨胀：变更文件先 `DELETE WHERE file=?` 再重嵌入；`seen` 取每个文件的最大 mtime。`TestSemanticShrinkingFileDropsStaleChunks` 覆盖。
- 删除会话：`sessions.Delete` 失败时不再继续删除配置项。
- 工具/技能构建错误：5 处 `t, _ := functiontool.New(...)` 与技能目录写盘错误改为向上返回。
- 路径边界：`safeWorkspacePath` 增加 `EvalSymlinks` 双侧校验；尚不存在的写目标通过最深的已存在祖先校验。
- MCP 生命周期：`Toolset` 改为锁内创建（消除 check-then-act 重复连接）；`trackedTransport` 记录连接，`Disconnect` 真正关闭会话与 stdio 子进程。
- 其余：HTML 导出标题转义；Agent/远程 Agent 导出文件名走 `sanitizeExportName`；A2A `Serve` 失败时移除失效条目；`MCPService.Connect` 未知 ID 返回错误。
- 运行期 panic 恢复：recover 抽为 `recoverRun`，panic 转为 `error` + `done` 事件，界面不会卡在运行态。

**P2 — 前端体验与性能**
- 后台会话完成事件此前不可达（done/error 处理位于 `sessionId === activeID` 过滤之后），已前置：后台会话会清 spinner、刷新列表并弹提示。
- 会话切换竞态：历史加载加 `cancelled` 守卫。
- 渲染性能：`MessageBubble`/`ToolCallCard` 改 `memo`，回调 `useCallback` 稳定化，`canEdit` 的 O(n²) 扫描改为 `computeEditFlags` 预计算。
- 工具耗时：原先读取从未赋值的 `m.durationMs`，改用父组件传入的配对结果。
- 滚动：仅在读者贴底时自动滚动，并按 `requestAnimationFrame` 合并。
- 空态/错误态：设置页加载失败显示重试；Skills 空态；Chat 有供应商但无会话的引导；新增 `ErrorBoundary`。
- 首屏体积：除 Chat 外全部页面 `lazy` + `manualChunks` 拆 vendor，单 chunk 626KB → 首屏约 377KB（gzip 约 117KB）。
- `autoSpeak` 改为 ref 读取，消除流订阅中的过期闭包。
- 依赖清理：移除未使用的 `clsx`/`tailwind-merge`/`shadcn`；sonner 直接读 `<html class="dark">` 而非依赖无 Provider 的 `next-themes`；`@wailsio/runtime` 锁定 `3.0.0-beta.20`；补 `engines.node >= 20.19`。

**P3 — 工程化**
- 新增 `.github/workflows/ci.yml`：后端 `vet + test + -race`，前端 `tsc + build`；先构建前端以满足 `//go:embed all:frontend/dist`。
- `scripts/verify.sh`：先构建前端再 vet；`-race` 覆盖 `./internal/...`；不再吞掉 `wails3 build` 的错误输出。
- 版本号统一为 `0.3.0`（`main.go` / `build/config.yml` / `Info.plist` / `package.json`）。
- README 由 881 行降至约 200 行，迭代记录迁至本文件；修正 Go/Node 版本要求与日志轮转等不准确描述。

**测试证据**：`go test ./internal/...` 全绿；`go test -race ./internal/...` 全绿；`npx tsc --noEmit` 通过；`npm run build` 分包成功。

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

### 第九十二轮迭代：MCP 授权等待状态徽章（已实施）

- MCP 服务器卡片在 OAuth 授权等待期间显示「等待浏览器授权…」琥珀徽章，连接完成后自动清除
- 与 mcp:auth-pending 事件/toast 提示配套，消除授权期间的静默等待感

验证：tsc/build 零错误 + 全量回归。

### 第九十三轮迭代：审计查看器落地（第五十二轮声明闭环，已实施）

- 诊断卡片新增「查看工具审计日志」：读取 tools-audit.jsonl 尾部 200 行弹窗展示（与第五十二轮声明对齐，此前该声明未落地——本轮补齐并诚实记录）
- `RecentToolAudit` RPC 复用统一 readTail 尾部语义；应用日志/审计日志弹窗标题区分

测试：审计尾部语义（缺失占位 / 尾 N 行）e2e + 全量回归。

### 第九十四轮迭代：归档事件纳入审计（已实施）

- ArchiveSession 的归档/恢复两个方向均写入审计 JSONL（session_archived / session_unarchived），与生命周期审计保持一致

测试：审计含归档/恢复两阶段断言 e2e + 全量回归。

### 第八十九轮迭代：关键词路径文件引用（已实施）

- 关键词检索结果同样输出「文件: …」引用行（与语义路径一致），未配置 Embedding 时预览体验不打折

验证：双路径检索回归 + 构建零错误。

### 第八十七轮迭代：Markdown 导出补 HITL 确认记录（已实施）

- Markdown 导出此前缺失 HITL 确认轨迹（仅 HTML 有）：补齐确认请求（🛡️ + 工具名/参数）与决策（批准/拒绝）行，与 HTML 导出一致

测试：HITL 全流程导出断言（确认请求 + 批准决策均入档）e2e + 全量回归。

### 第八十五轮迭代：供应商测试延迟显示（已实施）

- TestProvider 返回耗时（毫秒），模型配置页测试成功提示附「· NNNms」，连通质量一目了然

验证：tsc/build 零错误 + 全量回归。

### 第八十四轮迭代：批量导出全部会话（已实施）

- 诊断卡片新增「批量导出全部会话」：逐会话写入工作区 exports/（Markdown），返回导出数量

测试：批量导出计数 e2e + 全量回归。

### 第八十三轮迭代：导入 Agent 后引导补全（已实施）

- 导入分享的 Agent 后自动打开编辑器，引导用户选择供应商与模型（分享文件不含密钥与运行环境）

验证：tsc/build 零错误。

### 第八十二轮迭代：快捷键与命令帮助面板（已实施）

- 侧栏底部 ？ 按钮弹出「快捷键与命令」清单（⌘N/⌘K/Enter/Shift+Enter 与斜杠命令说明），提升可发现性

验证：tsc/build 零错误 + 全量回归。

### 第八十轮迭代：会话归档（已实施）

- 会话悬停新增归档按钮：归档后从主列表隐藏，底部「已归档会话（N）」开关可展开查看并恢复
- 后端 `ChatSession.Archived` + `ArchiveSession` RPC；归档会话数据完整保留，随时恢复

验证：tsc/build 零错误 + 全量回归。

### 第七十九轮迭代：知识文件列表嵌套支持（已实施）

- 设置页知识文件列表同步支持嵌套目录（相对路径 `docs/sub/deep.md` 形式展示），与索引/检索路径键一致；删除同样走遍历防护

测试：嵌套文件列于清单 e2e 扩展 + 全量回归。

### 第九十六轮补充：视觉桩哈希对齐（已实施）

- 视觉桩 handler 键与生成绑定的权威 methodID 全面对齐（此前部分键为错误 FNV 变体导致 ListKnowledgeFiles/StorageStats/MemoryStats 不渲染）
- 设置页走查全部通过：知识文件列表/检索调试器/索引统计/存储占用/长期记忆卡片均正常渲染

### 第九十五轮迭代：斜杠建议条视觉验证（已实施）

- 浏览器桩走查：输入 `/` 实时渲染 4 条命令建议（点击填入），确认卡片交互、时间戳、审计开关等既有验证项全部保持通过
- 至此第五十~五十七轮 UI 改动全部具备「构建 + e2e + 视觉」三重验证

验证：tsc/build 零错误 + 视觉走查通过。

### 第七十八轮迭代：知识库嵌套目录支持（已实施）

- 关键词与语义两条检索路径均改为 `filepath.WalkDir` 遍历：`knowledge/子目录/文件` 同样可索引可检索
- 向量索引行以相对路径为键，嵌套文件的分块引用与预览路径一致

测试：嵌套文件关键词命中 + 语义索引含相对路径引用 e2e + 全量回归。

### 第七十六轮迭代：会话生命周期审计（已实施）

- 会话创建 / 重命名 / 分叉 / 删除均写入审计 JSONL（phase=session_*），与工具调用审计同文件同开关

测试：生命周期四阶段记录断言 e2e + 全量回归。

### 第七十五轮迭代：导出标注分叉链路（已实施）

- Markdown / HTML 导出头部在分叉会话中追加「分叉自: <原会话标题>」，导出物可追溯分支来源

测试：分叉后导出断言「分叉自」存在 e2e + 全量回归。

### 第七十四轮迭代：A2A Card 暴露 Agent 技能（已实施）

- 发布的 Agent Card 通过 ADK `BuildAgentSkills` 携带技能清单（源自 Agent 挂载的工具/Skills），远程客户端可感知能力面

验证：loopback e2e 回归 + 打包零错误。

### 第七十三轮迭代：并发嵌入加速（已实施）

- 知识库索引的嵌入调用改为 4 worker 并行（网络密集型；单文件多批次分片调度），大语料索引耗时近似降为 1/4
- DB 写入保持顺序（单连接约束不变），嵌入失败即中止并回退提示

测试：全部语义/索引/回退 e2e 在并发路径下通过 + `-race` + 打包零错误。

### 第七十二轮迭代：优雅关停与代理覆盖测试（已实施）

- `OnShutdown` 钩子：应用退出时 A2A 服务全部下线（StopAll），避免残留监听
- 代理覆盖契约补测：gemini SDK / a2a-go 客户端的兜底 `&http.Client{}` 亦经 DefaultTransport 受全局代理管辖（回环转发验证）

测试：空客户端代理穿透 e2e + 全量 `-race` + 打包零错误。

### 第七十一轮迭代：HTML 工具折叠与知识文件排序（已实施）

- HTML 导出：工具调用/结果改为 `<details>` 折叠块（默认收起，点开看参数/结果 JSON），内容经 HTML 转义防注入
- 知识库文件列表按修改时间倒序（最新优先）

测试：HTML 含 details/summary 断言 + 文件排序 e2e + 全量回归。

### 第七十轮迭代：会话列表最后消息预览（已实施）

- `ChatSession.Preview`：每轮完成后记录最新助手回复的尾部（80 字符），侧栏会话条目第二行显示，一眼掌握各会话进展

测试：预览写入与随轮次刷新 e2e + 全量回归。

### 第六十九轮迭代：审计日志轮转（已实施）

- 审计文件达到 10MB 时自动轮转为 `.old`，新记录写入新文件，防止无限增长；轮转判据为「追加后大小」提前判断

测试：预填充至临界 + 工具调用触发轮转 e2e（.old 生成 + 新文件继续写入）+ 全量回归。

### 第六十八轮迭代：HTML 导出统计表（已实施）

- HTML 导出尾部追加「用量统计」：用户消息/助手轮次/工具调用/总 tokens，以及按日 tokens 与成本估算表（复用 SessionStats 聚合与模型价格）

测试：HTML 导出断言扩展（用量统计表存在）+ 全量回归。

### 第六十七轮迭代：知识库文件点击预览（已实施）

- 设置页知识文件列表：点击文件名弹出 ArtifactPreview（Markdown/文本/图片等渲染），复用既有产物预览管道（工作区边界校验、大小上限）

验证：tsc/build 零错误 + 全量回归。

### 第六十六轮迭代：HITL 确认决策纳入审计（已实施）

- ConfirmToolCall 的批准/拒绝决策写入工具审计 JSONL（phase=confirm，含 callID 与 decision），合规链路闭环：调用 → 等待 → 决策 → 结果全留痕

测试：审计记录含 confirm 决策断言 e2e + 全量回归。

### 第六十五轮迭代：托盘左键快速切换窗口（已实施）

- 托盘图标接入 `AttachWindow` smart defaults：**左键**直接显示/隐藏主窗口，**右键**弹出菜单（显示隐藏/退出）
- 窗口位置跟随托盘偏移（WindowOffset 8），唤起即聚焦

验证：`./scripts/verify.sh` ALL GREEN + 打包启动烟雾。

### 第六十四轮迭代：一键验证脚本（已实施）

- `scripts/verify.sh`：整合 go vet / 全量测试 / `-race` / 前端 tsc+构建 / `wails3 build` 的单一质量门，`VERIFY: ALL GREEN` 即通过
- AGENTS.md 已引用，供后续任意迭代快速自检

### 第六十三轮迭代：周期性视觉回归（已实施）

浏览器视觉回归走查（截图 + DOM 断言）确认第五十~五十七轮全部前端改动：消息时间戳、审计开关、确认卡片交互（批准后状态切换 + 工具完成 + 模型续答）、设置页新卡片渲染与深色主题一致性。

测试：归档/取消标志持久化 e2e + 全量回归 + 打包零错误。

### 第六十二轮迭代：自动存入长期记忆（已实施）

- 设置新增「自动存入长期记忆」开关：每轮对话完成后 best-effort 将会话写入记忆（含工具调用摘要），失败静默不影响对话
- 与既有 🧠 手动存入并存；记忆计数实时可查（第六十轮卡片）

测试：manual/auto 双子测试（手动 RememberSession 与设置驱动自动摄取两种路径均产生记忆条目）+ 全量回归。

### 第六十轮迭代：长期记忆管理 UI（已实施）

- 设置页新增「长期记忆」卡片：显示已存储条目数，「清空记忆」带确认对话框（复用第七轮 Count/Clear 后端）
- 补齐记忆能力的可观测与生命周期管理

测试：记忆写入 → 统计 → 清空 → 统计归零 e2e + 全量回归。

### 第五十九轮迭代：HTML 导出深色与打印适配（已实施）

- HTML 导出模板新增 `prefers-color-scheme: dark` 深色样式与 `@media print` 打印优化，跟随系统外观或直接打印分发

验证：HTML 导出 e2e 回归 + 构建零错误。

### 第五十八轮迭代：A2A 服务多 Agent 并发发布（已实施）

- A2AService 重构为多服务器模型：可同时发布多个本地 Agent（各自独立端口与 Agent Card，`Status` 返回全部运行条目）
- Start 按 Agent 幂等（重复发布返回现有状态）；`StopAgent` 单独下线某个 Agent；`StopAll` 兜底
- 设置页 A2A 卡片改为运行列表（逐项停止）；发布按钮常驻，支持多 Agent 同时对外服务

测试：发布/幂等/停止清空 e2e 更新 + 全量 `-race` + 打包零错误。

### 第五十七轮迭代：输入框斜杠命令（已实施）

- 输入框支持本地斜杠命令：`/export`（导出 Markdown）、`/exporthtml`（导出 HTML）、`/stats`（会话统计）、`/new`（新建对话）
- 未知命令提示可用列表；不触发模型调用

验证：tsc/build 零错误 + 全量回归。

### 第五十六轮迭代：知识库文件管理（已实施）

- 设置页语义检索卡片列出 `knowledge/` 目录文件（名称 / 大小 / 修改时间），支持删除（确认对话框 + 目录穿越防护）
- 删除文件后向量索引自动同步移除该文件分块（既有 mtime 感知机制）

测试：列表 / 缺目录空态 / 遍历拒绝 / 删除生效 e2e + 全量回归。

### 第五十五轮迭代：远程 Agent 分享导出/导入（已实施）

### 第五十四轮迭代：知识库检索调试器（已实施）

- 设置页语义检索卡片新增「测试检索」：输入检索词即时执行底层 searchHybrid（含增量索引与混合评分），结果原文回显，调参不再需要进对话试探
- 覆盖双路径：Embedding 已配置走语义混合，未配置走关键词回退——两者输出均可预览

测试：底层 TestKnowledgeSearch 直通 e2e（复用语义/关键词双路径）+ 全量回归。

### 第五十三轮迭代：会话内容搜索（已实施）

- 侧栏搜索升级：输入关键词后按 250ms 防抖调用后端 `SearchSessions`，匹配**标题或历史消息内容**（对 events 负载的 LIKE 探测，CJK 原生匹配）
- 空查询回落到完整列表（置顶优先 + 最近活跃排序）；不匹配则空态

测试：内容命中 / 空查询全量 / 不匹配为空 e2e + 全量回归。

### 第五十二轮迭代：会话导出为自包含 HTML（已实施）

- 对话顶栏新增「导出为自包含 HTML」（FileCode2 图标）：完整会话经 gomarkdown 渲染为带内联样式的单文件 HTML（标题/二级标题/引用块/表格/代码样式），可直接在浏览器阅读或分享
- 与既有 Markdown 导出并存（HTML 走相同事件渲染管道，含工具调用与 HITL 确认记录）

测试：HTML 导出 e2e（DOCTYPE/标题/渲染正文/文档闭合）+ 全量 `-race`。

### 第五十一轮迭代：存储占用显示与会话库压缩（已实施）

- 诊断卡片显示会话库 / 知识索引的磁盘占用；「压缩会话库」按钮执行 VACUUM，回收修剪/重新生成历史后产生的空页
- `AppService.StorageStats` / `VacuumSessions` RPC，数据库位置经 SetStoragePaths 注入

测试：真实文件尺寸统计、缺失文件 -1 语义、VACUUM 后数据库仍可用 e2e + 全量回归。

### 第五十轮迭代：工具调用耗时显示（已实施）

- 每个工具结果卡片头部显示调用耗时（如 `1.2s`）：前端按工具名 FIFO 配对 call/result 时间戳计算，流式与历史回放均适用
- 配对逻辑同时校正 runningTools 判定（同名并发调用按序配对）

验证：tsc/build 零错误 + 全量回归。

### 第四十九轮迭代：团队成员时间线（已实施）

旧路线图「团队运行时间线」落地：

- `SessionStats.Authors`：按对话参与者（团队成员/远程 Agent）聚合轮次、工具调用与 tokens（排除用户自身）
- 统计弹层在多参与者会话中显示「成员时间线」表（成员 / 轮次 / 工具调用 / tokens），单人会话自动隐藏

测试：按作者聚合断言（含 sanitize 后名称与轮次计数）+ 全量回归。

### 第四十八轮迭代：消息时间戳悬停显示（已实施）

- 用户气泡悬停显示发送时间（本地 HH:MM）；助手回复作者旁常显时间
- 历史消息透传存储时间戳，流式消息在到达时本地打点

验证：tsc/build 零错误 + 全量回归。

### 第四十七轮迭代：知识库索引重建（已实施）

- 设置页语义检索卡片新增「重建索引」：清空本地向量缓存，下次检索自动从工作区文件重新嵌入（换模型/换文件批量调整后的管理闭环）
- ConfigService.ResetKnowledgeIndex → KnowledgeIndex.Reset（全表清空 + 互斥保护）

测试：重置清空 + 自动重建 e2e + 全量回归。

### 第四十六轮迭代：代理覆盖确认与保存校验（已实施）

- **覆盖确认**：核验 gemini SDK（`&http.Client{}`）、a2a-go JSONRPC 客户端（同）与 MCP streamable（`http.DefaultClient`）均回落到 `http.DefaultTransport` —— 全局代理设置对所有出站流量生效，无需逐客户端接线
- **保存即校验**：SaveSettings 校验代理地址（scheme http/https + host），格式错误立即报错，不再等到重启后

测试：合法/非法代理保存 e2e + 全量回归。

### 第四十五轮迭代：模型请求瞬态重试（已实施）

- OpenAI 兼容与 Anthropic 适配器共享 `postWithRetry`：连接失败与 502/503/504 网关错误按固定退避（0.5s/1.5s）自动重试最多 2 次
- **仅在未消费任何响应字节时重试**——流式输出绝不重复；4xx（鉴权/参数错误）不重试、立即报错
- 已知边界补充：重试针对瞬态网关故障，模型返回的业务错误不重试

测试：503×2 后恢复 e2e（恰 3 次请求）+ 既有 401 语义回归 + 全量 `-race`。

### 第四十四轮迭代：MCP OAuth 授权等待提示（已实施）

- Manager 新增 `SetAuthPendingHook`：OAuth 授权码流程等待用户在浏览器操作时，向 Wails 事件总线发 `mcp:auth-pending`（携带服务器名与授权地址）
- MCP 页监听该事件并 toast 提示「请在浏览器中完成 MCP 授权…完成后连接自动继续」，消除等待期间的静默困惑
- 钩子经 `buildTransport` 贯穿至官方 OAuth handler 的 Fetcher（打开浏览器前触发）

测试：OAuth 流程 e2e 扩展（钩子收到 /authorize 地址断言）+ 全量回归。

### 第四十三轮迭代：代理覆盖契约固化（已实施）

- 契约测试钉住：无显式 HTTPClient 的 MCP streamable 传输（`http.DefaultClient` → `http.DefaultTransport`）必须经过全局代理——回环转发桩验证流量真实穿过代理服务器
- 顺带确认 gemini SDK / 模型适配器 / 向量客户端同路径覆盖；代理桩需直连转发（避免自环）

测试：代理覆盖 e2e（流量真实经过代理服务器）+ 回归。

### 第四十二轮迭代：全量代码审查（已实施）

双子代理（Standards + Spec）并行审查 35 个提交：修复 tool_result 双发 bug、预算豁免确认应答、错误中文化、semantic.go 清理、README 章节排序（22/29 节补齐）。

### 第四十一轮迭代：长会话自动上下文压缩（已实施）

依据 ADK `session/compaction` 官方能力（滑动窗口 + 尾部保留）：

- 设置页「长会话上下文压缩」：**滑动窗口间隔**（每 N 轮把更早对话压缩为 LLM 摘要，携带 1 轮重叠保证连续性）与 **Token 阈值触发**（提示超阈值时压缩其余、保留尾部）
- 摘要用会话根 Agent 自己的模型（ADK 默认行为）；远程 A2A 会话无本地模型、自动跳过
- 两者可同时启用；均为 0 关闭（零破坏）

测试：压缩 e2e（开启后出现额外摘要模型调用 + 回答完整；关闭后恰好一次调用）+ 全量回归。

### 第四十轮迭代：危险操作确认（已实施）

删除会话 / Agent / 团队 / 远程 Agent / 供应商 / MCP 服务器 / Skill 一律弹出确认对话框，防止误删不可恢复的数据。

验证：tsc/build 零错误 + 全量回归。

### 第三十九轮迭代：知识库检索结果可预览（已实施）

- search_knowledge 结果附带 `refs`（工作区相对文件路径，去重排序）；ToolCallCard 路径识别扩展 `refs` 键，检索结果可直接点开「预览」查看原文（Markdown/文本渲染）

测试：语义/关键词检索回归 + 前端 tsc/build 零错误。

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

### 第二十九轮迭代：发布就绪检查与总览刷新（已实施）

- README 功能总览/目录结构对齐全部新能力；新增「发布就绪检查」章节（后端全量 + -race、tsc/build、wails3 打包、启动烟雾、视觉回归覆盖）
- 顺带修复四十一轮累积的章节顺序与笔误

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

### 第二十二轮迭代：会话统计按日成本汇总（已实施）

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

本次持续迭代会话至此完成第八至第八十九轮共 80+ 轮迭代记录。
