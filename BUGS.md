# BUGS.md — 已知未处理问题

> 状态：已知未处理（非 blocking，留作后续）。
> 新发现直接追加。修复后在条目上标 ✅ 并注明 commit。

---

## BUG-001 tools 包无测试

**位置**：`internal/tools/`（shell.go, fileio.go）  
**现象**：shell 执行器和文件读写工具是系统操作的实际执行点，但没有 `*_test.go`。  
**风险**：中——超时行为、stderr 捕获、文件权限边界未经自动验证。  
**状态**：已知未处理

---

## BUG-002 RunLocal 全链路无自动化测试

**位置**：`cli/local.go` → `agent.LocalSession`  
**现象**：本地进程内对话（`ops` 无参）的完整路径（真模型 + 终端输入输出）没有集成测试。靠 `loop_test.go` 的 `net.Pipe` 间接覆盖 engine/handle 路径，但 `RunLocal` 的 glue 层（configured 判断 → onboard → pipe → session）未被覆盖。  
**风险**：低——glue 代码简单；local_test.go 覆盖了 `configured()` 和 `persistLocalConfig`。  
**状态**：已知未处理（TASK.md 也有标注）

---

## BUG-003 patrol 满盘多次触发重复 todo（M5 遗留）

**位置**：`internal/agent/patrol.go`  
**现象**：M5 实现中，disk/load 超阈每个 tick 都会尝试写 todo。M6-B 加了 `OpenTodoExists` 去重，但该逻辑依赖标题精确匹配，若标题格式变化会失效。  
**风险**：低（去重已加），但边界未经 live 验证。  
**状态**：已知，M6-B 缓解，待 live 验收

---

## BUG-004 cmd/ops 无测试

**位置**：`cmd/ops/main.go`  
**现象**：入口文件的子命令分发逻辑（flag 解析、错误路径、`resolveSocket`）没有测试。  
**风险**：低——逻辑简单，属薄 glue。  
**状态**：已知未处理

---

## BUG-005 Anthropic extended thinking 未实现

**位置**：`internal/model/anthropic.go`  
**现象**：OpenAI 路径已支持 `reasoning_content` 回放（`deepseek-v4-pro` live 验证通过），但 Anthropic 的 extended thinking（thinking block 机制完全不同）未做。  
**风险**：低——当前未用 Claude 推理模型。  
**状态**：已知未处理，TASK.md 跨里程碑待办有标注

---

## BUG-006 M5/M6 未经 live 验收

**位置**：巡检自愈（M5）、多模型诊断/fan-out（M6）  
**现象**：离线验收（本机测试）通过，但 live 验收（真 Linux + 真模型 + 真 SSH）尚未跑通。  
**风险**：中——patrol 自动重启和 fan-out 确认策略的实际行为未经真实环境验证。  
**状态**：已知，待 live 验收（TASK.md M5/M6 节有完整描述）
