package agent

import (
	"context"
	"strings"

	"github.com/areming/ops-agent/internal/model"
	"github.com/areming/ops-agent/internal/transport"
)

// runTurn streams one assistant reply for the current session state:
// call the provider, forward text deltas to the client, accumulate the
// reply into history, and finish with Done. M1 has no tool-call cycle;
// that loop is added in M2.
func runTurn(ctx context.Context, conn *transport.Conn, prov model.Provider, sess *session) error {
	ch, err := prov.StreamChat(ctx, model.ChatRequest{
		System:   systemPrompt,
		Messages: sess.msgs,
	})
	if err != nil {
		writeError(conn, err.Error())
		return conn.WriteFrame(transport.Frame{Type: transport.TypeDone})
	}

	var reply strings.Builder
	for ev := range ch {
		switch ev.Type {
		case model.EventTextDelta:
			reply.WriteString(ev.Text)
			df, ferr := transport.TextFrame(transport.TypeAssistantDelta, ev.Text)
			if ferr != nil {
				return ferr
			}
			if werr := conn.WriteFrame(df); werr != nil {
				return werr
			}
		case model.EventError:
			writeError(conn, ev.Err.Error())
		}
	}

	sess.addAssistant(reply.String())
	return conn.WriteFrame(transport.Frame{Type: transport.TypeDone})
}
