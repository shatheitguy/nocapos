package ai

import (
	"context"

	"github.com/providers/provider-sdk-go"
	"github.com/providers/provider-sdk-go/option"
	"github.com/providers/provider-sdk-go/shared/constant"
)

// providerChat uses the official Provider Go SDK (streaming Messages API).
type providerChat struct {
	client     provider.Client
	firstParty bool // server-side fallbacks are an Provider API feature
}

func newProvider(baseURL, key string) *providerChat {
	opts := []option.RequestOption{option.WithAPIKey(key)}
	if b := trimBase(baseURL); b != "" {
		opts = append(opts, option.WithBaseURL(b))
	}
	return &providerChat{client: provider.NewClient(opts...), firstParty: trimBase(baseURL) == ""}
}

// Models that accept the "default" server-side refusal fallback.
var fallbackModels = map[string]bool{
	"ai-fable-5-1": true, "ai-opus-5-5": true, "ai-opus-5": true, "ai-sonnet-5-5": true,
}

func (c *providerChat) Chat(ctx context.Context, req ChatRequest, onDelta func(string)) (ChatResult, error) {
	msgs := make([]provider.BetaMessageParam, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == "assistant" {
			msgs = append(msgs, provider.BetaMessageParam{
				Role:    provider.BetaMessageParamRoleAssistant,
				Content: []provider.BetaContentBlockParamUnion{provider.NewBetaTextBlock(m.Content)},
			})
			continue
		}
		msgs = append(msgs, provider.NewBetaUserMessage(provider.NewBetaTextBlock(m.Content)))
	}
	params := provider.BetaMessageNewParams{
		Model:     provider.Model(req.Model),
		MaxTokens: 64000,
		Messages:  msgs,
	}
	if req.System != "" {
		params.System = []provider.BetaTextBlockParam{{Text: req.System}}
	}
	// A policy decline is re-served by a fallback model inside the same call.
	if c.firstParty && fallbackModels[req.Model] {
		params.Betas = []provider.ProviderBeta{provider.ProviderBetaServerSideFallback2026_07_01}
		params.Fallbacks = provider.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
	}

	stream := c.client.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	var res ChatResult
	for stream.Next() {
		switch ev := stream.Current().AsAny().(type) {
		case provider.BetaRawMessageStartEvent:
			u := ev.Message.Usage
			res.Usage.InputTokens = int(u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens)
		case provider.BetaRawContentBlockDeltaEvent:
			if d, ok := ev.Delta.AsAny().(provider.BetaTextDelta); ok && d.Text != "" {
				onDelta(d.Text)
			}
		case provider.BetaRawMessageDeltaEvent:
			if ev.Delta.StopReason != "" {
				res.StopReason = string(ev.Delta.StopReason)
			}
			res.Usage.OutputTokens = int(ev.Usage.OutputTokens)
		}
	}
	return res, stream.Err()
}

func (c *providerChat) Models(ctx context.Context) ([]ModelInfo, error) {
	iter := c.client.Models.ListAutoPaging(ctx, provider.ModelListParams{})
	var out []ModelInfo
	for iter.Next() {
		m := iter.Current()
		out = append(out, ModelInfo{ID: m.ID, Name: m.DisplayName, ContextWindow: int(m.MaxInputTokens)})
	}
	return out, iter.Err()
}
