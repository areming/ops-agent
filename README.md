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

## 构建

```powershell
./build.ps1
```

交叉编译到 `./dist/opsagent-{linux-amd64,linux-arm64,windows-amd64}`，每个都是单静态二进制（`file dist/opsagent-linux-amd64` 显示 `statically linked`）。

## 部署

**最简方式——引导式向导**（推荐首次使用）：

```bash
opsagent setup
```

它会一步步问你 provider / 模型 / 目标主机，**自动检查 SSH 与免密 sudo**（不通会给出修复提示），然后部署并验证服务起来了。全程只回答问题，不用记 flag。

或者手动一条命令部署：

```bash
# API key 从 stdin 读，不进 shell history / 进程列表
echo "$DEEPSEEK_KEY" | opsagent enroll web1 --provider deepseek --model deepseek-chat
```

`enroll` 会经 SSH：探测目标机架构 → scp 对应二进制 → 跑一段幂等的特权 bootstrap，完成：

- 建系统用户 `opsagent`（`--user` 可改）、装二进制到 `/usr/local/bin/opsagent`；
- 写 sudoers 白名单（`visudo` 校验，仅 systemctl/journalctl NOPASSWD）；
- 写 systemd unit（API key **不**进 unit，只进加密 keystore）；
- base64 经管道把 key 存进 keystore（不落远端磁盘）；
- 把你的登录用户加入 `opsagent` 组（便于 `connect`）、`enable --now` 起服务。

完成后即可 `opsagent connect web1`（首次若被拒，重新登录让新组生效）。

主要 `enroll` flags：`--provider`（默认 `deepseek`）、`--model`、`--base-url`、`--user`（默认 `opsagent`）、`--bin`（默认 `dist/opsagent-linux-<arch>`）。

## 使用

```bash
opsagent connect <host>                       # 开一段对话（SSH）
opsagent run -c "<指令>" <host>... [--yes]    # 批量：一条指令并发跑多台
opsagent logs [-n N]                          # 审计轨迹（含 source: chat/patrol）
opsagent todos                                # 巡检/自愈待办
opsagent key set <name>                       # 存密钥（值从 stdin 读）
opsagent key list
```

巡检与 fan-out 的细节（边界、默认安全、验证脚本）见 [ONBOARDING.md](ONBOARDING.md) §6/§7。

## 配置

目前走环境变量（TOML 配置后置）。常用：

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

## 开发

```bash
go test ./...        # 全部测试
go vet ./...
gofmt -l internal/ cmd/
```

任务流程与决策记录见 `TASK.md`（唯一执行事实来源）；设计文档见 `REQUIREMENTS.md` / `TECH_STACK.md` / `ARCHITECTURE.md` / `ROADMAP.md`；上手与内部结构见 [ONBOARDING.md](ONBOARDING.md)。
