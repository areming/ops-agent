package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/areming/ops-agent/internal/version"
)

// Setup walks the operator through deploying the agent to one host: it
// collects the model settings and target, checks SSH and passwordless sudo
// (with fix hints), runs enroll, and verifies the service started. It only
// drives the existing pieces; no deployment logic lives here.
func Setup() error {
	wizardTitle("引导部署", "把 ops 部署到一台 Linux 服务器")
	r := bufio.NewReader(os.Stdin)
	host, err := prompt(r, "目标主机 (ssh host)", "")
	if err != nil {
		return err
	}
	if host == "" {
		return fmt.Errorf("未填主机，已取消")
	}
	return setupHost(r, host)
}

// SetupHost runs the deploy wizard for an already-known host. It is used when
// `connect <host>` finds the agent is not installed there yet.
func SetupHost(host string) error {
	wizardTitle("引导部署", "把 ops 部署到 "+host)
	return setupHost(bufio.NewReader(os.Stdin), host)
}

// setupHost collects model settings and the API key, checks SSH/sudo, then
// enrolls the host and verifies the service started.
func setupHost(r *bufio.Reader, host string) error {
	entry, modelName, baseURL, err := collectModel(r)
	if err != nil {
		return err
	}
	user, err := prompt(r, "服务运行用户", "opsagent")
	if err != nil {
		return err
	}
	services, err := prompt(r, "巡检监控并自动重启哪些服务（逗号分隔，回车跳过）", "")
	if err != nil {
		return err
	}
	diagModel, err := prompt(r, "诊断模型（回车=用主模型）", "")
	if err != nil {
		return err
	}

	if err := preflightWithRetry(r, host); err != nil {
		return err
	}

	apiKey, err := promptAPIKey(r)
	if err != nil {
		return err
	}
	if apiKey == "" {
		return fmt.Errorf("空 key，已取消")
	}

	fmt.Print("\n" + setupSummary(host, user, entry.Label, modelName, baseURL, services, diagModel))
	confirm, err := prompt(r, "确认部署? [Y/n]", "Y")
	if err != nil {
		return err
	}
	if !isYes(confirm) {
		return fmt.Errorf("已取消")
	}

	if err := Enroll(host, EnrollOptions{
		User:      user,
		Provider:  entry.Adapter,
		Model:     modelName,
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Services:  services,
		DiagModel: diagModel,
		Version:   version.Value,
	}); err != nil {
		return err
	}

	fmt.Println("→ 验证服务状态...")
	if err := sshCheck(host, "systemctl is-active opsagent"); err != nil {
		fmt.Printf("  ⚠ 服务可能未就绪，查日志：ssh %s journalctl -u opsagent -n 50\n", host)
	} else {
		fmt.Println("  ✓ opsagent 服务运行中")
	}

	fmt.Printf("\n✓ 完成。开始对话：\n    ops connect %s\n", host)
	fmt.Println("  （若 connect 被拒，重新登录一次 SSH 让 opsagent 组生效）")
	return nil
}

// preflightWithRetry runs the SSH/sudo checks, letting the user fix and
// retry rather than aborting on the common passwordless-sudo gap.
func preflightWithRetry(r *bufio.Reader, host string) error {
	for {
		if err := preflight(host); err != nil {
			fmt.Printf("\n%v\n\n", err)
			again, perr := prompt(r, "修复后回车重试，或输入 q 放弃", "")
			if perr != nil {
				return perr
			}
			if strings.EqualFold(again, "q") {
				return fmt.Errorf("已取消")
			}
			continue
		}
		return nil
	}
}

// preflight verifies the two hard prerequisites for enroll and explains how
// to fix the usual failure.
func preflight(host string) error {
	fmt.Printf("→ 检查 SSH 连接 (%s)...\n", host)
	if err := sshCheck(host, "echo ok"); err != nil {
		return fmt.Errorf("SSH 连不上 %s（先确认你能 `ssh %s` 登录、host 别名/密钥配好）: %w", host, host, err)
	}
	fmt.Println("  ✓ SSH 可连")

	fmt.Println("→ 检查免密 sudo...")
	if err := sshCheck(host, "sudo -n true"); err != nil {
		return fmt.Errorf("SSH 用户不能免密 sudo（enroll 需要）。\n"+
			"  在 %s 上 `sudo visudo` 加一行（<用户>换成你的登录名，验收后可收回）：\n"+
			"    <用户> ALL=(ALL) NOPASSWD:ALL\n"+
			"  原始错误: %w", host, err)
	}
	fmt.Println("  ✓ 免密 sudo 可用")
	return nil
}

// sshCheck runs a command on host over SSH, surfacing its stderr.
func sshCheck(host, remoteCmd string) error {
	cmd := exec.Command("ssh", host, remoteCmd)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// prompt prints label (with an optional default) and reads one line; an
// empty line returns def.
func prompt(r *bufio.Reader, label, def string) (string, error) {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "y", "yes":
		return true
	}
	return false
}

// setupSummary renders the deployment plan for confirmation.
func setupSummary(host, user, provider, model, baseURL, services, diagModel string) string {
	var b strings.Builder
	b.WriteString("即将部署：\n")
	fmt.Fprintf(&b, "  主机:     %s\n", host)
	fmt.Fprintf(&b, "  运行用户: %s\n", user)
	fmt.Fprintf(&b, "  provider: %s\n", provider)
	fmt.Fprintf(&b, "  模型:     %s\n", model)
	if baseURL != "" {
		fmt.Fprintf(&b, "  base URL: %s\n", baseURL)
	}
	if services != "" {
		fmt.Fprintf(&b, "  巡检服务: %s\n", services)
	}
	if diagModel != "" {
		fmt.Fprintf(&b, "  诊断模型: %s\n", diagModel)
	}
	return b.String()
}
