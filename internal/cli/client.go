// Package cli is the thin local client. It renders the conversation and
// forwards input; it holds no model or business logic. M0 provides a
// line-based REPL; a richer TUI arrives later.
package cli

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"

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
	sshArgs := []string{host, remoteBin, "_bridge"}
	if remoteSocket != "" {
		sshArgs = append(sshArgs, "--socket", remoteSocket)
	}
	cmd := exec.Command("ssh", sshArgs...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	rerr := repl(transport.NewConnRW(stdout, stdin))
	_ = stdin.Close()
	if werr := cmd.Wait(); rerr == nil {
		rerr = werr
	}
	return rerr
}

// repl reads a line, sends it as UserInput, then prints the streamed
// reply until Done. EOF on stdin ends the session.
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
		if err := drain(conn); err != nil {
			return err
		}
	}
}

// drain prints assistant deltas until a Done or Error frame.
func drain(conn *transport.Conn) error {
	for {
		f, err := conn.ReadFrame()
		if err != nil {
			return err
		}
		switch f.Type {
		case transport.TypeAssistantDelta:
			s, _ := f.Text()
			fmt.Print(s)
		case transport.TypeDone:
			fmt.Println()
			return nil
		case transport.TypeError:
			s, _ := f.Text()
			fmt.Fprintf(os.Stderr, "\n[error] %s\n", s)
			return nil
		}
	}
}
