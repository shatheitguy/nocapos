package ai

import (
	"context"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

// claude uses the official Anthropic Go SDK (streaming Messages API).
type claude struct {
	client     anthropic.Client
	firstParty bool // server-side fallbacks are a Claude API feature
}

func newClaude(baseURL, key string) *claude {
	opts := []option.RequestOption{option.WithAPIKey(key)}
	if b := trimBase(baseURL); b != "" {
		opts = append(opts, option.WithBaseURL(b))
	}
	return &claude{client: anthropic.NewClient(opts...), firstParty: trimBase(baseURL) == ""}
}

// Models that accept the "default" server-side refusal fallback.
var fallbackModels = map[string]bool{
	"claude-fable-5-1": true, "claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true,
}

func (c *claude) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	msgs := make([]anthropic.BetaMessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			msgs = append(msgs, anthropic.BetaMessageParam{
				Role:    anthropic.BetaMessageParamRoleAssistant,
				Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(m.Content)},
			})
			continue
		}
		msgs = append(msgs, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(m.Content)))
	}
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(req.Model),
		MaxTokens: 64000,
		Messages:  msgs,
	}
	if req.System != "" {
		params.System = []anthropic.BetaTextBlockParam{{Text: req.System}}
	}
	// A policy decline is re-served by a fallback model inside the same call.
	if c.firstParty && fallbackModels[req.Model] {
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
		params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
	}

	stream := c.client.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	var res ChatResult
	for stream.Next() {
		switch ev := stream.Current().AsAny().(type) {
		case anthropic.BetaRawMessageStartEvent:
			u := ev.Message.Usage
			res.Usage.InputTokens = int(u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens)
		case anthropic.BetaRawContentBlockDeltaEvent:
			if d, ok := ev.Delta.AsAny().(anthropic.BetaTextDelta); ok && d.Text != "" {
				onDelta(d.Text)
			}
		case anthropic.BetaRawMessageDeltaEvent:
			if ev.Delta.StopReason != "" {
				res.StopReason = string(ev.Delta.StopReason)
			}
			res.Usage.OutputTokens = int(ev.Usage.OutputTokens)
		}
	}
	return res, stream.Err()
}

func (c *claude) Models(ctx context.Context) ([]ModelInfo, error) {
	iter := c.client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{})
	var out []ModelInfo
	for iter.Next() {
		m := iter.Current()
		out = append(out, ModelInfo{ID: m.ID, Name: m.DisplayName, ContextWindow: int(m.MaxInputTokens)})
	}
	return out, iter.Err()
}
