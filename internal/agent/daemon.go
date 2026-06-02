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
	"strconv"
	"strings"
	"sync"

	"github.com/areming/ops-agent/internal/config"
	"github.com/areming/ops-agent/internal/memory"
	"github.com/areming/ops-agent/internal/model"
	"github.com/areming/ops-agent/internal/safety"
	"github.com/areming/ops-agent/internal/secret"
	"github.com/areming/ops-agent/internal/tools"
	"github.com/areming/ops-agent/internal/transport"
)

// apiKeySecretName is the keystore entry holding the model provider's API
// key when it is not supplied via OPSAGENT_API_KEY.
const apiKeySecretName = "api_key"

// server holds the per-process dependencies shared across connections.
type server struct {
	eng          *engine
	store        *memory.Store
	systemPrompt string // base prompt with knowledge files folded in
	historyDepth int

	// mu guards cfg and prov, which /models swaps at runtime while chat
	// turns read them.
	mu   sync.Mutex
	cfg  config.Config // retained so /models can rebuild the provider and persist
	prov model.Provider
}

// provider returns the current model provider under the lock.
func (srv *server) provider() model.Provider {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.prov
}

// newServer builds the per-process dependencies (provider, state store,
// knowledge-folded prompt, tool engine) shared across connections. The
// caller owns the returned server and must Close it to release the store.
func newServer(cfg config.Config) (*server, error) {
	apiKey, err := resolveAPIKey(cfg)
	if err != nil {
		return nil, err
	}
	prov, err := model.New(cfg.Provider, apiKey, cfg.BaseURL, cfg.Model)
	if err != nil {
		return nil, err
	}
	store, err := memory.Open(cfg.DBPath)
	if err != nil {
		return nil, err
	}
	knowledge, err := memory.LoadKnowledge(cfg.KnowledgeDir)
	if err != nil {
		store.Close()
		return nil, err
	}
	eng := &engine{
		reg:   tools.NewRegistry(tools.Shell{}, tools.ReadFile{}, tools.WriteFile{}),
		store: store,
	}
	return &server{
		cfg:          cfg,
		prov:         prov,
		eng:          eng,
		store:        store,
		systemPrompt: composeSystemPrompt(knowledge),
		historyDepth: cfg.HistoryDepth,
	}, nil
}

// Close releases the server's state store.
func (srv *server) Close() error { return srv.store.Close() }

// Serve builds the agent from configuration, then listens on the unix socket
// and serves connections until the listener errors. The background patrol
// loop runs for the daemon's lifetime, independent of any CLI connection.
func Serve(socketPath string) error {
	cfg := config.Load()
	srv, err := newServer(cfg)
	if err != nil {
		return err
	}
	defer srv.Close()

	if cfg.Patrol.Enabled {
		apiKey, err := resolveAPIKey(cfg)
		if err != nil {
			return err
		}
		diagProv, err := model.New(cfg.DiagProvider, apiKey, cfg.DiagBaseURL, cfg.DiagModel)
		if err != nil {
			return fmt.Errorf("diagnosis provider: %w", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go newPatrol(srv.eng, diagProv, cfg.Patrol).Run(ctx)
	}

	ln, err := transport.Listen(socketPath)
	if err != nil {
		return err
	}
	defer ln.Close()
	log.Printf("opsagent serve: listening on %s (provider=%s model=%s)", socketPath, srv.prov.Name(), srv.prov.Model())

	for {
		nc, err := ln.Accept()
		if err != nil {
			return err
		}
		go srv.handle(nc)
	}
}

// LocalSession runs one in-process conversation over nc, building a fresh
// agent from configuration and closing it when the connection ends. It is
// the agent side of `ops` (no args): the CLI drives the other end of an
// in-memory pipe, so the local conversation reuses the exact frame protocol
// and turn loop the SSH path uses. Patrol is intentionally not started — a
// local interactive session is ephemeral.
func LocalSession(nc net.Conn) error {
	srv, err := newServer(config.Load())
	if err != nil {
		return err
	}
	defer srv.Close()
	srv.handle(nc)
	return nil
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
		return "", fmt.Errorf("no API key: set OPSAGENT_API_KEY or run `ops key set %s`", apiKeySecretName)
	}
	return key, nil
}

func (srv *server) handle(nc net.Conn) {
	defer nc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn := transport.NewConn(nc)
	sess := newInteractiveSession(srv.store, srv.historyDepth)
	if err := sess.hydrate(ctx); err != nil {
		log.Printf("load history: %v", err)
	}

	// One goroutine owns every frame read for the connection's life. A running
	// turn must watch for a Cancel frame while it streams, and a second reader
	// on the same Conn would race this loop; instead all frames flow through
	// `frames`, consumed by this loop between turns and by chatTurn during one.
	// `quit` lets the reader exit if it is parked on a send when handle returns.
	frames := make(chan transport.Frame)
	quit := make(chan struct{})
	defer close(quit)
	go func() {
		defer close(frames)
		for {
			f, err := conn.ReadFrame()
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.Printf("read frame: %v", err)
				}
				return
			}
			select {
			case frames <- f:
			case <-quit:
				return
			}
		}
	}()

	for f := range frames {
		switch f.Type {
		case transport.TypeUserInput:
			text, err := f.Text()
			if err != nil {
				writeError(conn, "decode payload: "+err.Error())
				continue
			}
			sess.addUser(ctx, text)
			if err := srv.chatTurn(ctx, conn, sess, frames); err != nil {
				log.Printf("turn: %v", err)
				return
			}
		case transport.TypeControlRequest:
			if err := srv.handleControl(ctx, conn, sess, f); err != nil {
				log.Printf("control: %v", err)
				return
			}
		case transport.TypeConfirmReply, transport.TypeCancel:
			// A turn-scoped frame that arrived just as the turn ended (e.g. a
			// late Cancel). No turn is running now, so there is nothing to do.
		default:
			writeError(conn, "unexpected frame type: "+string(f.Type))
		}
	}
}

// handleControl answers an in-conversation slash command with a single
// control reply. It is invoked from the same goroutine as the read loop, so
// /clear can reset the session in place.
func (srv *server) handleControl(ctx context.Context, conn *transport.Conn, sess *session, f transport.Frame) error {
	var req transport.ControlRequestPayload
	if err := f.Decode(&req); err != nil {
		return controlReply(conn, "", "decode control: "+err.Error())
	}
	switch req.Cmd {
	case "models":
		text, err := srv.controlModels(req.Arg)
		return controlReply(conn, text, errString(err))
	case "logs":
		text, err := srv.controlLogs(ctx, req.Arg)
		return controlReply(conn, text, errString(err))
	case "clear":
		sess.msgs = nil
		return controlReply(conn, "对话已清空。", "")
	case "yolo":
		return controlReply(conn, srv.controlYolo(sess, req.Arg), "")
	default:
		return controlReply(conn, "", "unknown control command: "+req.Cmd)
	}
}

// controlModels lists known models (empty arg) or switches to arg, rebuilding
// the provider and persisting the choice so it survives a restart. The switch
// applies to whichever agent this connection talks to (local or remote).
func (srv *server) controlModels(arg string) (string, error) {
	srv.mu.Lock()
	cfg := srv.cfg
	current := srv.prov.Model()
	srv.mu.Unlock()

	if arg == "" {
		return formatModelList(cfg.Provider, current), nil
	}

	apiKey, err := resolveAPIKey(cfg)
	if err != nil {
		return "", err
	}
	prov, err := model.New(cfg.Provider, apiKey, cfg.BaseURL, arg)
	if err != nil {
		return "", err
	}
	cfg.Model = arg

	srv.mu.Lock()
	srv.prov = prov
	srv.cfg = cfg
	srv.mu.Unlock()

	if err := config.Save(cfg); err != nil {
		return "", err
	}
	return fmt.Sprintf("已切换模型 → %s", arg), nil
}

// controlYolo toggles or sets the session's auto-approve mode. Arg "on"/"off"
// sets explicitly; empty toggles. Danger commands still require confirmation.
func (srv *server) controlYolo(sess *session, arg string) string {
	switch strings.ToLower(strings.TrimSpace(arg)) {
	case "on":
		sess.yolo = true
	case "off":
		sess.yolo = false
	default:
		sess.yolo = !sess.yolo
	}
	if sess.yolo {
		return "自动放行：开（默认）。非危险操作直接执行，危险命令仍会确认。/yolo off 改为逐条确认。"
	}
	return "自动放行：关。本会话写操作恢复逐条确认。/yolo on 恢复默认。"
}

// controlLogs returns the most recent audit entries, mirroring the `logs`
// subcommand's format.
func (srv *server) controlLogs(ctx context.Context, arg string) (string, error) {
	n := 20
	if arg != "" {
		if v, err := strconv.Atoi(arg); err == nil && v > 0 {
			n = v
		}
	}
	entries, err := srv.store.RecentAudit(ctx, n)
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "(暂无审计记录)", nil
	}
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s  %s  [%s/%s] exit=%d  %s\n", e.CreatedAt, e.Source, e.Decision, e.Risk, e.ExitCode, e.Command)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// formatModelList renders the known models for a provider, marking the
// current one.
func formatModelList(provider, current string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "provider=%s  当前=%s\n已知模型：\n", provider, current)
	for _, m := range model.KnownModels(provider) {
		mark := "  "
		if m == current {
			mark = "* "
		}
		fmt.Fprintf(&b, "%s%s\n", mark, m)
	}
	b.WriteString("切换：/models <名称>")
	return b.String()
}

// controlReply sends one control reply frame.
func controlReply(conn *transport.Conn, text, errMsg string) error {
	f, err := transport.PayloadFrame(transport.TypeControlReply, transport.ControlReplyPayload{Text: text, Err: errMsg})
	if err != nil {
		return err
	}
	return conn.WriteFrame(f)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// chatTurn runs one turn for a CLI connection and closes it with a Done
// frame. It runs under a cancelable child context: a watcher goroutine drains
// turn-scoped frames off `frames` for the turn's duration — a Cancel frame
// cancels the context (stopping the model stream and any running command), and
// a ConfirmReply is handed to the confirm handshake. The watcher is torn down
// before chatTurn returns so the handle loop reclaims `frames`. It returns an
// error only when the connection can no longer be written, ending the session.
func (srv *server) chatTurn(ctx context.Context, conn *transport.Conn, sess *session, frames <-chan transport.Frame) error {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	replyCh := make(chan transport.ConfirmReplyPayload, 1)
	stop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		for {
			select {
			case <-stop:
				return
			case f, ok := <-frames:
				if !ok {
					cancel() // connection closed mid-turn
					return
				}
				switch f.Type {
				case transport.TypeCancel:
					cancel()
				case transport.TypeConfirmReply:
					var p transport.ConfirmReplyPayload
					if err := f.Decode(&p); err == nil {
						select {
						case replyCh <- p:
						default:
						}
					}
				}
			}
		}
	}()

	ia := &connInteraction{conn: conn, store: srv.store, sess: sess, replyCh: replyCh, ctx: turnCtx}
	runErr := srv.eng.runTurn(turnCtx, srv.provider(), srv.systemPrompt, ia, sess)

	// Stop the watcher before returning. The client never sends the next
	// UserInput until it sees Done (below), so no input frame can be in flight
	// here; at worst the watcher consumes a late Cancel, which is harmless.
	close(stop)
	<-watchDone

	if runErr != nil {
		return runErr
	}
	return conn.WriteFrame(transport.Frame{Type: transport.TypeDone})
}

func writeError(conn *transport.Conn, msg string) {
	if ef, err := transport.TextFrame(transport.TypeError, msg); err == nil {
		_ = conn.WriteFrame(ef)
	}
}

// connInteraction is the chat-path interaction: it streams frames to a CLI
// connection and asks the human to confirm flagged actions.
type connInteraction struct {
	conn  *transport.Conn
	store *memory.Store
	sess  *session
	// replyCh delivers the user's confirm decision, demuxed off the connection
	// by chatTurn's watcher. ctx is the turn context; its cancellation aborts a
	// pending confirm so an ESC during a prompt stops the turn.
	replyCh <-chan transport.ConfirmReplyPayload
	ctx     context.Context
}

func (connInteraction) source() string { return "chat" }

func (c *connInteraction) onDelta(text string) error {
	df, err := transport.TextFrame(transport.TypeAssistantDelta, text)
	if err != nil {
		return err
	}
	return c.conn.WriteFrame(df)
}

func (c *connInteraction) onToolStart(tool, command string) {
	if sf, err := transport.PayloadFrame(transport.TypeToolStart, transport.ToolStartPayload{
		Tool:    tool,
		Command: command,
	}); err == nil {
		_ = c.conn.WriteFrame(sf)
	}
}

func (c *connInteraction) onError(msg string) { writeError(c.conn, msg) }

func (c *connInteraction) confirm(tool, command string, v safety.Verdict) (bool, error) {
	// Hard danger rules always prompt, regardless of yolo or prior approval.
	if !v.Danger {
		if c.sess.yolo || c.sess.approved[command] {
			return true, nil
		}
	}
	req, err := transport.PayloadFrame(transport.TypeConfirmRequest, transport.ConfirmRequestPayload{
		Tool:    tool,
		Command: command,
		Risk:    v.Risk,
		Reason:  v.Reason,
	})
	if err != nil {
		return false, err
	}
	if err := c.conn.WriteFrame(req); err != nil {
		return false, err
	}
	select {
	case <-c.ctx.Done():
		// Canceled while waiting on the user. Report it as an error so the turn
		// stops cleanly instead of recording a false denial for this command.
		return false, c.ctx.Err()
	case reply := <-c.replyCh:
		if reply.Approved && reply.Always && !v.Danger {
			c.sess.approveAlways(command)
		}
		return reply.Approved, nil
	}
}

func (c *connInteraction) declineRun(ctx context.Context, command string, v safety.Verdict) string {
	audit(ctx, c.store, "chat", command, v, "denied", 0, "")
	return "user denied this action; it was not run"
}
