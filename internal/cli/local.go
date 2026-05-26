package cli

import (
	"net"

	"github.com/areming/ops-agent/internal/agent"
	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/secret"
	"github.com/areming/ops-agent/internal/transport"
)

// RunLocal starts an in-process conversation on this machine. If no model is
// configured yet it runs first-time onboarding, then connects the CLI to an
// in-process agent over an in-memory pipe — so the local session reuses the
// same frame protocol and REPL as the SSH path.
func RunLocal() error {
	if !configured() {
		if err := onboardLocal(); err != nil {
			return err
		}
	}

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
