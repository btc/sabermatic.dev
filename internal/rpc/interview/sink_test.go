package interview

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

// concurrentSinkHandler serves SubmitTurn by sending interviewer tokens and
// TTS chunks from two goroutines at once through one connectTurnSink, as
// ExecuteTurn does (the conductor goroutine streams tokens while the TTS
// goroutine streams audio).
type concurrentSinkHandler struct {
	drillv1connect.UnimplementedInterviewServiceHandler
	perSender int
}

func (h *concurrentSinkHandler) SubmitTurn(_ context.Context, _ *connect.Request[drillv1.SubmitTurnRequest], stream *connect.ServerStream[drillv1.TurnEvent]) error {
	sink := &connectTurnSink{stream: stream}
	audio := make([]byte, 4096)
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := range h.perSender {
			sink.InterviewerToken(&drillv1.InterviewerToken{Token: fmt.Sprintf("token %d ", i)})
		}
	})
	wg.Go(func() {
		for i := range h.perSender {
			sink.TtsChunk(&drillv1.TtsChunk{Data: audio, Seq: int32(i), MimeType: "audio/L16;rate=24000;channels=1"}) //nolint:gosec // small test counter
		}
	})
	wg.Wait()
	return nil
}

// TestConnectTurnSink_ConcurrentSends checks that events sent from several
// goroutines arrive intact. connect.ServerStream.Send is not safe for
// concurrent use; without serialization the frames interleave and the
// browser's JSON decoder fails mid-turn.
func TestConnectTurnSink_ConcurrentSends(t *testing.T) {
	const perSender = 300
	_, h := drillv1connect.NewInterviewServiceHandler(&concurrentSinkHandler{perSender: perSender})
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	// The browser uses the Connect protocol with JSON.
	client := drillv1connect.NewInterviewServiceClient(srv.Client(), srv.URL, connect.WithProtoJSON())
	stream, err := client.SubmitTurn(context.Background(), connect.NewRequest(&drillv1.SubmitTurnRequest{}))
	require.NoError(t, err)

	tokens, chunks := 0, 0
	for stream.Receive() {
		switch stream.Msg().Event.(type) {
		case *drillv1.TurnEvent_InterviewerToken:
			tokens++
		case *drillv1.TurnEvent_TtsChunk:
			chunks++
		}
	}
	require.NoError(t, stream.Err())
	require.NoError(t, stream.Close())
	require.Equal(t, perSender, tokens)
	require.Equal(t, perSender, chunks)
}
