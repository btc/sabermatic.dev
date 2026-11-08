package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai/aitest"
)

func userMsgs(text string) []anthropic.BetaMessageParam {
	return []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(text))}
}

// textResponse is a fake streamed response carrying one text block.
func textResponse(tokens ...string) aitest.Response {
	return aitest.Response{
		Model:        "claude-sonnet-5-5",
		Blocks:       []aitest.Block{aitest.TextBlock(tokens...)},
		InputTokens:  25,
		OutputTokens: int64(len(tokens)),
	}
}

// collect reads tokens until EOF or error.
func collect(t *testing.T, stream *TokenStream) ([]string, error) {
	t.Helper()
	var got []string
	for {
		token, err := stream.Next()
		if err == io.EOF {
			return got, nil
		}
		if err != nil {
			return got, err
		}
		got = append(got, token)
	}
}

func TestStreamAndLog_YieldsTokens(t *testing.T) {
	tokens := []string{"Hello", " there", ", how", " are", " you?"}
	srv := aitest.NewServer(t, textResponse(tokens...))

	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:     "claude-sonnet-5-5",
		System:    "You are a helpful assistant.",
		Messages:  userMsgs("Hi"),
		MaxTokens: 100,
		UserID:    uuid.New(),
		Role:      "interviewer",
		SessionID: uuid.New(),
	})
	require.NoError(t, err)

	got, err := collect(t, stream)
	require.NoError(t, err)
	assert.Equal(t, tokens, got)
}

func TestStreamAndLog_SkipsLeadingThinkingBlocks(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model: "claude-sonnet-5-5",
		Blocks: []aitest.Block{
			aitest.ThinkingBlock(),
			aitest.TextBlock("Tell me", " about scale."),
		},
	})
	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:    "claude-sonnet-5-5",
		Messages: userMsgs("Hi"),
	})
	require.NoError(t, err)

	got, err := collect(t, stream)
	require.NoError(t, err)
	assert.Equal(t, []string{"Tell me", " about scale."}, got)
	assert.Equal(t, "Tell me about scale.", stream.FullMessage())
}

func TestStreamAndLog_RequestBody(t *testing.T) {
	srv := aitest.NewServer(t, textResponse("ok"))
	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:     "claude-sonnet-5-5",
		System:    "sys",
		Messages:  userMsgs("Hi"),
		MaxTokens: 8192,
		Effort:    "low",
	})
	require.NoError(t, err)
	_, err = collect(t, stream)
	require.NoError(t, err)

	reqs := srv.Requests()
	require.Len(t, reqs, 1)
	body := reqs[0]
	assert.Equal(t, "claude-sonnet-5-5", body["model"])
	assert.Equal(t, float64(8192), body["max_tokens"])
	assert.Equal(t, true, body["stream"])
	assert.Equal(t, map[string]any{"effort": "low"}, body["output_config"])
	assert.Equal(t, "default", body["fallbacks"])
	for _, k := range []string{"tool_choice", "tools", "thinking", "temperature", "top_p", "top_k"} {
		assert.NotContains(t, body, k)
	}
	assert.Contains(t, srv.Headers()[0].Get("anthropic-beta"), "server-side-fallback-2026-07-01")
}

func TestStreamAndLog_FallbacksDisabled(t *testing.T) {
	srv := aitest.NewServer(t, textResponse("ok"))
	client := NewTestClient(srv.URL, nil)
	client.fallbacks = false

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:    "claude-sonnet-5-5",
		Messages: userMsgs("Hi"),
	})
	require.NoError(t, err)
	_, err = collect(t, stream)
	require.NoError(t, err)

	body := srv.Requests()[0]
	assert.NotContains(t, body, "fallbacks")
	assert.NotContains(t, srv.Headers()[0].Get("anthropic-beta"), "server-side-fallback")
}

func TestStreamAndLog_RefusalMidStream(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:           "claude-sonnet-5-5",
		Blocks:          []aitest.Block{aitest.TextBlock("Partial ")},
		StopReason:      "refusal",
		RefusalCategory: "cyber",
	})
	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:    "claude-sonnet-5-5",
		Messages: userMsgs("Hi"),
	})
	require.NoError(t, err)

	got, err := collect(t, stream)
	assert.Equal(t, []string{"Partial "}, got)
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, "cyber", refusal.Category)
	assert.Equal(t, "claude-sonnet-5-5", refusal.Model)
	assert.True(t, IsRefusal(err))

	// The error is sticky: a later Next does not report EOF.
	_, err = stream.Next()
	assert.True(t, IsRefusal(err))

	require.NoError(t, stream.Close(context.Background()))
}

func TestStreamAndLog_MaxTokens(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:      "claude-sonnet-5-5",
		Blocks:     []aitest.Block{aitest.ThinkingBlock(), aitest.TextBlock("Cut")},
		StopReason: "max_tokens",
	})
	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:     "claude-sonnet-5-5",
		Messages:  userMsgs("Hi"),
		MaxTokens: 50,
	})
	require.NoError(t, err)

	_, err = collect(t, stream)
	require.ErrorIs(t, err, ErrMaxTokens)
	assert.Contains(t, err.Error(), "50")
}

func TestStreamAndLog_FullMessage(t *testing.T) {
	srv := aitest.NewServer(t, textResponse("The ", "answer ", "is ", "42."))
	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:     "claude-sonnet-5-5",
		Messages:  userMsgs("What is the answer?"),
		MaxTokens: 100,
		UserID:    uuid.New(),
		Role:      "evaluator",
	})
	require.NoError(t, err)

	_, err = collect(t, stream)
	require.NoError(t, err)
	assert.Equal(t, "The answer is 42.", stream.FullMessage())
}

func TestStreamAndLog_CloseNilPool(t *testing.T) {
	srv := aitest.NewServer(t, textResponse("ok"))
	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:     "claude-sonnet-5-5",
		Messages:  userMsgs("test"),
		MaxTokens: 100,
		UserID:    uuid.New(),
		Role:      "interviewer",
	})
	require.NoError(t, err)

	// Close without draining -- should drain internally and not error.
	err = stream.Close(context.Background())
	assert.NoError(t, err)

	// FullMessage should still have accumulated text even though we never called Next.
	assert.Equal(t, "ok", stream.FullMessage())
}

func TestStreamAndLog_CloseWithTxNil(t *testing.T) {
	srv := aitest.NewServer(t, textResponse("a", "b"))
	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:     "claude-sonnet-5-5",
		Messages:  userMsgs("test"),
		MaxTokens: 100,
		UserID:    uuid.New(),
		Role:      "interviewer",
	})
	require.NoError(t, err)

	// CloseWithTx with nil tx should skip persistence and not error.
	err = stream.CloseWithTx(context.Background(), nil)
	assert.NoError(t, err)
	assert.Equal(t, "ab", stream.FullMessage())
}

func TestTokenStream_ServedModelAfterFallback(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model: "claude-sonnet-5-5",
		Blocks: []aitest.Block{
			aitest.FallbackBlock("claude-sonnet-5-5", "claude-sonnet-5"),
			aitest.TextBlock("Hello"),
		},
	})
	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:    "claude-sonnet-5-5",
		Messages: userMsgs("Hi"),
	})
	require.NoError(t, err)
	got, err := collect(t, stream)
	require.NoError(t, err)
	assert.Equal(t, []string{"Hello"}, got)

	stream.mu.Lock()
	model := servedModel(&stream.message, stream.model)
	stream.mu.Unlock()
	assert.Equal(t, "claude-sonnet-5", model)
}

func TestCall_ReturnsTextAfterThinking(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:        "claude-sonnet-5-5",
		Blocks:       []aitest.Block{aitest.ThinkingBlock(), aitest.TextBlock("Hello ", "from Claude!")},
		InputTokens:  10,
		OutputTokens: 5,
	})
	client := NewTestClient(srv.URL, nil)

	result, err := client.Call(context.Background(), CallParams{
		Model:     "claude-sonnet-5-5",
		System:    "Be helpful.",
		Messages:  userMsgs("Hi"),
		MaxTokens: 4096,
		Effort:    "low",
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello from Claude!", result.Text)
	assert.Equal(t, "claude-sonnet-5-5", result.Model)
	assert.Equal(t, int64(10), result.InputTokens)
	assert.Equal(t, int64(5), result.OutputTokens)

	body := srv.Requests()[0]
	assert.Equal(t, true, body["stream"], "blocking calls stream internally")
	assert.Equal(t, map[string]any{"effort": "low"}, body["output_config"])
}

func TestCall_LargeMaxTokensStreams(t *testing.T) {
	// The SDK rejects non-streaming requests above ~21K max_tokens before
	// sending. Call must still work at the evaluator's 32000.
	srv := aitest.NewServer(t, textResponse("ok"))
	client := NewTestClient(srv.URL, nil)

	result, err := client.Call(context.Background(), CallParams{
		Model:     "claude-opus-5-5",
		Messages:  userMsgs("Hi"),
		MaxTokens: 32000,
	})
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Text)
}

func TestCall_ReportsServedModel(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model: "claude-sonnet-5-5",
		Blocks: []aitest.Block{
			aitest.FallbackBlock("claude-sonnet-5-5", "claude-sonnet-5"),
			aitest.TextBlock("ok"),
		},
	})
	client := NewTestClient(srv.URL, nil)

	result, err := client.Call(context.Background(), CallParams{Model: "claude-sonnet-5-5", Messages: userMsgs("Hi")})
	require.NoError(t, err)
	assert.Equal(t, "claude-sonnet-5", result.Model)
}

func TestCall_Refusal(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:           "claude-sonnet-5-5",
		StopReason:      "refusal",
		RefusalCategory: "frontier_llm",
	})
	client := NewTestClient(srv.URL, nil)

	_, err := client.Call(context.Background(), CallParams{Model: "claude-sonnet-5-5", Messages: userMsgs("Hi")})
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, "frontier_llm", refusal.Category)
	assert.Contains(t, err.Error(), "frontier_llm")
}

func TestCall_MaxTokens(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:      "claude-sonnet-5-5",
		Blocks:     []aitest.Block{aitest.ThinkingBlock()},
		StopReason: "max_tokens",
	})
	client := NewTestClient(srv.URL, nil)

	_, err := client.Call(context.Background(), CallParams{Model: "claude-sonnet-5-5", Messages: userMsgs("Hi"), MaxTokens: 4096})
	require.ErrorIs(t, err, ErrMaxTokens)
	assert.False(t, IsRefusal(err))
}

func TestEstimateCost(t *testing.T) {
	tests := []struct {
		model string
		want  float64
	}{
		// 1000 input + 500 output tokens.
		{"claude-sonnet-5-5", 1000*2.0/1e6 + 500*10.0/1e6}, // 0.007
		{"claude-opus-5-5", 1000*4.0/1e6 + 500*20.0/1e6},   // 0.014
		{"claude-opus-5", 1000*5.0/1e6 + 500*25.0/1e6},
		{"claude-opus-4-8", 1000*5.0/1e6 + 500*25.0/1e6},
		{"claude-sonnet-5", 1000*2.0/1e6 + 500*10.0/1e6},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.InDelta(t, tt.want, EstimateCost(tt.model, 1000, 500), 1e-12)
		})
	}
	assert.InDelta(t, 0.007, EstimateCost("claude-sonnet-5-5", 1000, 500), 1e-12)
	assert.InDelta(t, 0.014, EstimateCost("claude-opus-5-5", 1000, 500), 1e-12)
}

func TestEstimateCost_UnknownModel(t *testing.T) {
	cost := EstimateCost("unknown-model", 1000, 500)
	assert.Equal(t, 0.0, cost)
}

func TestNumericFromFloat(t *testing.T) {
	n := NumericFromFloat(0.0105)
	assert.True(t, n.Valid)
	assert.Equal(t, int32(-10), n.Exp)

	// Reconstruct the value: Int * 10^Exp
	// 0.0105 * 10^10 = 105000000
	expected := big.NewInt(105000000)
	assert.Equal(t, 0, expected.Cmp(n.Int))
}

func TestNumericFromFloat_Zero(t *testing.T) {
	n := NumericFromFloat(0)
	assert.True(t, n.Valid)
	assert.Equal(t, int32(-10), n.Exp)
	assert.Equal(t, 0, big.NewInt(0).Cmp(n.Int))
}

func TestPricing_AllModelsHaveCosts(t *testing.T) {
	// Verify that all pricing entries have non-zero costs.
	for model, p := range pricing {
		assert.Greater(t, p.Input, 0.0, "model %s has zero input cost", model)
		assert.Greater(t, p.Output, 0.0, "model %s has zero output cost", model)
	}
}

func TestRefusalError_Message(t *testing.T) {
	err := error(&RefusalError{Model: "claude-opus-5-5"})
	assert.Contains(t, err.Error(), "unspecified")
	assert.NotContains(t, err.Error(), "recommended")
	wrapped := errors.Join(errors.New("ctx"), err)
	assert.True(t, IsRefusal(wrapped))
}

func TestCall_RefusalRecommendedModel(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:            "claude-opus-5-5",
		StopReason:       "refusal",
		RefusalCategory:  "cyber",
		RecommendedModel: "claude-opus-4-8",
	})
	client := NewTestClient(srv.URL, nil)

	_, err := client.Call(context.Background(), CallParams{Model: "claude-opus-5-5", Messages: userMsgs("Hi")})
	var refusal *RefusalError
	require.ErrorAs(t, err, &refusal)
	assert.Equal(t, "claude-opus-4-8", refusal.RecommendedModel)
	assert.Contains(t, err.Error(), "recommended retry model: claude-opus-4-8")
}

// fallbackIterations is the per-attempt usage for a request that
// claude-opus-5-5 declined mid-stream and claude-opus-4-8 then served.
var fallbackIterations = []aitest.Iteration{
	{Type: "message", Model: "claude-opus-5-5", InputTokens: 1000, OutputTokens: 300},
	{Type: "fallback_message", Model: "claude-opus-4-8", InputTokens: 1200, OutputTokens: 500},
}

// fallbackCost is the cost of fallbackIterations, each attempt at its own
// model's price.
const fallbackCost = 1000*4.0/1e6 + 300*20.0/1e6 + 1200*5.0/1e6 + 500*25.0/1e6

func TestCall_CostSumsFallbackIterations(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model: "claude-opus-5-5",
		Blocks: []aitest.Block{
			aitest.TextBlock("par"),
			aitest.FallbackBlock("claude-opus-5-5", "claude-opus-4-8"),
			aitest.TextBlock("tial"),
		},
		// Top-level usage covers only the served attempt.
		InputTokens:  1200,
		OutputTokens: 500,
		Iterations:   fallbackIterations,
	})
	client := NewTestClient(srv.URL, nil)

	result, err := client.Call(context.Background(), CallParams{Model: "claude-opus-5-5", Messages: userMsgs("Hi")})
	require.NoError(t, err)
	assert.Equal(t, "claude-opus-4-8", result.Model)
	assert.Equal(t, int64(2200), result.InputTokens)
	assert.Equal(t, int64(800), result.OutputTokens)
	assert.InDelta(t, fallbackCost, result.Cost, 1e-12)
	// Pricing only the top-level usage at the served model would undercount.
	assert.Greater(t, result.Cost, EstimateCost("claude-opus-4-8", 1200, 500))
}

func TestTokenStream_UsageIncludesFallbackIterations(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model: "claude-opus-5-5",
		Blocks: []aitest.Block{
			aitest.TextBlock("par"),
			aitest.FallbackBlock("claude-opus-5-5", "claude-opus-4-8"),
			aitest.TextBlock("tial"),
		},
		InputTokens:  1200,
		OutputTokens: 500,
		Iterations:   fallbackIterations,
	})
	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{Model: "claude-opus-5-5", Messages: userMsgs("Hi")})
	require.NoError(t, err)
	_, err = collect(t, stream)
	require.NoError(t, err)

	stream.mu.Lock()
	usage := stream.message.Usage
	model := servedModel(&stream.message, stream.model)
	stream.mu.Unlock()
	in, out := billedTokens(usage)
	assert.Equal(t, int64(2200), in)
	assert.Equal(t, int64(800), out)
	assert.InDelta(t, fallbackCost, usageCost(usage, model), 1e-12)
}

func TestUsageCost_NoIterationsUsesTopLevel(t *testing.T) {
	u := anthropic.BetaUsage{InputTokens: 1000, OutputTokens: 500}
	assert.InDelta(t, EstimateCost("claude-sonnet-5-5", 1000, 500), usageCost(u, "claude-sonnet-5-5"), 1e-12)
	in, out := billedTokens(u)
	assert.Equal(t, int64(1000), in)
	assert.Equal(t, int64(500), out)
}

func TestCall_MaxTokensReturnsUsage(t *testing.T) {
	srv := aitest.NewServer(t, aitest.Response{
		Model:        "claude-sonnet-5-5",
		Blocks:       []aitest.Block{aitest.ThinkingBlock()},
		StopReason:   "max_tokens",
		InputTokens:  100,
		OutputTokens: 4096,
	})
	client := NewTestClient(srv.URL, nil)

	result, err := client.Call(context.Background(), CallParams{Model: "claude-sonnet-5-5", Messages: userMsgs("Hi"), MaxTokens: 4096})
	require.ErrorIs(t, err, ErrMaxTokens)
	assert.Equal(t, "claude-sonnet-5-5", result.Model)
	assert.Equal(t, int64(4096), result.OutputTokens)
	assert.Greater(t, result.Cost, 0.0)
	assert.Empty(t, result.Text)
}

// TestTokenStream_CloseAfterBrokenStreamDoesNotDrain checks that Close
// returns promptly when Next stopped on a non-stop-reason error, instead of
// reading the rest of a generation that is still running.
func TestTokenStream_CloseAfterBrokenStreamDoesNotDrain(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-5-5\",\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n")
		// A block start at the wrong index makes Accumulate fail.
		fmt.Fprint(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":5,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		w.(http.Flusher).Flush()
		// The generation keeps running.
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{Model: "claude-sonnet-5-5", Messages: userMsgs("Hi")})
	require.NoError(t, err)
	_, err = stream.Next()
	require.Error(t, err)
	require.NotErrorIs(t, err, io.EOF)

	done := make(chan error, 1)
	go func() { done <- stream.Close(context.Background()) }()
	select {
	case err := <-done:
		assert.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked draining a broken stream")
	}
}

func TestSessionID_NilConversion(t *testing.T) {
	// When sessionID is zero-value UUID, pgtype.UUID should be invalid (null).
	var zeroID uuid.UUID
	sessionID := pgtype.UUID{}
	if zeroID != uuid.Nil {
		sessionID = pgtype.UUID{Bytes: zeroID, Valid: true}
	}
	assert.False(t, sessionID.Valid)
}
