package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/term"
)

// Setup walks the operator through deploying the agent to one host: it
// collects the model settings and target, checks SSH and passwordless sudo
// (with fix hints), runs enroll, and verifies the service started. It only
// drives the existing pieces; no deployment logic lives here.
func Setup() error {
	fmt.Print("opsagent setup — 引导部署到一台 Linux 服务器。\n\n")
	r := bufio.NewReader(os.Stdin)

	provider, err := promptProvider(r)
	if err != nil {
		return err
	}
	modelName, err := prompt(r, "模型名", defaultModel(provider))
	if err != nil {
		return err
	}
	baseURL, err := prompt(r, "自定义 base URL（回车跳过）", "")
	if err != nil {
		return err
	}
	user, err := prompt(r, "服务运行用户", "opsagent")
	if err != nil {
		return err
	}
	host, err := prompt(r, "目标主机 (ssh host)", "")
	if err != nil {
		return err
	}
	if host == "" {
		return fmt.Errorf("未填主机，已取消")
	}

	if err := preflightWithRetry(r, host); err != nil {
		return err
	}

	apiKey, err := promptSecret(r, "粘贴 API key（不回显）")
	if err != nil {
		return err
	}
	if apiKey == "" {
		return fmt.Errorf("空 key，已取消")
	}

	fmt.Print("\n" + setupSummary(host, user, provider, modelName, baseURL))
	confirm, err := prompt(r, "确认部署? [Y/n]", "Y")
	if err != nil {
		return err
	}
	if !isYes(confirm) {
		return fmt.Errorf("已取消")
	}

	if err := Enroll(host, EnrollOptions{
		User:     user,
		Provider: provider,
		Model:    modelName,
		BaseURL:  baseURL,
		APIKey:   apiKey,
	}); err != nil {
		return err
	}

	fmt.Println("→ 验证服务状态...")
	if err := sshCheck(host, "systemctl is-active opsagent"); err != nil {
		fmt.Printf("  ⚠ 服务可能未就绪，查日志：ssh %s journalctl -u opsagent -n 50\n", host)
	} else {
		fmt.Println("  ✓ opsagent 服务运行中")
	}

	fmt.Printf("\n✓ 完成。开始对话：\n    opsagent connect %s\n", host)
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

// promptProvider asks for the model provider, accepting either the menu
// number or the name.
func promptProvider(r *bufio.Reader) (string, error) {
	for {
		fmt.Println("选择模型 provider:")
		fmt.Println("  [1] deepseek   [2] openai   [3] anthropic")
		choice, err := prompt(r, "选择", "1")
		if err != nil {
			return "", err
		}
		if p, ok := normalizeProvider(choice); ok {
			return p, nil
		}
		fmt.Println("  无效选择，请输入 1/2/3 或 provider 名。")
	}
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

// promptSecret reads a secret without echoing on a terminal, falling back to
// a plain line read when stdin is not a terminal (e.g. piped input). It
// reads the terminal directly; the assumption is interactive line input, so
// the bufio reader used for other prompts holds no buffered bytes here.
func promptSecret(r *bufio.Reader, label string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Print(label + ": ")
		b, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	return prompt(r, label, "")
}

// normalizeProvider maps a menu choice or name to a canonical provider.
func normalizeProvider(in string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(in)) {
	case "1", "deepseek":
		return "deepseek", true
	case "2", "openai", "openai-compatible":
		return "openai", true
	case "3", "anthropic", "claude":
		return "anthropic", true
	}
	return "", false
}

// defaultModel suggests a model for the chosen provider; the user can
// override it at the prompt.
func defaultModel(provider string) string {
	switch provider {
	case "deepseek":
		return "deepseek-chat"
	case "openai":
		return "gpt-4o"
	case "anthropic":
		return "claude-sonnet-4-6"
	}
	return ""
}

func isYes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "y", "yes":
		return true
	}
	return false
}

// setupSummary renders the deployment plan for confirmation.
func setupSummary(host, user, provider, model, baseURL string) string {
	var b strings.Builder
	b.WriteString("即将部署：\n")
	fmt.Fprintf(&b, "  主机:     %s\n", host)
	fmt.Fprintf(&b, "  运行用户: %s\n", user)
	fmt.Fprintf(&b, "  provider: %s\n", provider)
	fmt.Fprintf(&b, "  模型:     %s\n", model)
	if baseURL != "" {
		fmt.Fprintf(&b, "  base URL: %s\n", baseURL)
	}
	return b.String()
}
