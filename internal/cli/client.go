// Package cli is the thin local client. It renders the conversation,
// forwards input, and answers the agent's confirmation prompts; it holds
// no model or business logic. M2 adds the confirm handshake; a richer TUI
// arrives later.
package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/areming/ops-agent/internal/transport"
)

// ConnectLocal dials the agent socket directly on the same machine.
// Used for development verification without SSH.
func ConnectLocal(socketPath string) error {
	nc, err := transport.Dial(socketPath)
	if err != nil {
		return err
	}
	defer nc.Close()
	return repl(transport.NewConn(nc))
}

// ConnectSSH runs `opsagent _bridge` on host over SSH and speaks the
// Frame protocol across the SSH stdio. An empty remoteSocket lets the
// remote use its own default path.
func ConnectSSH(host, remoteSocket, remoteBin string) error {
	conn, cleanup, err := sshBridge(host, remoteSocket, remoteBin)
	if err != nil {
		return err
	}
	rerr := repl(conn)
	if cerr := cleanup(); rerr == nil {
		rerr = cerr
	}
	return rerr
}

// sshBridge starts `opsagent _bridge` on host over SSH and returns a Conn
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
// ends the session.
func repl(conn *transport.Conn) error {
	fmt.Println("opsagent connected. type a message (Ctrl-D to quit).")
	in := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !in.Scan() {
			return in.Err()
		}
		uf, err := transport.TextFrame(transport.TypeUserInput, in.Text())
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

// drain handles frames for one turn until Done.
func drain(conn *transport.Conn, in *bufio.Scanner) error {
	for {
		f, err := conn.ReadFrame()
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
		case transport.TypeConfirmRequest:
			var p transport.ConfirmRequestPayload
			_ = f.Decode(&p)
			reply, err := transport.PayloadFrame(transport.TypeConfirmReply,
				transport.ConfirmReplyPayload{Approved: askConfirm(in, p)})
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

// askConfirm shows the flagged action and reads a yes/no answer. EOF or
// anything other than y/yes is treated as a denial.
func askConfirm(in *bufio.Scanner, p transport.ConfirmRequestPayload) bool {
	fmt.Printf("\n⚠ 需要确认 [risk=%s] %s\n  命令: %s\n  原因: %s\n  执行? [y/N] ",
		p.Risk, p.Tool, p.Command, p.Reason)
	if !in.Scan() {
		return false
	}
	ans := strings.ToLower(strings.TrimSpace(in.Text()))
	return ans == "y" || ans == "yes"
}
