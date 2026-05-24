# TASK.md — ops-agent 执行状态

> 唯一执行事实来源。设计见 REQUIREMENTS / TECH_STACK / ARCHITECTURE / ROADMAP（这些只讲设计，不记执行状态）。
> 图例：✅ 完成并验证 ｜ 🟡 代码完成待验证/待提交 ｜ ⬜ 未开始
> 最后更新：2026-05-24

---

## 下次会话从哪开始

**当前 task：M1（doing）**，代码已完成，剩两件收尾：
1. ⬜ `git commit + push M1`（需你明确说"提交"）——离线测试/构建已过，commit message 注明 live 验收待办。
2. ⬜ DeepSeek live 流式对话验收（按你要求暂缓；恢复时设 `OPSAGENT_PROVIDER=deepseek` 等环境变量后由我起 serve + connect 验）。

M1 收尾后 → **M2（todo）**。M2 第一步会撞 **SQLite 依赖**（见决策日志待决项），动手前需你点头，并先走 explore→plan→等"go"。

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
- [ ] DeepSeek live 流式对话验收　← 暂缓
- [ ] git commit + push M1　← 待你说"提交"

---

## 待办（todo）

### M2 — Tools + Safety Gate（= MVP：对话式执行操作）　⬜
- [ ] transport 增帧类型：`ToolCall` / `ConfirmRequest` / `ConfirmReply`
- [ ] `tools`：Tool 接口 + registry；shell 执行器（捕获 stdout/stderr/exit）；文件读写工具
- [ ] Provider 适配器扩展工具调用（OpenAI `tools`/`tool_calls`、Anthropic `tool_use`）+ Agent Loop 的 tool-call 循环
- [ ] `safety`：规则黑名单（不可逆/高危）+ 模型自评 reversible + 只读白名单自动放行
- [ ] 确认流：写操作 → `ConfirmRequest` → CLI 确认 → `ConfirmReply`
- [ ] `memory/audit`：最小 SQLite，写操作全部落审计表　⚠️依赖待决：`modernc.org/sqlite`
- [ ] CLI 端高危确认交互
- [ ] safety 分类器单测（黑名单/白名单关键用例）
- [ ] 验收：重启服务跑通；`rm -rf` 必被拦；只读自动放行；audit 可查

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
- [ ] 依赖决策待点头：SQLite(M2) / 加密库(M3) / bubbletea(M6) / TOML(M6)

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
