package model

import (
	"bufio"
	"io"
	"strings"
)

// sseScanner yields the payload of each `data:` line from a Server-Sent
// Events stream. It skips other fields (e.g. Anthropic's `event:` lines)
// and stops at an explicit "[DONE]" sentinel or end of stream.
type sseScanner struct {
	sc *bufio.Scanner
}

func newSSE(r io.Reader) *sseScanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20) // allow long SSE lines
	return &sseScanner{sc: sc}
}

// next returns the next data payload, or ok=false at end of stream.
func (s *sseScanner) next() (data string, ok bool) {
	for s.sc.Scan() {
		line := s.sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data = strings.TrimSpace(line[len("data:"):])
		if data == "[DONE]" {
			return "", false
		}
		return data, true
	}
	return "", false
}

func (s *sseScanner) err() error { return s.sc.Err() }
