package rpc

import (
	"fmt"
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	authsvc "github.com/btc/drill/internal/rpc/auth"
	"github.com/btc/drill/internal/rpc/billing"
	samplerpc "github.com/btc/drill/internal/rpc/sample"
	"github.com/btc/drill/internal/rpc/coach"
	"github.com/btc/drill/internal/rpc/educator"
	"github.com/btc/drill/internal/rpc/evaluation"
	interviewsvc "github.com/btc/drill/internal/rpc/interview"
	"github.com/btc/drill/internal/rpc/question"
	"github.com/btc/drill/internal/rpc/session"
	"github.com/btc/drill/internal/rpc/user"
)

// ConnectPathPrefixes returns all path prefixes used by registered Connect
// services. Used by the CSRF middleware to exempt Connect routes.
func ConnectPathPrefixes() []string {
	return []string{
		drillv1connect.AuthServiceName,
		drillv1connect.BillingServiceName,
		drillv1connect.EducatorServiceName,
		drillv1connect.QuestionServiceName,
		drillv1connect.CoachServiceName,
		drillv1connect.EvaluationServiceName,
		drillv1connect.InterviewServiceName,
		drillv1connect.SampleServiceName,
		drillv1connect.SessionServiceName,
		drillv1connect.UserServiceName,
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

	// Public endpoints (no auth interceptor).
	publicOpts := connect.WithInterceptors(otelInterceptor)
	mux.Handle(drillv1connect.NewAuthServiceHandler(authsvc.NewServer(b), publicOpts))
	mux.Handle(drillv1connect.NewSampleServiceHandler(samplerpc.NewServer(b.SampleService), publicOpts))

	mux.Handle(drillv1connect.NewBillingServiceHandler(billing.NewServer(b), opts))
	mux.Handle(drillv1connect.NewEducatorServiceHandler(educator.NewServer(b), opts))
	mux.Handle(drillv1connect.NewQuestionServiceHandler(question.NewServer(b), opts))
	mux.Handle(drillv1connect.NewCoachServiceHandler(coach.NewServer(b), opts))
	mux.Handle(drillv1connect.NewEvaluationServiceHandler(evaluation.NewServer(b), opts))
	mux.Handle(drillv1connect.NewInterviewServiceHandler(interviewsvc.NewServer(b), opts))
	mux.Handle(drillv1connect.NewSessionServiceHandler(session.NewServer(b), opts))
	mux.Handle(drillv1connect.NewUserServiceHandler(user.NewServer(b), opts))
	return nil
}
