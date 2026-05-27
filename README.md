# ops-agent

中文 | [English](README.en.md)

轻量级运维助手：一个纯 Go 静态二进制，常驻服务器当“大脑”，你从本地用 CLI 经 SSH 跟它对话来管服务器——跑命令、读写文件（带安全闸门），还能后台巡检自愈、批量下发指令。

为“服务器是老 Ubuntu、CPU 可能很旧”的场景而设计：`CGO_ENABLED=0` 纯 Go 单静态二进制，无运行时依赖，一条命令部署。

---

## 功能特性

- **对话式运维**：自然语言 → 模型生成命令 → 安全闸门分类 → 确认/放行 → 执行 → 结果回灌续问。
- **安全闸门**：规则黑名单（`rm -rf`、`mkfs`、`dd of=`、`reboot`、`drop table` 等）+ 只读命令白名单 + 模型自评，取更谨慎者；危险操作模型无法降级放行。
- **后台巡检自愈**：定时检查 `disk` / `load` / `key_services`；被监控服务挂掉 → 走窄白名单自动重启并留痕；高危/不可逆 → 跳过并记待办，绝不擅自执行。
- **多模型诊断**：对无法自动修复的异常（磁盘/负载），用诊断模型只读调查、给出根因与建议，写进待办。
- **批量 fan-out**：一条指令并发跑多台主机，非交互（默认拒绝需确认的写操作）。
- **加密 keystore**：API key 用 secretbox 加密落盘，配置/环境/进程列表里无明文。
- **单二进制部署**：`enroll <host>` 一条命令完成建用户、装服务、配 sudo 白名单、供给密钥、起 systemd。
- **零网络端口**：agent 不监听任何端口，CLI 经 SSH 隧道连本地 unix socket，攻击面最小。

## 架构

```
本地 CLI ──SSH──> 服务器上的 opsagent daemon ──出站 HTTPS──> 模型 API
                    (unix socket，无网络端口)
```

agent 以专用用户 `opsagent` 运行，提权走自动生成的 sudo 白名单（仅 `systemctl`/`journalctl`）。更细的内部结构见 [ONBOARDING.md](ONBOARDING.md)。

## 环境要求

- **构建**：Go 1.25+。
- **运行（目标机）**：Linux amd64 或 arm64；无其它运行时依赖（单静态二进制）。systemd（部署用）。
- **部署前提**：对目标机有 SSH 访问，且 SSH 用户能免密 sudo（NOPASSWD）或本就是 root（`enroll` 用 `sudo -n`，需要口令会立即清晰报错）。
- **模型**：一个模型 API key（DeepSeek / OpenAI 兼容 / Anthropic 任一）。

## 安装

在**你自己的机器**上安装 `ops` CLI（用来管理远程服务器）。

### Linux（amd64 / arm64）

一行命令下载最新 release、校验 sha256、装到 `/usr/local/bin/ops`：

```sh
curl -fsSL https://raw.githubusercontent.com/areming/ops-agent/main/install.sh | sudo sh
```

指定版本：

```sh
curl -fsSL https://raw.githubusercontent.com/areming/ops-agent/main/install.sh | sudo OPS_VERSION=v0.0.1 sh
```

装完直接运行 `ops`，首次会引导你选模型 provider 并填入 API key，然后进入对话。

### Windows

先从源码构建，再一键安装（把 `ops` 放进 PATH + 启用 ssh-agent）：

```powershell
./build.ps1
./install.ps1
```

装完打开新终端，加载 SSH 私钥，然后就可以用了：

```powershell
ssh-add $env:USERPROFILE\.ssh\id_ed25519
ops
```

## 构建（从源码）

需要 Go 1.25+，交叉编译到 `./dist/ops-{linux-amd64,linux-arm64,windows-amd64}`：

```powershell
./build.ps1
```

## 部署

### 前置：SSH 准备

ops 用你本地的 `ssh`/`scp` 操作目标机，所以部署前先确保：

1. **能免密 `ssh <host>`**（密钥认证）。私钥带 passphrase 的，先加载进 ssh-agent 避免反复输密码：
   - Windows：`./install.ps1`（会启用 ssh-agent），再 `ssh-add $env:USERPROFILE\.ssh\id_ed25519`。
   - macOS/Linux：`ssh-add ~/.ssh/id_ed25519`。
2. **目标机在跳板机后面**（内网机只能经一台外网机到达）：用 ProxyJump 在 `~/.ssh/config` 配好别名，之后 enroll/connect 直接用别名：
   ```sshconfig
   Host gw
       HostName <跳板机外网IP>
       User <跳板机用户>
   Host vps
       HostName <内网IP>
       User <内网机用户>
       ProxyJump gw
   ```
   验证：`ssh vps "echo ok"` 应免密直接返回。
3. **目标机的 SSH 用户能免密 sudo**（enroll 用 `sudo -n`）：不行就在目标机 `sudo visudo` 加 `<用户> ALL=(ALL) NOPASSWD:ALL`（验收后可收窄）。
4. **目标机能出站 HTTPS** 到模型 API（agent 运行时要调它）。

### 部署

**最简方式——引导式向导**（推荐首次使用）：

```bash
ops setup
```

它会一步步问你 provider / 模型 / 目标主机（还可顺带配「巡检监控并自动重启哪些服务」「诊断模型」），**自动检查 SSH 与免密 sudo**（不通会给出修复提示），然后部署并验证服务起来了。全程只回答问题，不用记 flag。

或者手动一条命令部署：

```bash
# API key 从 stdin 读，不进 shell history / 进程列表
echo "$DEEPSEEK_KEY" | ops enroll web1 --provider deepseek --model deepseek-chat
```

可选 flag：`--services nginx,sshd`（巡检监控并自动重启这些服务）、`--diag-model <模型>`（诊断用模型，复用主 provider/key）、`--user`、`--base-url`、`--bin`。

`enroll` 会经 SSH：探测目标机架构 → scp 对应二进制 → 跑一段幂等的特权 bootstrap，完成：

- 建系统用户 `opsagent`（`--user` 可改）、装二进制到 `/usr/local/bin/ops`（并建 `opsagent` 软链兼容旧名）；
- 写 sudoers 白名单（`visudo` 校验，仅 systemctl/journalctl NOPASSWD）；
- 写 systemd unit（API key **不**进 unit，只进加密 keystore）；
- base64 经管道把 key 存进 keystore（不落远端磁盘）；
- 把你的登录用户加入 `opsagent` 组（便于 `connect`）、`enable --now` 起服务。

完成后即可 `ops connect web1`（首次若被拒，重新登录让新组生效）。

主要 `enroll` flags：`--provider`（默认 `deepseek`）、`--model`、`--base-url`、`--user`（默认 `opsagent`）、`--bin`（默认 `dist/ops-linux-<arch>`）。

## 使用

```bash
ops                                      # 本地对话（未配置则先引导）；对话内 /help 看命令
ops connect <host>                       # 从本地开一段对话（SSH）
ops connect --local /run/opsagent/agent.sock  # 在服务器上直接对话（无需 SSH；用户需在 opsagent 组）
ops run -c "<指令>" <host>... [--yes]    # 批量：一条指令并发跑多台
ops logs [-n N]                          # 审计轨迹（含 source: chat/patrol）
ops todos                                # 巡检/自愈待办
ops key set <name>                       # 存密钥（值从 stdin 读）
ops key list
```

对话里支持 `/命令`（对齐常见 CLI）：`/models [名称]` 看/切当前会话所连机器的模型、`/logs [N]` 看操作日志、`/clear` 清空当前对话、`/help`、`/quit`。

巡检与 fan-out 的细节（边界、默认安全、验证脚本）见 [ONBOARDING.md](ONBOARDING.md) §6/§7。

## 配置

配置优先级 **环境变量 > `config.json`（StateDir 下）> 内置默认**。模型选择（provider/model/base_url + 诊断三件）会在引导或 `/models` 切换时落进 `config.json`；API key **不**进配置，单独加密存在 keystore（条目名固定 `api_key`）。常用环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `OPSAGENT_PROVIDER` | `openai` | `openai` / `deepseek` / `anthropic` |
| `OPSAGENT_MODEL` | — | 模型名 |
| `OPSAGENT_API_KEY` | — | 明文覆盖；留空则从加密 keystore 取 |
| `OPSAGENT_BASE_URL` | — | API base 覆盖 |
| `OPSAGENT_DIAG_PROVIDER/_MODEL/_BASE_URL` | 回退主模型 | 巡检诊断专用模型 |
| `OPSAGENT_PATROL` | `true` | 是否开启后台巡检 |
| `OPSAGENT_PATROL_INTERVAL` | `5m` | 巡检周期 |
| `OPSAGENT_PATROL_CHECKS` | `disk,load,key_services` | 启用的检查 |
| `OPSAGENT_PATROL_SERVICES` | （空） | 巡检监控并可自动重启的 unit；**空则不会自动重启任何东西** |
| `OPSAGENT_PATROL_DISK_PCT` | `90` | 磁盘使用率告警阈值 |
| `OPSAGENT_PATROL_LOAD` | `2.0` | 每核 1 分钟负载告警阈值 |

完整列表（含 state/db/knowledge 路径等）见 [ONBOARDING.md](ONBOARDING.md) §5。

### 换 key / 换厂商

ops 同一时刻只有**一个活跃 provider**（诊断模型默认复用它的 key），key 在 keystore 里固定存为 `api_key`。

**① key 失效（过期/欠费/吊销），厂商不变**

本地（`ops` 在你自己机器上）：

```bash
echo "$NEW_KEY" | ops key set api_key    # 或 ops key set api_key 后粘贴、Ctrl-D
```

下次 `ops` 即生效（本地每次运行都是新进程）。

远程（enroll 过的机器，跑在 systemd 下）——运行中的 daemon 启动时已读 key，换完要重启重载。最省事是从本地重跑 enroll（幂等，自动换 key + 重启）：

```bash
echo "$NEW_KEY" | ops enroll <host> --provider <原 provider> --model <原 model>
```

或上机手动：

```bash
sudo runuser -u opsagent -- env OPSAGENT_STATE_DIR=/var/lib/opsagent ops key set api_key
sudo systemctl restart opsagent
```

**② 换厂商 / 加新厂商**

对话内 `/models <名称>` **只能在当前 provider 内换模型**，改不了 provider。换厂商要同时改 provider + base_url + key：

本地：删掉 `config.json` 让下次 `ops` 重走引导，或手动改 `config.json` 的 `provider`/`model`/`base_url` 再 `ops key set api_key` 填新 key。本地配置目录：Windows `%AppData%\opsagent\`、macOS/Linux `~/.config/opsagent/`（含 `config.json` + `keystore.json`）。

远程：直接用新厂商重跑 enroll，它会重写 unit 环境变量 + 换 key + 重启：

```bash
echo "$ANTHROPIC_KEY" | ops enroll <host> --provider anthropic --model claude-... [--base-url ...]
```

> **注（enroll 过的机器）**：enroll 把 `OPSAGENT_PROVIDER`（以及给了 `--model` 时的 `OPSAGENT_MODEL`）写进 systemd unit 的 `Environment=`，按上面的优先级会**盖过 `config.json`**。所以远程换 provider 靠改 `config.json` 无效，必须重跑 enroll（或手动改 unit 后 `daemon-reload`）；同理若 unit 钉了 `OPSAGENT_MODEL`，对话内 `/models` 的切换重启后可能被还原。

## 开发

```bash
go test ./...        # 全部测试
go vet ./...
gofmt -l internal/ cmd/
```

任务流程与决策记录见 `TASK.md`（唯一执行事实来源）；设计文档见 `REQUIREMENTS.md` / `TECH_STACK.md` / `ARCHITECTURE.md` / `ROADMAP.md`；上手与内部结构见 [ONBOARDING.md](ONBOARDING.md)。
