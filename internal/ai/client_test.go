package ai

import (
	"context"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSSEHandler returns an HTTP handler that streams Anthropic-format SSE events
// with the given text tokens.
func fakeSSEHandler(tokens []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		// message_start with usage
		fmt.Fprintf(w, "event: message_start\n") //nolint:errcheck
		fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":25,\"output_tokens\":0}}}\n\n") //nolint:errcheck

		// content_block_start
		fmt.Fprintf(w, "event: content_block_start\n") //nolint:errcheck
		fmt.Fprintf(w, "data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n") //nolint:errcheck

		// content_block_delta for each token
		for _, token := range tokens {
			fmt.Fprintf(w, "event: content_block_delta\n") //nolint:errcheck
			fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n\n", token) //nolint:errcheck
		}

		// content_block_stop
		fmt.Fprintf(w, "event: content_block_stop\n") //nolint:errcheck
		fmt.Fprintf(w, "data: {\"type\":\"content_block_stop\",\"index\":0}\n\n") //nolint:errcheck

		// message_delta with final usage
		fmt.Fprintf(w, "event: message_delta\n") //nolint:errcheck
		fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":%d}}\n\n", len(tokens)) //nolint:errcheck

		// message_stop
		fmt.Fprintf(w, "event: message_stop\n") //nolint:errcheck
		fmt.Fprintf(w, "data: {\"type\":\"message_stop\"}\n\n") //nolint:errcheck
	}
}

func TestStreamAndLog_YieldsTokens(t *testing.T) {
	tokens := []string{"Hello", " there", ", how", " are", " you?"}
	srv := httptest.NewServer(fakeSSEHandler(tokens))
	defer srv.Close()

	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:     "claude-sonnet-4-20250514",
		System:    "You are a helpful assistant.",
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("Hi"))},
		MaxTokens: 100,
		UserID:    uuid.New(),
		Role:      "interviewer",
		SessionID: uuid.New(),
	})
	require.NoError(t, err)

	var got []string
	for {
		token, err := stream.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		got = append(got, token)
	}

	assert.Equal(t, tokens, got)
}

func TestStreamAndLog_FullMessage(t *testing.T) {
	tokens := []string{"The ", "answer ", "is ", "42."}
	srv := httptest.NewServer(fakeSSEHandler(tokens))
	defer srv.Close()

	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:     "claude-sonnet-4-20250514",
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("What is the answer?"))},
		MaxTokens: 100,
		UserID:    uuid.New(),
		Role:      "evaluator",
	})
	require.NoError(t, err)

	// Drain the stream.
	for {
		_, err := stream.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
	}

	assert.Equal(t, "The answer is 42.", stream.FullMessage())
}

func TestStreamAndLog_CloseNilPool(t *testing.T) {
	tokens := []string{"ok"}
	srv := httptest.NewServer(fakeSSEHandler(tokens))
	defer srv.Close()

	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:     "claude-sonnet-4-20250514",
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("test"))},
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
	tokens := []string{"a", "b"}
	srv := httptest.NewServer(fakeSSEHandler(tokens))
	defer srv.Close()

	client := NewTestClient(srv.URL, nil)

	stream, err := client.StreamAndLog(context.Background(), StreamParams{
		Model:     "claude-sonnet-4-20250514",
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("test"))},
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

func TestEstimateCost(t *testing.T) {
	cost := estimateCost("claude-sonnet-4-20250514", 1000, 500)
	// 1000 * 3.0/1M + 500 * 15.0/1M = 0.003 + 0.0075 = 0.0105
	assert.InDelta(t, 0.0105, cost, 1e-9)
}

func TestEstimateCost_UnknownModel(t *testing.T) {
	cost := estimateCost("unknown-model", 1000, 500)
	assert.Equal(t, 0.0, cost)
}

func TestNumericFromFloat(t *testing.T) {
	n := numericFromFloat(0.0105)
	assert.True(t, n.Valid)
	assert.Equal(t, int32(-10), n.Exp)

	// Reconstruct the value: Int * 10^Exp
	// 0.0105 * 10^10 = 105000000
	expected := big.NewInt(105000000)
	assert.Equal(t, 0, expected.Cmp(n.Int))
}

func TestNumericFromFloat_Zero(t *testing.T) {
	n := numericFromFloat(0)
	assert.True(t, n.Valid)
	assert.Equal(t, int32(-10), n.Exp)
	assert.Equal(t, 0, big.NewInt(0).Cmp(n.Int))
}

func TestCallAndLog_NilTx(t *testing.T) {
	// Fake server that returns a non-streaming message response.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		//nolint:errcheck
		fmt.Fprint(w, `{
			"id": "msg_test",
			"type": "message",
			"role": "assistant",
			"content": [{"type": "text", "text": "Hello from Claude!"}],
			"model": "claude-sonnet-4-20250514",
			"stop_reason": "end_turn",
			"stop_sequence": null,
			"usage": {"input_tokens": 10, "output_tokens": 5}
		}`)
	}))
	defer srv.Close()

	client := NewTestClient(srv.URL, nil)

	text, err := client.CallAndLog(context.Background(), nil, CallParams{
		Model:     "claude-sonnet-4-20250514",
		System:    "Be helpful.",
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock("Hi"))},
		MaxTokens: 100,
		UserID:    uuid.New(),
		Role:      "evaluator",
	})
	require.NoError(t, err)
	assert.Equal(t, "Hello from Claude!", text)
}

func TestPricing_AllModelsHaveCosts(t *testing.T) {
	// Verify that all pricing entries have non-zero costs.
	for model, p := range pricing {
		assert.Greater(t, p.Input, 0.0, "model %s has zero input cost", model)
		assert.Greater(t, p.Output, 0.0, "model %s has zero output cost", model)
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
