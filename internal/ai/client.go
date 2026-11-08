package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	// RecommendedModel is stop_details.recommended_model: the fallback model
	// the API suggests retrying on directly. The API sets it only when a
	// fallback attempt could not run (the fallback model was rate-limited or
	// overloaded); empty otherwise.
	RecommendedModel string
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
	if e.RecommendedModel != "" {
		msg += " (recommended retry model: " + e.RecommendedModel + ")"
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
func (c *Client) newParams(r *request) anthropic.BetaMessageNewParams {
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
func (c *Client) complete(ctx context.Context, params *anthropic.BetaMessageNewParams) (*anthropic.BetaMessage, time.Duration, error) {
	start := time.Now()
	stream := c.anthropic.Beta.Messages.NewStreaming(ctx, *params)
	defer closeStream(stream)

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

// closeStream closes an Anthropic event stream, releasing its HTTP response
// body. A close error has no effect on a result already read from the
// stream, so it is logged rather than returned.
func closeStream(stream *ssestream.Stream[anthropic.BetaRawMessageStreamEventUnion]) {
	if err := stream.Close(); err != nil {
		slog.Warn("ai: close anthropic stream", "error", err)
	}
}

// checkStop converts the stop reasons that mean "no usable output" into errors.
// maxTokens is the limit the request was sent with, for the error message.
func checkStop(stopReason anthropic.BetaStopReason, details *anthropic.BetaRefusalStopDetails, model string, maxTokens int64) error {
	switch stopReason {
	case anthropic.BetaStopReasonRefusal:
		return &RefusalError{
			Model:            model,
			Category:         string(details.Category),
			Explanation:      details.Explanation,
			RecommendedModel: details.RecommendedModel,
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
	for i := range msg.Content {
		block := &msg.Content[i]
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

	params := c.newParams(&request{
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
// of the response. On success it persists the call within the caller's
// transaction; a nil tx skips persistence.
//
// A refusal returns a *RefusalError and a max_tokens stop returns an error
// wrapping ErrMaxTokens. Those responses, and responses with no text or with
// invalid JSON, are still billed, so the call is persisted through the pool
// (outside tx, which the caller rolls back on error) before the error is
// returned. Persistence is skipped when the client has no pool.
func (c *Client) CallJSONAndLog(ctx context.Context, tx pgx.Tx, p *JSONParams) (_ json.RawMessage, err error) {
	ctx, span := tracer.Start(ctx, "Client.CallJSONAndLog")
	defer func() { drilotel.End(span, err) }()

	if err := CheckOutputSchema(p.Schema); err != nil {
		return nil, err
	}

	params := c.newParams(&request{
		model:     p.Model,
		system:    p.System,
		messages:  p.Messages,
		maxTokens: p.MaxTokens,
		effort:    p.Effort,
		schema:    p.Schema,
	})

	resp, latency, err := c.complete(ctx, &params)
	if err != nil {
		return nil, err
	}
	model := servedModel(resp, p.Model)
	rec := func() (callRecord, error) {
		return messageRecord(resp, model, &params, p.UserID, p.Role, p.SessionID, latency)
	}

	out, outErr := jsonOutput(resp, model, params.MaxTokens)
	if outErr != nil {
		c.persistFailed(ctx, rec, outErr)
		return nil, outErr
	}

	if tx == nil {
		return out, nil
	}
	r, err := rec()
	if err != nil {
		return out, err
	}
	if err := insertCall(ctx, db.New(tx), &r); err != nil {
		return out, err
	}
	return out, nil
}

// jsonOutput returns the JSON text of a structured-output response, or the
// error that makes the response unusable.
func jsonOutput(resp *anthropic.BetaMessage, model string, maxTokens int64) (json.RawMessage, error) {
	if err := checkStop(resp.StopReason, &resp.StopDetails, model, maxTokens); err != nil {
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
	return out, nil
}

// messageRecord builds the llm_calls record for a completed blocking call.
// The response column keeps the full message, which includes stop_reason and
// stop_details.
func messageRecord(resp *anthropic.BetaMessage, model string, params *anthropic.BetaMessageNewParams, userID uuid.UUID, role string, sessionID uuid.UUID, latency time.Duration) (callRecord, error) {
	respJSON := json.RawMessage(resp.RawJSON())
	if !json.Valid(respJSON) {
		var err error
		respJSON, err = json.Marshal(resp)
		if err != nil {
			return callRecord{}, fmt.Errorf("marshal response: %w", err)
		}
	}
	promptJSON, err := json.Marshal(params)
	if err != nil {
		return callRecord{}, fmt.Errorf("marshal prompt: %w", err)
	}
	return callRecord{
		model:     model,
		userID:    userID,
		role:      role,
		sessionID: sessionID,
		usage:     resp.Usage,
		latency:   latency,
		prompt:    promptJSON,
		response:  respJSON,
	}, nil
}

// persistFailed records a billed call whose output could not be used. It
// writes through the pool because the caller's transaction rolls back on
// error. Failures are logged, not returned: callErr is the error the caller
// needs to see.
func (c *Client) persistFailed(ctx context.Context, rec func() (callRecord, error), callErr error) {
	if c.pool == nil {
		return
	}
	r, err := rec()
	if err == nil {
		err = insertCall(ctx, db.New(c.pool), &r)
	}
	if err != nil {
		slog.Warn("ai: persist llm_call record for failed call", "error", err, "call_error", callErr)
	}
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
	Text  string
	Model string // model that served the response (may be a fallback model)
	// InputTokens and OutputTokens are billed totals: summed over
	// usage.iterations when the API reports per-attempt usage (a server-side
	// fallback ran), else the top-level usage.
	InputTokens  int64
	OutputTokens int64
	// Cost is the estimated USD cost, each attempt priced at the model that
	// ran it.
	Cost    float64
	Latency time.Duration
}

// Call makes a blocking Anthropic request without logging. Returns the text
// response and usage metadata. The caller is responsible for persisting the
// call if desired — use this for background/system-level work where logging
// happens in a separate transaction.
//
// A refusal returns a *RefusalError and a max_tokens stop returns an error
// wrapping ErrMaxTokens. Both are billed, so on those errors the returned
// CallResult still carries Model, token counts, Cost and Latency (Text is
// empty) for the caller to log.
func (c *Client) Call(ctx context.Context, p CallParams) (_ CallResult, err error) {
	ctx, span := tracer.Start(ctx, "Client.Call")
	defer func() { drilotel.End(span, err) }()

	params := c.newParams(&request{
		model:     p.Model,
		system:    p.System,
		messages:  p.Messages,
		maxTokens: p.MaxTokens,
		effort:    p.Effort,
	})

	resp, latency, err := c.complete(ctx, &params)
	if err != nil {
		return CallResult{}, err
	}
	model := servedModel(resp, p.Model)
	in, out := billedTokens(&resp.Usage)
	result := CallResult{
		Model:        model,
		InputTokens:  in,
		OutputTokens: out,
		Cost:         usageCost(&resp.Usage, model),
		Latency:      latency,
	}
	if err := checkStop(resp.StopReason, &resp.StopDetails, model, params.MaxTokens); err != nil {
		return result, err
	}
	result.Text = textOf(resp)
	return result, nil
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
			if err := checkStop(ts.message.StopReason, &ts.message.StopDetails, servedModel(&ts.message, ts.model), ts.maxTokens); err != nil {
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
	defer closeStream(ts.stream)

	if ts.pool == nil {
		return nil
	}
	return ts.persist(ctx, db.New(ts.pool))
}

// CloseWithTx drains the stream if needed, then persists the LLM call within
// the caller's transaction. If tx is nil, persistence is skipped.
func (ts *TokenStream) CloseWithTx(ctx context.Context, tx pgx.Tx) error {
	ts.drain()
	defer closeStream(ts.stream)

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
	if ts.err != nil {
		// Next already stopped on an error. A stop-reason error (refusal,
		// max_tokens) arrives in message_delta, which carries the final usage,
		// so only message_stop is left. Any other error means the stream is
		// broken or the rest of the generation is unusable; reading it to the
		// end would block Close for the remaining generation time.
		ts.done = true
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
	return insertCall(ctx, q, &callRecord{
		model:     model,
		userID:    ts.userID,
		role:      ts.role,
		sessionID: ts.sessionID,
		usage:     usage,
		latency:   time.Since(ts.startTime),
		prompt:    promptJSON,
		response:  responseJSON,
	})
}

// callRecord holds the data written to llm_calls + llm_call_content.
type callRecord struct {
	model     string // model that served the response
	userID    uuid.UUID
	role      string
	sessionID uuid.UUID
	usage     anthropic.BetaUsage
	latency   time.Duration
	prompt    json.RawMessage
	response  json.RawMessage
}

// insertCall inserts into llm_calls and llm_call_content. The token columns
// and estimated_cost cover every billed attempt (see billedTokens and
// usageCost); the model column names the model that served the response.
func insertCall(ctx context.Context, q *db.Queries, r *callRecord) error {
	cost := usageCost(&r.usage, r.model)
	inputTokens, outputTokens := billedTokens(&r.usage)

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
		InputTokens:   int32(inputTokens),
		OutputTokens:  int32(outputTokens),
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

// billedTokens returns the input and output tokens billed for a response.
// With server-side fallbacks, top-level usage covers only the attempt that
// produced the returned message, and usage.iterations is the per-attempt
// record (a declined attempt is billed too), so the iterations are summed
// when present.
func billedTokens(u *anthropic.BetaUsage) (input, output int64) {
	if len(u.Iterations) == 0 {
		return u.InputTokens, u.OutputTokens
	}
	for i := range u.Iterations {
		it := &u.Iterations[i]
		input += it.InputTokens
		output += it.OutputTokens
	}
	return input, output
}

// usageCost returns the estimated USD cost of a response. When
// usage.iterations is present, each attempt is priced at the model that ran
// it (an entry without a model, such as compaction, is priced at
// servedModel); otherwise the top-level usage is priced at servedModel.
func usageCost(u *anthropic.BetaUsage, servedModel string) float64 {
	if len(u.Iterations) == 0 {
		return EstimateCost(servedModel, u.InputTokens, u.OutputTokens)
	}
	var cost float64
	for i := range u.Iterations {
		it := &u.Iterations[i]
		model := string(it.Model)
		if model == "" {
			model = servedModel
		}
		cost += EstimateCost(model, it.InputTokens, it.OutputTokens)
	}
	return cost
}

// warnedUnknownModels records models already reported as missing from the
// pricing map, so EstimateCost warns once per model per process.
var warnedUnknownModels sync.Map

// EstimateCost returns the estimated cost in USD. A model missing from the
// pricing map costs 0 and logs a warning once per process: under
// fallbacks:"default" Anthropic picks the serving model server-side, and the
// *_MODEL env vars accept any model, so a new model can appear at runtime.
func EstimateCost(model string, inputTokens, outputTokens int64) float64 {
	p, ok := pricing[model]
	if !ok {
		if _, seen := warnedUnknownModels.LoadOrStore(model, true); !seen {
			slog.Warn("ai: no pricing for model; llm_calls.estimated_cost will record 0", "model", model)
		}
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
