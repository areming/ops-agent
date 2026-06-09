# TASK.md — ops-agent 执行状态

> 唯一执行事实来源。设计见 REQUIREMENTS / TECH_STACK / ARCHITECTURE / ROADMAP（这些只讲设计，不记执行状态）。
> 图例：✅ 完成并验证 ｜ 🟡 代码完成待验证/待提交 ｜ ⬜ 未开始
> 最后更新：2026-06-09

---

## 下次会话从哪开始

**M8 自定义命令 + 执行动效已提交并推送 `main`（`ea802bc` + `e4ed947`，2026-06-09）**：两个用户反馈驱动的小特性，代码 + 离线验收（全测试/vet/gofmt 干净、交叉编译 amd64/arm64 仍 statically linked）通过。详见决策日志 2026-06-09 与下方 M8 小节。**待 live 验收（需 Linux 机 + 真模型）**：① agent 的 `commands/` 放个 `*.md` → `/commands` 列出 → `/<名称>` 触发跑通一轮（确认 + audit 照常）；② 长命令运行 / 轮次间观察状态行（`⠹ 执行中… 8s`），确认不再像卡死。**未动 enroll**：commands 目录开箱即读，但 enroll 暂不代种示例命令（同 knowledge，留作部署后手动一步）。

**M8-③ 命令目录可发现 + Tab 补全已提交 `main`、未 push（`7872a6e` 修复 + `ec9135c` 补全，2026-06-09）**：live 验收发现「文件放进去 `/commands` 仍列空」其实是放错目录（两条 local 路径目录不同），修法是让 `/commands` 报出 agent 真实读取的目录绝对路径；同时按用户反馈加了 `/命令` 名 Tab 补全（内置+自定义）。详见 M8 小节 ③ 与决策日志 2026-06-09 M8-③。**待 push + live 复验**。

---

**CLI 引导重构 + 文档纠错已提交 `main`（`1b72ea1` 起）**：onboarding 与部署向导统一成上下键菜单，新增 provider 目录（13 家 + 自定义，base URL/在售模型预填），API key 改掩码回显（露头尾）；同批校正了 README / ONBOARDING / ARCHITECTURE / TECH_STACK / ROADMAP / RUNBOOK 的命名与设计漂移（详见决策日志 2026-06-03）。发 `v0.0.13`。下一步：真机走一遍引导确认渲染，按需补 live 验收。

---

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
- [x] live 验收（2026-05-26）：经 gw 跳板部署到 vps 成功，agent 以 opsagent 用户运行、sudoers 白名单就位、`setup` 向导一键跑通、connect 可用。SSH 路径（ProxyJump）首次跑通
- [x] git commit + push M4（`c0b521b`+`e64a69a`）

### M5 — 巡检 + 自愈　🟡（代码+离线验收过，待 live 验收+提交）
- [x] `agent/patrol`：定时调度 + 检查集（disk/load/key_services），纯函数解析 + 单测
- [x] 复用 Safety Gate + Tools + audit：可逆且白名单（`IsPatrolAutoRemedy`）→自动执行；高危/不可逆→跳过 + 写 `todos`（决策：v1 巡检不调模型，模型诊断后置 M6）
- [x] `patrol_runs` 表 + `todos` 落库；`logs` 加 source 列 surface
- [x] 离线验收：解析器/whitelist/runOnce 集成测试、patrol_runs 落库、vet/gofmt 干净、交叉编译静态二进制
- [ ] live 验收：可逆异常（停被监控服务）自动修复留痕；高危场景（disk 超阈）不执行、写待办、CLI 可见
- [ ] git commit + push M5

### M6 — 增强　🟡（A/B/C 代码+离线验收过，待提交；其余按需）
- [x] 巡检 check 插件化（A）：抽 `check` 接口（`name`/`run(ctx,runner)`）+ `diskCheck/loadCheck/servicesCheck` + `buildChecks`（未知名 skip）；解析纯函数不变。纯重构，现有测试不改即过 + 新增 `TestBuildChecks`
- [x] 多模型场景化（B）：`OPSAGENT_DIAG_*` 诊断模型（未配回退主模型）；抽 `engine`+`interaction` 把 model↔tool 循环从 conn 解耦（chat=connInteraction 行为不变，loop approve/deny 测试无回归）；patrol 对无自动修复的 finding（disk/load）跑无连接诊断 turn，模型只读诊断/写操作→todo（附分析）；`OpenTodoExists` 去重（顺带修 M5「满盘每 tick 刷 todo」隐患）
- [x] 批量任务 fan-out（C）：`opsagent run -c "<指令>" <host>... [--yes]`，抽 `sshBridge` 复用、有界并发（5）、非交互 drain；默认拒绝需确认的写操作并标「需人工」，`--yes` 显式全批准；成组打印 + 汇总
- [x] 引导式部署向导（D）：`opsagent setup` 交互问答（provider/模型/host）→ 自动前置检查（ssh + `sudo -n`，失败给 visudo 修复提示并可重试）→ 复用 `cli.Enroll` 部署 → `systemctl is-active` 验证。隐藏 key 输入引 `golang.org/x/term`（第 3 个依赖，已批准）。纯函数 `normalizeProvider/defaultModel/isYes/setupSummary` 有单测
- [x] 易用性增强（E，用户反馈后）：① connect/run 提示符带 host 名（`[vps] >`）；② 向导/enroll 顺手 provision `OPSAGENT_PATROL_SERVICES`（`--services`）+ `OPSAGENT_DIAG_MODEL`（`--diag-model`，复用主 provider/key），写进 systemd unit；③ `install.ps1` Windows 本地一键安装（拷 exe 进 PATH + 启用 ssh-agent）。enroll_test 覆盖新 env 行。已 live 跑通（vps 经 gw 跳板部署成功）
- [x] 离线验收（A/B/C）：全测试/vet/gofmt 干净（新增 buildChecks、诊断记 todo+skipped、dedup 守卫、fanout decline/approve/失败汇总测试）；交叉编译 amd64/arm64 静态二进制
- [ ] git commit + push M6（A/B/C）
- [ ] live 验收：fan-out 多台跑通；DIAG 模型对真实 disk/load 异常给出有用诊断 todo（需 DeepSeek key + 多机）
- [ ] TUI 升级到 bubbletea　⚠️依赖待决：bubbletea/lipgloss（未做）
- [ ] 配置从环境变量升级到 TOML 文件　⚠️依赖待决：TOML 库（未做）

### M7 — ops 易用性重构（用户反馈驱动）　🟡（代码全部已提交，待 live 验收）
目标：默认零配置、用户只敲 `ops`、对话内 `/命令` 对齐 claude、本地与 SSH 远程共用一套对话代码。决策全锁定见决策日志 2026-05-26。

实际提交路径（各步内容分布在下列 commit，与计划拆法有出入但内容全覆盖）：
- `05c4699` feat(config): persist model selection to config.json → ①
- `dc31450` feat(transport): add control request/reply frames → ③
- `e8ad7e7` feat(model): add KnownModels for the /models command → ③
- `733d4cc` chore(version): add build version package → ⑤
- `aa77c23` feat(agent): in-process local session and slash-command control → ② ③
- `e54de68` feat(cli): local onboarding, slash commands, and connect self-install → ② ③ ⑤
- `6105e79` feat(cli): rename the user-facing command to ops → ④
- `32dbb57` docs(task): record M7 ops usability rework plan and progress

#### ① 配置落盘（基石）　✅ 已提交（`05c4699`）
- [x] `config.go`：`Load()` 优先级 env > `config.json`(StateDir 下) > 默认；`fileConfig` 只含模型选择字段（provider/model/base_url + diag 三件）
- [x] `config.Save(cfg)`：原子写（temp+rename）、0600，不写 API key/派生路径；损坏文件静默回退 env/默认（注释说明）
- [x] `config_test.go`：缺文件用默认、读文件、env 覆盖文件、Save 往返且文件不含 key
- [x] 离线验收：gofmt/vet/`go test ./...` 全绿无回归
- [x] git commit（`05c4699`）

#### ② `ops`（无参）本地进程内对话 + 未配置引导　✅ 已提交（`aa77c23` + `e54de68`）
- [x] `agent/daemon.go` 抽 `newServer(cfg) (*server, error)` + `(*server).Close()`；`Serve` 改为调它（diagProv 仅 patrol 启用时建）；`server` 加 `cfg` 字段供③用
- [x] `agent/daemon.go` 加 `LocalSession(nc net.Conn) error`：`newServer`→`srv.handle(nc)`→关 store；本地交互**不起 patrol**
- [x] `cli/onboard.go`(新) `onboardLocal()`：复用 setup 的 prompt 系列→收 provider/model/base_url/key→`persistLocalConfig`（keystore 存 `api_key` + `config.Save`，抽出供测试）
- [x] `cli/local.go`(新) `RunLocal()`：`configured()`(先判 model 空避免无谓创建 master.key)→未配则 `onboardLocal`→`net.Pipe()`→`go agent.LocalSession`→`repl(_,"local")`
- [x] `cmd/.../main.go`：无参由「usage+exit2」改为 `cli.RunLocal()`；usage 加无参说明行
- [x] 测试 `cli/local_test.go`：`configured()` 真值表（无 model/有 model 无 key/env key/落盘后）+ 不过早创建 master.key + `persistLocalConfig` 往返
- [x] 离线验收：`go build/vet/test ./...` 全绿、gofmt 干净、无回归
- 注：`LocalSession` 全链路需真模型+终端，未单测；其 engine/handle 路径已由 `loop_test.go`(net.Pipe) 覆盖，`RunLocal` 仅薄 glue
- 注（2026-06-04 后续）：`RunLocal` 不再总是起进程内会话——已 enroll 的机器上先接管常驻 daemon（见决策日志「2026-06-04 一机一脑」）；进程内会话抽成 `runLocalSession`，仅在无常驻 daemon 时走
- [x] git commit（`aa77c23` + `e54de68`）

#### ③ 对话内 `/命令`（控制帧，本地远程统一）　✅ 已提交（`dc31450` + `e8ad7e7` + `aa77c23` + `e54de68`）
- [x] `transport/protocol.go`：加 `TypeControlRequest`/`TypeControlReply` + `ControlRequestPayload{Cmd,Arg}` / `ControlReplyPayload{Text,Err}`
- [x] `model/registry.go`：`KnownModels(provider) []string`（openai/deepseek/anthropic 常见清单，未知 nil）
- [x] `agent/daemon.go`：`server` 加 `sync.Mutex` 守 `cfg`/`prov`，加 `provider()` 加锁读、`chatTurn` 改用之；`handle()` 改 switch 加 `TypeControlRequest`→`handleControl`：
  - `models` 空→`formatModelList`(标当前)；有 arg→`resolveAPIKey`+`model.New` 重建 prov、`cfg.Model=arg`、`config.Save`（快照读+加锁写，race-clean by design）
  - `logs`→`RecentAudit` 同 `logs` 子命令格式
  - `clear`→`sess.msgs=nil`（在读循环同 goroutine，可原地重置）
- [x] `cli/client.go` `repl()`：行首 `/` 拦截——`/help`(`?`) `/quit`(`exit`/`q`) 本地；`/models`/`/logs`/`/clear` 走 `sendControl`（写请求帧+读单条 `ControlReply` 打印，无 Done）；banner 提示 `/help`
- [x] 测试：`model.KnownModels`；`agent` 的 `controlModels` 列表+切换持久化、`controlLogs`(空/有记录)、`handleControl("clear")` net.Pipe 往返且 sess 清空
- [x] 离线验收：`go build/vet/test ./...` 全绿、gofmt 干净（`-race` 因 CGO 关不可用，锁靠设计保证）
- [x] git commit（`dc31450` + `e8ad7e7` + `aa77c23` + `e54de68`）

#### ④ 门面改名 → `ops`（仅用户可见层）　✅ 已提交（`6105e79`）
- [x] `git mv cmd/opsagent cmd/ops`；包注释、`usage()`、错误前缀、`connect/run` 默认 `--bin` 全改 `ops`
- [x] `cli/enroll.go`：`installBinPath`→`/usr/local/bin/ops`；新增 `legacyBinPath` + bootstrap `ln -sf` 建 `opsagent`→`ops` 软链；`resolveBinary` 默认 `dist/ops-linux-<arch>`；成功提示 `ops connect`
- [x] `client.go`(banner+_bridge 注释)、`daemon.go`(`ops key set` 提示)、`bridge.go`(注释)、`setup.go`(`ops setup`/`ops connect`)
- [x] `build.ps1`/`install.ps1`：产物名+`./cmd/ops`+安装名 `ops.exe`
- [x] `enroll_test.go`：ExecStart/install/key-set 路径改 `/usr/local/bin/ops`，新增软链断言
- [x] README.md/README.en.md：命令示例改 `ops` + 补 `ops` 无参与 `/命令` 说明（设计文档的 daemon/项目名叙述保留 `opsagent`）
- [x] **未动**（核对）：单元 `opsagent.service`、用户/组 `opsagent`、`/var/lib/opsagent`、`/run/opsagent`、`OPSAGENT_*`、persona prompt
- [x] 离线验收：`go build/vet/test ./...` 全绿、gofmt 干净；grep 审计残留 `opsagent` 仅 infra
- [x] git commit（`6105e79`）

#### ⑤ 自举安装（release 包，仅「没装就装」）　✅ 已提交（`733d4cc` + `e54de68` + `6105e79`）
- [x] `internal/version`(新)：`Value`(默认 "dev")，`-ldflags -X .../version.Value=<tag>` 刻入
- [x] `cmd/ops/main.go`：`ops version`/`-v`/`--version`；enroll 传 `Version: version.Value`
- [x] `.github/workflows/release.yml`(新)：tag `v*` 触发，`CGO_ENABLED=0` 交叉编译 `ops-linux-{amd64,arm64}` + `sha256sum > SHA256SUMS`，`gh release create` 上传
- [x] `build.ps1`：从 `$env:OPS_VERSION`/`git describe`/"dev" 取版本，`-ldflags` 刻入（amd64/arm64/windows 都带）
- [x] `cli/release.go`(新)：`releaseBinURL`/`releaseSumsURL`(repo 硬编码)、`fetchChecksum`(本地拉 SHA256SUMS)、`parseChecksum`——可复用留给将来 `ops update`
- [x] `cli/enroll.go`：`localBinary`(显式 --bin 缺失报错；无则 dist/ 有则用，否则 "")；`obtainStep`(本地有→scp 设 `$BIN_SRC`；无→version 非 dev 则远程 `curl`+`sha256sum -c`，dev 报错指引 build.ps1)；`buildBootstrap` 改吃 obtain 片段、`install "$BIN_SRC"`
- [x] `cli/setup.go`：抽 `setupHost(r,host)` + 导出 `SetupHost(host)`(connect 复用)；Enroll 传 Version
- [x] `cli/client.go` `ConnectSSH`：先 `RemoteHasBinary`(`command -v`，ExitError=未装)；未装→`promptYesNo`→`SetupHost`→续连
- [x] 测试：`release_test`(URL 拼装、`parseChecksum` 命中/缺失)、`enroll_test`(fetch-mode bootstrap、`localBinary` 显式缺失报错/无 dist 返回空，`t.Chdir` 隔离)
- [x] 离线验收：`go build/vet/test ./...` 全绿、gofmt 干净；`CGO_ENABLED=0` 交叉编译 linux amd64+arm64 通过；`ops version` 刻入值正确
- 注：connect 自举全链路（真 SSH+真 release）需 live 验收，离线只覆盖各纯函数+脚本生成
- [x] git commit（`733d4cc` + `e54de68` + `6105e79`）

#### 收尾
- [x] git commit（各步已提交，见上方 commit 列表）
- [x] push M7（已推送 main）
- [x] release `v0.0.9`（2026-05-31）：含 M7 后续 CLI 体验打磨（欢迎页机器人吉祥物 + truecolor、对话样式 `⏺` 标记+留白、`/exit`·`/model` 对齐 claude、`NO_COLOR`/`COLORTERM`/`WT_SESSION` 上色 gate，零新依赖）。release workflow 出包就绪
- [ ] live 验收：①干净机 `ops`→引导→对话；②会话内 `/models` 切换重启仍生效、`/logs`、`/clear`；③干净机 `ops connect <host>`→确认→拉 release 装→进对话（v0.0.9 已出包）；④老机重 enroll 后 `opsagent` 软链可用

### M8 — 自定义命令 + 执行动效（用户反馈驱动）　🟡（代码+离线验收过，已提交推送，待 live 验收）
两个小特性，分两个 commit（`ea802bc` 特性 1 / `e4ed947` 特性 2，已 push main）。

#### ① 自定义命令 `/<名称>`　✅ 已提交（`ea802bc`）
- [x] `memory/commands.go`：`*.md` 加载 + 极简 frontmatter（`name`/`description` + 正文）解析，仿 `knowledge.go`；`LoadCommands`/`FindCommand`。缺正文跳过、缺 frontmatter 整体作正文（裸脚本可用）。
- [x] `config.go`：`OPSAGENT_COMMANDS_DIR`，默认 `StateDir/commands`。
- [x] `transport`：新帧 `TypeRunCommand`（`RunCommandPayload{Name,Args}`）——开一整轮而非单条 control 回复；`CmdCommandList` + `CommandListReply`/`CommandInfo`。
- [x] `agent/commands.go` + `daemon.go`：`runCommandTurn` 每次重读目录（随放随用免重启）→ 命中则 `buildCommandPrompt`（框架说明 + 正文 + 附加参数）注入成本轮 user 输入 → 跑正常 `chatTurn`（工具/安全闸门/确认/审计**照旧、不绕过**）；未命中 → Error + Done 干净返回。`handleControl` 加 `command.list`。
- [x] `cli/customcmd.go` + `client.go`/`repl_raw.go`：非内置 `/xxx` 转发 RunCommand 后 drain；新增 `/commands`(`/cmds`) 列表、`/help` 末尾追加自定义命令、help 文案更新；`handleSlash` 签名加 `in *bufio.Scanner`（cooked 路径 drain 需要）。
- [x] 测试：commands 解析/加载/Find（含 crlf、未闭合 fence、desc 别名、缺目录）、`buildCommandPrompt`、`runCommandTurn`（命中跑轮+注入校验 / 未知名 Error+Done+不动 session）、`commandList` JSON、`printCommands`（描述/无描述/空/quiet）。
- [x] 离线验收：全测试/vet/gofmt 干净、交叉编译 amd64/arm64 statically linked。
- [ ] live 验收：放 `*.md` → `/commands` 列出 → `/<名称>` 触发跑通一轮（含确认 + audit）。

#### ② 执行动效（消除"死机感"）　✅ 已提交（`e4ed947`）
- [x] 根因：原 spinner 每个 frame 到达即停、只有 `ToolStart` 重启 → 命令静默期 + 轮次间思考期无动画。
- [x] raw 路径（真实交互）：`drainRaw` 改用 select 内 `time.Ticker` 同步渲染状态行（spinner + 思考中/执行中 + 已等秒数），与小窗口同 goroutine 绘制避开光标竞争；去抖 150ms、`replyOpen` 期间不画（不打断流式回复，复查时抓到并修了这个会抹半句回复的 bug）；状态行落小窗口下一行或独立行。
- [x] cooked 路径（非 TTY 兜底）：最小生命周期修复，`ToolOutput` 后重启 spinner 让静默期也动；spinner 计时复用纯函数 `statusBody`。
- [x] 测试：`statusBody`（亚秒只显标签 / 过秒显计时）、`TestDrainRawAnimatesDuringSilentGap`（ToolStart 后静默 → 确实出现"执行中"）；`TestDrainRawEscSendsCancel` 无回归。
- [x] 离线验收：全测试/vet/gofmt 干净、交叉编译 amd64/arm64 statically linked。
- [ ] live 验收：长命令运行 / 轮次间状态行可见、不再像卡死。

文档已同步：README（`/命令` 行 + 自定义命令小节 + `OPSAGENT_COMMANDS_DIR`）、ARCHITECTURE（§3.2.1 自定义命令、帧列表加 RunCommand/ToolOutput、目录树 memory 加 commands）。

#### ③ live 验收补强：命令目录可发现 + Tab 补全　🟡（代码+离线验收过，已提交 `main`，待 push + live 复验）
两特性改的文件不交叉，按特性分两个 commit（`7872a6e` 修复 / `ec9135c` 补全）。

- [x] **BUG-016 修复**（`7872a6e`，live 验收发现）：放进 `/var/lib/opsagent/commands/` 的 `*.md`，裸 `ops`（`[local]`）`/commands` 列空、`/<名称>` 报「未知命令」。根因**不是 loader**——错误文案正是 agent 端 `runCommandTurn` 输出，帧到达了 agent；是裸 `ops` 两条路径（接管常驻 daemon vs 进程内会话，见决策日志「2026-06-04 一机一脑」）`CommandsDir` 不同（daemon=`/var/lib/opsagent/commands`，进程内=登录用户 `defaultStateDir()` 下的 `commands`），而操作者/模型无从得知应答 agent 实际读哪个目录，文件落错目录就静默不加载。修：`CommandListReply` 加 `Dir`；`commandList` 填 `srv.commandsDir()`；`printCommands` 空态+列表态都打印真实绝对路径，并说明"重开 ops 即自动读取"。（BUGS.md BUG-016，gitignore 本地档案不入库。）
- [x] **Tab 命令补全**（`ec9135c`，用户反馈）：raw REPL 行编辑器加 `keyTab`，仅 `/` 开头且无空格（补命令名而非参数）时生效；候选 = 内置命令 + 自定义命令（按 Tab 经控制帧**实时拉**，新加的 `*.md` 也补）；0 匹配下方提示「无匹配命令」（即时告知敲错/无此命令）、1 匹配补全+空格、多匹配补公共前缀/补不动则列出候选并重画提示行。纯函数 `completeCommand`+`longestCommonPrefix`（**逐 rune**，CJK 名不切坏）。`/help` 加 Tab 提示行。cooked 路径（非 TTY）逐行读拦不到 Tab，不做。
- [x] 测试：`completeCommand`（7 场景）、`lineEditor.complete`（5 场景：唯一补全+空格/多匹配列出/无匹配提示/参数内 inert）、`keyTab` 解码用例。
- [x] 离线验收：全测试/vet/gofmt 干净；两特性各自独立可编译（提交前 stash 验证）。
- [ ] 待 push（用户尚未要求推送）。
- [ ] live 复验：放 `*.md` 进 `/commands` 报出的**真实目录** → `/<名称>` 触发跑通；`/<前缀><Tab>` 补全、`/不存在<Tab>` 提示无匹配。

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

- **2026-05-25 M6 范围（已定）**：本轮只做无新依赖的三项——A 巡检 check 插件化、B 多模型场景化、C 批量 fan-out。TUI(bubbletea)/TOML 仍待依赖点头，未做。
- **2026-05-25 M6-B loop 解耦（已定）**：抽 `engine`（model↔tool 循环核心）+ `interaction` 接口，把循环从 `*transport.Conn` 解耦。chat 走 `connInteraction`（与原行为逐帧一致）；patrol 诊断走 `patrolInteraction`（`confirm`恒 false、写操作记 skipped + 回灌「改成建议」、delta 累积进文本）。理由：对话与自愈复用同一循环（ARCHITECTURE 既定），且无连接诊断不能弹确认。硬约束：`loop_test.go` approve/deny 不回归。
- **2026-05-25 M6-B 诊断触发面（已定）**：只对**无自动修复**的 finding（disk/load）触发强模型诊断；key_services 已自动重启不再调模型。诊断用 throwaway session（store=nil）不污染对话线程。理由：成本最可控、分工自然。模型未配（`OPSAGENT_DIAG_*` 空）则回退主模型。
- **2026-05-25 M6-B todo 去重（已定）**：`OpenTodoExists` 按标题去重——同一持续问题只诊断一次、不每 tick 刷 todo。顺带修掉 M5 的 todo 刷屏隐患。
- **2026-05-25 M6-C fan-out 确认策略（已定）**：非交互批量默认**拒绝**需确认的写操作（只跑自动放行的只读/白名单），declined 标「需人工」；`--yes` 显式 opt-in 全批准（危险，手动开）。备选「串行逐台交互」被否（与批量初衷相悖）。SSH stderr 仍直通 os.Stderr（多机会轻微交错，可接受，美化后置）。
- **2026-05-26 M7 范围（已定）**：用户反馈驱动的易用性重构，5 步：①配置落盘 ②`ops` 无参本地对话+引导 ③对话内 `/命令` ④门面改名 `ops` ⑤自举安装。原则：默认零配置、只敲 `ops`、本地与 SSH 远程共用一套对话代码。
- **2026-05-26 M7 配置格式（已定，更正旧决策）**：配置落盘选 **JSON（stdlib，零依赖）**，不用 TOML。**更正** 2026-05-24「TOML 后置 M7」的计划——JSON 反而避免引入 TOML 库，更贴合「纯静态/最小依赖」基调。优先级 env > config.json > 默认，env 仍可覆盖故不破坏 live（systemd unit 的 `OPSAGENT_*` 照旧生效）。只存模型选择字段，不存 API key（仍在 keystore）/派生路径。`跨里程碑待办` 里 TOML 依赖待决项就此关闭。
- **2026-05-26 M7 改名范围（已定，门面 only）**：只改用户可见层（二进制名、prompt、帮助、`connect/run` 默认 `--bin`、装到 `/usr/local/bin/ops`）。基础设施名（systemd 单元 `opsagent`、服务用户、`/var/lib/opsagent`、`OPSAGENT_*` 环境变量）**不动**——它们与已部署的 live 绑定，改名要重装迁移。老机过渡：下次 enroll 自动建 `opsagent`→`ops` 软链兼容。
- **2026-05-26 M7 `/models` 语义（已定）**：切换作用于「当前对话所在的那台 agent」——本地会话切本地、SSH 会话切远程那台。实现为发给当前 agent 的控制帧，agent 重建自身 provider + 落盘自身 config.json。本地与远程一份代码（本地走 `net.Pipe()` 内存管道复用同一 Conn/REPL）。
- **2026-05-26 M7 自举安装（已定）**：库公开→匿名 curl，release 地址硬编码默认（零配置，仅镜像例外才进配置）。版本编译进二进制→`connect` 新机装「同本机版本」，不问版本。下载远程自取为主、本地下载+scp 兜底（无外网）。**必做 SHA256 校验**（网络来的二进制要当 root 跑，供应链面）。本轮仅「没装就装」；升级走后续手动 `ops update`（复用 `release.go`）。
- **2026-05-25 M6-D 引导向导（已定）**：用户反馈手动多步部署「有点小复杂」，加 `opsagent setup` 交互向导降低首用门槛。范围只做向导（安装维持本地 build.ps1，不引 GitHub Releases/安装脚本），入口显式子命令（不抢无参 usage 行为）。隐藏 key 输入引 `golang.org/x/term`（第 3 个第三方依赖，已批准；纯 Go、与 x/crypto 同源、`CGO_ENABLED=0` 交叉编译仍 statically linked 已核对）。向导不重写部署，复用 `cli.Enroll`；前置检查 ssh+`sudo -n` 失败给修复提示并可重试。
- **2026-06-03 CLI 引导重构（已做，提交 `1b72ea1`）**：onboardLocal 与部署向导统一成 claude 风格上下键菜单（复用会话内确认菜单的 raw-mode 渲染），非 TTY 回退编号选择。新增 `internal/cli/wizard.go` provider 目录：DeepSeek/OpenAI/Anthropic/Moonshot/Qwen/z.ai/Doubao/Gemini/xAI/Groq/Mistral/OpenRouter/SiliconFlow + 自定义，base URL 与在售模型列表按 2026-06 官方现状预填；模型列表末尾留 Custom 手填。第三方 OpenAI 兼容商统一存 `provider=openai + baseURL`，**不动** `model.New`/config schema（代价：banner/`/model` 显示 adapter 名而非品牌，靠模型名区分）。API key 改掩码回显（`maskSecret` 露头尾、中间打码）。移除 `normalizeProvider/promptProvider/promptSecret/defaultModel`（迁入 wizard.go 并目录化）；M6-D 的 `normalizeProvider` 单测随之换成 `lookupProvider/maskSecret/目录校验`。
- **2026-06-03 文档纠错（已做）**：按代码/历史校正说明文档的命名与设计漂移——命令名 `opsagent`→`ops`（`cmd/ops`、`dist/ops-*`；服务用户/单元/`OPSAGENT_*` 等基础设施名保留）、配置 TOML→JSON（`StateDir/config.json`）、keystore `age`→仅 NaCl secretbox、终端 UI bubbletea→自写 raw-mode REPL（未引框架）、SSH 实为委托系统 `ssh`/`scp`（非 `x/crypto/ssh`）、本地配置目录 `~/.config/opsagent`（RUNBOOK 原写成 `ops-agent`，已修）。涉及 README(.en)/ONBOARDING/ARCHITECTURE/TECH_STACK/ROADMAP/RUNBOOK。设计文档里被取代的草案（ARCHITECTURE §3.3 TOML 示例、§8 拍板项、TECH_STACK 选型表）保留原文并加「已更正/实际落地」注，不改写历史/理由。
- **2026-06-03 发版 v0.0.13（已发）**：含上述 CLI 引导重构 + 文档纠错。tag 推送触发 `.github/workflows/release.yml` 出 linux-amd64/arm64 + windows-amd64 + SHA256SUMS。
- **2026-06-04 一机一脑 / 裸 `ops` 接管常驻 agent（已做）**：修「已 enroll 机器裸 `ops` 重复引导」——原会另起进程内会话、读登录用户的空配置（`$HOME/.config/opsagent`），重复引导出与巡检 daemon 无关的第二大脑。改 `RunLocal`：Linux 上先探测常驻 daemon socket（`/run/opsagent/agent.sock`），纯函数 `classifyResident(dialErr, unitInstalled)` 四分流——可连即**接管**（复用 daemon 的模型/记忆/巡检，复用 `replOverConn`，等价 `connect --local`）；EACCES → 提示加 `opsagent` 组重登 / `sudo ops`；unit 装了没跑 → 提示 `systemctl start`；都不命中（无 daemon / 非 Linux）才走 `runLocalSession`（原进程内会话 + 引导）。**原则**：一台机器只有一个常驻 agent，是该机配置/记忆/审计/巡检的唯一事实来源，所有入口只是连它的瘦客户端；登录用户经 socket 借 daemon 配置、自身零配置（**不回退** 2026-06-01 把 `defaultStateDir` 改 `UserConfigDir` 的作用域隔离）。顺手抽 `replOverConn` 消除 `ConnectLocal` 的重复 dial+banner+repl；`connect --local` flag 保留作 dev 逃生口，仅从用户文档撤宣传。新增 `TestClassifyResident`（四分支）。文档全扫：README(.en)/ONBOARDING/RUNBOOK/ARCHITECTURE/TASK 同步。
- **2026-06-04 模型档案 + `/model` 面板（Phase 1 已做）**：把「一机一个活跃模型」升级成「档案列表 + 活跃指针」。**1a 后端**：`config.json` 改 `{active, models[]}`（每档案 provider/model/base_url + `key_ref`），`config.ListProfiles/AddProfile/SetActive/DeleteProfile`，Load 解析活跃档案并暴露 `KeyRef`，移除扁平 `Save`；老扁平 config.json 自动迁移成 `default` 档案（仍指 `api_key`，升级零重输）。`secret.Keystore.Delete`。`transport` 加 `model.list/switch/add/delete` 控制载荷（JSON 走 Arg/Text，不加帧类型；add 带 key 由 daemon 入库）。`agent` 四 handler + `resolveAPIKey` 读 `KeyRef`，删 `formatModelList`。删冗余 `model.KnownModels`（过时、按 adapter 取会显示错牌子）。提交 `1c51b74`。**1b 客户端**：`/model` 在 raw 路径开交互面板（建在现有 `keys` 事件通道上——stdin 被 key-reader 独占，不能直读；菜单/文本输入复用 keyEvent，新增 `keysMenu`/`keysReadLine`，「新增」复用 `providerCatalog`），cooked 路径退化为文本列表 + `/model <名称>` 切换；抽 `controlRoundTrip`（conn/frames 两实现）去重 `sendControl`/`sendControlRaw`；onboard 改写一个档案。Phase 1 不碰 enroll/unit——enrolled 远端 env 仍覆盖，切换/新增当次生效但重启还原（Phase 2 改 enroll 停钉 env + 种 config.json）。
- **2026-06-09 M8-① 自定义命令机制（已定）**：命令存 **agent 侧**（`StateDir/commands/*.md`，同模型/记忆/knowledge），本地与 `connect <host>` 远端会话都生效；客户端保持瘦客户端、不解析命令定义。触发选 **开一整轮**（新帧 `RunCommand`，非 `ControlRequest` 单条回复）——把命令正文注入成本轮 user 输入跑正常 `chatTurn`，于是工具/安全闸门/确认/审计**一律照旧、命令不绕过任何安全机制**；shell 与自然语言因此统一成「模型读定义后执行」，天然满足用户「模型了解要做什么 + 操作空间」的诉求。每次触发重读目录（随放随用免重启）。**MVP 边界（明说）**：「操作空间」靠定义文本传达给模型，**不**做 per-command 强制只读/限定工具的硬隔离（要动 `safety/`，留作后续）；enroll 暂不代种示例命令（同 knowledge）。
- **2026-06-09 M8-② 执行动效根因 + 方案（已定）**：用户反馈等待时界面像卡死。根因不是缺 spinner，而是 spinner 生命周期——每个 frame 到达即停、只有 `ToolStart` 重启，命令静默期与轮次间思考期没有任何动画。raw 路径（真实交互）选**根因修法**：`drainRaw` 改用 select 内 `time.Ticker` 同步渲染状态行，与小窗口同一 goroutine 绘制，彻底避开「异步 spinner 与小窗口光标重绘竞争」（这正是原设计每帧停 spinner 的原因）；去抖 150ms 防流式出字时闪烁，`replyOpen` 期间不画防 `\r` 抹掉半句回复（复查时抓到的 bug）。cooked 路径（非 TTY 兜底，plain 模式无光标竞争）保留异步 spinner，仅做最小生命周期修复（`ToolOutput` 后重启）。状态行带「已等秒数」让静默也明显在走。零新依赖（stdlib `time`）。
- **2026-06-09 M8-③ 命令目录可发现 + Tab 补全（已定）**：M8-① live 验收暴露可发现性缺口——文件放进 daemon 的 `/var/lib/opsagent/commands/`，但裸 `ops` 进程内会话读的是登录用户 `defaultStateDir()` 下的 `commands`，操作者/模型无从得知应答 agent 实际读哪个目录（两条路径见「2026-06-04 一机一脑」）。定：不去强行合并两条路径的目录（作用域隔离是有意的），而是**把真实目录暴露出来**——`/commands` 报 `CommandListReply.Dir`，空态+列表态都打印绝对路径，错位立刻可见可纠。Tab 补全：候选实时拉（含自定义命令），多匹配走 readline 风格「补公共前缀 + 列候选」、不做循环选中；只在 raw 模式做（cooked 逐行读拦不到 Tab）。**流程反馈（用户提出，已记忆）**：今后接到新功能先商讨方案 → 写进 TASK.md 立项 → 再按 Task 执行。
- **2026-06-04 模型档案 Phase 2：远端真源（已做）**：让 enrolled 远端的档案选择重启后保持。`buildSystemdUnit` **去掉** `OPSAGENT_PROVIDER/MODEL/BASE_URL`（只留 `OPSAGENT_STATE_DIR` + 可选 `PATROL_SERVICES`/`DIAG_MODEL`——后两者属部署期运维设置，不是面板管的活跃模型）。`buildBootstrap` 把 `key set api_key` 换成新的内部命令 `ops _seed --provider/--model/--base-url`（key 仍 base64 经 stdin），以服务用户身份把首个档案写进 daemon 的 `config.json` + seal key（`cli.Seed` → `saveModelProfile`）。`_seed` 加进 `cmd/ops` 分发。**幂等**：抽 `config.UpsertProfile`（provider+model+base_url 命中则就地 reseal+激活、否则新增），enroll 重跑/面板「新增同款」都不再产生重复档案——**换 key = 新增同 provider/模型填新 key**（`agent.modelAdd` 也改用 UpsertProfile，回「已更新并切换」）。档案 key 一律 per-profile（`model.<id>.key`），不再有固定 `api_key`（迁移的 default 档案除外）。`enroll_test` 断言更新（unit 不含 provider/model、bootstrap 含 `_seed`），新增 `TestSeedIdempotent`。**迁移**：v0.0.15 及更早 enroll 的老机 unit 仍钉 env、覆盖 config.json，需用 v0.0.16+ 重跑一次 enroll。文档全扫（README(.en) §换 key 重写、ONBOARDING §5、ARCHITECTURE）。**待 live 验收**：远端 `/model` 切换/新增重启后保持、老机重 enroll 迁移。
