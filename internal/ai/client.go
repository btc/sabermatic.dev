package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/anthropics/anthropic-sdk-go/v2/packages/ssestream"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
)

var tracer = drilotel.Tracer("ai")

// pricing maps model names to per-token list prices (USD). Thinking tokens are
// billed as output and are already included in usage.output_tokens.
//
// claude-opus-5, claude-opus-4-8 and claude-sonnet-5 are here because the
// server-side refusal fallback ("default" mode) can serve a request on them,
// and llm_calls.model records the model that served the response.
var pricing = map[string]struct{ Input, Output float64 }{
	"claude-opus-5-5":   {Input: 4.0 / 1_000_000, Output: 20.0 / 1_000_000},
	"claude-sonnet-5-5": {Input: 2.0 / 1_000_000, Output: 10.0 / 1_000_000},
	"claude-opus-5":     {Input: 5.0 / 1_000_000, Output: 25.0 / 1_000_000},
	"claude-opus-4-8":   {Input: 5.0 / 1_000_000, Output: 25.0 / 1_000_000},
	"claude-sonnet-5":   {Input: 2.0 / 1_000_000, Output: 10.0 / 1_000_000},
}

// defaultMaxTokens applies when a caller leaves MaxTokens at zero.
const defaultMaxTokens = 4096

// ErrMaxTokens is returned (wrapped) when a response stops because it hit
// max_tokens. Thinking counts against max_tokens, so the visible output may be
// short or empty.
var ErrMaxTokens = errors.New("ai: response stopped at max_tokens")

// RefusalError is returned when a response ends with stop_reason "refusal":
// the model or a safety classifier declined the request. When server-side
// fallbacks are enabled, this means every model in the fallback chain declined.
// Retrying the same request is not expected to succeed.
type RefusalError struct {
	Model       string // model that produced the final (refused) response
	Category    string // stop_details.category; empty when the API sent none
	Explanation string // stop_details.explanation; may be empty
}

func (e *RefusalError) Error() string {
	category := e.Category
	if category == "" {
		category = "unspecified"
	}
	msg := fmt.Sprintf("ai: %s declined the request (refusal category: %s)", e.Model, category)
	if e.Explanation != "" {
		msg += ": " + e.Explanation
	}
	return msg
}

// IsRefusal reports whether err is or wraps a *RefusalError.
func IsRefusal(err error) bool {
	var r *RefusalError
	return errors.As(err, &r)
}

// Options configures a Client.
type Options struct {
	// DisableFallbacks turns off server-side refusal fallbacks. When false
	// (the default), every request sends fallbacks:"default" with the
	// server-side-fallback-2026-07-01 beta header, so a classifier refusal is
	// retried on the model Anthropic recommends for that refusal category.
	DisableFallbacks bool
}

// Client wraps the Anthropic SDK with logging to the DB. All requests go
// through the Beta Messages endpoint, which carries the fallbacks parameter.
type Client struct {
	anthropic anthropic.Client
	pool      *pgxpool.Pool // nil in unit tests
	fallbacks bool
}

// NewClient constructs a Client using the given API key.
// pool may be nil for unit tests (DB persistence is skipped).
func NewClient(apiKey string, pool *pgxpool.Pool, opts Options) *Client {
	return &Client{
		anthropic: anthropic.NewClient(option.WithAPIKey(apiKey)),
		pool:      pool,
		fallbacks: !opts.DisableFallbacks,
	}
}

// NewTestClient constructs a Client pointed at a fake server, with fallbacks
// enabled (the production default). pool may be nil for unit tests.
func NewTestClient(baseURL string, pool *pgxpool.Pool) *Client {
	return &Client{
		anthropic: anthropic.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(baseURL),
			option.WithMaxRetries(0),
		),
		pool:      pool,
		fallbacks: true,
	}
}

// request holds the fields shared by every call shape.
type request struct {
	model     string
	system    string
	messages  []anthropic.BetaMessageParam
	maxTokens int64
	effort    string
	schema    map[string]any // non-nil requests structured JSON output
}

// newParams builds the Beta Messages request. It never sets temperature,
// top_p, top_k, thinking, or budget_tokens: thinking is left at the model
// default (adaptive, display omitted) and controlled through effort.
func (c *Client) newParams(r request) anthropic.BetaMessageNewParams {
	maxTokens := r.maxTokens
	if maxTokens == 0 {
		maxTokens = defaultMaxTokens
	}
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(r.model),
		MaxTokens: maxTokens,
		Messages:  r.messages,
	}
	if r.system != "" {
		params.System = []anthropic.BetaTextBlockParam{{Text: r.system}}
	}
	if r.effort != "" {
		params.OutputConfig.Effort = anthropic.BetaOutputConfigEffort(r.effort)
	}
	if r.schema != nil {
		params.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: r.schema}
	}
	if c.fallbacks {
		params.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
		params.Betas = []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01}
	}
	return params
}

// complete sends params as a streaming request and accumulates the final
// message. Every blocking call goes through here: the Go SDK refuses
// non-streaming requests whose max_tokens implies more than ten minutes of
// generation, and thinking pushes the limits we need past that.
func (c *Client) complete(ctx context.Context, params anthropic.BetaMessageNewParams) (*anthropic.BetaMessage, time.Duration, error) {
	start := time.Now()
	stream := c.anthropic.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()

	var msg anthropic.BetaMessage
	for stream.Next() {
		if err := msg.Accumulate(stream.Current()); err != nil {
			return nil, time.Since(start), fmt.Errorf("anthropic stream accumulate: %w", err)
		}
	}
	latency := time.Since(start)
	if err := stream.Err(); err != nil {
		return nil, latency, fmt.Errorf("anthropic messages stream: %w", err)
	}
	return &msg, latency, nil
}

// checkStop converts the stop reasons that mean "no usable output" into errors.
// maxTokens is the limit the request was sent with, for the error message.
func checkStop(stopReason anthropic.BetaStopReason, details anthropic.BetaRefusalStopDetails, model string, maxTokens int64) error {
	switch stopReason {
	case anthropic.BetaStopReasonRefusal:
		return &RefusalError{
			Model:       model,
			Category:    string(details.Category),
			Explanation: details.Explanation,
		}
	case anthropic.BetaStopReasonMaxTokens:
		return fmt.Errorf("%w: %s used all %d tokens (thinking counts toward the limit)", ErrMaxTokens, model, maxTokens)
	case anthropic.BetaStopReasonModelContextWindowExceeded:
		return fmt.Errorf("ai: %s stopped: model context window exceeded", model)
	}
	return nil
}

// textOf concatenates the text blocks of msg, skipping thinking, fallback and
// any other block types.
func textOf(msg *anthropic.BetaMessage) string {
	var b strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			b.WriteString(block.Text)
		}
	}
	return b.String()
}

// servedModel returns the model that produced msg (the fallback model when a
// server-side fallback served it), or requested if the response named none.
func servedModel(msg *anthropic.BetaMessage, requested string) string {
	if msg.Model != "" {
		return string(msg.Model)
	}
	return requested
}

// StreamParams configures a streaming LLM call.
type StreamParams struct {
	Model     string
	System    string
	Messages  []anthropic.BetaMessageParam
	MaxTokens int64
	Effort    string // output_config.effort: low, medium, high, xhigh, max; empty = model default
	UserID    uuid.UUID
	Role      string    // "interviewer", "evaluator", etc.
	SessionID uuid.UUID // zero value means no session
}

// StreamAndLog creates a streaming Anthropic request and returns a TokenStream.
// When the stream is closed, the call is persisted to llm_calls + llm_call_content.
func (c *Client) StreamAndLog(ctx context.Context, p StreamParams) (_ *TokenStream, err error) {
	ctx, span := tracer.Start(ctx, "Client.StreamAndLog")
	defer func() { drilotel.End(span, err) }()

	params := c.newParams(request{
		model:     p.Model,
		system:    p.System,
		messages:  p.Messages,
		maxTokens: p.MaxTokens,
		effort:    p.Effort,
	})

	start := time.Now()
	stream := c.anthropic.Beta.Messages.NewStreaming(ctx, params)

	return &TokenStream{
		stream:    stream,
		pool:      c.pool,
		model:     p.Model,
		maxTokens: params.MaxTokens,
		userID:    p.UserID,
		role:      p.Role,
		sessionID: p.SessionID,
		startTime: start,
		prompt:    params,
	}, nil
}

// JSONParams configures a blocking LLM call whose output is constrained to a
// JSON schema (structured outputs, output_config.format).
type JSONParams struct {
	Model     string
	System    string
	Messages  []anthropic.BetaMessageParam
	MaxTokens int64
	Effort    string
	// Schema is the JSON schema the response must match. It must satisfy the
	// structured-outputs subset; see CheckOutputSchema.
	Schema    map[string]any
	UserID    uuid.UUID
	Role      string
	SessionID uuid.UUID
}

// CallJSONAndLog makes a structured-output request and returns the JSON text
// of the response. It persists the call within the caller's transaction.
//
// A refusal returns a *RefusalError and a max_tokens stop returns an error
// wrapping ErrMaxTokens; in both cases nothing is persisted, because the
// output may not match the schema.
func (c *Client) CallJSONAndLog(ctx context.Context, tx pgx.Tx, p JSONParams) (_ json.RawMessage, err error) {
	ctx, span := tracer.Start(ctx, "Client.CallJSONAndLog")
	defer func() { drilotel.End(span, err) }()

	if err := CheckOutputSchema(p.Schema); err != nil {
		return nil, err
	}

	params := c.newParams(request{
		model:     p.Model,
		system:    p.System,
		messages:  p.Messages,
		maxTokens: p.MaxTokens,
		effort:    p.Effort,
		schema:    p.Schema,
	})

	resp, latency, err := c.complete(ctx, params)
	if err != nil {
		return nil, err
	}
	model := servedModel(resp, p.Model)
	if err := checkStop(resp.StopReason, resp.StopDetails, model, params.MaxTokens); err != nil {
		return nil, err
	}

	text := textOf(resp)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("ai: %s returned no text content (stop_reason %q)", model, resp.StopReason)
	}
	out := json.RawMessage(text)
	if !json.Valid(out) {
		return nil, fmt.Errorf("ai: %s returned invalid JSON (stop_reason %q)", model, resp.StopReason)
	}

	// Persist within the caller's transaction. The response column keeps the
	// full message, as the previous tool-use path did.
	respJSON := json.RawMessage(resp.RawJSON())
	if !json.Valid(respJSON) {
		respJSON, err = json.Marshal(resp)
		if err != nil {
			return out, fmt.Errorf("marshal response: %w", err)
		}
	}
	promptJSON, err := json.Marshal(params)
	if err != nil {
		return out, fmt.Errorf("marshal prompt: %w", err)
	}
	err = insertCall(ctx, db.New(tx), callRecord{
		model:        model,
		userID:       p.UserID,
		role:         p.Role,
		sessionID:    p.SessionID,
		inputTokens:  resp.Usage.InputTokens,
		outputTokens: resp.Usage.OutputTokens,
		latency:      latency,
		prompt:       promptJSON,
		response:     respJSON,
	})
	if err != nil {
		return out, err
	}
	return out, nil
}

// CallParams configures a blocking text LLM call.
type CallParams struct {
	Model     string
	System    string
	Messages  []anthropic.BetaMessageParam
	MaxTokens int64
	Effort    string
}

// CallResult holds the response and usage metadata from a blocking LLM call.
type CallResult struct {
	Text         string
	Model        string // model that served the response (may be a fallback model)
	InputTokens  int64
	OutputTokens int64
	Latency      time.Duration
}

// Call makes a blocking Anthropic request without logging. Returns the text
// response and usage metadata. The caller is responsible for persisting the
// call if desired — use this for background/system-level work where logging
// happens in a separate transaction.
//
// A refusal returns a *RefusalError and a max_tokens stop returns an error
// wrapping ErrMaxTokens.
func (c *Client) Call(ctx context.Context, p CallParams) (_ CallResult, err error) {
	ctx, span := tracer.Start(ctx, "Client.Call")
	defer func() { drilotel.End(span, err) }()

	params := c.newParams(request{
		model:     p.Model,
		system:    p.System,
		messages:  p.Messages,
		maxTokens: p.MaxTokens,
		effort:    p.Effort,
	})

	resp, latency, err := c.complete(ctx, params)
	if err != nil {
		return CallResult{}, err
	}
	model := servedModel(resp, p.Model)
	if err := checkStop(resp.StopReason, resp.StopDetails, model, params.MaxTokens); err != nil {
		return CallResult{}, err
	}

	return CallResult{
		Text:         textOf(resp),
		Model:        model,
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
		Latency:      latency,
	}, nil
}

// TokenStream wraps an Anthropic streaming response and yields text tokens
// one at a time. Thinking and fallback blocks are skipped.
type TokenStream struct {
	stream    *ssestream.Stream[anthropic.BetaRawMessageStreamEventUnion]
	pool      *pgxpool.Pool
	model     string // requested model
	maxTokens int64
	userID    uuid.UUID
	role      string
	sessionID uuid.UUID
	startTime time.Time
	prompt    anthropic.BetaMessageNewParams

	mu      sync.Mutex
	buf     strings.Builder // accumulated text
	message anthropic.BetaMessage
	err     error // terminal error, returned by every later Next
	done    bool
}

// Next returns the next text token from the stream.
// Returns io.EOF when the stream completes normally. A refusal returns a
// *RefusalError and a max_tokens stop returns an error wrapping ErrMaxTokens;
// text already returned by Next is partial in both cases and should be
// discarded.
func (ts *TokenStream) Next() (string, error) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if ts.err != nil {
		return "", ts.err
	}
	if ts.done {
		return "", io.EOF
	}

	for ts.stream.Next() {
		event := ts.stream.Current()
		if err := ts.message.Accumulate(event); err != nil {
			ts.err = fmt.Errorf("accumulate: %w", err)
			return "", ts.err
		}

		switch event.Type {
		case "content_block_delta":
			if event.Delta.Type == "text_delta" {
				ts.buf.WriteString(event.Delta.Text)
				return event.Delta.Text, nil
			}
		case "message_delta":
			// stop_reason arrives here, before message_stop.
			if err := checkStop(ts.message.StopReason, ts.message.StopDetails, servedModel(&ts.message, ts.model), ts.maxTokens); err != nil {
				ts.err = err
				return "", err
			}
		}
		// Skip other events (message_start, thinking deltas, block start/stop).
	}

	ts.done = true
	if err := ts.stream.Err(); err != nil {
		ts.err = fmt.Errorf("stream: %w", err)
		return "", ts.err
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
	return ts.persist(ctx, db.New(ts.pool))
}

// CloseWithTx drains the stream if needed, then persists the LLM call within
// the caller's transaction. If tx is nil, persistence is skipped.
func (ts *TokenStream) CloseWithTx(ctx context.Context, tx pgx.Tx) error {
	ts.drain()
	defer ts.stream.Close()

	if tx == nil {
		return nil
	}
	return ts.persist(ctx, db.New(tx))
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

func (ts *TokenStream) persist(ctx context.Context, q *db.Queries) error {
	ts.mu.Lock()
	model := servedModel(&ts.message, ts.model)
	usage := ts.message.Usage
	text := ts.buf.String()
	ts.mu.Unlock()

	promptJSON, err := json.Marshal(ts.prompt)
	if err != nil {
		return fmt.Errorf("marshal prompt: %w", err)
	}
	responseJSON, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return fmt.Errorf("marshal response: %w", err)
	}
	return insertCall(ctx, q, callRecord{
		model:        model,
		userID:       ts.userID,
		role:         ts.role,
		sessionID:    ts.sessionID,
		inputTokens:  usage.InputTokens,
		outputTokens: usage.OutputTokens,
		latency:      time.Since(ts.startTime),
		prompt:       promptJSON,
		response:     responseJSON,
	})
}

// callRecord holds the data written to llm_calls + llm_call_content.
type callRecord struct {
	model        string // model that served the response
	userID       uuid.UUID
	role         string
	sessionID    uuid.UUID
	inputTokens  int64
	outputTokens int64
	latency      time.Duration
	prompt       json.RawMessage
	response     json.RawMessage
}

// insertCall inserts into llm_calls and llm_call_content.
func insertCall(ctx context.Context, q *db.Queries, r callRecord) error {
	cost := EstimateCost(r.model, r.inputTokens, r.outputTokens)

	sessionID := pgtype.UUID{}
	if r.sessionID != uuid.Nil {
		sessionID = pgtype.UUID{Bytes: r.sessionID, Valid: true}
	}
	userID := pgtype.UUID{}
	if r.userID != uuid.Nil {
		userID = pgtype.UUID{Bytes: r.userID, Valid: true}
	}

	callID, err := q.InsertLLMCall(ctx, db.InsertLLMCallParams{
		SessionID:     sessionID,
		UserID:        userID,
		Role:          r.role,
		Model:         r.model,
		InputTokens:   int32(r.inputTokens),
		OutputTokens:  int32(r.outputTokens),
		EstimatedCost: NumericFromFloat(cost),
		LatencyMs:     int32(r.latency.Milliseconds()),
	})
	if err != nil {
		return fmt.Errorf("insert llm_call: %w", err)
	}

	err = q.InsertLLMCallContent(ctx, db.InsertLLMCallContentParams{
		LlmCallID: callID,
		Prompt:    r.prompt,
		Response:  r.response,
	})
	if err != nil {
		return fmt.Errorf("insert llm_call_content: %w", err)
	}
	return nil
}

// EstimateCost returns the estimated cost in USD.
func EstimateCost(model string, inputTokens, outputTokens int64) float64 {
	p, ok := pricing[model]
	if !ok {
		return 0
	}
	return float64(inputTokens)*p.Input + float64(outputTokens)*p.Output
}

// NumericFromFloat converts a float64 to pgtype.Numeric.
func NumericFromFloat(f float64) pgtype.Numeric {
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
