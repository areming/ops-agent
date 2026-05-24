package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAnthropicStreamChat feeds canned Messages-API SSE events and
// checks only text_delta content is surfaced, ending with Done.
func TestAnthropicStreamChat(t *testing.T) {
	const stream = `event: message_start
data: {"type":"message_start"}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"Hel"}}

event: content_block_delta
data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}

event: message_stop
data: {"type":"message_stop"}

`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "k" {
			t.Errorf("x-api-key = %q, want k", got)
		}
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path = %q, want /v1/messages", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	}))
	defer srv.Close()

	p := NewAnthropic("k", srv.URL, "claude-x")
	ch, err := p.StreamChat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var got strings.Builder
	var done bool
	for ev := range ch {
		switch ev.Type {
		case EventTextDelta:
			got.WriteString(ev.Text)
		case EventError:
			t.Fatalf("unexpected error event: %v", ev.Err)
		case EventDone:
			done = true
		}
	}
	if got.String() != "Hello" {
		t.Errorf("text = %q, want %q", got.String(), "Hello")
	}
	if !done {
		t.Error("missing Done event")
	}
}
