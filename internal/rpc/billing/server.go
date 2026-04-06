package billing

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

// Server implements the BillingService Connect handler.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.BillingServiceHandler = (*Server)(nil)

// NewServer creates a new BillingService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// Checkout creates a Stripe Checkout session and returns the checkout URL.
func (s *Server) Checkout(
	ctx context.Context,
	req *connect.Request[drillv1.CheckoutRequest],
) (*connect.Response[drillv1.CheckoutResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	url, err := s.b.CreateCheckoutSession(ctx, backend.CheckoutParams{
		UserID:      user.ID,
		Email:       user.Email,
		Type:        req.Msg.Type,
		Plan:        req.Msg.Plan,
		PackMinutes: int(req.Msg.Minutes),
	})
	if err != nil {
		slog.Error("create checkout session", "error", err, "user_id", user.ID)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to create checkout session"))
	}

	return connect.NewResponse(&drillv1.CheckoutResponse{
		Url: url,
	}), nil
}

// Portal creates a Stripe billing portal session and returns the portal URL.
func (s *Server) Portal(
	ctx context.Context,
	req *connect.Request[drillv1.PortalRequest],
) (*connect.Response[drillv1.PortalResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	url, err := s.b.CreatePortalSession(ctx, user.ID)
	if err != nil {
		if errors.Is(err, backend.ErrNoStripeAccount) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("no billing account"))
		}
		slog.Error("create portal session", "error", err, "user_id", user.ID)
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to create portal session"))
	}

	return connect.NewResponse(&drillv1.PortalResponse{
		Url: url,
	}), nil
}
