package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const anthropicVersion = "2023-06-01"

// Anthropic talks to the Claude Messages API.
type Anthropic struct {
	apiKey    string
	baseURL   string
	model     string
	maxTokens int
	http      *http.Client
}

func NewAnthropic(apiKey, baseURL, model string) *Anthropic {
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	return &Anthropic{
		apiKey:    apiKey,
		baseURL:   strings.TrimRight(baseURL, "/"),
		model:     model,
		maxTokens: 4096,
		http:      &http.Client{},
	}
}

func (a *Anthropic) Name() string  { return "anthropic" }
func (a *Anthropic) Model() string { return a.model }

func (a *Anthropic) StreamChat(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error) {
	payload := map[string]any{
		"model":      a.model,
		"max_tokens": a.maxTokens,
		"messages":   buildAnthropicMessages(req),
		"stream":     true,
	}
	if req.System != "" {
		payload["system"] = req.System
	}
	if len(req.Tools) > 0 {
		payload["tools"] = anthropicTools(req.Tools)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, a.baseURL+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", anthropicVersion)

	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("anthropic: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	ch := make(chan ChatEvent)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		streamAnthropic(ctx, resp.Body, ch)
	}()
	return ch, nil
}

func streamAnthropic(ctx context.Context, r io.Reader, ch chan<- ChatEvent) {
	sse := newSSE(r)
	byIndex := map[int]*toolAcc{}
	var order []int

	for {
		data, ok := sse.next()
		if !ok {
			break
		}
		var ev struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "content_block_start":
			if ev.ContentBlock.Type == "tool_use" {
				byIndex[ev.Index] = &toolAcc{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
				order = append(order, ev.Index)
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				if ev.Delta.Text != "" && !send(ctx, ch, ChatEvent{Type: EventTextDelta, Text: ev.Delta.Text}) {
					return
				}
			case "input_json_delta":
				if acc := byIndex[ev.Index]; acc != nil {
					acc.args.WriteString(ev.Delta.PartialJSON)
				}
			}
		}
	}
	if err := sse.err(); err != nil {
		send(ctx, ch, ChatEvent{Type: EventError, Err: err})
		return
	}
	for _, idx := range order {
		acc := byIndex[idx]
		if !send(ctx, ch, ChatEvent{Type: EventToolCall, Tool: &ToolCall{
			ID:        acc.id,
			Name:      acc.name,
			Arguments: rawOrEmpty(acc.args.String()),
		}}) {
			return
		}
	}
	send(ctx, ch, ChatEvent{Type: EventDone})
}

func anthropicTools(ts []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": json.RawMessage(t.Schema),
		})
	}
	return out
}

// buildAnthropicMessages translates provider-agnostic messages into the
// Messages API shape: assistant tool calls become tool_use content
// blocks, and consecutive tool results are grouped into a single user
// message of tool_result blocks (the API requires alternating roles).
func buildAnthropicMessages(req ChatRequest) []map[string]any {
	var msgs []map[string]any
	for i := 0; i < len(req.Messages); {
		m := req.Messages[i]
		switch m.Role {
		case RoleTool:
			var blocks []map[string]any
			for i < len(req.Messages) && req.Messages[i].Role == RoleTool {
				tm := req.Messages[i]
				blocks = append(blocks, map[string]any{
					"type":        "tool_result",
					"tool_use_id": tm.ToolCallID,
					"content":     tm.Content,
				})
				i++
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": blocks})
			continue
		case RoleAssistant:
			if len(m.ToolCalls) > 0 {
				var blocks []map[string]any
				if m.Content != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
				}
				for _, tc := range m.ToolCalls {
					blocks = append(blocks, map[string]any{
						"type":  "tool_use",
						"id":    tc.ID,
						"name":  tc.Name,
						"input": rawOrEmpty(string(tc.Arguments)),
					})
				}
				msgs = append(msgs, map[string]any{"role": "assistant", "content": blocks})
			} else {
				msgs = append(msgs, map[string]any{"role": "assistant", "content": m.Content})
			}
		default:
			msgs = append(msgs, map[string]any{"role": string(m.Role), "content": m.Content})
		}
		i++
	}
	return msgs
}
