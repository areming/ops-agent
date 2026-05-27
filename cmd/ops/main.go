// Command ops is a single static binary with subcommands:
//
//	serve     run the resident agent on a server
//	connect   open a conversation with an agent (local or over SSH)
//	key       manage secrets in the encrypted keystore (set/list)
//	_bridge   internal: pipe SSH stdio to the local agent socket
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/areming/ops-agent/internal/agent"
	"github.com/areming/ops-agent/internal/cli"
	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/secret"
	"github.com/areming/ops-agent/internal/transport"
	"github.com/areming/ops-agent/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		// Bare `ops` opens a local conversation (onboarding first if the
		// machine has no model configured yet).
		if err := cli.RunLocal(); err != nil {
			fmt.Fprintf(os.Stderr, "ops: %v\n", err)
			os.Exit(1)
		}
		return
	}
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "serve":
		err = runServe(args)
	case "setup":
		err = cli.Setup()
	case "connect":
		err = runConnect(args)
	case "run":
		err = runFanOut(args)
	case "_bridge":
		err = runBridge(args)
	case "key":
		err = runKey(args)
	case "enroll":
		err = runEnroll(args)
	case "update":
		err = runUpdate(args)
	case "uninstall":
		err = runUninstall(args)
	case "logs":
		err = runLogs(args)
	case "todos":
		err = runTodos(args)
	case "version", "-v", "--version":
		fmt.Println(version.Value)
		return
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ops %s: %v\n", cmd, err)
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
	bin := fs.String("bin", "ops", "remote ops binary (ssh mode)")
	_ = fs.Parse(args)

	if *local != "" {
		return cli.ConnectLocal(resolveSocket(*local))
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: ops connect <host> | --local <socket>")
	}
	return cli.ConnectSSH(rest[0], *socket, *bin)
}

// runFanOut runs one instruction non-interactively across several hosts.
// Actions needing confirmation are skipped per host unless --yes is given.
func runFanOut(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	instruction := fs.String("c", "", "instruction to run on each host")
	socket := fs.String("socket", "", "remote agent socket path")
	bin := fs.String("bin", "ops", "remote ops binary")
	yes := fs.Bool("yes", false, "auto-approve actions that need confirmation (dangerous)")
	_ = fs.Parse(args)

	hosts := fs.Args()
	if *instruction == "" || len(hosts) == 0 {
		return fmt.Errorf(`usage: ops run -c "<instruction>" <host>... [--yes]`)
	}
	return cli.FanOut(hosts, *instruction, *socket, *bin, *yes)
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
		return fmt.Errorf("usage: ops key (set <name> | list)")
	}
	switch args[0] {
	case "set":
		if len(args) < 2 {
			return fmt.Errorf("usage: ops key set <name>  (value is read from stdin)")
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
// newline so piping (`echo $KEY | ops key set ...`) works cleanly.
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

// runEnroll deploys the agent to a remote Linux host. The model API key is
// read from stdin so it never lands in shell history or the process list.
func runEnroll(args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ExitOnError)
	provider := fs.String("provider", "deepseek", "model provider (openai|deepseek|anthropic)")
	modelName := fs.String("model", "", "model name")
	baseURL := fs.String("base-url", "", "optional API base URL override")
	user := fs.String("user", "opsagent", "dedicated service user to run the agent as")
	bin := fs.String("bin", "", "path to the linux agent binary (default: dist/ops-linux-<arch>)")
	services := fs.String("services", "", "comma-separated units patrol watches and may auto-restart")
	diagModel := fs.String("diag-model", "", "diagnosis model (reuses the main provider/key)")
	_ = fs.Parse(args)

	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: ops enroll <host> [flags]  (API key read from stdin)")
	}
	apiKey, err := readSecret()
	if err != nil {
		return err
	}
	if apiKey == "" {
		return fmt.Errorf("empty API key; nothing to enroll")
	}
	return cli.Enroll(rest[0], cli.EnrollOptions{
		User:      *user,
		Provider:  *provider,
		Model:     *modelName,
		BaseURL:   *baseURL,
		BinPath:   *bin,
		Version:   version.Value,
		APIKey:    apiKey,
		Services:  *services,
		DiagModel: *diagModel,
	})
}

func runUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also delete state/config and (Linux enrolled) service user")
	_ = fs.Parse(args)
	return cli.Uninstall(*purge)
}

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	checkOnly := fs.Bool("check", false, "check for updates without installing")
	_ = fs.Parse(args)
	return cli.Update(*checkOnly)
}

// runLogs prints the most recent audit entries from the local state DB.
func runLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	n := fs.Int("n", 20, "number of entries to show")
	db := fs.String("db", "", "state DB path (default: config)")
	_ = fs.Parse(args)

	store, err := openStore(*db)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("(no audit entries)")
			return nil
		}
		return err
	}
	defer store.Close()

	entries, err := store.RecentAudit(context.Background(), *n)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		fmt.Println("(no audit entries)")
		return nil
	}
	for _, e := range entries {
		fmt.Printf("%s  %s  [%s/%s] exit=%d  %s\n", e.CreatedAt, e.Source, e.Decision, e.Risk, e.ExitCode, e.Command)
	}
	return nil
}

// runTodos prints open self-heal todos from the local state DB. The table
// is populated by patrol (M5); until then this lists nothing.
func runTodos(args []string) error {
	fs := flag.NewFlagSet("todos", flag.ExitOnError)
	db := fs.String("db", "", "state DB path (default: config)")
	_ = fs.Parse(args)

	store, err := openStore(*db)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("(no open todos)")
			return nil
		}
		return err
	}
	defer store.Close()

	todos, err := store.ListOpenTodos(context.Background())
	if err != nil {
		return err
	}
	if len(todos) == 0 {
		fmt.Println("(no open todos)")
		return nil
	}
	for _, t := range todos {
		fmt.Printf("#%d  [%s]  %s\n    %s\n", t.ID, t.Severity, t.Title, t.Detail)
	}
	return nil
}

// openStore opens the state DB read-only: logs/todos only read, and the
// operator viewing an agent's DB may have only group-read access.
func openStore(dbPath string) (*memory.Store, error) {
	if dbPath == "" {
		dbPath = config.Load().DBPath
	}
	return memory.OpenReadOnly(dbPath)
}

func runBridge(args []string) error {
	fs := flag.NewFlagSet("_bridge", flag.ExitOnError)
	socket := fs.String("socket", "", "local agent socket path")
	_ = fs.Parse(args)
	return transport.Bridge(resolveSocket(*socket))
}

// resolveSocket falls back to the fixed service path on Linux (where enroll
// installs the agent) and a temp path elsewhere for development. serve and
// _bridge share this default so an enrolled `connect <host>` needs no flag.
func resolveSocket(p string) string {
	if p != "" {
		return p
	}
	if runtime.GOOS == "linux" {
		return cli.AgentSocketPath
	}
	return filepath.Join(os.TempDir(), "opsagent.sock")
}

func usage() {
	fmt.Fprint(os.Stderr, `ops — lightweight ops assistant

usage:
  ops                            open a local conversation (onboards if unconfigured)
  ops setup                      guided first-time deploy (interactive)
  ops enroll <host> [flags]      deploy the agent to a Linux host
  ops connect <host> [--socket REMOTE_PATH] [--bin REMOTE_BIN]
  ops connect --local PATH
  ops run -c "<instruction>" <host>... [--yes]   run one instruction across hosts
  ops serve [--socket PATH]
  ops key set <name>             (value read from stdin)
  ops key list
  ops update [-check]            update to the latest release
  ops uninstall [--purge]        remove ops from this machine
  ops logs [-n N] [--db PATH]    show the audit trail
  ops todos [--db PATH]          show open self-heal todos
  ops _bridge [--socket PATH]    (internal; invoked over SSH)
`)
}
