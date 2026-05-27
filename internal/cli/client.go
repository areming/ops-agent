// Package cli is the thin local client. It renders the conversation,
// forwards input, and answers the agent's confirmation prompts; it holds
// no model or business logic. M2 adds the confirm handshake; a richer TUI
// arrives later.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/areming/ops-agent/internal/transport"
)

// RemoteHasBinary reports whether bin is on PATH on host. A non-zero exit
// from `command -v` means absent; any other failure (e.g. SSH itself) is an
// error.
func RemoteHasBinary(host, bin string) (bool, error) {
	cmd := exec.Command("ssh", host, "command -v "+bin)
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// promptYesNo asks label on stderr and reads one line; empty or y/yes is yes.
func promptYesNo(label string) bool {
	fmt.Fprint(os.Stderr, label)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return isYes(line)
}

// ConnectLocal dials the agent socket directly on the same machine.
// Used for development verification without SSH.
func ConnectLocal(socketPath string) error {
	nc, err := transport.Dial(socketPath)
	if err != nil {
		return err
	}
	defer nc.Close()
	return repl(transport.NewConn(nc), "local")
}

// ConnectSSH runs `ops _bridge` on host over SSH and speaks the
// Frame protocol across the SSH stdio. An empty remoteSocket lets the
// remote use its own default path. If the agent isn't installed on the host
// yet, it offers to run the deploy wizard first ("没装就装").
func ConnectSSH(host, remoteSocket, remoteBin string) error {
	installed, err := RemoteHasBinary(host, remoteBin)
	if err != nil {
		return err
	}
	if !installed {
		fmt.Fprintf(os.Stderr, "%s 上还没装 ops。\n", host)
		if !promptYesNo(fmt.Sprintf("现在引导安装到 %s? [Y/n] ", host)) {
			return fmt.Errorf("已取消")
		}
		if err := SetupHost(host); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "（若 connect 被拒，重新登录一次 SSH 让 opsagent 组生效）")
	}

	conn, cleanup, err := sshBridge(host, remoteSocket, remoteBin)
	if err != nil {
		return err
	}
	rerr := repl(conn, host)
	if cerr := cleanup(); rerr == nil {
		rerr = cerr
	}
	return rerr
}

// sshBridge starts `ops _bridge` on host over SSH and returns a Conn
// over its stdio plus a cleanup func that closes the input and waits for
// the remote to exit. An empty remoteSocket lets the remote use its default
// path.
func sshBridge(host, remoteSocket, remoteBin string) (*transport.Conn, func() error, error) {
	sshArgs := []string{host, remoteBin, "_bridge"}
	if remoteSocket != "" {
		sshArgs = append(sshArgs, "--socket", remoteSocket)
	}
	cmd := exec.Command("ssh", sshArgs...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	cleanup := func() error {
		_ = stdin.Close()
		return cmd.Wait()
	}
	return transport.NewConnRW(stdout, stdin), cleanup, nil
}

// repl reads a line, sends it as UserInput, then handles the streamed
// reply (text, tool activity, confirmations) until Done. EOF on stdin
// ends the session. label identifies the connected host in the banner and
// prompt so multiple sessions are easy to tell apart.
func repl(conn *transport.Conn, label string) error {
	fmt.Printf("ops @ %s — type a message, /help for commands (Ctrl-D to quit).\n", label)
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Printf("[%s] > ", label)
		if !in.Scan() {
			return in.Err()
		}
		line := in.Text()
		if strings.HasPrefix(strings.TrimSpace(line), "/") {
			quit, err := handleSlash(conn, strings.TrimSpace(line))
			if err != nil {
				return err
			}
			if quit {
				return nil
			}
			continue
		}
		uf, err := transport.TextFrame(transport.TypeUserInput, line)
		if err != nil {
			return err
		}
		if err := conn.WriteFrame(uf); err != nil {
			return err
		}
		if err := drain(conn, in); err != nil {
			return err
		}
	}
}

// handleSlash interprets a /command typed at the prompt. Local-only commands
// (/help, /quit) are handled here; /models, /logs, /clear and /yolo become
// control frames to the connected agent — so they act on whichever machine the
// session is talking to (local or remote). quit=true ends the session.
func handleSlash(conn *transport.Conn, line string) (quit bool, err error) {
	cmd, arg, _ := strings.Cut(strings.TrimPrefix(line, "/"), " ")
	cmd = strings.ToLower(cmd)
	arg = strings.TrimSpace(arg)
	switch cmd {
	case "help", "?":
		printSlashHelp()
		return false, nil
	case "quit", "exit", "q":
		return true, nil
	case "models", "logs", "clear", "yolo":
		return false, sendControl(conn, cmd, arg)
	default:
		fmt.Printf("未知命令 /%s（试试 /help）\n", cmd)
		return false, nil
	}
}

// sendControl writes a control request and prints the agent's single reply.
func sendControl(conn *transport.Conn, cmd, arg string) error {
	req, err := transport.PayloadFrame(transport.TypeControlRequest, transport.ControlRequestPayload{Cmd: cmd, Arg: arg})
	if err != nil {
		return err
	}
	if err := conn.WriteFrame(req); err != nil {
		return err
	}
	f, err := conn.ReadFrame()
	if err != nil {
		return err
	}
	if f.Type != transport.TypeControlReply {
		return fmt.Errorf("expected control reply, got %s", f.Type)
	}
	var reply transport.ControlReplyPayload
	if err := f.Decode(&reply); err != nil {
		return err
	}
	if reply.Err != "" {
		fmt.Fprintf(os.Stderr, "[error] %s\n", reply.Err)
		return nil
	}
	fmt.Println(reply.Text)
	return nil
}

func printSlashHelp() {
	fmt.Print(`命令：
  /models [名称]   查看模型；带名称则切换当前会话所连机器的模型
  /logs [N]        查看最近 N 条操作日志（默认 20）
  /yolo [on|off]   切换自动放行：开启后写操作不再逐条确认（危险命令仍拦），不带参数为切换
  /clear           清空当前对话
  /help            显示本帮助
  /quit            退出
`)
}

// drain handles frames for one turn until Done. It shows a spinner on stderr
// during the silent gaps — waiting on the model's first token, and while a
// command runs — so the session never looks frozen. The spinner is cleared the
// instant any frame arrives, and never runs while assistant text is streaming.
func drain(conn *transport.Conn, in *bufio.Scanner) error {
	sp := startSpinner("思考中…")
	for {
		f, err := conn.ReadFrame()
		sp.stop()
		sp = nil
		if err != nil {
			return err
		}
		switch f.Type {
		case transport.TypeAssistantDelta:
			s, _ := f.Text()
			fmt.Print(s)
		case transport.TypeToolStart:
			var p transport.ToolStartPayload
			_ = f.Decode(&p)
			fmt.Printf("\n▶ %s: %s\n", p.Tool, p.Command)
			sp = startSpinner("执行中…")
		case transport.TypeConfirmRequest:
			var p transport.ConfirmRequestPayload
			_ = f.Decode(&p)
			approved, always := askConfirm(in, p)
			reply, err := transport.PayloadFrame(transport.TypeConfirmReply,
				transport.ConfirmReplyPayload{Approved: approved, Always: always})
			if err != nil {
				return err
			}
			if err := conn.WriteFrame(reply); err != nil {
				return err
			}
		case transport.TypeError:
			s, _ := f.Text()
			fmt.Fprintf(os.Stderr, "\n[error] %s\n", s)
			// keep reading; the turn still ends with Done
		case transport.TypeDone:
			fmt.Println()
			return nil
		}
	}
}

// askConfirm shows the flagged action and reads the answer. "y" approves once;
// "a" approves and asks the agent to auto-allow this exact command for the rest
// of the session. EOF or anything else is a denial.
func askConfirm(in *bufio.Scanner, p transport.ConfirmRequestPayload) (approved, always bool) {
	fmt.Printf("\n⚠ 需要确认 [risk=%s] %s\n  命令: %s\n  原因: %s\n  执行? [y=本次 / a=本会话始终 / N=拒绝] ",
		p.Risk, p.Tool, p.Command, p.Reason)
	if !in.Scan() {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(in.Text())) {
	case "y", "yes":
		return true, false
	case "a", "always":
		return true, true
	default:
		return false, false
	}
}

// spinner animates a one-line status indicator on stderr. It is purely
// cosmetic — output goes to stderr so it never mixes into piped stdout, and it
// only animates when stderr is an interactive terminal.
type spinner struct {
	quit chan struct{}
	done chan struct{}
}

// startSpinner begins animating label until stop is called. It returns nil
// (a no-op spinner) when stderr is not a terminal.
func startSpinner(label string) *spinner {
	if !term.IsTerminal(int(os.Stderr.Fd())) {
		return nil
	}
	s := &spinner{quit: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(s.done)
		frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
		tk := time.NewTicker(120 * time.Millisecond)
		defer tk.Stop()
		for i := 0; ; i++ {
			select {
			case <-s.quit:
				return
			case <-tk.C:
				fmt.Fprintf(os.Stderr, "\r%s %s ", frames[i%len(frames)], label)
			}
		}
	}()
	return s
}

// stop halts the animation, waits for the goroutine to exit, then clears the
// status line — so no stray frame can land after the line is cleared. Safe to
// call on a nil spinner (the non-terminal case).
func (s *spinner) stop() {
	if s == nil {
		return
	}
	close(s.quit)
	<-s.done
	fmt.Fprint(os.Stderr, "\r"+strings.Repeat(" ", 40)+"\r")
}
