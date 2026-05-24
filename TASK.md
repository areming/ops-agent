# TASK.md — ops-agent 执行状态

> 唯一执行事实来源。设计见 REQUIREMENTS / TECH_STACK / ARCHITECTURE / ROADMAP（这些只讲设计，不记执行状态）。
> 图例：✅ 完成并验证 ｜ 🟡 代码完成待验证/待提交 ｜ ⬜ 未开始
> 最后更新：2026-05-24

---

## 下次会话从哪开始

**M1/M2 已 live 验收通过**（DeepSeek 真实模型，2026-05-24）。M0/M1/M2 代码均已提交推送。
验收结论：
- M1 纯对话流式 ✅
- M2 工具循环 ✅：模型→tool_call→安全闸门判 Confirm→确认握手→执行→审计落库（`decision=approved`），多轮工具链（write_file→pwd→…）也正常。
- **发现一个待决缺陷**：推理/thinking 模型（如 `deepseek-v4-pro`）在工具执行后的第二轮会返回 400 `reasoning_content ... must be passed back`——我们的 OpenAI 适配器没捕获/回放 `reasoning_content`。非推理模型 `deepseek-chat` 全程正常。详见决策日志与"跨里程碑待办"。

**推理模型 `reasoning_content` 修复已完成并 live 复跑通过**（2026-05-24，见 M2 段与决策日志）。改动 6 文件未提交（等你说"提交"）。
**下一步**：提交本次修复 → 进 **M3（记忆 + Keystore 加密）**，第一步撞加密库依赖（NaCl secretbox / age，二选一需你点头），动手前走 explore→plan→等"go"。

---

## 进行中（doing）

### M1 — Provider 抽象 + 纯对话 loop　🟡
- [x] `model.Provider` 接口 + 类型（预留 tools 字段）
- [x] SSE 读取器
- [x] OpenAI 兼容适配器（含 DeepSeek 别名）
- [x] Anthropic 适配器
- [x] registry + config（环境变量，零依赖）
- [x] session 历史 + 对话 loop，daemon 接入真实 provider
- [x] 适配器离线测试（httptest）、`go vet`/`test`/`build`、交叉编译
- [x] git commit + push M1（`3f07f21`）
- [x] DeepSeek live 流式对话验收（2026-05-24，deepseek-chat / deepseek-v4-pro 均通）

---

## 待办（todo）

### M2 — Tools + Safety Gate（= MVP：对话式执行操作）　✅（代码+离线+live 验收均通过）
实现顺序（一次一件，每件可验）：
- [x] ① transport：加 `ConfirmRequest`/`ConfirmReply`（结构化 payload）+ `ToolStart`
- [x] ① model/provider：Message 支持 tool_call/tool_result，ChatEvent 加 `EventToolCall`，定义 ToolCall 类型
- [x] ② tools：Tool 接口 + registry；shell 执行器（sh -c，捕获 stdout/stderr/exit，超时）；文件读/写工具
- [x] ③ safety：规则黑名单 + 只读白名单 + 模型自评字段，取并集判定（规则为硬约束）；分类器单测（19 用例）
- [x] ④ loop：tool-call 循环（tool_call→safety→确认/放行→执行→写审计→回灌→续问）+ 集成测试（approve/deny via net.Pipe）
- [x] ④ provider 工具调用：OpenAI tool_calls（流式累积）+ Anthropic tool_use/tool_result；离线测试
- [x] ⑤ memory/audit：`modernc.org/sqlite`，写操作落 audit 表 + 单测；确认 CGO 关、交叉编译仍单静态二进制
- [x] ⑥ CLI：处理 `ConfirmRequest`（y/n）+ `ToolStart`（显示"▶ 运行: …"）
- [x] ⑦ 离线验收：全测试过、vet、交叉编译 linux amd64/arm64 静态链接、本机 serve+connect 启动冒烟（错误路径优雅）
- [x] git commit + push M2（`66db745` + `3fe4cf1`，已推送）
- [x] DeepSeek live 验收：真实模型→生成命令→确认握手→执行→audit 落库（2026-05-24，deepseek-chat 全程通；附带发现推理模型 reasoning_content 缺陷，见决策日志）

### M3 — 记忆 + Keystore 加密　⬜
- [ ] `memory/knowledge`：加载 Markdown 知识档案，注入系统提示
- [ ] `memory/store`：会话历史持久化 + 跨 session 回看
- [ ] `secret/keystore`：API key 加密存取　⚠️依赖待决：加密库（NaCl secretbox / age）
- [ ] `opsagent key set/list` 子命令
- [ ] 验收：重开 session 引用历史；知识档案影响回答；key 密文落盘、重启自恢复、配置无明文 key

### M4 — enroll 一键部署　⬜
- [ ] `opsagent enroll <host>`：传二进制、建专用用户、生成 sudoers 白名单、装 systemd unit、初始化配置与目录
- [ ] `opsagent todos` / `logs` 查看入口
- [ ] 验收：干净 Linux 机一条命令部署、`connect` 即用、专用用户运行、提权走 sudo 白名单

### M5 — 巡检 + 自愈　⬜（依赖 M3 的 key 持久化）
- [ ] `agent/patrol`：定时调度 + 检查集（disk/load/key_services）
- [ ] 复用 Agent Loop + Safety Gate：可逆且白名单→自动执行；高危/不可逆→跳过 + 写 `todos`
- [ ] `patrol_runs` / `todos` 落库，下次 CLI surface
- [ ] 验收：可逆异常自动修复留痕；高危场景不执行、写待办、CLI 可见

### M6 — 增强　⬜（按需）
- [ ] 批量任务（CLI 侧 fan-out 到多台）
- [ ] TUI 升级到 bubbletea　⚠️依赖待决：bubbletea/lipgloss
- [ ] 多模型场景化（便宜模型巡检、强模型诊断）
- [ ] 巡检 check 插件化
- [ ] 配置从环境变量升级到 TOML 文件　⚠️依赖待决：TOML 库

### 跨里程碑待办
- [ ] M0 SSH 路径 live 验收（需你那台 Linux 机器）
- [ ] 依赖决策待点头：加密库(M3) / bubbletea(M6) / TOML(M6)　（SQLite(M2) 已批准并落地）
- [x] 推理模型支持（已修，2026-05-24，待提交）：OpenAI 适配器捕获 `delta.reasoning_content`（发 `EventReasoningDelta`），loop 累积存到 `Message.Reasoning`，`buildOpenAIMessages` 仅在非空时回传 `reasoning_content`。新增两个离线测试；live 用 `deepseek-v4-pro` 复跑第二轮不再 400。非推理模型零影响。Anthropic extended thinking 仍未做（另一套 thinking block 机制，等真用 Claude 推理再列）。

---

## 已完成（done）

### M0 — 最小能跑骨架　✅（提交 `20d4d2f`，已推送）
- [x] 单二进制 + 子命令骨架（`serve`/`connect`/`_bridge`）
- [x] transport：Frame 协议 / 读写器 / unix socket / SSH 桥接
- [x] serve echo daemon、CLI connect（`--local` + SSH）
- [x] 帧回环单测、`go vet`/`build`、交叉编译 linux amd64/arm64（静态链接）
- [x] 本机 echo 回环验收通过、推送 GitHub
- 注：SSH 路径端到端 live 验收仍待办（见跨里程碑待办）

---

## 决策日志

> 工作中做的非显然决定，便于回看。

- **2026-05-24 架构**：选方案 B（全 Go 单二进制，agent 端带大脑）。理由：巡检自愈要 CLI 不开也能跑 + 单二进制部署简单。
- **2026-05-24 通信**：CLI↔agent 复用 SSH 隧道连本地 unix socket，agent 不开任何网络端口。理由：部署最简、攻击面最小。
- **2026-05-24 运行身份**：默认专用用户 `opsagent` + 自动生成 sudo 白名单（enroll 配好）。理由：最小权限且不破坏"部署简单"。
- **2026-05-24 自愈边界**：后台自愈遇高危/不可逆操作 → 跳过 + 记待办，绝不擅自执行。
- **2026-05-24 自主白名单**：首版只读命令自动放行，写操作一律确认。
- **2026-05-24 key**：agent 本地加密存储（重启自恢复，自愈不中断）。
- **2026-05-24 module 路径**：`github.com/areming/ops-agent`，远程 `git@github.com:areming/ops-agent.git`。
- **2026-05-24 M1 配置**：用环境变量（零依赖）；TOML 文件后置到 M6（避免现在加依赖）。
- **2026-05-24 DeepSeek**：走 OpenAI 兼容适配器；`OPSAGENT_PROVIDER=deepseek` 时默认 base_url=`https://api.deepseek.com`。
- **2026-05-24 SSE**：手写 HTTP + SSE 解析，不引模型 SDK，保持零第三方依赖。
- **2026-05-24 SQLite（已批准）**：M2 审计落库用 `modernc.org/sqlite`（纯 Go，CGO 关，保单静态二进制可交叉编译）。这是项目第一个第三方依赖。

- **2026-05-24 M2 安全判定（已定）**：规则 + 模型自评 reversible 取并集。实现：工具 schema 要求模型给出 `reversible`/`risk` 自评字段（不额外发模型调用）；最终判定取规则与自评中更谨慎者。规则黑名单为硬约束，模型自评不能把它降级放行。
- **2026-05-24 M2 适配器范围（已定）**：OpenAI 和 Anthropic 适配器都实现工具调用（含各自的消息结构：OpenAI tool_calls / Anthropic tool_use+tool_result）。DeepSeek（OpenAI 路径）做现场验收，Anthropic 路径离线测试覆盖。
- **2026-05-24 live 验收发现（已修）**：DeepSeek 推理模型 `deepseek-v4-pro` 在工具执行后的续问轮返回 400 `The reasoning_content in the thinking mode must be passed back to the API`。根因：OpenAI 适配器未捕获/回放 assistant 的 `reasoning_content`。修法：捕获 `delta.reasoning_content` → `EventReasoningDelta` → 存 `Message.Reasoning` → 仅非空时回传。live 复跑已通过，非推理模型零影响。
- **2026-05-24 部署约束（用户提醒）**：服务器全是 Ubuntu，CPU 可能很老（用户曾装 Claude Code CLI 失败）。坚持 `CGO_ENABLED=0` 纯 Go 静态二进制、`GOAMD64` 不高于默认 v1、不引需 CGO 或现代指令的依赖。已核对 build.ps1 符合（CGO 关、无 GOAMD64 设置、ELF statically linked）。
