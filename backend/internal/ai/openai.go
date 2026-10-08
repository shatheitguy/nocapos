package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// openAICompat speaks the de-facto OpenAI Chat Completions protocol used by
// OpenAI, OpenRouter, LM Studio, vLLM, llama.cpp server, LocalAI and others.
type openAICompat struct {
	base   string
	key    string
	http   *http.Client
	stream *http.Client
}

func newOpenAICompat(base, key string) *openAICompat {
	return &openAICompat{
		base:   trimBase(base),
		key:    key,
		http:   &http.Client{Timeout: 20 * time.Second},
		stream: &http.Client{},
	}
}

func (o *openAICompat) do(ctx context.Context, client *http.Client, method, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, o.base+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.key != "" {
		req.Header.Set("Authorization", "Bearer "+o.key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach %s: %w", o.base, err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, apiError(resp.StatusCode, b)
	}
	return resp, nil
}

func (o *openAICompat) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	msgs := make([]map[string]string, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	resp, err := o.do(ctx, o.stream, http.MethodPost, "/chat/completions", map[string]any{
		"model":          req.Model,
		"messages":       msgs,
		"stream":         true,
		"stream_options": map[string]bool{"include_usage": true},
	})
	if err != nil {
		return ChatResult{}, err
	}
	defer resp.Body.Close()

	var res ChatResult
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return res, fmt.Errorf("provider error: %s", chunk.Error.Message)
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				onDelta(c.Delta.Content)
			}
			if c.FinishReason != "" {
				res.StopReason = c.FinishReason
			}
		}
		if chunk.Usage != nil {
			res.Usage = Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
		}
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	if res.StopReason == "content_filter" {
		res.StopReason = "refusal"
	}
	return res, ctx.Err()
}

func (o *openAICompat) Models(ctx context.Context) ([]ModelInfo, error) {
	resp, err := o.do(ctx, o.http, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var list struct {
		Data []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"` // OpenRouter
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(list.Data))
	for _, m := range list.Data {
		out = append(out, ModelInfo{ID: m.ID, Name: m.Name, ContextWindow: m.ContextLength})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
