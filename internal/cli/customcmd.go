// Client side of custom commands: a `/name` typed at the prompt that is not a
// built-in is forwarded to the agent as a TypeRunCommand frame, which opens a
// full turn (the agent injects the command's definition and streams the usual
// reply/tools/confirms). The list of available commands is fetched over a
// control frame for /help and /commands. The client stays thin: it neither
// stores nor interprets command definitions — those live on the agent.
package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/areming/ops-agent/internal/transport"
)

// fetchCommands asks the agent for its saved custom commands and the directory
// the agent reads them from.
func fetchCommands(rt controlRoundTrip) (transport.CommandListReply, error) {
	reply, err := rt(transport.CmdCommandList, "")
	if err != nil {
		return transport.CommandListReply{}, err
	}
	if reply.Err != "" {
		return transport.CommandListReply{}, fmt.Errorf("%s", reply.Err)
	}
	var lr transport.CommandListReply
	if err := json.Unmarshal([]byte(reply.Text), &lr); err != nil {
		return transport.CommandListReply{}, err
	}
	return lr, nil
}

// printCommands lists the saved custom commands to w. When there are none it
// prints a hint, unless quiet (used when appending to /help, where a missing
// section is better than a noisy "none" line). Both the hint and the listing
// name the agent's real commands directory so the operator drops *.md files in
// the directory the answering agent actually reads — local sessions and the
// resident daemon use different ones.
func printCommands(rt controlRoundTrip, w io.Writer, quiet bool) error {
	lr, err := fetchCommands(rt)
	if err != nil {
		return err
	}
	if len(lr.Commands) == 0 {
		if !quiet {
			fmt.Fprintln(w, muted(emptyCommandsHint(lr.Dir)))
		}
		return nil
	}
	fmt.Fprintf(w, "%s\n", muted(commandsHeader(lr.Dir)))
	for _, c := range lr.Commands {
		if c.Description != "" {
			fmt.Fprintf(w, "  %s%s  %s\n", accent("/"), c.Name, muted(c.Description))
		} else {
			fmt.Fprintf(w, "  %s%s\n", accent("/"), c.Name)
		}
	}
	return nil
}

// emptyCommandsHint tells the operator how to add a command, naming the real
// directory when the agent reported one and making clear the file is picked up
// on the next session — the agent re-reads the directory each time, so dropping
// a *.md there and reopening ops is all it takes (no restart of the daemon, no
// registration step).
func emptyCommandsHint(dir string) string {
	if dir == "" {
		return "（还没有自定义命令；在 agent 的 commands 目录放 *.md 即可，用 /name 触发）"
	}
	return fmt.Sprintf("（还没有自定义命令。把 *.md 放进 %s —— 重开 ops 即自动读取；之后用 /name 触发）", dir)
}

// commandsHeader labels the listing, naming the directory the commands were
// loaded from so the operator knows where to add or edit them; new *.md files
// dropped there load on the next ops session automatically.
func commandsHeader(dir string) string {
	if dir == "" {
		return "自定义命令："
	}
	return fmt.Sprintf("自定义命令（目录 %s，放 *.md 即自动加载）：", dir)
}

// runCommandFrame sends the trigger for a custom command. The caller then
// drains the turn it opens (drain / drainRaw), just like user input.
func runCommandFrame(conn *transport.Conn, name, args string) error {
	f, err := transport.PayloadFrame(transport.TypeRunCommand, transport.RunCommandPayload{Name: name, Args: args})
	if err != nil {
		return err
	}
	return conn.WriteFrame(f)
}
