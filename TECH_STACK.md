# TECH_STACK.md — ops-agent

> 阶段 2 产出物：技术选型
> 状态：待你审阅确认。确认后进入阶段 3（架构设计）。
> 记录原则：写清"选了什么 + 为什么 + 代价"，便于以后回看决策。

---

## 0. 核心架构方案：B —— Go 单二进制，agent 带大脑

| 项 | 决策 |
|---|---|
| 形态 | 本地 CLI 客户端 + 每台服务器一个常驻 agent |
| 大脑位置 | **在 agent 端**：agent 自己调用模型，独立跑巡检/自愈，CLI 不开也能干活 |
| 语言 | **全 Go**，CLI 和 agent 同一套代码，编译成**一个二进制 + 子命令** |

**为什么选 B（而非纯 SSH 的 A 或分层的 C）**：
- 你要的"巡检 + 自愈、CLI 不开也能自动处理"只有 agent 端常驻且能调模型才能完整做到。
- 单 Go 二进制直接命中"部署简单"硬约束（交叉编译、无运行时依赖）。

**已知代价（接受）**：
- Go 的 LLM/agent 生态比 Python 薄，工具调用循环、provider 适配要自己写。
- API key 需要落到每台服务器（见第 3 节的处理方案）。

---

## 1. 语言与运行形态

- **语言**：Go（建议 1.22+）。
- **构建**：`CGO_ENABLED=0` 纯静态编译，`GOOS/GOARCH` 交叉编译，产出**单个静态二进制**，无外部运行时依赖。
- **一个二进制，多子命令**：
  - `ops serve` —— 在被管服务器上以常驻 agent 运行。
  - `ops connect <host>` —— 本地 CLI，连到某台 agent 对话。
  - （其余如 `setup`、`enroll`、`run`、`logs`、`todos` 等。）
- **部署方式**：scp 一个二进制 + 一条命令启动（或配 systemd unit）。契合"部署简单"。

---

## 2. CLI ↔ agent 通信：复用 SSH 隧道

- **决策**：CLI 通过你已有的 SSH 连接，在远端拉起/连接 `ops serve` 的本地 socket，走 SSH stdio 通信。**agent 不监听任何对外网络端口。**
- **为什么**：部署最简单（无端口/证书/防火墙）、攻击面最小、复用 SSH 既有认证与连接审计。
- **代价**：流式输出走 SSH stdio 需少量工程处理；依赖 SSH 可达。
- **可逆性**：协议层做抽象，未来若要常驻服务端口，可替换为 gRPC+mTLS 或 HTTPS+token，不影响上层。

---

## 3. 模型接入：自定义 Provider 抽象，多家可切换

- **决策**：定义统一 `Provider` 接口（chat / streaming / tool-calling），首批适配：
  - **Anthropic Messages API**（Claude）
  - **OpenAI 兼容接口**（base_url + key，覆盖 OpenAI 及大多数国产/自托管网关）
- **多模型切换**：通过配置选择 provider + model，运行时可切。
- **为什么自己写**：Go 没有 litellm 级别的成熟统一层；但 LLM 调用本质是 HTTP，自写抽象层可控、依赖少。

### 3.1 API key 安全：agent 本地加密存储
- **决策**：key 加密后存在 agent 配置目录（候选：NaCl secretbox / age，密钥与机器绑定）。
- **收益**：agent 重启后自恢复，**自愈循环不中断**（这是选此项而非"仅内存"的关键）。
- **代价**：key 以密文落服务器磁盘——已知并接受。
- 解密密钥的保管方式（机器绑定 / 启动时输入 / 文件权限）留阶段 3 定。

---

## 4. 存储与记忆

- **会话历史 + 审计日志**：纯 Go SQLite（`modernc.org/sqlite`，无 CGO，保持单二进制可交叉编译）。结构化、可查询。
- **每台机知识档案**：**Markdown 文件**（类 CLAUDE.md），人可读、可手改、agent 启动时读取。
  - 选 markdown 而非塞进 DB：符合"概念简单"，你能直接 `vim` 编辑机器的用途/坑。
- **审计日志**：所有写操作结构化记录（动作、时间、命令、结果），存 SQLite，可回看。

---

## 5. 终端交互体验（CLI）

- **决策（默认，可调）**：用 `charmbracelet/bubbletea` + `lipgloss` 做对标 claude code 的流式终端 UI。
  - 若想首版更省事，可先做 readline 风格 REPL，后续再升级 TUI——不影响架构。
  - **实际落地**：选了后者并保持——自写 raw-mode REPL（truecolor + 流式 + ↑/↓ 菜单），**未引** bubbletea/lipgloss，守住零额外依赖。

---

## 6. 安全边界的技术承载（呼应 REQUIREMENTS 第 4 节）

- **不可逆操作拦截**：命令在执行前过一道分类（规则 + 模型判断），命中不可逆/高危类需确认。
  - ⚠️ 待解设计点：后台自愈在 CLI 离线时遇到高危操作如何处理（建议：跳过 + 记待办，绝不擅自执行不可逆动作）。留阶段 3。
- **完整可审计**：见第 4 节审计日志。

---

## 7. 依赖清单（初步，尽量少）

| 用途 | 选型 | 说明 |
|---|---|---|
| 语言/构建 | Go 1.22+，CGO 关 | 单静态二进制 |
| CLI 命令 | 标准库 `flag` 或 `spf13/cobra` | 子命令多则用 cobra |
| 终端 UI | bubbletea + lipgloss（默认） | 可先用 REPL 替代 |
| 通信 | SSH（`golang.org/x/crypto/ssh` 客户端 + stdio 编解码） | 无对外端口 |
| 模型调用 | 标准库 `net/http` + 自写 Provider 抽象 | 适配 Anthropic / OpenAI 兼容 |
| 存储 | modernc.org/sqlite（纯 Go） | 会话+审计 |
| 知识档案 | Markdown 文件 | 人可改 |
| key 加密 | NaCl secretbox 或 age | 待阶段 3 定 |
| 配置 | YAML 或 TOML | 待阶段 3 定 |

> **实际落地（上表「待定」项的最终选择）**：终端 UI = 自写 raw-mode REPL（**未引** bubbletea/lipgloss）；SSH = 委托系统 `ssh`/`scp` 命令（**非** `x/crypto/ssh` 库）；CLI = 标准库 `flag`（非 cobra）；key 加密 = NaCl secretbox（非 age）；配置 = JSON `config.json` + 环境变量（非 YAML/TOML）。三方依赖最终只有 `modernc.org/sqlite`、`golang.org/x/crypto`、`golang.org/x/term`。

---

## 8. 留给阶段 3（架构设计）解决的设计点

1. SSH stdio 上的消息/流式协议具体怎么编解码。
2. 解密密钥的保管方式。
3. 后台自愈遇到高危/不可逆操作的处理策略（CLI 离线时）。
4. agent 的巡检循环、工具调用循环、对话循环如何组织。
5. 知识档案 + 会话记忆如何喂给模型上下文。
6. 配置文件格式与 agent 注册（enroll）流程。

---

## 附：本阶段决策清单

| 维度 | 决策 | 关键理由 |
|---|---|---|
| 架构 | B：Go 单二进制，agent 带大脑 | 自愈完整 + 部署简单 |
| 语言 | 全 Go，一个二进制多子命令 | 单静态二进制，零运行时依赖 |
| 通信 | 复用 SSH 隧道，agent 不开端口 | 部署最简、攻击面最小 |
| 模型接入 | 自写 Provider 抽象，多家可切换 | Go 无成熟统一层；HTTP 可控 |
| key 安全 | agent 本地加密存储 | 重启自恢复，自愈不中断 |
| 存储 | 纯 Go SQLite + Markdown 知识档案 | 单二进制 + 人可改 |
| 终端 UI | 自写 raw-mode REPL（未引 bubbletea） | 对标 claude code，零额外依赖 |
