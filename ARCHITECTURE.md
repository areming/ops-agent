# ARCHITECTURE.md — ops-agent

> 阶段 3 产出物：架构设计
> 状态：待你审阅确认。确认后进入阶段 4（实施计划）。
> 上游：见 REQUIREMENTS.md（需求）、TECH_STACK.md（选型）。

---

## 1. 高层组件图

```
┌────────────────────────── 你的本地机器 ──────────────────────────┐
│                                                                  │
│   ops connect <host>                                             │
│   ┌──────────────────────────────┐                              │
│   │  CLI 客户端 (薄)              │   收发 Frame 协议            │
│   │  - 渲染流式对话              │   (无 TUI 框架)             │
│   │  - 高危操作确认弹窗          │   (自写 raw-mode REPL)       │
│   └──────────────┬───────────────┘                              │
└──────────────────┼──────────────────────────────────────────────┘
                   │  SSH (你已有的密钥认证)
                   │  在远端运行 `ops _bridge`，
                   │  把 SSH stdio ↔ 本地 unix socket 对接
                   ▼
┌────────────────────────── 被管服务器 ────────────────────────────┐
│   unix socket (文件权限限定, 无网络端口)                          │
│            │                                                      │
│   ┌────────▼──────────────────────────────────────────────┐     │
│   │           ops serve  (常驻 agent · 大脑)               │     │
│   │                                                        │     │
│   │  ┌─────────────┐   ┌──────────────┐  ┌─────────────┐  │     │
│   │  │ Session/    │   │  Agent Loop  │  │  Patrol     │  │     │
│   │  │ 对话管理    │──▶│ model↔tool   │  │  巡检+自愈  │  │     │
│   │  └─────────────┘   │  循环        │  │  (定时)     │  │     │
│   │                    └──┬────────┬──┘  └──────┬──────┘  │     │
│   │             ┌─────────▼──┐  ┌──▼──────────┐ │         │     │
│   │             │ Safety Gate │  │  Tools      │ │         │     │
│   │             │ 分类/拦截/  │  │  shell/file │◀┘         │     │
│   │             │ 白名单      │  │  执行器     │           │     │
│   │             └─────────────┘  └─────────────┘           │     │
│   │   ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌─────────┐  │     │
│   │   │ Model    │ │ Memory   │ │ Keystore │ │ Config  │  │     │
│   │   │ Provider │ │ SQLite+  │ │ 加密 key │ │ JSON    │  │     │
│   │   │ 抽象     │ │ 知识档案 │ │          │ │         │  │     │
│   │   └────┬─────┘ └──────────┘ └──────────┘ └─────────┘  │     │
│   └────────┼──────────────────────────────────────────────┘     │
└────────────┼──────────────────────────────────────────────────────┘
             │  出站 HTTPS
             ▼
      ┌─────────────────┐
      │  模型 API        │  Anthropic / OpenAI 兼容
      └─────────────────┘
```

---

## 2. 组件职责

| 组件 | 职责 | 备注 |
|---|---|---|
| **CLI 客户端** | 渲染流式对话、采集输入、弹出高危确认。**不含业务逻辑、不调模型** | 薄客户端 |
| **Transport** | SSH stdio ↔ unix socket 桥接；Frame 消息编解码 | agent 无网络端口 |
| **Session 管理** | 维护一次对话的消息历史、上下文拼装 | 每个 CLI 连接一个 session |
| **Agent Loop** | 核心循环：调模型 → 解析 tool call → 过 Safety Gate → 执行 → 回灌结果 → 续写 | 对话与自愈复用同一循环 |
| **Safety Gate** | 命令分类（规则 + 模型自评）、白名单放行、高危拦截 | 安全底线的执行点 |
| **Tools** | 实际执行器：跑 shell、读写文件等 | 每个工具有 JSON schema |
| **Patrol** | 定时巡检调度；发现异常触发诊断/自愈 | 独立 goroutine，CLI 不开也跑 |
| **Model Provider** | 统一模型接口，多家可切换 | 自写抽象 |
| **Memory** | SQLite（会话/审计/待办）+ Markdown 知识档案 | |
| **Keystore** | API key 加密存取 | |
| **Config** | 配置加载（provider/model、巡检、运行身份等） | env + JSON（config.json） |

---

## 3. 数据模型

### 3.1 SQLite（`StateDir/state.db`；StateDir = 服务 `/var/lib/opsagent` 或本地 `~/.config/opsagent`）

```
sessions(
  id, host, source[chat|patrol], created_at, ended_at, summary)

messages(
  id, session_id, role[user|assistant|tool],
  content, tool_calls_json, tool_results_json, created_at)

audit(
  id, session_id?, source[chat|patrol],
  command, target, exit_code, output_excerpt,
  risk[low|medium|high], reversible[bool],
  decision[auto|approved|denied|skipped],
  created_at)                      -- 所有写操作必落此表（安全底线②）

patrol_runs(
  id, started_at, finished_at, checks_json, findings_json)

todos(                              -- 自愈遇高危/不可逆时记此（安全底线①）
  id, created_at, source, severity,
  title, detail, suggested_action,
  status[open|done|dismissed], related_audit_id)
```

### 3.2 知识档案（Markdown，人可读可手改）
- 路径：`StateDir/knowledge/`（每台机一份）。
- 内容：本机用途、装了什么、约束、踩过的坑。
- agent 启动 / 每次 session 开始时读取，作为系统提示的一部分喂给模型。

### 3.3 配置（实际：JSON，`StateDir/config.json`）

> 已更正：配置实际用 **JSON**（`StateDir/config.json`），优先级 env > config.json > 默认，**只持久化模型选择字段**（provider/model/base_url + 诊断三件）；运行身份/巡检/白名单走 `OPSAGENT_*` 环境变量与代码默认。下方 TOML 为早期设计草案，仅示意可配字段。

```toml
run_as = "opsagent"            # 运行身份（见 §6）

[model]
provider = "anthropic"         # 当前选用
model    = "claude-..."

[providers.anthropic]
# key 不写这里，存在加密 keystore，仅引用
[providers.openai_compat]
base_url = "https://..."

[autonomy]
auto_allow = "readonly"        # 只读命令自动放行（首版默认）

[patrol]
enabled  = true
interval = "5m"
checks   = ["disk", "load", "key_services"]
```

---

## 4. 关键流程

### 4.1 对话式执行操作（MVP 主流程）
```
你输入需求
  → CLI 经 SSH 发 Frame{UserInput} 给 agent
  → Agent Loop：拼上下文(知识档案+历史) 调 Model
  → Model 返回 tool_call（如 run "systemctl restart nginx"）
  → Safety Gate 分类：
        · 只读 & 命中白名单 → 直接执行
        · 写操作/未知 → Frame{ConfirmRequest} 回 CLI，等你确认
        · 不可逆/高危 → 必须确认（白名单永不覆盖此类）
  → Tools 执行，捕获输出 → 写 audit 表
  → 结果回灌 Model → 继续循环直到产出最终答复
  → Frame{AssistantDelta...Done} 流式回 CLI
```

### 4.2 后台巡检 + 自愈（CLI 可不在线）
```
Patrol 定时触发
  → 跑检查集（disk/load/services...）
  → 无异常：记 patrol_runs，结束
  → 有异常：用本地加密 key 调 Model 诊断 → 提出修复动作
        · 动作可逆 & 命中白名单 → 自动执行 + 写 audit
        · 高危/不可逆 → 【跳过 + 写 todos】绝不擅自执行   ← 安全底线①
  → 全程留痕；你下次开 CLI 时 surface 待办与本次自愈记录
```

### 4.3 批量任务（后置里程碑）
```
CLI 侧 fan-out：对多台 host 各开一条 SSH→agent，
并发下发同一指令，聚合回显。（每台机各自的 agent 执行）
```

---

## 5. 对外接口（关键 Go 抽象）

```go
// 模型 provider —— 多家可切换的统一接口
type Provider interface {
    Name() string
    StreamChat(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error)
}
// ChatRequest 携带 messages + tools(JSON schema)；
// ChatEvent 含文本增量 / tool_call / 结束。

// 工具 —— agent 能调用的能力
type Tool interface {
    Name() string
    Description() string
    Schema() json.RawMessage
    Execute(ctx context.Context, args json.RawMessage) (ToolResult, error)
}

// 安全闸门
type Verdict struct {
    Decision   Decision // Allow | Confirm | Deny
    Reversible bool
    Risk       string   // low|medium|high
    Reason     string
}
type Classifier interface {
    Classify(cmd Command, ctx ExecContext) Verdict
}

// 传输帧
type Frame struct {
    // UserInput|AssistantDelta|ToolStart|ConfirmRequest|ConfirmReply|
    // ControlRequest|ControlReply|Cancel|Done|Error
    // Cancel（cli→agent，ESC/Ctrl-C）中断运行中的一轮：取消该轮 ctx，停模型流、
    // kill 正在跑的命令，仍以 Done 收尾。
    Type    FrameType
    Payload json.RawMessage
}
```

### 入口与「一机一脑」

一台机器只有一个常驻 agent（`ops serve` daemon），它是该机模型 / key / 记忆 / 审计 / 巡检的**唯一事实来源**。所有入口都只是连到它的瘦客户端：远端 `ops connect <host>` 经 SSH 把 stdio 桥到它的 unix socket；在该机本地直接敲 `ops` 也会探测并**接管同一个 daemon**（socket 装了没跑 / 无权访问时，分别提示起服务、重新登录使 `opsagent` 组生效，**绝不另起第二个会话重复引导**）。只有没有常驻 daemon 的机器（笔记本、未 enroll，或非 Linux）裸 `ops` 才退化成进程内的独立本地会话。服务配置随服务身份走（`opsagent` 用户 + `/var/lib/opsagent`：模型档案在它的 `config.json`，由 enroll 种入、可经 `/model` 增删切换；运行参数如巡检/诊断经 systemd unit 的 `OPSAGENT_*`），登录用户经 socket 借用 daemon 的配置，自身无需任何配置。

### CLI 子命令（对你的接口）
| 命令 | 作用 |
|---|---|
| `ops` | 本机有常驻 agent 则接管它；否则本地对话（未配置先引导）。见上「一机一脑」 |
| `ops setup` | 引导式部署向导 |
| `ops enroll <host>` | 一键部署：传二进制、建专用用户、写 sudoers 白名单、装 systemd、初始化配置 |
| `ops connect <host>` | 连上某台 agent 对话（日常主入口） |
| `ops serve` | 在服务器上以常驻 agent 运行（由 systemd 拉起） |
| `ops _bridge` | 内部：远端把 SSH stdio 桥到 unix socket（不直接用） |
| `ops key set/list` | 管理加密的模型 API key |
| `ops todos` / `logs` | 查自愈待办 / 审计日志 |

---

## 6. 安全模型

| 底线 | 落地 |
|---|---|
| **最小权限** | agent 默认跑在**专用用户 `opsagent`** 下；需提权的操作走 **sudoers 白名单**（由 `enroll` 自动生成）。这样既最小权限，又不破坏"部署简单"。可选 root（配置显式开启，文档标红风险）。 |
| **不可逆操作拦截** ① | Safety Gate 规则层（`rm -rf`/`mkfs`/`dd`/`drop database`/重定向覆盖设备 等）+ 模型自评 reversible。命中 → 对话中必确认；自愈中直接跳过记待办，**白名单永不覆盖**。 |
| **完整可审计** ② | 所有写操作落 `audit` 表（命令、目标、退出码、输出摘要、风险、决策来源）。 |
| **自主白名单** | 首版 `auto_allow=readonly`：只读命令自动放行，写操作一律确认。 |
| **key 安全** | API key 加密存 keystore（NaCl secretbox），重启自恢复。 |

---

## 7. 目录结构（Go 项目）

```
ops-agent/
├── cmd/ops/main.go             # 入口，子命令分发
├── internal/
│   ├── cli/                    # 薄客户端（自写 raw-mode REPL，无 TUI 框架）
│   ├── agent/                  # daemon / session / loop / patrol
│   ├── transport/              # protocol / socket / bridge
│   ├── model/                  # provider 抽象 + anthropic / openai
│   ├── tools/                  # tool 接口 + shell / fileio
│   ├── safety/                 # classifier / rules
│   ├── memory/                 # store(sqlite) / knowledge / audit
│   ├── secret/                 # keystore
│   └── config/                 # 配置加载
├── go.mod
└── README.md
```

---

## 8. 需要你拍板的设计决策

> 已在阶段 3 提问中定的：运行身份（专用用户+sudo，我权衡决定）、接入方式（SSH→unix socket）、自愈高危（跳过+记待办）、自主白名单（只读放行）。以下是**剩余待你确认/选择**的点：

1. **运行身份请最终确认**：我定的是"专用用户 `opsagent` + 自动 sudo 白名单"。你认同，还是个人服务器图省事就直接 root？（这是安全/便利的权衡，想让你过目）
2. **配置格式**：已定 **JSON**（stdlib、零依赖；见 TASK.md 2026-05-26 决策），不用 TOML/YAML。
3. **知识档案 + 状态目录位置**：默认放专用用户 home 下 `~/.opsagent/`。是否有偏好（如 `/etc/opsagent/`）？
4. **keystore 解密密钥怎么保管**：候选 (a) 机器绑定自动解密（省事，但拿到磁盘即可解）、(b) systemd 启动时从受保护文件读、(c) 首次启动手输（最安全但重启需人工）。倾向 (a) 配合严格文件权限。
5. **会话历史保留期**：默认无限保留 + 手动清理，还是设默认滚动（如 90 天）？

---

## 9. 留给阶段 4 / 后续里程碑

- 批量任务（§4.3）：CLI 侧 fan-out，后置。
- 巡检检查集的可扩展机制（插件式 check）。
- 多模型按场景自动选择（如便宜模型做巡检、强模型做诊断）。
- TUI 从 REPL 升级到 bubbletea 的时机。
