package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/transport"
)

// runCommandTurn triggers a saved custom command as a full turn: it looks the
// command up (re-reading the directory so edits land), injects its definition
// as the turn's input, and runs the normal chat loop — so tools, the safety
// gate, and audit all apply exactly as for typed input. An unknown name ends
// the turn with an error + Done so the client returns cleanly to the prompt.
func (srv *server) runCommandTurn(ctx context.Context, conn *transport.Conn, sess *session, frames <-chan transport.Frame, p transport.RunCommandPayload) error {
	cmd, ok := memory.FindCommand(srv.loadCommands(), p.Name)
	if !ok {
		writeError(conn, fmt.Sprintf("未知命令 /%s（/help 查看可用命令，/commands 看自定义命令）", p.Name))
		return conn.WriteFrame(transport.Frame{Type: transport.TypeDone})
	}
	sess.addUser(ctx, buildCommandPrompt(cmd, p.Args))
	return srv.chatTurn(ctx, conn, sess, frames)
}

// loadCommands re-reads the custom-command directory on every call so a command
// added or edited mid-session takes effect without restarting the daemon. A
// read error yields no commands (custom commands are optional, never fatal).
func (srv *server) loadCommands() []memory.Command {
	cmds, err := memory.LoadCommands(srv.commandsDir())
	if err != nil {
		return nil
	}
	return cmds
}

// commandList returns the saved commands (JSON CommandListReply) for /help and
// the /commands listing.
func (srv *server) commandList() (string, error) {
	var reply transport.CommandListReply
	for _, c := range srv.loadCommands() {
		reply.Commands = append(reply.Commands, transport.CommandInfo{Name: c.Name, Description: c.Description})
	}
	b, err := json.Marshal(reply)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// buildCommandPrompt turns a triggered command into the user-turn text fed to
// the model: a short frame telling it this is a saved command (its definition
// is the task and its operation space), the definition body, and any extra args
// the operator typed after the /name. Kept pure for straightforward testing.
func buildCommandPrompt(cmd memory.Command, args string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[自定义命令 /%s]\n", cmd.Name)
	b.WriteString("以下是该命令的定义（可能是自然语言指令或 shell 脚本）。请理解其意图并执行——这就是本次任务的操作范围；不要超出它做无关的事。涉及系统变更的步骤照常通过工具执行并接受安全确认。\n\n")
	b.WriteString(cmd.Body)
	if args = strings.TrimSpace(args); args != "" {
		fmt.Fprintf(&b, "\n\n[附加参数] %s", args)
	}
	return b.String()
}
