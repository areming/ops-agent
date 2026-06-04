package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"runtime"

	"github.com/areming/ops-agent/internal/agent"
	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/secret"
	"github.com/areming/ops-agent/internal/transport"
	"github.com/areming/ops-agent/internal/version"
)

// RunLocal handles bare `ops` on this machine. One server, one brain: if a
// resident agent daemon is installed here (by enroll), it attaches to that
// daemon — reusing its model, key, memory and patrol — instead of starting a
// separate, empty in-process session. Only a machine with no resident daemon
// (a laptop, an un-enrolled box) falls through to a standalone session.
func RunLocal() error {
	if sock, ok := residentSocket(); ok {
		nc, err := transport.Dial(sock)
		switch classifyResident(err, unitInstalled()) {
		case residentAttach:
			return replOverConn(nc, "本机常驻 agent", "local")
		case residentDenied:
			return errResidentDenied(sock)
		case residentServiceDown:
			return errResidentDown()
		}
		// residentNone: no daemon here → standalone session below.
	}
	return runLocalSession()
}

// runLocalSession starts an in-process conversation on this machine. If no
// model is configured yet it runs first-time onboarding, then connects the CLI
// to an in-process agent over an in-memory pipe — so the local session reuses
// the same frame protocol and REPL as the SSH path.
func runLocalSession() error {
	if !configured() {
		if err := onboardLocal(); err != nil {
			return err
		}
	}

	cfg := config.Load()
	printLocalBanner(cfg.Provider, cfg.Model, version.Value)

	client, server := net.Pipe()
	errc := make(chan error, 1)
	go func() { errc <- agent.LocalSession(server) }()

	rerr := repl(transport.NewConn(client), "local")
	_ = client.Close()
	if serr := <-errc; rerr == nil {
		rerr = serr
	}
	return rerr
}

// residentAction is what bare `ops` should do after probing the local resident
// agent socket.
type residentAction int

const (
	residentNone        residentAction = iota // no resident daemon → standalone local session
	residentAttach                            // a daemon is listening → attach to it
	residentDenied                            // a daemon exists but its socket is not accessible
	residentServiceDown                       // the service is installed but not running
)

// classifyResident maps the outcome of dialing the resident agent socket to
// the action bare `ops` should take. It is split out from the IO so the
// decision is unit-tested without a live socket. dialErr is the error from
// transport.Dial (nil on success); unitInstalled reports whether the systemd
// unit exists, which distinguishes "enrolled but stopped" from "never enrolled".
func classifyResident(dialErr error, unitInstalled bool) residentAction {
	if dialErr == nil {
		return residentAttach
	}
	if errors.Is(dialErr, fs.ErrPermission) {
		return residentDenied
	}
	if unitInstalled {
		return residentServiceDown
	}
	return residentNone
}

// residentSocket returns the fixed socket path of a locally-installed resident
// agent and whether this platform can have one. enroll installs the service
// only on Linux, so elsewhere there is no resident daemon and bare `ops` is
// always a standalone local session.
func residentSocket() (string, bool) {
	if runtime.GOOS == "linux" {
		return AgentSocketPath, true
	}
	return "", false
}

// unitInstalled reports whether the systemd unit enroll writes is present, so
// a failed socket dial can tell "service installed but down" from "never
// enrolled".
func unitInstalled() bool {
	_, err := os.Stat(unitPath)
	return err == nil
}

// errResidentDenied explains that a resident agent exists but the current user
// can't reach its socket — and how to fix it — rather than silently onboarding
// a second, separate brain.
func errResidentDenied(sock string) error {
	return fmt.Errorf("本机已装 opsagent 常驻服务，但当前用户无权访问它的 socket（%s）。\n"+
		"  你应已在 opsagent 组（enroll 时已加入）——重新登录一次让组生效即可直接 `ops`；\n"+
		"  或临时用 `newgrp opsagent`，或 `sudo ops`。", sock)
}

// errResidentDown explains that the resident service is installed but not
// running, and how to start it — again instead of onboarding a second brain.
func errResidentDown() error {
	return fmt.Errorf("本机装了 opsagent 常驻服务，但它没在运行。\n" +
		"  启动：sudo systemctl start opsagent\n" +
		"  看状态/日志：systemctl status opsagent · journalctl -u opsagent -n 50")
}

// configured reports whether a model and API key are both available, so
// RunLocal can skip onboarding. It checks the model first so a fresh machine
// (no config.json) returns false without opening the keystore — which would
// otherwise create the master key file before the user has set anything up.
func configured() bool {
	cfg := config.Load()
	if cfg.Model == "" {
		return false
	}
	if cfg.APIKey != "" {
		return true
	}
	ks, err := secret.Open(cfg.KeystorePath, cfg.MasterKeyPath)
	if err != nil {
		return false
	}
	_, ok, _ := ks.Get(localKeySecretName)
	return ok
}
