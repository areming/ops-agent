// Package cli is the thin local client. It renders the conversation,
// forwards input, and answers the agent's confirmation prompts; it holds
// no model or business logic. M2 adds the confirm handshake; a richer TUI
// arrives later.
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/areming/ops-agent/internal/transport"
	"github.com/areming/ops-agent/internal/version"
)

// --- terminal styling -------------------------------------------------------
//
// Output is colored with 24-bit ("truecolor") ANSI escapes, gated on the stream
// being a real terminal that advertises truecolor (COLORTERM) with NO_COLOR
// unset — so piped output, the basic 8/16-color consoles common on old target
// machines, and users who opt out all stay plain. We keep this hand-rolled (a
// few escapes) rather than pulling in a TUI framework, to honor the project's
// minimal-dependency, pure-static-binary constraint.

// colorAllowedByEnv reports whether the environment permits ANSI color:
// NO_COLOR must be unset/empty (https://no-color.org), and the terminal must
// advertise 24-bit color — via COLORTERM, or via WT_SESSION (Windows Terminal
// is always truecolor-capable but sets that instead of COLORTERM). Requiring an
// explicit signal keeps us from emitting truecolor escapes a basic 8/16-color
// console would mangle.
func colorAllowedByEnv() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if ct := os.Getenv("COLORTERM"); ct == "truecolor" || ct == "24bit" {
		return true
	}
	return os.Getenv("WT_SESSION") != ""
}

// colorEnabled folds the env policy together with the stream being a terminal.
func colorEnabled(fd uintptr) bool {
	return colorAllowedByEnv() && term.IsTerminal(int(fd))
}

var (
	colorStdout = colorEnabled(os.Stdout.Fd())
	colorStderr = colorEnabled(os.Stderr.Fd())
)

// rgb wraps s in a truecolor foreground; a no-op off a terminal.
func rgb(r, g, b int, s string) string {
	if !colorStdout {
		return s
	}
	return fmt.Sprintf("\x1b[38;2;%d;%d;%dm%s\x1b[0m", r, g, b, s)
}

// dimRGB is rgb plus the faint attribute, for secondary text.
func dimRGB(r, g, b int, s string) string {
	if !colorStdout {
		return s
	}
	return fmt.Sprintf("\x1b[2;38;2;%d;%d;%dm%s\x1b[0m", r, g, b, s)
}

// bold wraps s in the bold attribute.
func bold(s string) string {
	if !colorStdout {
		return s
	}
	return "\x1b[1m" + s + "\x1b[0m"
}

// accent is the warm amber the eye should land on (✻, ⏺, /help, ›); muted is
// the dim gray for labels and hints.
func accent(s string) string { return rgb(235, 170, 70, s) }
func muted(s string) string  { return dimRGB(150, 150, 160, s) }

// errLabel paints a short error marker red on stderr (no-op off a terminal).
func errLabel(s string) string {
	if !colorStderr {
		return s
	}
	return "\x1b[1;31m" + s + "\x1b[0m"
}

// gradientRule returns a horizontal rule of n cells fading warm-orange→magenta
// — the one truecolor flourish under the wordmark. Plain dashes off a terminal.
func gradientRule(n int) string {
	if !colorStdout {
		return strings.Repeat("─", n)
	}
	var b strings.Builder
	for i := range n {
		t := float64(i) / float64(n-1)
		r := int(255 - 55*t)
		g := int(140 - 50*t)
		bl := int(50 + 150*t)
		fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm─", r, g, bl)
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// printLocalBanner is the bare-`ops` welcome: mascot + wordmark + active model.
func printLocalBanner(provider, model, ver string) {
	modelVal := provider
	if model != "" {
		modelVal = provider + " / " + model
	}
	printBanner([2][2]string{{"模型", modelVal}, {"版本", ver}})
}

// printConnectBanner is the `ops connect` welcome: it shows the host reached
// instead of a model (the remote model isn't known to the client).
func printConnectBanner(host, ver string) {
	printBanner([2][2]string{{"主机", host}, {"版本", ver}})
}

// printBanner renders the startup banner: a small robot mascot beside the ops
// wordmark, a gradient rule, two info rows, and the /help hint. The mascot is
// box-drawing only (no CJK), so it always aligns; info rows are left-aligned
// with no right border, sidestepping CJK double-width alignment entirely.
func printBanner(rows [2][2]string) {
	slate := func(s string) string { return rgb(120, 140, 170, s) }
	eyes := rgb(120, 200, 200, "● ●")

	fmt.Println()
	fmt.Printf(" %s\n", muted("╷"))
	fmt.Printf(" %s  %s %s\n", slate("╭─┴─╮"), accent("✻"), bold("ops"))
	fmt.Printf(" %s%s%s  %s\n", slate("│"), eyes, slate("│"), muted("轻量运维助手 · 服务器守护机器人"))
	fmt.Printf(" %s\n", slate("╰───╯"))
	fmt.Printf(" %s\n", gradientRule(34))
	for i, row := range rows {
		val := rgb(205, 205, 210, row[1])
		if i == 0 {
			val = rgb(120, 200, 200, row[1]) // the primary row (model / host) pops
		}
		fmt.Printf(" %s  %s\n", muted(row[0]), val)
	}
	fmt.Printf("\n %s%s\n", accent("/help"), muted(" 查看命令"))
}

// clearScreen clears the terminal using ANSI escape sequences.
func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

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
	printConnectBanner("local", version.Value)
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
	} else {
		// Already installed: a fresh install is current by definition, so only
		// the already-installed path checks for a newer release.
		maybeOfferUpdate(host, remoteBin)
	}

	conn, cleanup, err := sshBridge(host, remoteSocket, remoteBin)
	if err != nil {
		return err
	}
	printConnectBanner(host, version.Value)
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

// repl drives one interactive session. On a real terminal it runs the raw-mode
// loop (replRaw), which adds ESC/Ctrl-C interruption of a running turn; piped or
// non-TTY sessions (automation, SSH without a tty, tests) fall back to the
// line-based loop, which has no key watching but identical conversation flow.
func repl(conn *transport.Conn, label string) error {
	if term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) {
		if err := replRaw(conn, label); !errors.Is(err, errRawUnavailable) {
			return err
		}
		// Couldn't enter raw mode; fall through to the line-based loop.
	}
	return replCooked(conn, label)
}

// replCooked reads a line, sends it as UserInput, then handles the streamed
// reply (text, tool activity, confirmations) until Done. EOF on stdin
// ends the session. label identifies the connected host in the banner and
// prompt so multiple sessions are easy to tell apart.
func replCooked(conn *transport.Conn, label string) error {
	in := bufio.NewScanner(os.Stdin)
	prompt := fmt.Sprintf("%s %s ", muted("["+label+"]"), accent("›"))
	for {
		fmt.Printf("\n%s", prompt)
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
// (/help, /exit) are handled here; /model, /logs, /clear and /yolo become
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
	case "clear":
		clearScreen()
		return false, sendControl(conn, cmd, arg)
	case "models", "model":
		// /model (singular, like claude) is an alias; the agent control is "models".
		return false, sendControl(conn, "models", arg)
	case "logs", "yolo":
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
		fmt.Fprintf(os.Stderr, "%s %s\n", errLabel("✗"), reply.Err)
		return nil
	}
	fmt.Println(reply.Text)
	return nil
}

func printSlashHelp() { printSlashHelpTo(os.Stdout) }

func printSlashHelpTo(w io.Writer) {
	fmt.Fprint(w, `命令：
  /model [名称]    查看模型；带名称则切换当前会话所连机器的模型
  /logs [N]        查看最近 N 条操作日志（默认 20）
  /yolo [on|off]   自动放行（默认开）：开=非危险操作直接执行，关=逐条确认；危险命令始终确认
  /clear           清空当前对话
  /help            显示本帮助
  /exit            退出
`)
}

// drain handles frames for one turn until Done. It shows a spinner on stderr
// during the silent gaps — waiting on the model's first token, and while a
// command runs — so the session never looks frozen. The spinner is cleared the
// instant any frame arrives, and never runs while assistant text is streaming.
func drain(conn *transport.Conn, in *bufio.Scanner) error {
	sp := startSpinner("思考中…")
	var live *liveOutput
	finishLive := func() {
		if live != nil {
			live.finish()
			live = nil
		}
	}
	replyOpen := false // have we started the assistant's reply block this turn?
	for {
		f, err := conn.ReadFrame()
		sp.stop()
		sp = nil
		if err != nil {
			return err
		}
		switch f.Type {
		case transport.TypeAssistantDelta:
			finishLive()
			s, _ := f.Text()
			if !replyOpen {
				fmt.Print("\n") // blank line sets the reply apart from input/tool
				replyOpen = true
			}
			fmt.Print(s)
		case transport.TypeToolStart:
			finishLive()
			replyOpen = false
			var p transport.ToolStartPayload
			_ = f.Decode(&p)
			fmt.Printf("\n%s %s%s\n", accent("⏺"), bold(p.Tool), muted("("+p.Command+")"))
			sp = startSpinner("执行中…")
		case transport.TypeToolOutput:
			replyOpen = false
			if live == nil {
				// Cooked path: no cursor tricks, just stream lines as they arrive.
				live = newLiveOutput(os.Stdout, false, 0, 0)
			}
			s, _ := f.Text()
			live.feed([]byte(s))
		case transport.TypeConfirmRequest:
			finishLive()
			replyOpen = false
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
			finishLive()
			s, _ := f.Text()
			fmt.Fprintf(os.Stderr, "\n%s %s\n", errLabel("✗ 错误"), s)
			// keep reading; the turn still ends with Done
		case transport.TypeDone:
			finishLive()
			fmt.Println()
			return nil
		}
	}
}

// askConfirm shows the flagged action and returns the user's decision. On an
// interactive terminal it renders an arrow-key menu (↑/↓ to move, Enter to
// choose) with a one-line purpose under each option; on a non-TTY session
// (piped input/output) it falls back to a single-line y/a/N prompt so
// automation and SSH-without-tty still work.
func askConfirm(in *bufio.Scanner, p transport.ConfirmRequestPayload) (approved, always bool) {
	inFd := int(os.Stdin.Fd())
	if term.IsTerminal(inFd) && term.IsTerminal(int(os.Stdout.Fd())) {
		if a, al, ok := askConfirmMenu(inFd, p); ok {
			return a, al
		}
	}
	return askConfirmLine(in, p)
}

// confirmChoice is one selectable option and the decision it carries.
type confirmChoice struct {
	label    string
	desc     string
	approved bool
	always   bool
}

// confirmChoices are the menu options, in display order. The middle option's
// "always" maps to ConfirmReplyPayload.Always — the agent auto-runs this exact
// command for the rest of the session.
var confirmChoices = []confirmChoice{
	{"本次执行", "只运行这一条命令", true, false},
	{"本会话始终允许", "本会话内不再询问这条命令", true, true},
	{"拒绝", "不执行，把原因反馈给助手", false, false},
}

// askConfirmMenu renders an interactive arrow-key menu on a raw terminal.
// ok is false when raw mode can't be entered, so the caller falls back to the
// line prompt. A read error, Esc, or Ctrl-C is treated as a denial.
func askConfirmMenu(fd int, p transport.ConfirmRequestPayload) (approved, always, ok bool) {
	fmt.Printf("\n%s  %s\n  命令: %s\n  原因: %s\n  %s\n",
		rgb(235, 180, 60, "⚠ 需要确认"),
		muted("risk="+p.Risk+" · "+p.Tool),
		bold(p.Command), p.Reason,
		muted("↑/↓ 选择 · Enter 确认 · Esc 拒绝"))

	old, err := term.MakeRaw(fd)
	if err != nil {
		return false, false, false
	}
	defer fmt.Print("\n")       // runs after Restore: clean line in cooked mode
	defer term.Restore(fd, old) //nolint:errcheck // best-effort restore

	sel := 0
	render := func() {
		for i, c := range confirmChoices {
			marker, label := "  ", c.label
			if i == sel {
				marker, label = accent("❯")+" ", bold(c.label)
			}
			fmt.Printf("\r\x1b[K%s%s  %s\r\n", marker, label, muted(c.desc))
		}
	}
	render()

	// In raw mode an arrow key arrives as a 3-byte escape sequence (ESC [ A/B).
	buf := make([]byte, 4)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil || n == 0 {
			return false, false, true
		}
		switch {
		case buf[0] == 3, buf[0] == 27 && n == 1: // Ctrl-C or lone Esc → deny
			return false, false, true
		case buf[0] == '\r', buf[0] == '\n':
			c := confirmChoices[sel]
			return c.approved, c.always, true
		case n >= 3 && buf[0] == 27 && buf[1] == '[':
			switch buf[2] {
			case 'A': // up
				if sel > 0 {
					sel--
				}
			case 'B': // down
				if sel < len(confirmChoices)-1 {
					sel++
				}
			}
		case buf[0] == 'k':
			if sel > 0 {
				sel--
			}
		case buf[0] == 'j':
			if sel < len(confirmChoices)-1 {
				sel++
			}
		case buf[0] >= '1' && buf[0] <= '9':
			if i := int(buf[0] - '1'); i < len(confirmChoices) {
				c := confirmChoices[i]
				return c.approved, c.always, true
			}
		}
		fmt.Printf("\x1b[%dA", len(confirmChoices)) // back to the first option line
		render()
	}
}

// askConfirmLine is the non-interactive fallback. "y" approves once; "a"
// approves and auto-allows this exact command for the session. EOF or anything
// else is a denial.
func askConfirmLine(in *bufio.Scanner, p transport.ConfirmRequestPayload) (approved, always bool) {
	fmt.Printf("\n%s  %s\n  命令: %s\n  原因: %s\n  执行? [y=本次 / a=本会话始终 / N=拒绝] ",
		rgb(235, 180, 60, "⚠ 需要确认"),
		muted("risk="+p.Risk+" · "+p.Tool),
		bold(p.Command), p.Reason)
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
