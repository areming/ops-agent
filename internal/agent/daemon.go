// Package agent is the resident daemon: the "brain" that hosts the model
// loop and (in later milestones) tools, safety gate and patrol. M1 wires
// a real model provider behind the conversation; there is still no tool
// execution or system access.
package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"

	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/model"
	"github.com/areming/ops-agent/internal/secret"
	"github.com/areming/ops-agent/internal/tools"
	"github.com/areming/ops-agent/internal/transport"
)

// apiKeySecretName is the keystore entry holding the model provider's API
// key when it is not supplied via OPSAGENT_API_KEY.
const apiKeySecretName = "api_key"

// server holds the per-process dependencies shared across connections.
type server struct {
	prov         model.Provider
	reg          *tools.Registry
	store        *memory.Store
	systemPrompt string // base prompt with knowledge files folded in
	historyDepth int
}

// Serve builds the model provider from configuration, then listens on
// the unix socket and serves connections until the listener errors.
func Serve(socketPath string) error {
	cfg := config.Load()
	apiKey, err := resolveAPIKey(cfg)
	if err != nil {
		return err
	}
	prov, err := model.New(cfg.Provider, apiKey, cfg.BaseURL, cfg.Model)
	if err != nil {
		return err
	}

	store, err := memory.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	knowledge, err := memory.LoadKnowledge(cfg.KnowledgeDir)
	if err != nil {
		return err
	}

	srv := &server{
		prov:         prov,
		reg:          tools.NewRegistry(tools.Shell{}, tools.ReadFile{}, tools.WriteFile{}),
		store:        store,
		systemPrompt: composeSystemPrompt(knowledge),
		historyDepth: cfg.HistoryDepth,
	}

	// Patrol runs for the lifetime of the daemon, independent of any CLI
	// connection.
	if cfg.Patrol.Enabled {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go newPatrol(store, cfg.Patrol).Run(ctx)
	}

	ln, err := transport.Listen(socketPath)
	if err != nil {
		return err
	}
	defer ln.Close()
	log.Printf("opsagent serve: listening on %s (provider=%s model=%s)", socketPath, prov.Name(), prov.Model())

	for {
		nc, err := ln.Accept()
		if err != nil {
			return err
		}
		go srv.handle(nc)
	}
}

// resolveAPIKey prefers OPSAGENT_API_KEY (dev override) and otherwise reads
// the key from the encrypted keystore, so production keeps no plaintext key
// in config, environment, or the process list.
func resolveAPIKey(cfg config.Config) (string, error) {
	if cfg.APIKey != "" {
		return cfg.APIKey, nil
	}
	ks, err := secret.Open(cfg.KeystorePath, cfg.MasterKeyPath)
	if err != nil {
		return "", fmt.Errorf("open keystore: %w", err)
	}
	key, ok, err := ks.Get(apiKeySecretName)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no API key: set OPSAGENT_API_KEY or run `opsagent key set %s`", apiKeySecretName)
	}
	return key, nil
}

func (srv *server) handle(nc net.Conn) {
	defer nc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := transport.NewConn(nc)
	sess := newSession(srv.store, srv.historyDepth)
	if err := sess.hydrate(ctx); err != nil {
		log.Printf("load history: %v", err)
	}
	for {
		f, err := conn.ReadFrame()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.Printf("read frame: %v", err)
			}
			return
		}
		if f.Type != transport.TypeUserInput {
			writeError(conn, "unexpected frame type: "+string(f.Type))
			continue
		}
		text, err := f.Text()
		if err != nil {
			writeError(conn, "decode payload: "+err.Error())
			continue
		}
		sess.addUser(ctx, text)
		if err := srv.runTurn(ctx, conn, sess); err != nil {
			log.Printf("turn: %v", err)
			return
		}
	}
}

func writeError(conn *transport.Conn, msg string) {
	if ef, err := transport.TextFrame(transport.TypeError, msg); err == nil {
		_ = conn.WriteFrame(ef)
	}
}
