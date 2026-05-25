# TASK.md — ops-agent 执行状态

> 唯一执行事实来源。设计见 REQUIREMENTS / TECH_STACK / ARCHITECTURE / ROADMAP（这些只讲设计，不记执行状态）。
> 图例：✅ 完成并验证 ｜ 🟡 代码完成待验证/待提交 ｜ ⬜ 未开始
> 最后更新：2026-05-25

---

## 下次会话从哪开始

**M0–M4 全部已提交推送（M3=`9476136`+`f3e780f`，M4=`c0b521b`+`e64a69a`）。M5 代码完成 + 离线验收通过，待 live 验收 + 待提交。** 工作树有 M5 未提交改动。

M5 已做（巡检 + 自愈）：
- `agent/patrol.go`：后台 patrol goroutine（`Serve` 内、server 生命周期 ctx 起，CLI 不开也跑）。启动先跑一次再按 interval。检查集 disk/load/key_services，每个 check 跑**只读**命令（`df -P`/`cat /proc/loadavg`+`nproc`/`systemctl is-active`）+ **纯函数解析**（`parseDiskUsage`/`parseLoadAvg`/`parseNproc`，均有单测）。
- 自愈边界：被监控 unit `inactive/failed` → 候选 `sudo -n systemctl restart <unit>`，过 `safety.IsPatrolAutoRemedy`（仅 systemctl start/restart + 已配置 unit + 不撞危险规则，接受可选 sudo 前缀）→ 通过则执行 + `audit(source=patrol,decision=auto)`；disk/load 等无安全自愈 → 写 `todo`（不执行）；被 gate 拒的写操作 → `decision=skipped` audit + todo。
- 持久化：新建 `patrol_runs` 表（每次扫描落 checks/findings JSON）；`audit` 加 `source` 形参（chat/patrol）+ `skipped` 决策值；`logs` 输出加 source 列。
- config：`OPSAGENT_PATROL`(默认开)/`_INTERVAL`(5m)/`_CHECKS`(disk,load,key_services)/`_SERVICES`(默认空)/`_DISK_PCT`(90)/`_LOAD`(2.0/核)。**自动重启在 `_SERVICES` 列出 unit 前不会触发**（安全默认）。

离线验收（本机已过）：全测试+vet+gofmt 干净；新增 patrol 解析器单测、`IsPatrolAutoRemedy` 用例（含 sudo 前缀/拒绝面）、`runOnce` 集成测试（down unit→自动重启+auto audit+patrol_run；disk 超阈→只写 todo 不跑写命令）、patrol_runs 落库单测；交叉编译 amd64/arm64 仍 `statically linked`。

**未动 enroll**：patrol 开箱即跑只读检查；要让某机自动重启服务，需操作者在该机设 `OPSAGENT_PATROL_SERVICES`（enroll 暂不代填，留作部署后手动一步）。

**待 live 验收（需你那台 Linux 机）**：
- M5：列一个 unit 进 `OPSAGENT_PATROL_SERVICES` → 停掉它 → patrol 自动重启 + `opsagent logs` 见 patrol/auto 留痕；造 disk 超阈 → `opsagent todos` 看得到、不自动动手。
- 仍欠的旧 live：M3 两条（知识档案影响回答、重连引用历史，需 DeepSeek key）+ M4 部署全链路（干净机 `enroll <host>` → `connect <host>` 即用 → 专用用户运行、提权走 sudo 白名单，首次跑通 SSH 路径）。

**待提交**：M5 改动尚未 commit。

下一步 = 你那边 live 验收（M3/M4/M5），过了提交 M5；然后 M6（按需增强）。

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

### M3 — 记忆 + Keystore 加密　🟡（代码+离线验收过，待 live 验收+提交）
- [x] `memory/knowledge`：加载 Markdown 知识档案（`knowledge.go`），注入系统提示
- [x] `memory/store`：会话历史持久化（`history.go`：messages 表 + Append/Recent）+ 跨 session 回看（单一滚动线程）
- [x] `secret/keystore`：API key 加密存取（secretbox，主密钥独立 0600 文件）
- [x] `opsagent key set/list` 子命令（set 从 stdin 读值）
- [x] 离线验收：单测/vet 过、交叉编译静态二进制、key 密文落盘无明文、serve 从 keystore 启动
- [ ] live 验收：重开 session 引用历史；知识档案影响回答（需 DeepSeek key）
- [x] git commit + push M3（`9476136`+`f3e780f`）

### M4 — enroll 一键部署　🟡（代码+离线验收过，待 live 验收+提交）
- [x] `opsagent enroll <host>`：scp 二进制、建系统用户、生成 sudoers 白名单（仅 systemctl/journalctl）、装 systemd unit、初始化目录、provision key+provider
- [x] `opsagent todos` / `logs` 查看入口（只读打开本地 DB）
- [x] 离线验收：enroll 生成物单测（arch/sudoers/unit/bootstrap）、logs/todos 读 DB、交叉编译静态二进制、vet/gofmt 干净
- [ ] live 验收：干净 Linux 机一条命令部署、`connect` 即用、专用用户运行、提权走 sudo 白名单（需你那台机，首次跑通 SSH 路径）
- [x] git commit + push M4（`c0b521b`+`e64a69a`）

### M5 — 巡检 + 自愈　🟡（代码+离线验收过，待 live 验收+提交）
- [x] `agent/patrol`：定时调度 + 检查集（disk/load/key_services），纯函数解析 + 单测
- [x] 复用 Safety Gate + Tools + audit：可逆且白名单（`IsPatrolAutoRemedy`）→自动执行；高危/不可逆→跳过 + 写 `todos`（决策：v1 巡检不调模型，模型诊断后置 M6）
- [x] `patrol_runs` 表 + `todos` 落库；`logs` 加 source 列 surface
- [x] 离线验收：解析器/whitelist/runOnce 集成测试、patrol_runs 落库、vet/gofmt 干净、交叉编译静态二进制
- [ ] live 验收：可逆异常（停被监控服务）自动修复留痕；高危场景（disk 超阈）不执行、写待办、CLI 可见
- [ ] git commit + push M5

### M6 — 增强　⬜（按需）
- [ ] 批量任务（CLI 侧 fan-out 到多台）
- [ ] TUI 升级到 bubbletea　⚠️依赖待决：bubbletea/lipgloss
- [ ] 多模型场景化（便宜模型巡检、强模型诊断）
- [ ] 巡检 check 插件化
- [ ] 配置从环境变量升级到 TOML 文件　⚠️依赖待决：TOML 库

### 跨里程碑待办
- [ ] M0 SSH 路径 live 验收（需你那台 Linux 机器）——将随 M4 enroll/connect live 验收一并跑通
- [ ] 依赖决策待点头：bubbletea(M6) / TOML(M6)　（SQLite(M2)、x/crypto secretbox(M3) 已批准并落地）
- [x] 推理模型支持（已修+已推送 `95019ae`，2026-05-24）：OpenAI 适配器捕获 `delta.reasoning_content`（发 `EventReasoningDelta`），loop 累积存到 `Message.Reasoning`，`buildOpenAIMessages` 仅在非空时回传 `reasoning_content`。新增两个离线测试；live 用 `deepseek-v4-pro` 复跑第二轮不再 400。非推理模型零影响。Anthropic extended thinking 仍未做（另一套 thinking block 机制，等真用 Claude 推理再列）。

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

- **2026-05-25 M3 加密库（已定）**：选 `golang.org/x/crypto/nacl/secretbox`（XSalsa20+Poly1305），不选 age。理由：只需"本地主密钥加密几个 key"，secretbox API 极简、纯 Go、几乎无额外传递依赖，最契合"最小依赖/纯静态二进制"。项目第二个第三方依赖（已批准）。
- **2026-05-25 M3 主密钥模型（已定）**：32B 随机主密钥落独立 0600 文件，与密文 keystore 分离。理由：决策日志要求"重启自恢复、自愈不中断"→ 启动不能让人输口令，必须无人值守自解，主密钥就得机器可读。**边界（明说不掩盖）**：这防的是明文 key 进配置/环境变量/进程列表/备份，**不防**能读到 opsagent 用户文件的攻击者——无人值守自解的固有取舍。
- **2026-05-25 M3 keystore 存储（已定）**：密文存独立 `keystore.json`（非复用 sqlite）。理由：secrets 与运营数据（audit/会话）生命周期分离、权限单独 chmod、备份 DB 不连带导出密钥、secret 包不依赖 sqlite。
- **2026-05-25 M3 会话记忆（已定）**：单一滚动线程，新连接注水最近 N 条（默认 50）。理由：单服务器+单运维场景最简且够用；带 session ID 要改 transport 握手和 CLI，超出 M3 验收。
- **2026-05-25 M3 API key 优先级（已定）**：`OPSAGENT_API_KEY` 存在则用（dev 覆盖），否则从 keystore 取 `api_key`。理由：满足"配置无明文 key"同时保留 dev 便利。

- **2026-05-25 M4 socket 接入（已定）**：组权限——socket `/run/opsagent/agent.sock` 组 `opsagent` 0660，enroll 把登录用户加进该组，`connect`/`_bridge` 直连不改。理由：标准做法、改动最小；备选 sudo 代跳要改 ConnectSSH 拼 sudo + 多一条 sudoers。注：组成员要重登生效，但 connect 每次新 SSH 会话故第一次即生效。
- **2026-05-25 M4 sudoers 范围（已定）**：仅 `systemctl`+`journalctl` NOPASSWD。理由：最小权限起步；写操作另有安全闸门确认拦，sudoers 只管"技术上允许什么"。拓宽留给用户事后手动。
- **2026-05-25 M4 key 供给（已定）**：enroll 顺手 provision——收 `--provider/--model/--base-url` + stdin 读 key；远端写 unit Environment(provider/model) + base64→`key set` 存密文。理由：满足"connect 即用"验收。
- **2026-05-25 M4 路径与默认（已定）**：Linux 上 config 默认走生产路径（state `/var/lib/opsagent`、socket `/run/opsagent/agent.sock`），dev(Windows/mac) 仍 UserConfigDir/temp。理由：serve/key/logs/_bridge 零参数对齐，`connect <host>` 免 flag。
- **2026-05-25 M4 enroll 机制（已定）**：`scp` 二进制 + `ssh host sudo -n bash -s` 跑幂等 bootstrap 脚本（key 走 base64 经管道进 `key set`，不落远端磁盘）。前提：SSH 用户免密 sudo 或 root，`sudo -n` 失败即清晰报错。systemd unit 走精简版（不加重度沙箱，因 sudo 需 NoNewPrivileges=false）。
- **2026-05-25 M4 logs/todos 读取（已定）**：只读打开本地 DB（`OpenReadOnly`，免 migrate 写），远端查看走 `ssh host opsagent logs`。理由：避免操作者组只读访问触发 migrate 写而失败；远端美化视图后置。

- **2026-05-25 M5 自愈白名单（已定）**：要满足 ROADMAP「服务停了→自动重启」，但 `systemctl restart` 在现有 Safety Gate 是写操作=需确认，巡检无连接不能确认。选**窄白名单** `safety.IsPatrolAutoRemedy`：仅 `systemctl start/restart <已配置 unit>`（接受可选 sudo 前缀、不撞危险规则）才允许巡检无人值守执行；其余写操作一律跳过+写 todo。备选「只读到底（任何写都只写 todo）」被否，因不满足自动重启完成标准。chat 路径不受影响仍走 `Classify` 确认。
- **2026-05-25 M5 巡检不调模型（已定）**：v1 检查全确定性（disk/load/key_services），自愈也确定性（重启挂掉的被监控 unit）。理由：ROADMAP 自评 M5「主要是组装」，且 M6 已明确「便宜模型巡检、强模型诊断」属后续；定时调模型增成本/不确定性/难离线测。模型驱动诊断留给 M6。
- **2026-05-25 M5 自动重启默认安全（已定）**：patrol 默认开但只跑只读检查；自动重启**仅对** `OPSAGENT_PATROL_SERVICES` 显式列出的 unit 触发，默认空 → 开箱不会擅自动手。enroll 暂不代填该变量，留作部署后操作者手动一步。
- **2026-05-25 M5 audit 扩展（已定）**：共享 `audit()` 加 `source` 形参（chat|patrol），新增 `skipped` 决策值（巡检拒绝执行的写操作留痕）；新建 `patrol_runs` 表存每次扫描 checks/findings JSON，对齐 ARCHITECTURE 数据模型。
