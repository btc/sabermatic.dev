// Package aitest provides fake Anthropic Messages API servers for tests.
//
// Every ai.Client call streams (the blocking JSON and text paths accumulate the
// stream internally), so the fakes here speak server-sent events only.
package aitest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Block is one content block in a fake response.
type Block struct {
	Type string // "text", "thinking", or "fallback"
	Text string // text for "text" blocks; ignored otherwise
	// Deltas splits Text into several text_delta events. When nil, Text is
	// sent as a single delta.
	Deltas []string
	// FallbackFrom and FallbackTo name the models for a "fallback" block.
	FallbackFrom, FallbackTo string
}

// TextBlock returns a text block streamed as the given deltas.
func TextBlock(deltas ...string) Block {
	text := ""
	for _, d := range deltas {
		text += d
	}
	return Block{Type: "text", Text: text, Deltas: deltas}
}

// ThinkingBlock returns a thinking block with empty text, which is what the
// API sends under the default thinking display ("omitted").
func ThinkingBlock() Block { return Block{Type: "thinking"} }

// FallbackBlock returns the marker block the API inserts when a server-side
// refusal fallback switches models.
func FallbackBlock(from, to string) Block {
	return Block{Type: "fallback", FallbackFrom: from, FallbackTo: to}
}

// Response describes a complete fake streamed response.
type Response struct {
	// Model is the model named in message_start.
	Model      string
	Blocks     []Block
	StopReason string // defaults to "end_turn"
	// RefusalCategory, when StopReason is "refusal", is sent as
	// stop_details.category.
	RefusalCategory string
	InputTokens     int64
	OutputTokens    int64
	// RecommendedModel, when StopReason is "refusal", is sent as
	// stop_details.recommended_model.
	RecommendedModel string
	// Iterations, when set, is sent as usage.iterations in message_delta: the
	// per-attempt usage the API reports when a server-side fallback ran.
	Iterations []Iteration
}

// Iteration is one entry of usage.iterations.
type Iteration struct {
	Type         string // "message" or "fallback_message"
	Model        string
	InputTokens  int64
	OutputTokens int64
}

// WriteSSE writes resp to w as an Anthropic Messages streaming response.
func WriteSSE(w http.ResponseWriter, resp Response) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)

	stopReason := resp.StopReason
	if stopReason == "" {
		stopReason = "end_turn"
	}

	event(w, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_test", "type": "message", "role": "assistant",
			"content": []any{}, "model": resp.Model,
			"stop_reason": nil, "stop_sequence": nil, "stop_details": nil,
			"usage": map[string]any{"input_tokens": resp.InputTokens, "output_tokens": 0},
		},
	})

	for i, b := range resp.Blocks {
		switch b.Type {
		case "thinking":
			event(w, "content_block_start", map[string]any{
				"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""},
			})
			event(w, "content_block_delta", map[string]any{
				"type": "content_block_delta", "index": i,
				"delta": map[string]any{"type": "signature_delta", "signature": "sig"},
			})
		case "fallback":
			event(w, "content_block_start", map[string]any{
				"type": "content_block_start", "index": i,
				"content_block": map[string]any{
					"type": "fallback",
					"from": map[string]any{"model": b.FallbackFrom},
					"to":   map[string]any{"model": b.FallbackTo},
				},
			})
		default:
			event(w, "content_block_start", map[string]any{
				"type": "content_block_start", "index": i,
				"content_block": map[string]any{"type": "text", "text": ""},
			})
			deltas := b.Deltas
			if deltas == nil {
				deltas = []string{b.Text}
			}
			for _, d := range deltas {
				event(w, "content_block_delta", map[string]any{
					"type": "content_block_delta", "index": i,
					"delta": map[string]any{"type": "text_delta", "text": d},
				})
			}
		}
		event(w, "content_block_stop", map[string]any{"type": "content_block_stop", "index": i})
	}

	var stopDetails any
	if stopReason == "refusal" {
		details := map[string]any{
			"type":        "refusal",
			"category":    resp.RefusalCategory,
			"explanation": "declined by test server",
		}
		if resp.RecommendedModel != "" {
			details["recommended_model"] = resp.RecommendedModel
		}
		stopDetails = details
	}
	usage := map[string]any{"output_tokens": resp.OutputTokens}
	if len(resp.Iterations) > 0 {
		its := make([]any, 0, len(resp.Iterations))
		for _, it := range resp.Iterations {
			its = append(its, map[string]any{
				"type":                        it.Type,
				"model":                       it.Model,
				"input_tokens":                it.InputTokens,
				"output_tokens":               it.OutputTokens,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     0,
			})
		}
		usage["iterations"] = its
	}
	event(w, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil, "stop_details": stopDetails},
		"usage": usage,
	})
	event(w, "message_stop", map[string]any{"type": "message_stop"})
}

func event(w io.Writer, name string, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("aitest: marshal %s: %v", name, err))
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
}

// Server is a fake Anthropic server that answers every request with the same
// response and records the request bodies and headers it receives.
type Server struct {
	*httptest.Server

	mu       sync.Mutex
	bodies   []map[string]any
	headers  []http.Header
	response Response
}

// NewServer starts a Server that answers with resp. It is closed on test
// cleanup.
func NewServer(t *testing.T, resp Response) *Server {
	t.Helper()
	s := &Server{response: resp}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.bodies = append(s.bodies, body)
		s.headers = append(s.headers, r.Header.Clone())
		resp := s.response
		s.mu.Unlock()
		WriteSSE(w, resp)
	}))
	t.Cleanup(s.Close)
	return s
}

// SetResponse replaces the response sent to later requests.
func (s *Server) SetResponse(resp Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.response = resp
}

// Requests returns the decoded JSON bodies received so far.
func (s *Server) Requests() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.bodies...)
}

// Headers returns the request headers received so far.
func (s *Server) Headers() []http.Header {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]http.Header(nil), s.headers...)
}
