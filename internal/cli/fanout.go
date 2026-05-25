package cli

import (
	"fmt"
	"strings"
	"sync"

	"github.com/areming/ops-agent/internal/transport"
)

// maxFanOut bounds how many hosts run concurrently.
const maxFanOut = 5

// turnResult is one host's outcome of a fan-out run.
type turnResult struct {
	host     string
	text     string // the assistant's final text
	declined int    // actions that needed confirmation and were skipped
	err      error
}

// FanOut runs one instruction non-interactively across hosts over SSH,
// concurrently, then prints a per-host result and a summary. Actions the
// safety gate flags for confirmation are auto-declined and counted as
// "needs attention" unless approveAll is set. It returns an error when any
// host fails to complete.
func FanOut(hosts []string, instruction, remoteSocket, remoteBin string, approveAll bool) error {
	results := make([]turnResult, len(hosts))
	sem := make(chan struct{}, maxFanOut)
	var wg sync.WaitGroup
	for i, host := range hosts {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = fanOutHost(host, instruction, remoteSocket, remoteBin, approveAll)
		})
	}
	wg.Wait()
	return printFanOut(results)
}

// fanOutHost runs the instruction against a single host and collects its
// outcome; a connection or protocol failure is captured, never panics.
func fanOutHost(host, instruction, remoteSocket, remoteBin string, approveAll bool) turnResult {
	conn, cleanup, err := sshBridge(host, remoteSocket, remoteBin)
	if err != nil {
		return turnResult{host: host, err: err}
	}
	text, declined, terr := runOneTurn(conn, instruction, approveAll)
	if cerr := cleanup(); terr == nil {
		terr = cerr
	}
	return turnResult{host: host, text: strings.TrimSpace(text), declined: declined, err: terr}
}

// runOneTurn sends one instruction and drains the reply for a single turn,
// answering confirmation prompts with approveAll (declining when false).
func runOneTurn(conn *transport.Conn, instruction string, approveAll bool) (text string, declined int, err error) {
	uf, ferr := transport.TextFrame(transport.TypeUserInput, instruction)
	if ferr != nil {
		return "", 0, ferr
	}
	if werr := conn.WriteFrame(uf); werr != nil {
		return "", 0, werr
	}

	var b strings.Builder
	for {
		f, rerr := conn.ReadFrame()
		if rerr != nil {
			return b.String(), declined, rerr
		}
		switch f.Type {
		case transport.TypeAssistantDelta:
			s, _ := f.Text()
			b.WriteString(s)
		case transport.TypeConfirmRequest:
			if !approveAll {
				declined++
			}
			reply, perr := transport.PayloadFrame(transport.TypeConfirmReply,
				transport.ConfirmReplyPayload{Approved: approveAll})
			if perr != nil {
				return b.String(), declined, perr
			}
			if werr := conn.WriteFrame(reply); werr != nil {
				return b.String(), declined, werr
			}
		case transport.TypeError:
			s, _ := f.Text()
			b.WriteString("\n[error] " + s)
		case transport.TypeDone:
			return b.String(), declined, nil
		}
	}
}

// printFanOut prints each host's result grouped, then a one-line summary,
// and returns an error if any host failed.
func printFanOut(results []turnResult) error {
	var ok, attention, failed int
	for _, r := range results {
		fmt.Printf("\n=== %s ===\n", r.host)
		if r.err != nil {
			fmt.Printf("[failed] %v\n", r.err)
			failed++
			continue
		}
		if r.text != "" {
			fmt.Println(r.text)
		}
		if r.declined > 0 {
			fmt.Printf("[needs attention] %d action(s) required confirmation and were skipped\n", r.declined)
			attention++
		} else {
			ok++
		}
	}
	fmt.Printf("\n— %d ok, %d need attention, %d failed (of %d hosts)\n",
		ok, attention, failed, len(results))
	if failed > 0 {
		return fmt.Errorf("%d host(s) failed", failed)
	}
	return nil
}
