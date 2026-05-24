package model

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenAIStreamChat feeds canned OpenAI-style SSE chunks through a
// test server and checks the adapter assembles the text and finishes
// with Done. No network access.
func TestOpenAIStreamChat(t *testing.T) {
	const stream = `data: {"choices":[{"delta":{"content":"Hel"}}]}

data: {"choices":[{"delta":{"content":"lo"}}]}

data: {"choices":[{"delta":{}}]}

data: [DONE]

`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer k")
		}
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %q, want /chat/completions", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	}))
	defer srv.Close()

	p := NewOpenAI("k", srv.URL, "deepseek-chat")
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

// TestOpenAIStreamChatHTTPError surfaces a non-200 as a setup error.
func TestOpenAIStreamChatHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad key"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()

	p := NewOpenAI("k", srv.URL, "m")
	if _, err := p.StreamChat(context.Background(), ChatRequest{}); err == nil {
		t.Fatal("expected error on 401, got nil")
	}
}
