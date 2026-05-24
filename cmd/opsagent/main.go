// Command opsagent is a single static binary with subcommands:
//
//	serve     run the resident agent on a server
//	connect   open a conversation with an agent (local or over SSH)
//	_bridge   internal: pipe SSH stdio to the local agent socket
//
// M0 is a skeleton: serve echoes input, there is no model or tool yet.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/areming/ops-agent/internal/agent"
	"github.com/areming/ops-agent/internal/cli"
	"github.com/areming/ops-agent/internal/transport"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "connect":
		err = runConnect(args)
	case "_bridge":
		err = runBridge(args)
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "opsagent %s: %v\n", cmd, err)
		os.Exit(1)
	}
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	socket := fs.String("socket", "", "unix socket path to listen on")
	_ = fs.Parse(args)
	return agent.Serve(resolveSocket(*socket))
}

func runConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	local := fs.String("local", "", "connect directly to a local socket (dev)")
	socket := fs.String("socket", "", "remote agent socket path (ssh mode)")
	bin := fs.String("bin", "opsagent", "remote opsagent binary (ssh mode)")
	_ = fs.Parse(args)

	if *local != "" {
		return cli.ConnectLocal(resolveSocket(*local))
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: opsagent connect <host> | --local <socket>")
	}
	return cli.ConnectSSH(rest[0], *socket, *bin)
}

func runBridge(args []string) error {
	fs := flag.NewFlagSet("_bridge", flag.ExitOnError)
	socket := fs.String("socket", "", "local agent socket path")
	_ = fs.Parse(args)
	return transport.Bridge(resolveSocket(*socket))
}

// resolveSocket falls back to a per-OS temp path when none is given.
// Later milestones source this from config / the runtime dir.
func resolveSocket(p string) string {
	if p != "" {
		return p
	}
	return filepath.Join(os.TempDir(), "opsagent.sock")
}

func usage() {
	fmt.Fprint(os.Stderr, `opsagent — lightweight ops assistant (M0 skeleton)

usage:
  opsagent serve [--socket PATH]
  opsagent connect <host> [--socket REMOTE_PATH] [--bin REMOTE_BIN]
  opsagent connect --local PATH
  opsagent _bridge [--socket PATH]   (internal; invoked over SSH)
`)
}
