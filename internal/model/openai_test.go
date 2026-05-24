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

// TestOpenAIStreamChatToolCall checks that tool_call fragments split
// across chunks are accumulated into one EventToolCall.
func TestOpenAIStreamChatToolCall(t *testing.T) {
	const stream = `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"run_command","arguments":"{\"comm"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"and\":\"ls\"}"}}]}}]}

data: [DONE]

`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	}))
	defer srv.Close()

	p := NewOpenAI("k", srv.URL, "m")
	ch, err := p.StreamChat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "list files"}},
		Tools:    []Tool{{Name: "run_command", Description: "run", Schema: []byte(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var got *ToolCall
	for ev := range ch {
		if ev.Type == EventToolCall {
			got = ev.Tool
		}
	}
	if got == nil {
		t.Fatal("no tool call event")
	}
	if got.ID != "call_1" || got.Name != "run_command" {
		t.Errorf("got id=%q name=%q", got.ID, got.Name)
	}
	if string(got.Arguments) != `{"command":"ls"}` {
		t.Errorf("arguments = %s, want {\"command\":\"ls\"}", got.Arguments)
	}
}

// TestOpenAIStreamChatReasoning checks that streamed reasoning_content
// (a thinking-model field) is surfaced as EventReasoningDelta, separate
// from the visible text.
func TestOpenAIStreamChatReasoning(t *testing.T) {
	const stream = `data: {"choices":[{"delta":{"reasoning_content":"let me "}}]}

data: {"choices":[{"delta":{"reasoning_content":"think"}}]}

data: {"choices":[{"delta":{"content":"answer"}}]}

data: [DONE]

`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(stream))
	}))
	defer srv.Close()

	p := NewOpenAI("k", srv.URL, "deepseek-v4-pro")
	ch, err := p.StreamChat(context.Background(), ChatRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var reasoning, text strings.Builder
	for ev := range ch {
		switch ev.Type {
		case EventReasoningDelta:
			reasoning.WriteString(ev.Text)
		case EventTextDelta:
			text.WriteString(ev.Text)
		case EventError:
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	if reasoning.String() != "let me think" {
		t.Errorf("reasoning = %q, want %q", reasoning.String(), "let me think")
	}
	if text.String() != "answer" {
		t.Errorf("text = %q, want %q", text.String(), "answer")
	}
}

// TestBuildOpenAIMessagesReasoning checks reasoning_content is replayed on
// an assistant message that has it, and omitted when it is empty.
func TestBuildOpenAIMessagesReasoning(t *testing.T) {
	msgs := buildOpenAIMessages(ChatRequest{Messages: []Message{
		{Role: RoleAssistant, Content: "a", Reasoning: "because"},
		{Role: RoleAssistant, Content: "b"},
	}})
	if len(msgs) != 2 {
		t.Fatalf("got %d messages, want 2", len(msgs))
	}
	if got := msgs[0]["reasoning_content"]; got != "because" {
		t.Errorf("msg[0] reasoning_content = %v, want %q", got, "because")
	}
	if _, ok := msgs[1]["reasoning_content"]; ok {
		t.Error("msg[1] should not carry reasoning_content when empty")
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
