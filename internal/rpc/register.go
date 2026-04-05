package rpc

import (
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	"github.com/btc/drill/internal/rpc/question"
)

// ConnectPathPrefixes returns all path prefixes used by registered Connect
// services. Used by the CSRF middleware to exempt Connect routes.
func ConnectPathPrefixes() []string {
	return []string{
		drillv1connect.QuestionServiceName,
	}
}

// Register mounts all ConnectRPC services on the given mux.
func Register(mux *http.ServeMux, b *backend.Backend) error {
	otelInterceptor, err := otelconnect.NewInterceptor()
	if err != nil {
		return fmt.Errorf("otelconnect: %w", err)
	}

	opts := connect.WithInterceptors(
		otelInterceptor,
		AuthInterceptor(b),
	)

	mux.Handle(drillv1connect.NewQuestionServiceHandler(question.NewServer(b), opts))
	return nil
}
