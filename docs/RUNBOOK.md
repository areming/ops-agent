# RUNBOOK — ops-agent

## 构建

```powershell
# Windows（交叉编译 linux-amd64 / linux-arm64 / windows-amd64）
./build.ps1
# 输出在 dist/ops-linux-amd64, dist/ops-linux-arm64, dist/ops-windows-amd64.exe
```

版本号刻入：`build.ps1` 按 `$env:OPS_VERSION` > `git describe --tags` > `"dev"` 顺序取值。

## 本地开发运行（无 SSH）

```bash
# 1. 构建
go build -o ops ./cmd/ops

# 2. 启动 agent daemon（另一个终端）
OPSAGENT_PROVIDER=deepseek \
OPSAGENT_MODEL=deepseek-chat \
OPSAGENT_API_KEY=sk-xxx \
./ops serve --socket /tmp/opsagent.sock

# 3. 连接（同机器）
./ops connect --local /tmp/opsagent.sock
```

直接 `./ops`（无参）走 `RunLocal()`，先探测本机常驻 daemon 的 socket（`/run/opsagent/agent.sock`，仅 Linux）：

- **可连** → 接管该 daemon（复用其模型/记忆/巡检），等价于 `connect --local`。
- **unit 已装但没在跑** → 提示 `sudo systemctl start opsagent`，不重复引导。
- **无权访问 socket（EACCES）** → 提示重新登录使 `opsagent` 组生效（或 `sudo ops`），不重复引导。
- **没有常驻 daemon**（笔记本、未 enroll，或非 Linux）→ 走 `runLocalSession()`：进程内起 agent + `net.Pipe()` 连接，无需 serve；未配置时自动进入引导（`onboardLocal`），配置落 `~/.config/opsagent/config.json`（Linux/Mac）、`%AppData%\opsagent\config.json`（Windows）。

## 本地测试

```bash
go test ./...          # 全部测试
go vet ./...           # vet
gofmt -l internal/ cmd/   # 格式检查（有输出=需格式化）
```

无 CGO 依赖，不需要特殊环境变量。

## 交叉编译验证

```powershell
./build.ps1
# 验证静态链接（需 Linux 或 WSL）：
# file dist/ops-linux-amd64   → 应显示 statically linked
```

## 首次部署到远程机器

前置条件（README §SSH准备 已覆盖，关键点）：

1. `ssh <host>` 免密可达（密钥认证，passphrase 的需 `ssh-add`）
2. 目标机 SSH 用户能免密 sudo（`sudo -n whoami` 无密码返回）
3. 目标机能出站 HTTPS 到模型 API

```bash
# 推荐：引导向导
ops setup

# 或手动一步完成
echo "$DEEPSEEK_KEY" | ops enroll web1 --provider deepseek --model deepseek-chat

# 连接验证
ops connect web1
```

**README 没说但实际需要做的事**：

- Windows 上运行 `install.ps1` 会启用 ssh-agent 服务并把 `ops` 放进 PATH，之后才能免密 SSH（不装直接用会报 agent 错误）。
- 第一次 `enroll` 后需重新登录（或 `newgrp opsagent`）让新增的 `opsagent` 组生效，之后在该机器上直接敲 `ops`（或 `connect --local`）即可连上常驻 agent。
- `ops connect <host>` 如果目标机没有 `ops` 二进制，会提示是否自动安装——选是会从 GitHub Release 拉对应版本（版本是 `"dev"` 时不触发远程拉取，需先 `build.ps1`）。
- 跳板机场景必须先配好 `~/.ssh/config` ProxyJump，`ops` 本身不处理跳板逻辑（完全委托给本地 `ssh` 命令）。

## 常用运维命令

```bash
ops logs -n 30         # 查最近 30 条审计日志
ops todos              # 查巡检产生的待办
ops key set api_key    # 更换模型 API key（值从 stdin 读）
ops key list           # 列已存密钥名
ops run -c "df -h" host1 host2   # 批量只读查询
ops version            # 查当前二进制版本
ops uninstall          # 卸载（保留数据）
ops uninstall --purge  # 干净全量卸载：删二进制 + 全部数据（密钥库/审计·会话库/知识档案）；enrolled 机另删 /var/lib/opsagent 与 opsagent 用户。执行前需手输 yes 确认
```

## 环境变量速查

| 变量 | 说明 |
|---|---|
| `OPSAGENT_PROVIDER` | `openai` / `deepseek` / `anthropic` |
| `OPSAGENT_MODEL` | 模型名 |
| `OPSAGENT_API_KEY` | 明文覆盖（dev 用，生产走 keystore） |
| `OPSAGENT_BASE_URL` | API base 覆盖 |
| `OPSAGENT_PATROL` | `true`/`false`，默认开 |
| `OPSAGENT_PATROL_SERVICES` | 逗号分隔 unit 名，**空则不自动重启任何东西** |
| `OPSAGENT_PATROL_INTERVAL` | 默认 `5m` |

完整列表见 README.md §配置。
