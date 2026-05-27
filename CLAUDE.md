# CLAUDE.md — ops-agent

## 项目简介

轻量级运维助手。纯 Go 静态二进制，常驻服务器当"大脑"，CLI 经 SSH 对话来管服务器。

**关键约束**：`CGO_ENABLED=0`，`GOAMD64` 不高于默认 v1，不引需 CGO 或现代 CPU 指令的依赖。目标机是老 Ubuntu，CPU 可能很旧。

## 技术栈

- Go 1.25，单二进制，无运行时依赖
- 三方依赖（仅这三个）：`modernc.org/sqlite`、`golang.org/x/crypto`、`golang.org/x/term`
- 模型 API：自实现 HTTP+SSE，无 SDK

## 目录结构

```
cmd/ops/           入口，子命令分发，无业务逻辑
internal/
  agent/           daemon、session、agent loop、patrol（核心）
  cli/             客户端 REPL、enroll、setup、fanout、local 对话入口
  transport/       Frame 协议、unix socket、SSH stdio 桥
  model/           Provider 接口 + OpenAI/Anthropic/DeepSeek 适配
  tools/           Tool 接口、shell 执行器、文件读写
  safety/          规则黑名单、只读白名单、巡检自愈白名单
  memory/          SQLite store（会话/审计/待办/巡检）+ 知识档案
  secret/          NaCl secretbox keystore
  config/          配置加载（env > config.json > 默认）
  version/         版本字符串（ldflags 刻入）
```

## 构建与测试

```bash
go test ./...
go vet ./...
gofmt -l internal/ cmd/
./build.ps1      # 交叉编译 linux-amd64 / linux-arm64 / windows-amd64
```

## 当前执行状态

M7（ops 易用性重构）5 步代码全部完成，待分步 git commit + push，然后 live 验收。  
详见 `TASK.md`（唯一执行事实来源）。已知问题见 `BUGS.md`。

## 接手项目特别说明

中途接手，对历史决策理解有限：

1. 看似不合理的代码先假设有理由——查 git blame / TASK.md 决策日志 / 测试
2. >50 行重构先讨论
3. 不降低测试覆盖
4. 未知决策标"待确认"，不替原作者拍板
5. 发现 docs/ARCHITECTURE.md 不准立即更新

## 红线

- `internal/tools/` 无测试——改动 shell/fileio 要同时加测试
- 不能提升 `GOAMD64`，不能引 CGO 依赖
- 安全闸门（`internal/safety/`）改动必须有对应测试用例
- 所有写操作必须落 audit 表（`memory.Store.Audit()`）
