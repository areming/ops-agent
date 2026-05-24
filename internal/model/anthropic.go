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
	msgs := make([]map[string]string, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": string(m.Role), "content": m.Content})
	}
	payload := map[string]any{
		"model":      a.model,
		"max_tokens": a.maxTokens,
		"messages":   msgs,
		"stream":     true,
	}
	if req.System != "" {
		payload["system"] = req.System
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
		sse := newSSE(resp.Body)
		for {
			data, ok := sse.next()
			if !ok {
				break
			}
			var ev struct {
				Type  string `json:"type"`
				Delta struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			if ev.Type == "content_block_delta" && ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				select {
				case ch <- ChatEvent{Type: EventTextDelta, Text: ev.Delta.Text}:
				case <-ctx.Done():
					return
				}
			}
		}
		if err := sse.err(); err != nil {
			ch <- ChatEvent{Type: EventError, Err: err}
			return
		}
		ch <- ChatEvent{Type: EventDone}
	}()
	return ch, nil
}
