// Package agent is the resident daemon: the "brain" that hosts the model
// loop and (in later milestones) tools, safety gate and patrol. M1 wires
// a real model provider behind the conversation; there is still no tool
// execution or system access.
package agent

import (
	"context"
	"encoding/json"
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
	keyRef := cfg.KeyRef
	if keyRef == "" {
		keyRef = apiKeySecretName
	}
	key, ok, err := ks.Get(keyRef)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("no API key: set OPSAGENT_API_KEY or run `ops key set %s`", keyRef)
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
	case transport.CmdModelList, "models", "model":
		text, err := srv.modelList()
		return controlReply(conn, text, errString(err))
	case transport.CmdModelSwitch:
		text, err := srv.modelSwitch(req.Arg)
		return controlReply(conn, text, errString(err))
	case transport.CmdModelAdd:
		text, err := srv.modelAdd(req.Arg)
		return controlReply(conn, text, errString(err))
	case transport.CmdModelDelete:
		text, err := srv.modelDelete(req.Arg)
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

// modelList returns the saved profiles (JSON ModelListReply) for the /model
// panel, marking the active one. It applies to whichever agent this connection
// talks to (local or the remote daemon).
func (srv *server) modelList() (string, error) {
	profs, active := config.ListProfiles(srv.stateDir())
	var reply transport.ModelListReply
	for _, p := range profs {
		reply.Profiles = append(reply.Profiles, transport.ModelProfile{
			ID: p.ID, Label: p.Label, Provider: p.Provider, Model: p.Model,
			BaseURL: p.BaseURL, Active: p.ID == active,
		})
	}
	b, err := json.Marshal(reply)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// modelSwitch makes the profile named by arg (its id, or a matching model
// name/label) active and rebuilds the provider so the choice takes effect now
// and survives a restart.
func (srv *server) modelSwitch(arg string) (string, error) {
	if arg == "" {
		return "", fmt.Errorf("usage: /model <名称>")
	}
	id, err := resolveProfileID(srv.stateDir(), arg)
	if err != nil {
		return "", err
	}
	if err := config.SetActive(srv.stateDir(), id); err != nil {
		return "", err
	}
	if err := srv.reloadActive(); err != nil {
		return "", err
	}
	return "已切换模型 → " + srv.provider().Model(), nil
}

// modelAdd seals the new profile's API key and appends the profile (which
// becomes active), then rebuilds the provider. On a seal failure it rolls back
// the dangling profile so the list never points at a missing key.
func (srv *server) modelAdd(arg string) (string, error) {
	var req transport.ModelAddRequest
	if err := json.Unmarshal([]byte(arg), &req); err != nil {
		return "", fmt.Errorf("bad add request: %w", err)
	}
	if req.Provider == "" || req.Model == "" || req.Key == "" {
		return "", fmt.Errorf("provider / model / key 不能为空")
	}
	srv.mu.Lock()
	cfg := srv.cfg
	srv.mu.Unlock()

	stored, err := config.AddProfile(cfg.StateDir, config.Profile{
		Label: req.Label, Provider: req.Provider, Model: req.Model, BaseURL: req.BaseURL,
	})
	if err != nil {
		return "", err
	}
	ks, err := secret.Open(cfg.KeystorePath, cfg.MasterKeyPath)
	if err != nil {
		_, _ = config.DeleteProfile(cfg.StateDir, stored.ID)
		return "", err
	}
	if err := ks.Set(stored.KeyRef, req.Key); err != nil {
		_, _ = config.DeleteProfile(cfg.StateDir, stored.ID)
		return "", err
	}
	if err := srv.reloadActive(); err != nil {
		return "", err
	}
	return "已添加并切换 → " + stored.Label, nil
}

// modelDelete removes the profile named by arg and its sealed key. It refuses
// to delete the active profile (switch away first) or the last remaining one.
func (srv *server) modelDelete(arg string) (string, error) {
	stateDir := srv.stateDir()
	id, err := resolveProfileID(stateDir, arg)
	if err != nil {
		return "", err
	}
	profs, active := config.ListProfiles(stateDir)
	if len(profs) <= 1 {
		return "", fmt.Errorf("这是最后一个模型配置，不能删除（至少保留一个）")
	}
	if id == active {
		return "", fmt.Errorf("不能删除当前在用的模型，先切到别的再删")
	}
	removed, err := config.DeleteProfile(stateDir, id)
	if err != nil {
		return "", err
	}
	srv.mu.Lock()
	cfg := srv.cfg
	srv.mu.Unlock()
	if removed.KeyRef != "" && removed.KeyRef != config.LegacyKeyName {
		if ks, err := secret.Open(cfg.KeystorePath, cfg.MasterKeyPath); err == nil {
			_ = ks.Delete(removed.KeyRef)
		}
	}
	return "已删除模型配置 " + removed.Label, nil
}

// reloadActive re-resolves the active profile from config, rebuilds the
// provider, and swaps both in under the lock. Used after switch/add.
func (srv *server) reloadActive() error {
	cfg := config.Load()
	apiKey, err := resolveAPIKey(cfg)
	if err != nil {
		return err
	}
	prov, err := model.New(cfg.Provider, apiKey, cfg.BaseURL, cfg.Model)
	if err != nil {
		return err
	}
	srv.mu.Lock()
	srv.cfg = cfg
	srv.prov = prov
	srv.mu.Unlock()
	return nil
}

// stateDir returns the agent's state directory under the lock.
func (srv *server) stateDir() string {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.cfg.StateDir
}

// resolveProfileID matches arg against a saved profile by id, then
// case-insensitively by model name or label.
func resolveProfileID(stateDir, arg string) (string, error) {
	profs, _ := config.ListProfiles(stateDir)
	for _, p := range profs {
		if p.ID == arg {
			return p.ID, nil
		}
	}
	la := strings.ToLower(strings.TrimSpace(arg))
	for _, p := range profs {
		if strings.ToLower(p.Model) == la || strings.ToLower(p.Label) == la {
			return p.ID, nil
		}
	}
	return "", fmt.Errorf("没有匹配的模型配置：%q", arg)
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

func (c *connInteraction) onToolOutput(chunk string) {
	if of, err := transport.TextFrame(transport.TypeToolOutput, chunk); err == nil {
		_ = c.conn.WriteFrame(of)
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
