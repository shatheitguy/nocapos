package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ollama talks to Ollama's native API, which (unlike its OpenAI-compatible
// endpoint) supports keep_alive and num_ctx: models stay in RAM between chats
// instead of reloading after Ollama's 5-minute default.
type ollama struct {
	base   string
	http   *http.Client // short calls
	stream *http.Client // chat and model loading
}

func newOllama(base string) *ollama {
	b := trimBase(base)
	if b == "" {
		b = "http://127.0.0.1:11434"
	}
	b = strings.TrimSuffix(b, "/v1")
	return &ollama{base: b, http: &http.Client{Timeout: 20 * time.Second}, stream: &http.Client{}}
}

// keepAliveValue maps our setting to Ollama's: a duration string, or -1 (number) for forever.
func keepAliveValue(ka string) any {
	switch ka {
	case "":
		return DefaultKeepAlive
	case "-1", "forever":
		return -1
	default:
		return ka
	}
}

func (o *ollama) post(ctx context.Context, client *http.Client, path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.base+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Ollama is not reachable at %s: %w", o.base, err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, apiError(resp.StatusCode, body)
	}
	return resp, nil
}

func (o *ollama) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	msgs := make([]map[string]string, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}
	body := map[string]any{
		"model":      req.Model,
		"messages":   msgs,
		"stream":     true,
		"keep_alive": keepAliveValue(req.KeepAlive),
	}
	if req.ContextSize > 0 {
		body["options"] = map[string]any{"num_ctx": req.ContextSize}
	}
	resp, err := o.post(ctx, o.stream, "/api/chat", body)
	if err != nil {
		return ChatResult{}, err
	}
	defer resp.Body.Close()

	var res ChatResult
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		var chunk struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done            bool   `json:"done"`
			DoneReason      string `json:"done_reason"`
			PromptEvalCount int    `json:"prompt_eval_count"`
			EvalCount       int    `json:"eval_count"`
			Error           string `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &chunk); err != nil {
			continue
		}
		if chunk.Error != "" {
			return res, errors.New(chunk.Error)
		}
		if chunk.Message.Content != "" {
			onDelta(chunk.Message.Content)
		}
		if chunk.Done {
			res.StopReason = chunk.DoneReason
			res.Usage = Usage{InputTokens: chunk.PromptEvalCount, OutputTokens: chunk.EvalCount}
			return res, nil
		}
	}
	if err := sc.Err(); err != nil {
		return res, err
	}
	return res, ctx.Err()
}

func (o *ollama) Models(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Ollama is not reachable at %s: %w", o.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, apiError(resp.StatusCode, body)
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}
	running, _ := o.Running(ctx)
	byName := map[string]ModelInfo{}
	for _, r := range running {
		byName[r.ID] = r
	}
	out := make([]ModelInfo, 0, len(tags.Models))
	for _, m := range tags.Models {
		info := ModelInfo{ID: m.Name, Size: m.Size, ContextWindow: o.contextLength(ctx, m.Name)}
		if r, ok := byName[m.Name]; ok {
			info.Loaded, info.RAM, info.VRAM, info.ExpiresAt, info.ContextInUse = true, r.RAM, r.VRAM, r.ExpiresAt, r.ContextInUse
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// contextLength reads the model's maximum context from /api/show ("<arch>.context_length").
func (o *ollama) contextLength(ctx context.Context, model string) int {
	resp, err := o.post(ctx, o.http, "/api/show", map[string]string{"model": model})
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var show struct {
		ModelInfo map[string]any `json:"model_info"`
	}
	if json.NewDecoder(resp.Body).Decode(&show) != nil {
		return 0
	}
	for k, v := range show.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			if f, ok := v.(float64); ok {
				return int(f)
			}
		}
	}
	return 0
}

func (o *ollama) Running(ctx context.Context) ([]ModelInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.base+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	resp, err := o.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Ollama is not reachable at %s: %w", o.base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, apiError(resp.StatusCode, body)
	}
	var ps struct {
		Models []struct {
			Name          string    `json:"name"`
			Size          int64     `json:"size"`
			SizeVRAM      int64     `json:"size_vram"`
			ExpiresAt     time.Time `json:"expires_at"`
			ContextLength int       `json:"context_length"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ps); err != nil {
		return nil, err
	}
	out := make([]ModelInfo, 0, len(ps.Models))
	for _, m := range ps.Models {
		info := ModelInfo{ID: m.Name, Loaded: true, RAM: m.Size, VRAM: m.SizeVRAM, ContextInUse: m.ContextLength}
		// Ollama reports a far-future time for keep_alive -1.
		if !m.ExpiresAt.IsZero() && m.ExpiresAt.Before(time.Now().AddDate(10, 0, 0)) {
			t := m.ExpiresAt.UTC()
			info.ExpiresAt = &t
		}
		out = append(out, info)
	}
	return out, nil
}

// Load preloads a model into RAM (an empty generate request) and sets how long it stays.
func (o *ollama) Load(ctx context.Context, model, keepAlive string, contextSize int) error {
	body := map[string]any{"model": model, "keep_alive": keepAliveValue(keepAlive)}
	if contextSize > 0 {
		// Same num_ctx as chats use, otherwise the first chat reloads the model.
		body["options"] = map[string]any{"num_ctx": contextSize}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	resp, err := o.post(ctx, o.stream, "/api/generate", body)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

func (o *ollama) Unload(ctx context.Context, model string) error {
	resp, err := o.post(ctx, o.http, "/api/generate", map[string]any{"model": model, "keep_alive": 0})
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.Body.Close()
}

// ValidKeepAlive accepts the presets or any Go/Ollama duration like "90m".
func ValidKeepAlive(ka string) bool {
	if ka == "" || ka == "-1" {
		return true
	}
	if _, err := time.ParseDuration(ka); err == nil {
		return true
	}
	_, err := strconv.Atoi(ka)
	return err == nil
}
