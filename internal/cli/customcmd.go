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

// fetchCommands asks the agent for its saved custom commands.
func fetchCommands(rt controlRoundTrip) ([]transport.CommandInfo, error) {
	reply, err := rt(transport.CmdCommandList, "")
	if err != nil {
		return nil, err
	}
	if reply.Err != "" {
		return nil, fmt.Errorf("%s", reply.Err)
	}
	var lr transport.CommandListReply
	if err := json.Unmarshal([]byte(reply.Text), &lr); err != nil {
		return nil, err
	}
	return lr.Commands, nil
}

// printCommands lists the saved custom commands to w. When there are none it
// prints a hint, unless quiet (used when appending to /help, where a missing
// section is better than a noisy "none" line).
func printCommands(rt controlRoundTrip, w io.Writer, quiet bool) error {
	cmds, err := fetchCommands(rt)
	if err != nil {
		return err
	}
	if len(cmds) == 0 {
		if !quiet {
			fmt.Fprintln(w, muted("（还没有自定义命令；在 agent 的 commands 目录放 *.md 即可，用 /name 触发）"))
		}
		return nil
	}
	fmt.Fprintf(w, "%s\n", muted("自定义命令："))
	for _, c := range cmds {
		if c.Description != "" {
			fmt.Fprintf(w, "  %s%s  %s\n", accent("/"), c.Name, muted(c.Description))
		} else {
			fmt.Fprintf(w, "  %s%s\n", accent("/"), c.Name)
		}
	}
	return nil
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
