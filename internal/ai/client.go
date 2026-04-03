package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/db"
)

// pricing maps model names to per-token costs (USD).
var pricing = map[string]struct{ Input, Output float64 }{
	"claude-sonnet-4-20250514": {Input: 3.0 / 1_000_000, Output: 15.0 / 1_000_000},
	"claude-sonnet-4-0":        {Input: 3.0 / 1_000_000, Output: 15.0 / 1_000_000},
	"claude-opus-4-20250514":   {Input: 15.0 / 1_000_000, Output: 75.0 / 1_000_000},
	"claude-opus-4-0":          {Input: 15.0 / 1_000_000, Output: 75.0 / 1_000_000},
}

// Client wraps the Anthropic SDK with logging to the DB.
type Client struct {
	anthropic anthropic.Client
	pool      *pgxpool.Pool // nil in unit tests
}

// NewClient constructs a Client using the given API key.
// pool may be nil for unit tests (DB persistence is skipped).
func NewClient(apiKey string, pool *pgxpool.Pool) *Client {
	return &Client{
		anthropic: anthropic.NewClient(option.WithAPIKey(apiKey)),
		pool:      pool,
	}
}

// NewTestClient constructs a Client pointed at a fake server.
// pool may be nil for unit tests.
func NewTestClient(baseURL string, pool *pgxpool.Pool) *Client {
	return &Client{
		anthropic: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(baseURL),
		),
		pool: pool,
	}
}

// StreamParams configures a streaming LLM call.
type StreamParams struct {
	Model     string
	System    string
	Messages  []anthropic.MessageParam
	MaxTokens int64
	UserID    uuid.UUID
	Role      string    // "interviewer", "evaluator", etc.
	SessionID uuid.UUID // zero value means no session
}

// CallParams configures a blocking (non-streaming) LLM call.
type CallParams struct {
	Model     string
	System    string
	Messages  []anthropic.MessageParam
	MaxTokens int64
	UserID    uuid.UUID
	Role      string
	SessionID uuid.UUID
}

// StreamAndLog creates a streaming Anthropic request and returns a TokenStream.
// When the stream is closed, the call is persisted to llm_calls + llm_call_content.
func (c *Client) StreamAndLog(ctx context.Context, p StreamParams) (*TokenStream, error) {
	maxTokens := p.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.Model),
		MaxTokens: maxTokens,
		Messages:  p.Messages,
	}
	if p.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: p.System}}
	}

	start := time.Now()
	stream := c.anthropic.Messages.NewStreaming(ctx, params)

	return &TokenStream{
		stream:    stream,
		pool:      c.pool,
		model:     p.Model,
		userID:    p.UserID,
		role:      p.Role,
		sessionID: p.SessionID,
		startTime: start,
		prompt:    params,
	}, nil
}

// CallToolParams configures a blocking LLM call with forced tool use.
type CallToolParams struct {
	Model      string
	System     string
	Messages   []anthropic.MessageParam
	MaxTokens  int64
	UserID     uuid.UUID
	Role       string
	SessionID  uuid.UUID
	Tools      []anthropic.ToolUnionParam
	ToolChoice anthropic.ToolChoiceUnionParam
}

// CallToolAndLog makes a blocking Anthropic request with forced tool_choice and
// returns the raw tool input JSON. Persists the call within the caller's transaction.
func (c *Client) CallToolAndLog(ctx context.Context, tx pgx.Tx, p CallToolParams) (json.RawMessage, error) {
	maxTokens := p.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	params := anthropic.MessageNewParams{
		Model:      anthropic.Model(p.Model),
		MaxTokens:  maxTokens,
		Messages:   p.Messages,
		Tools:      p.Tools,
		ToolChoice: p.ToolChoice,
	}
	if p.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: p.System}}
	}

	start := time.Now()
	resp, err := c.anthropic.Messages.New(ctx, params)
	latency := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("anthropic messages.new: %w", err)
	}

	// Find the tool_use content block.
	var toolInput json.RawMessage
	for _, block := range resp.Content {
		if block.Type == "tool_use" {
			toolInput = block.Input
			break
		}
	}
	if toolInput == nil {
		return nil, fmt.Errorf("no tool_use block in response")
	}

	// Persist if we have a transaction.
	if tx != nil {
		respJSON, marshalErr := json.Marshal(resp)
		if marshalErr != nil {
			return toolInput, fmt.Errorf("marshal response: %w", marshalErr)
		}

		cost := estimateCost(p.Model, resp.Usage.InputTokens, resp.Usage.OutputTokens)
		sessionID := pgtype.UUID{}
		if p.SessionID != uuid.Nil {
			sessionID = pgtype.UUID{Bytes: p.SessionID, Valid: true}
		}

		callID, insertErr := db.New(tx).InsertLLMCall(ctx, db.InsertLLMCallParams{
			SessionID:     sessionID,
			UserID:        p.UserID,
			Role:          p.Role,
			Model:         p.Model,
			InputTokens:   int32(resp.Usage.InputTokens),
			OutputTokens:  int32(resp.Usage.OutputTokens),
			EstimatedCost: numericFromFloat(cost),
			LatencyMs:     int32(latency.Milliseconds()),
		})
		if insertErr != nil {
			return toolInput, fmt.Errorf("insert llm_call: %w", insertErr)
		}

		promptJSON, marshalErr := json.Marshal(params)
		if marshalErr != nil {
			return toolInput, fmt.Errorf("marshal prompt: %w", marshalErr)
		}

		insertErr = db.New(tx).InsertLLMCallContent(ctx, db.InsertLLMCallContentParams{
			LlmCallID: callID,
			Prompt:    promptJSON,
			Response:  respJSON,
		})
		if insertErr != nil {
			return toolInput, fmt.Errorf("insert llm_call_content: %w", insertErr)
		}
	}

	return toolInput, nil
}

// CallAndLog makes a blocking Anthropic request and persists the call within
// the caller's transaction. Returns the concatenated text response.
func (c *Client) CallAndLog(ctx context.Context, tx pgx.Tx, p CallParams) (string, error) {
	maxTokens := p.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.Model),
		MaxTokens: maxTokens,
		Messages:  p.Messages,
	}
	if p.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: p.System}}
	}

	start := time.Now()
	resp, err := c.anthropic.Messages.New(ctx, params)
	latency := time.Since(start)
	if err != nil {
		return "", fmt.Errorf("anthropic messages.new: %w", err)
	}

	// Extract text from content blocks.
	var b strings.Builder
	for _, block := range resp.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	text := b.String()

	// Persist if we have a transaction.
	if tx != nil {
		err = persistCall(ctx, db.New(tx), persistParams{
			model:        p.Model,
			userID:       p.UserID,
			role:         p.Role,
			sessionID:    p.SessionID,
			inputTokens:  resp.Usage.InputTokens,
			outputTokens: resp.Usage.OutputTokens,
			latency:      latency,
			prompt:       params,
			response:     text,
		})
		if err != nil {
			return text, fmt.Errorf("persist llm call: %w", err)
		}
	}

	return text, nil
}

// TokenStream wraps an Anthropic streaming response and yields tokens one at a time.
type TokenStream struct {
	stream  *ssestream.Stream[anthropic.MessageStreamEventUnion]
	pool    *pgxpool.Pool
	model   string
	userID  uuid.UUID
	role    string
	sessionID uuid.UUID
	startTime time.Time
	prompt    anthropic.MessageNewParams

	mu       sync.Mutex
	buf      strings.Builder // accumulated full response
	message  anthropic.Message
	pending  string // buffered token from last Next() lookahead
	done     bool
}

// Next returns the next text token from the stream.
// Returns io.EOF when the stream is complete.
func (ts *TokenStream) Next() (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.done {
		return "", io.EOF
	}

	for ts.stream.Next() {
		event := ts.stream.Current()
		if err := ts.message.Accumulate(event); err != nil {
			return "", fmt.Errorf("accumulate: %w", err)
		}

		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			token := event.Delta.Text
			ts.buf.WriteString(token)
			return token, nil
		}
		// Skip non-text events (message_start, content_block_start, etc.)
	}

	ts.done = true
	if err := ts.stream.Err(); err != nil {
		return "", fmt.Errorf("stream: %w", err)
	}
	return "", io.EOF
}

// FullMessage returns the accumulated text after streaming is complete.
// May be called during or after streaming.
func (ts *TokenStream) FullMessage() string {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return ts.buf.String()
}

// Close drains the stream if needed, then persists the LLM call using a
// connection from the pool. If pool is nil (unit tests), persistence is skipped.
func (ts *TokenStream) Close(ctx context.Context) error {
	ts.drain()
	defer ts.stream.Close()

	if ts.pool == nil {
		return nil
	}
	return persistCall(ctx, db.New(ts.pool), ts.persistParams())
}

// CloseWithTx drains the stream if needed, then persists the LLM call within
// the caller's transaction. If tx is nil, persistence is skipped.
func (ts *TokenStream) CloseWithTx(ctx context.Context, tx pgx.Tx) error {
	ts.drain()
	defer ts.stream.Close()

	if tx == nil {
		return nil
	}
	return persistCall(ctx, db.New(tx), ts.persistParams())
}

// drain reads remaining events from the stream to populate usage stats.
func (ts *TokenStream) drain() {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.done {
		return
	}
	for ts.stream.Next() {
		event := ts.stream.Current()
		_ = ts.message.Accumulate(event)
		if event.Type == "content_block_delta" && event.Delta.Type == "text_delta" {
			ts.buf.WriteString(event.Delta.Text)
		}
	}
	ts.done = true
}

func (ts *TokenStream) persistParams() persistParams {
	return persistParams{
		model:        ts.model,
		userID:       ts.userID,
		role:         ts.role,
		sessionID:    ts.sessionID,
		inputTokens:  ts.message.Usage.InputTokens,
		outputTokens: ts.message.Usage.OutputTokens,
		latency:      time.Since(ts.startTime),
		prompt:       ts.prompt,
		response:     ts.buf.String(),
	}
}

// persistParams holds the data needed to write llm_calls + llm_call_content.
type persistParams struct {
	model        string
	userID       uuid.UUID
	role         string
	sessionID    uuid.UUID
	inputTokens  int64
	outputTokens int64
	latency      time.Duration
	prompt       anthropic.MessageNewParams
	response     string
}

// persistCall inserts into llm_calls and llm_call_content.
func persistCall(ctx context.Context, q *db.Queries, p persistParams) error {
	cost := estimateCost(p.model, p.inputTokens, p.outputTokens)

	sessionID := pgtype.UUID{}
	if p.sessionID != uuid.Nil {
		sessionID = pgtype.UUID{Bytes: p.sessionID, Valid: true}
	}

	callID, err := q.InsertLLMCall(ctx, db.InsertLLMCallParams{
		SessionID:     sessionID,
		UserID:        p.userID,
		Role:          p.role,
		Model:         p.model,
		InputTokens:   int32(p.inputTokens),
		OutputTokens:  int32(p.outputTokens),
		EstimatedCost: numericFromFloat(cost),
		LatencyMs:     int32(p.latency.Milliseconds()),
	})
	if err != nil {
		return fmt.Errorf("insert llm_call: %w", err)
	}

	promptJSON, err := json.Marshal(p.prompt)
	if err != nil {
		return fmt.Errorf("marshal prompt: %w", err)
	}
	responseJSON, err := json.Marshal(map[string]string{"text": p.response})
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}

	err = q.InsertLLMCallContent(ctx, db.InsertLLMCallContentParams{
		LlmCallID: callID,
		Prompt:    promptJSON,
		Response:  responseJSON,
	})
	if err != nil {
		return fmt.Errorf("insert llm_call_content: %w", err)
	}

	return nil
}

// estimateCost returns the estimated cost in USD.
func estimateCost(model string, inputTokens, outputTokens int64) float64 {
	p, ok := pricing[model]
	if !ok {
		return 0
	}
	return float64(inputTokens)*p.Input + float64(outputTokens)*p.Output
}

// numericFromFloat converts a float64 to pgtype.Numeric.
func numericFromFloat(f float64) pgtype.Numeric {
	// Use big.Float for precise conversion to big.Int with exponent.
	// We store with 10 decimal places of precision.
	const scale = 10
	bf := new(big.Float).SetFloat64(f)
	shift := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(scale), nil))
	bf.Mul(bf, shift)

	bi, _ := bf.Int(nil)
	return pgtype.Numeric{
		Int:   bi,
		Exp:   -scale,
		Valid: true,
	}
}
