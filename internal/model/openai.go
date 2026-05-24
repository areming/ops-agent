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

// OpenAI talks to the OpenAI chat-completions API and any endpoint that
// is wire-compatible with it (DeepSeek, local gateways, most domestic
// providers) via a configurable base URL.
type OpenAI struct {
	apiKey  string
	baseURL string
	model   string
	http    *http.Client
}

func NewOpenAI(apiKey, baseURL, model string) *OpenAI {
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &OpenAI{
		apiKey:  apiKey,
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		http:    &http.Client{},
	}
}

func (o *OpenAI) Name() string  { return "openai" }
func (o *OpenAI) Model() string { return o.model }

func (o *OpenAI) StreamChat(ctx context.Context, req ChatRequest) (<-chan ChatEvent, error) {
	payload := map[string]any{
		"model":    o.model,
		"messages": buildOpenAIMessages(req),
		"stream":   true,
	}
	if len(req.Tools) > 0 {
		payload["tools"] = openAITools(req.Tools)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := o.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, fmt.Errorf("openai: status %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}

	ch := make(chan ChatEvent)
	go func() {
		defer resp.Body.Close()
		defer close(ch)
		streamOpenAI(ctx, resp.Body, ch)
	}()
	return ch, nil
}

// toolAcc accumulates a streamed tool call whose id/name/arguments arrive
// across multiple chunks.
type toolAcc struct {
	id   string
	name string
	args strings.Builder
}

func streamOpenAI(ctx context.Context, r io.Reader, ch chan<- ChatEvent) {
	sse := newSSE(r)
	byIndex := map[int]*toolAcc{}
	var order []int

	for {
		data, ok := sse.next()
		if !ok {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil || len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.ReasoningContent != "" && !send(ctx, ch, ChatEvent{Type: EventReasoningDelta, Text: d.ReasoningContent}) {
			return
		}
		if d.Content != "" && !send(ctx, ch, ChatEvent{Type: EventTextDelta, Text: d.Content}) {
			return
		}
		for _, tc := range d.ToolCalls {
			acc := byIndex[tc.Index]
			if acc == nil {
				acc = &toolAcc{}
				byIndex[tc.Index] = acc
				order = append(order, tc.Index)
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			acc.args.WriteString(tc.Function.Arguments)
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

func openAITools(ts []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(ts))
	for _, t := range ts {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  json.RawMessage(t.Schema),
			},
		})
	}
	return out
}

func buildOpenAIMessages(req ChatRequest) []map[string]any {
	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleTool:
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": m.ToolCallID,
				"content":      m.Content,
			})
		case RoleAssistant:
			am := map[string]any{"role": "assistant", "content": m.Content}
			// Thinking models require their prior reasoning_content to be
			// echoed back; absent for ordinary models, so never sent then.
			if m.Reasoning != "" {
				am["reasoning_content"] = m.Reasoning
			}
			if len(m.ToolCalls) > 0 {
				tcs := make([]map[string]any, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					tcs = append(tcs, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": string(tc.Arguments),
						},
					})
				}
				am["tool_calls"] = tcs
				if m.Content == "" {
					am["content"] = nil
				}
			}
			msgs = append(msgs, am)
		default:
			msgs = append(msgs, map[string]any{"role": string(m.Role), "content": m.Content})
		}
	}
	return msgs
}
