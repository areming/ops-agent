// Command opsagent is a single static binary with subcommands:
//
//	serve     run the resident agent on a server
//	connect   open a conversation with an agent (local or over SSH)
//	key       manage secrets in the encrypted keystore (set/list)
//	_bridge   internal: pipe SSH stdio to the local agent socket
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/areming/ops-agent/internal/agent"
	"github.com/areming/ops-agent/internal/cli"
	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/secret"
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
	case "key":
		err = runKey(args)
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

// runKey manages secrets in the encrypted keystore. `set` reads the value
// from stdin (never an argument) so it does not land in shell history or
// the process list; `list` prints names only.
func runKey(args []string) error {
	cfg := config.Load()
	ks, err := secret.Open(cfg.KeystorePath, cfg.MasterKeyPath)
	if err != nil {
		return err
	}

	if len(args) < 1 {
		return fmt.Errorf("usage: opsagent key (set <name> | list)")
	}
	switch args[0] {
	case "set":
		if len(args) < 2 {
			return fmt.Errorf("usage: opsagent key set <name>  (value is read from stdin)")
		}
		name := args[1]
		value, err := readSecret()
		if err != nil {
			return err
		}
		if value == "" {
			return fmt.Errorf("empty value; nothing stored")
		}
		if err := ks.Set(name, value); err != nil {
			return err
		}
		fmt.Printf("stored secret %q in %s\n", name, cfg.KeystorePath)
		return nil
	case "list":
		names := ks.List()
		if len(names) == 0 {
			fmt.Println("(no secrets stored)")
			return nil
		}
		for _, n := range names {
			fmt.Println(n)
		}
		return nil
	default:
		return fmt.Errorf("unknown key subcommand %q (want set|list)", args[0])
	}
}

// readSecret reads the secret value from stdin, trimming a single trailing
// newline so piping (`echo $KEY | opsagent key set ...`) works cleanly.
func readSecret() (string, error) {
	info, _ := os.Stdin.Stat()
	if info.Mode()&os.ModeCharDevice != 0 {
		fmt.Fprint(os.Stderr, "enter secret value, then EOF (Ctrl-D): ")
	}
	b, err := io.ReadAll(bufio.NewReader(os.Stdin))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
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
  opsagent key set <name>            (value read from stdin)
  opsagent key list
  opsagent _bridge [--socket PATH]   (internal; invoked over SSH)
`)
}
