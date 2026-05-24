# ROADMAP.md — ops-agent

> 阶段 4 产出物：实施计划
> 状态：待你审阅确认。确认后才进入 scaffold（阶段 5）。
> 排序原则：**按风险**，不按功能重要性。最可能推翻架构的不确定性最先做。
> M0 是"最小能跑骨架"，不含任何业务逻辑。

---

## 风险排序的理由（先说为什么是这个顺序）

| 风险点 | 等级 | 落在哪个里程碑先验证 |
|---|---|---|
| SSH stdio ↔ unix socket 桥接 + 流式 Frame 协议（整个交互模型的地基） | **最高（设计风险）** | M0 |
| Provider 抽象 + Agent Loop 的流式工具调用 | 高（设计风险） | M1 |
| Safety Gate 判错 = 误删数据（后果最重） | 高（后果风险） | M2 |
| 单静态二进制 + 纯 Go SQLite 交叉编译 | 中（已知可行，需证实） | M0 顺带验证 |
| enroll 部署（专用用户/sudoers/systemd） | 中（平台相关，活儿多于风险） | M4 |
| 巡检自愈自主循环 | 中（建立在已验证件上） | M5 |

> 核心判断：**传输层（M0）和 Agent Loop（M1）是地基级设计风险，必须最先证实能跑通**；Safety Gate（M2）后果最重所以紧随其后并重投测试。部署（M4）虽是你的硬约束，但风险是"繁琐"而非"未知"，且开发期可手动起 serve，故后置。

---

## M0 — 最小能跑骨架（无业务逻辑）

**目标**：打通"一个静态二进制 + CLI 经 SSH 连到常驻 daemon + 流式回传"的端到端通路。
**消除的风险**：传输层是否成立（最高设计风险）；单二进制交叉编译是否成立。

**包含**：
- `cmd/opsagent` 入口 + 子命令分发（`serve` / `connect` / `_bridge`）。
- `serve`：监听 unix socket（文件权限限定），收 Frame、原样 echo 流式回。
- `connect <host>`：SSH 进远端跑 `_bridge`，把 stdio 桥到 unix socket；本地读一行 → 发 → 流式打印回显。
- `transport`：Frame 编解码 + socket/bridge 骨架。
- 构建脚本：`CGO_ENABLED=0` 交叉编译出单静态二进制。

**不包含**：模型、工具、安全、存储、TUI、配置（全部桩或硬编码）。

**完成标准（验收）**：
- 在一台真实/本地 SSH 可达的机器上 `opsagent serve`，本地 `opsagent connect` 输入文字能流式收到 echo。
- `go build` 产出单个无依赖二进制，能交叉编译到 linux/amd64+arm64。
- `go vet` / lint 通过，无未用代码。

---

## M1 — Provider 抽象 + 纯对话 Agent Loop（不碰系统）

**目标**：CLI 连上去能和真实模型**流式对话**，可切换厂商。
**消除的风险**：Provider 接口设计、流式、工具调用协议的可行性——但此时**还不执行任何系统命令**，安全为零风险。

**包含**：
- `model.Provider` 接口 + Anthropic 适配 + OpenAI 兼容适配。
- `agent`：Session + Agent Loop 的对话部分（拼上下文、调模型、流式回 Frame）。
- 最小 `config`（TOML）：选 provider/model；key 暂可走环境变量（M3 再加密）。

**不包含**：工具执行、Safety Gate、持久化、知识档案。

**完成标准**：
- 配 Claude / 一个 OpenAI 兼容端点，各能流式对话；切换只改配置。
- 对话上下文在单 session 内连贯。

---

## M2 — Tools + Safety Gate（= 首版核心：对话式执行操作）⭐

**目标**：达成 MVP——"我说需求 → 它生成并执行命令搞定 → 全程可审计、危险操作拦截"。
**消除的风险**：误操作风险（后果最重）。本里程碑**重投测试**。

**包含**：
- `tools`：shell 执行器（+ 必要的文件读写工具），带 JSON schema，接入 Agent Loop 的 tool-call。
- `safety`：规则层（不可逆/高危黑名单）+ 模型自评 reversible；只读白名单自动放行；写操作走 `ConfirmRequest`→CLI 确认→`ConfirmReply`。
- `memory/audit`：所有写操作落 audit（先落 SQLite，最小表）。
- CLI 端：高危确认交互。

**不包含**：知识档案记忆、巡检自愈、key 加密、enroll。

**完成标准**：
- 对话让它"重启某服务"，能正确生成命令、弹确认、执行、回报结果。
- 构造 `rm -rf` 类命令，**必被拦截要求确认**；只读命令自动放行。
- 每条写操作在 audit 表可查。
- safety 分类器有单测覆盖关键黑名单与白名单用例。

---

## M3 — 记忆 + Keystore 加密

**目标**：让它"记住每台机"并安全存 key。
**消除的风险**：低中（标准件）。

**包含**：
- `memory/knowledge`：Markdown 知识档案加载，注入系统提示。
- `memory/store`：会话历史持久化 + 跨 session 回看。
- `secret/keystore`：API key 加密存取（NaCl secretbox / age，机器绑定 + 严格文件权限）；`opsagent key set/list`。

**完成标准**：
- 重开 session 能引用历史；知识档案内容影响模型回答。
- key 以密文落盘，agent 重启后自动可用，配置不再含明文 key。

---

## M4 — enroll 一键部署（兑现"部署简单"硬约束）

**目标**：`opsagent enroll <host>` 一条命令完成部署。
**消除的风险**：平台相关繁琐项（用户/sudoers/systemd）。

**包含**：
- 传二进制、建专用用户 `opsagent`、生成 sudoers 白名单、装 systemd unit、初始化配置与目录。
- `opsagent todos` / `logs` 查看入口。

**完成标准**：
- 在一台干净 Linux 机器上一条命令部署成功，`connect` 即可用。
- agent 以专用用户运行；提权操作走 sudo 白名单。

---

## M5 — 巡检 + 自愈

**目标**：后台定时巡检，异常诊断，可逆自愈，高危记待办。
**消除的风险**：已被 M2（loop+safety）和 M3（key 持久）消化，此处主要是组装。

**包含**：
- `agent/patrol`：定时调度 + 检查集（disk/load/key_services）。
- 复用 Agent Loop + Safety Gate：可逆且白名单 → 自动执行；高危/不可逆 → 跳过 + 写 `todos`（安全底线①）。
- `patrol_runs` / `todos` 落库；下次 CLI surface。

**完成标准**：
- 制造一个可逆异常（如某服务停了）→ 自动重启并留痕。
- 制造一个高危场景 → **不执行**，写待办，CLI 能看到。

---

## M6 — 增强（锦上添花，按需）

- 批量任务（CLI 侧 fan-out 到多台）。
- TUI 从 REPL 升级到 bubbletea。
- 多模型场景化（便宜模型巡检、强模型诊断）。
- 巡检 check 插件化。

---

## 每个里程碑的通用验收门槛

1. 该里程碑描述的行为确实工作（有最小演示）。
2. 相关单测通过（M2 的 safety 必须有测试）。
3. `go vet` / lint / `go build`（单二进制）通过。
4. 无未用代码、import、TODO 残留。

---

## 里程碑依赖关系

```
M0 (传输骨架)
 └─ M1 (Provider+对话loop)
     └─ M2 (Tools+Safety = MVP核心) ⭐
         ├─ M3 (记忆+key加密)
         │   └─ M5 (巡检自愈)   ← 需要 M3 的 key 持久化
         └─ M4 (enroll部署)
                 └─ M6 (增强)
```

> M2 完成即交付"对话式执行操作"的首版价值。M3/M4 可并行。M5 依赖 M3。
