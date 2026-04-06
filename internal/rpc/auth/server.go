package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"connectrpc.com/connect"

	iauth "github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

// Server implements the AuthService Connect handler.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.AuthServiceHandler = (*Server)(nil)

// NewServer creates a new AuthService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// Signup creates a new user account.
func (s *Server) Signup(
	ctx context.Context,
	req *connect.Request[drillv1.SignupRequest],
) (*connect.Response[drillv1.SignupResponse], error) {
	result, err := s.b.Signup(ctx, backend.SignupParams{
		Email:       req.Msg.Email,
		Password:    req.Msg.Password,
		DisplayName: req.Msg.DisplayName,
	})
	if err != nil {
		switch {
		case errors.Is(err, backend.ErrDuplicateEmail):
			return nil, connect.NewError(connect.CodeAlreadyExists, errors.New(err.Error()))
		case errors.Is(err, backend.ErrPasswordLength):
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(err.Error()))
		case errors.Is(err, backend.ErrMissingFields):
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(err.Error()))
		default:
			return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
		}
	}

	return connect.NewResponse(&drillv1.SignupResponse{
		Id:    result.UserID.String(),
		Email: result.Email,
	}), nil
}

// Login authenticates a user and sets a session cookie.
func (s *Server) Login(
	ctx context.Context,
	req *connect.Request[drillv1.LoginRequest],
) (*connect.Response[drillv1.LoginResponse], error) {
	result, err := s.b.Login(ctx, backend.LoginParams{
		Email:     req.Msg.Email,
		Password:  req.Msg.Password,
		IP:        req.Peer().Addr,
		UserAgent: req.Header().Get("User-Agent"),
	})
	if err != nil {
		if errors.Is(err, backend.ErrInvalidCredentials) {
			return nil, connect.NewError(connect.CodeUnauthenticated, errors.New(err.Error()))
		}
		slog.Error("login failed", "error", err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	cfg := s.b.Config()
	cookie := iauth.SessionCookie(
		result.Token,
		int(cfg.Auth.SessionTTL.Seconds()),
		cfg.Auth.SecureCookies(),
	)

	resp := connect.NewResponse(&drillv1.LoginResponse{
		Id:    result.UserID.String(),
		Email: result.Email,
	})
	resp.Header().Set("Set-Cookie", cookie.String())
	return resp, nil
}

// Logout invalidates the current session and clears the cookie.
func (s *Server) Logout(
	ctx context.Context,
	req *connect.Request[drillv1.LogoutRequest],
) (*connect.Response[drillv1.LogoutResponse], error) {
	// Read cookie from the request header.
	cookie, err := (&http.Request{Header: req.Header()}).Cookie(iauth.SessionCookieName)
	if err == nil && cookie.Value != "" {
		if logoutErr := s.b.Logout(ctx, cookie.Value); logoutErr != nil {
			slog.Error("logout session delete", "error", logoutErr)
		}
	}

	// Clear cookie regardless.
	cfg := s.b.Config()
	clearCookie := iauth.SessionCookie("", -1, cfg.Auth.SecureCookies())

	resp := connect.NewResponse(&drillv1.LogoutResponse{})
	resp.Header().Set("Set-Cookie", clearCookie.String())
	return resp, nil
}

// VerifyEmail marks a user's email as verified using a signed token.
func (s *Server) VerifyEmail(
	ctx context.Context,
	req *connect.Request[drillv1.VerifyEmailRequest],
) (*connect.Response[drillv1.VerifyEmailResponse], error) {
	if err := s.b.VerifyEmail(ctx, req.Msg.Token); err != nil {
		if errors.Is(err, backend.ErrInvalidToken) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(err.Error()))
		}
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}

	return connect.NewResponse(&drillv1.VerifyEmailResponse{}), nil
}

// ForgotPassword enqueues a password-reset email. Always succeeds to prevent
// email enumeration.
func (s *Server) ForgotPassword(
	ctx context.Context,
	req *connect.Request[drillv1.ForgotPasswordRequest],
) (*connect.Response[drillv1.ForgotPasswordResponse], error) {
	err := s.b.ForgotPassword(ctx, req.Msg.Email)
	if err != nil && !errors.Is(err, backend.ErrUserNotFound) {
		slog.Error("forgot password", "error", err)
	}
	// Always success to prevent email enumeration.
	return connect.NewResponse(&drillv1.ForgotPasswordResponse{}), nil
}

// ResetPassword resets a user's password via a signed token.
func (s *Server) ResetPassword(
	ctx context.Context,
	req *connect.Request[drillv1.ResetPasswordRequest],
) (*connect.Response[drillv1.ResetPasswordResponse], error) {
	if err := s.b.ResetPassword(ctx, backend.ResetPasswordParams{
		Token:       req.Msg.Token,
		NewPassword: req.Msg.Password,
	}); err != nil {
		switch {
		case errors.Is(err, backend.ErrPasswordLength):
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(err.Error()))
		case errors.Is(err, backend.ErrInvalidToken):
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(err.Error()))
		default:
			return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
		}
	}

	return connect.NewResponse(&drillv1.ResetPasswordResponse{}), nil
}

// DeleteAccount soft-deletes the authenticated user's account.
// TODO: implement when backend DeleteAccount method is added.
func (s *Server) DeleteAccount(
	ctx context.Context,
	req *connect.Request[drillv1.DeleteAccountRequest],
) (*connect.Response[drillv1.DeleteAccountResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("DeleteAccount not yet implemented"))
}
