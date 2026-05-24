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
	"github.com/areming/ops-agent/internal/tools"
	"github.com/areming/ops-agent/internal/transport"
)

// Serve builds the model provider from configuration, then listens on
// the unix socket and serves connections until the listener errors.
func Serve(socketPath string) error {
	cfg := config.Load()
	if cfg.APIKey == "" {
		return fmt.Errorf("OPSAGENT_API_KEY is not set")
	}
	prov, err := model.New(cfg.Provider, cfg.APIKey, cfg.BaseURL, cfg.Model)
	if err != nil {
		return err
	}

	store, err := memory.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()

	reg := tools.NewRegistry(tools.Shell{}, tools.ReadFile{}, tools.WriteFile{})

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
		go handle(nc, prov, reg, store)
	}
}

func handle(nc net.Conn, prov model.Provider, reg *tools.Registry, store *memory.Store) {
	defer nc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := transport.NewConn(nc)
	sess := &session{}
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
		sess.addUser(text)
		if err := runTurn(ctx, conn, prov, reg, store, sess); err != nil {
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
