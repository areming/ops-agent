// Package agent is the resident daemon: the "brain" that will host the
// model loop, tools, safety gate and patrol. M0 ships only a skeleton
// whose "brain" echoes input back, proving the transport round-trip
// without any model, tool, or system access.
package agent

import (
	"errors"
	"io"
	"log"
	"net"

	"github.com/areming/ops-agent/internal/transport"
)

// Serve listens on the unix socket at socketPath and serves connections
// until the listener errors or is closed.
func Serve(socketPath string) error {
	ln, err := transport.Listen(socketPath)
	if err != nil {
		return err
	}
	defer ln.Close()
	log.Printf("opsagent serve: listening on %s", socketPath)

	for {
		nc, err := ln.Accept()
		if err != nil {
			return err
		}
		go handle(nc)
	}
}

func handle(nc net.Conn) {
	defer nc.Close()
	conn := transport.NewConn(nc)
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
		echo(conn, text)
	}
}

// echo is the M0 stand-in for the agent loop: it streams the input back
// rune by rune as assistant deltas, then signals Done.
func echo(conn *transport.Conn, text string) {
	for _, r := range text {
		df, err := transport.TextFrame(transport.TypeAssistantDelta, string(r))
		if err != nil {
			return
		}
		if err := conn.WriteFrame(df); err != nil {
			return
		}
	}
	_ = conn.WriteFrame(transport.Frame{Type: transport.TypeDone})
}

func writeError(conn *transport.Conn, msg string) {
	if ef, err := transport.TextFrame(transport.TypeError, msg); err == nil {
		_ = conn.WriteFrame(ef)
	}
}
