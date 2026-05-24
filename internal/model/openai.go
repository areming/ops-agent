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
	msgs := make([]map[string]string, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": string(m.Role), "content": m.Content})
	}
	body, err := json.Marshal(map[string]any{
		"model":    o.model,
		"messages": msgs,
		"stream":   true,
	})
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
		sse := newSSE(resp.Body)
		for {
			data, ok := sse.next()
			if !ok {
				break
			}
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue // skip keep-alives / non-JSON lines
			}
			if len(chunk.Choices) == 0 || chunk.Choices[0].Delta.Content == "" {
				continue
			}
			select {
			case ch <- ChatEvent{Type: EventTextDelta, Text: chunk.Choices[0].Delta.Content}:
			case <-ctx.Done():
				return
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
