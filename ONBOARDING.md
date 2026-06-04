# ops-agent 上手指南

轻量级运维助手：一个纯 Go 静态二进制 `ops`，常驻在服务器上当“大脑”（systemd 服务名 `opsagent`），你从本地用 CLI 经 SSH 跟它对话来管服务器。它能跑命令、读写文件（带安全闸门），还能后台巡检自愈。

> 设计细节见 `REQUIREMENTS.md` / `TECH_STACK.md` / `ARCHITECTURE.md` / `ROADMAP.md`；执行状态与决策日志见 `TASK.md`（唯一事实来源）。

---

## 1. 它怎么搭起来的

```
你的本地 CLI ──SSH──> 服务器上的 opsagent daemon ──出站 HTTPS──> 模型 API
                          (unix socket，不开任何网络端口)
```

- **单二进制**：`serve` 在服务器常驻，`connect`/`run` 是本地客户端，二者经 SSH 隧道连本地 unix socket。agent 不监听任何网络端口（攻击面最小）。
- **运行身份**：部署后以专用用户 `opsagent` 跑，提权走自动生成的 sudo 白名单（仅 `systemctl`/`journalctl`）。
- **部署约束**：服务器是老 Ubuntu、CPU 可能很旧 → 坚持 `CGO_ENABLED=0` 纯 Go 静态二进制、不引需 CGO 或现代指令集的依赖。

**部署前置（SSH）**：opsagent 用本地 `ssh`/`scp` 操作目标机，部署前要 ① 能免密 `ssh <host>`（带 passphrase 的私钥先 `ssh-add` 进 ssh-agent；Windows 跑 `install.ps1` 启用 agent）；② 内网机经跳板机时在 `~/.ssh/config` 配 `ProxyJump`，用别名做 host；③ 目标机 SSH 用户能免密 sudo（enroll 用 `sudo -n`）；④ 目标机能出站 HTTPS 到模型 API。完整步骤见 README「前置：SSH 准备」。

## 2. 构建

```powershell
./build.ps1        # 交叉编译到 ./dist/ops-{linux-amd64,linux-arm64,windows-amd64}
./install.ps1      # 可选：把 ops 放进 PATH + 启用 ssh-agent（Windows 本地安装）
```

每个产物都是单静态二进制（`file dist/ops-linux-amd64` 应显示 `statically linked`）。

## 3. 仓库布局

```
cmd/ops/               入口 + 子命令分发
internal/
  transport/           Frame 协议、unix socket、SSH 桥接
  model/               模型 Provider 抽象（OpenAI 兼容 / DeepSeek / Anthropic）
  agent/               daemon、对话/工具循环（engine + interaction）、巡检（patrol）
  tools/               执行器：shell、文件读写
  safety/              安全闸门：规则黑名单 + 只读白名单 + 模型自评
  memory/              SQLite（audit / messages / todos / patrol_runs）+ 知识档案
  secret/              API key 加密存取（keystore）
  config/              环境变量配置
  cli/                 本地客户端（connect 交互、fan-out）
```

## 4. 常用命令

```
ops                                         在已部署机器上接管本机常驻 agent；未部署则本地对话（未配置先引导）
ops setup                                   引导式向导：问答 + 自动前置检查 + 部署（推荐首次用）
ops enroll <host> [flags]                   一条命令部署到 Linux 机（API key 从 stdin 读）
ops connect <host>                          从本地开一段对话（SSH）
ops connect --local <socket>                直连指定本地 socket（开发用；服务器上日常直接敲 ops 即可）
ops run -c "<指令>" <host>... [--yes]       批量：一条指令并发跑多台（见 §6）
ops serve [--socket PATH]                   常驻 daemon
ops key set <name>                          存密钥（值从 stdin 读，不进 shell history）
ops key list
ops logs [-n N]                             审计轨迹（含 source: chat/patrol）
ops todos                                   待办（巡检自愈遇高危/不可逆时记这）
```

## 5. 配置（环境变量 + config.json）

配置优先级 **环境变量 > `config.json`（StateDir 下）> 内置默认**。模型选择会在引导或 `/model` 切换时落进 `config.json`，systemd 注入的 `OPSAGENT_*` 仍优先。引导（本地 `ops` / `ops setup`）会**上下键**列出主流 provider（DeepSeek / OpenAI / Anthropic / Moonshot / Qwen / z.ai / Gemini / Grok 等）并预填各家 base URL，模型也是列表选择 + Custom 手填；第三方 OpenAI 兼容商底层统一记 `provider=openai` + 对应 base URL，所以下表的 provider 取值仍是这三个 adapter：

| 变量 | 默认 | 说明 |
|---|---|---|
| `OPSAGENT_PROVIDER` | `openai` | `openai` / `deepseek` / `anthropic` |
| `OPSAGENT_MODEL` | — | 模型名 |
| `OPSAGENT_API_KEY` | — | 明文覆盖；留空则从加密 keystore 取 `api_key` |
| `OPSAGENT_BASE_URL` | — | API base 覆盖 |
| `OPSAGENT_DIAG_PROVIDER/_MODEL/_BASE_URL` | 回退主模型 | 巡检诊断专用模型（见 §7） |

巡检（见 §7）：`OPSAGENT_PATROL`（默认开）、`OPSAGENT_PATROL_INTERVAL`（`5m`）、`OPSAGENT_PATROL_CHECKS`（`disk,load,key_services`）、`OPSAGENT_PATROL_SERVICES`（默认空）、`OPSAGENT_PATROL_DISK_PCT`（`90`）、`OPSAGENT_PATROL_LOAD`（`2.0`/核）。

---

## 6. Fan-out：一条指令跑多台

非交互地把同一条指令并发下发到多台主机，每台跑完成组打印结果，末尾给汇总。

```
ops run -c "df -h / 并总结磁盘占用" web1 web2 db1
ops run -c "重启 nginx" web1 web2 --yes
```

要点：

- **非交互**：因为没人能逐台答确认，安全闸门判为“需确认”的写操作**默认被拒绝**，并在该主机标记 `[needs attention]`；只有自动放行的只读/白名单动作会真正执行。
- **`--yes`**：显式 opt-in，对所有需确认动作一律批准。**危险**，只在你清楚这批操作安全时用。
- **并发有界**（默认 5 台同时），单台失败被隔离、不影响其它主机；末尾汇总 `N ok / N need attention / N failed`。
- 复用 `connect` 的 SSH-bridge 搭建（`internal/cli/client.go` 的 `sshBridge`）；核心非交互逻辑在 `internal/cli/fanout.go` 的 `runOneTurn`。

## 7. Patrol：后台巡检 + 自愈

agent 常驻期间后台跑一个巡检 goroutine（CLI 不开也跑），定时检查、按安全边界自愈或记待办。

### 检查集

每个 check 跑**只读**命令再用纯函数解析（`internal/agent/patrol.go`）：

- `disk`：`df -P` → 任一挂载点使用率 ≥ `OPSAGENT_PATROL_DISK_PCT`（默认 90%）即异常。
- `load`：`cat /proc/loadavg` + `nproc` → 1 分钟负载 / 核数 ≥ `OPSAGENT_PATROL_LOAD`（默认 2.0）即异常。
- `key_services`：对 `OPSAGENT_PATROL_SERVICES` 里每个 unit 跑 `systemctl is-active`，非 `active` 即异常。

检查集是插件化的：实现 `check` 接口（`name()` + `run(ctx, runner)`）并在 `buildChecks` 注册即可新增。

### 自愈边界（安全底线）

- **可逆 + 白名单 → 自动执行**：被监控 unit 挂了 → `sudo -n systemctl restart <unit>`，过窄白名单 `safety.IsPatrolAutoRemedy`（仅 `systemctl start/restart` + 已配置 unit + 不撞危险规则）才自动跑，留 `audit(source=patrol, decision=auto)`。
- **高危 / 不可逆 → 绝不自动执行**：一律跳过 + 写 `todos`（`ops todos` 可见），写操作另落 `decision=skipped` 审计。
- **默认安全**：`OPSAGENT_PATROL_SERVICES` 默认空 → 开箱只跑只读检查，在你显式列出 unit 前**不会自动重启任何东西**。

### 多模型诊断

对**没有自动修复**的异常（disk/load），巡检会用诊断模型（`OPSAGENT_DIAG_*`，未配则回退主模型）跑一次无连接的 agent loop：模型用只读命令调查、给出根因和建议，结果写进该待办的 suggested_action。模型若想做写操作，会被拒绝并提示它改成“建议”，绝不无人值守执行。同一持续问题靠 `OpenTodoExists` 按标题去重，只诊断一次，不每个周期刷屏。

### 验证巡检自愈（live）

```bash
# 在目标机上让 patrol 盯住某服务
export OPSAGENT_PATROL_SERVICES=nginx
# 重启 serve，然后停掉服务，等一个巡检周期
sudo systemctl stop nginx
ops logs                 # 应看到 patrol/auto 重启留痕
# 造一个 disk 超阈场景
ops todos                # 应看到带诊断分析的待办，且未自动动手
```

---

## 8. 安全闸门怎么判（共用于对话和巡检）

`safety.Classify` 取“规则黑名单”与“模型自评”中更谨慎者：

- 命中危险规则（`rm -rf`、`mkfs`、`dd of=`、`reboot`、`drop table` 等）→ 永远需确认，模型不能降级。
- 只读命令（含 `systemctl status`、只读 `journalctl`、管道里全只读）→ 自动放行。
- 其余写操作 → 需确认。对话路径问你 y/n；巡检路径除窄白名单外一律跳过写待办。

## 9. 上手做点什么

1. `./build.ps1` 出二进制。
2. 本地冒烟：设好 `OPSAGENT_*`，一个终端 `ops serve --socket /tmp/a.sock`，另一个 `ops connect --local /tmp/a.sock`。
3. 读 `TASK.md` 看当前进度、待办、以及每个决策“为什么这么做”。
4. 改巡检：看 `internal/agent/patrol.go`（检查/自愈/诊断）+ `internal/safety/rules.go`（白名单）。
5. 改批量：看 `internal/cli/fanout.go`。

> 任务流程见全局 / 项目 `CLAUDE.md`：超过 1 个文件的改动先 explore → plan → 等点头再 code；commit 用 Conventional Commits。
