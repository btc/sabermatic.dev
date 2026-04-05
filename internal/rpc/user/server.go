package user

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/db"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

// auth.UserFromContext returns *auth.AuthUser, which includes CreatedAt
// populated from the users join in GetAuthSessionByToken.

// Server implements the UserService Connect handler.
type Server struct {
	b *backend.Backend
}

var _ drillv1connect.UserServiceHandler = (*Server)(nil)

// NewServer creates a new UserService handler.
func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

// GetMe returns the authenticated user's profile.
func (s *Server) GetMe(
	ctx context.Context,
	req *connect.Request[drillv1.GetMeRequest],
) (*connect.Response[drillv1.GetMeResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	return connect.NewResponse(&drillv1.GetMeResponse{
		User: userToProto(user),
	}), nil
}

// GetUsage returns the authenticated user's balance and usage info.
func (s *Server) GetUsage(
	ctx context.Context,
	req *connect.Request[drillv1.GetUsageRequest],
) (*connect.Response[drillv1.GetUsageResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	summary, err := s.b.GetUsageSummary(ctx, user.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("get usage failed"))
	}

	grants := make([]*drillv1.Grant, len(summary.Grants))
	for i, g := range summary.Grants {
		grants[i] = grantToProto(g)
	}

	entries := make([]*drillv1.LedgerEntry, len(summary.RecentActivity))
	for i, e := range summary.RecentActivity {
		entries[i] = ledgerEntryToProto(e)
	}

	return connect.NewResponse(&drillv1.GetUsageResponse{
		TotalBalance:   summary.TotalBalance,
		FreeBalance:    summary.FreeBalance,
		PaidBalance:    summary.PaidBalance,
		Grants:         grants,
		RecentActivity: entries,
	}), nil
}

// UpdateProfile updates the authenticated user's profile fields.
// AIP-134: PATCH semantics via update_mask. Only fields in the mask are modified.
// TODO: implement when backend UpdateUser method is added.
func (s *Server) UpdateProfile(
	ctx context.Context,
	req *connect.Request[drillv1.UpdateProfileRequest],
) (*connect.Response[drillv1.UpdateProfileResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("UpdateProfile not yet implemented"))
}

// ExportData triggers an export of the authenticated user's data.
// TODO: implement when backend export mechanism is designed.
func (s *Server) ExportData(
	ctx context.Context,
	req *connect.Request[drillv1.ExportDataRequest],
) (*connect.Response[drillv1.ExportDataResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("ExportData not yet implemented"))
}

func userToProto(u *auth.AuthUser) *drillv1.User {
	return &drillv1.User{
		Id:            u.ID.String(),
		Email:         u.Email,
		DisplayName:   u.DisplayName,
		Role:          roleToProto(u.Role),
		Plan:          planToProto(u.Plan),
		EmailVerified: u.EmailVerified,
		CreateTime:    timestamppb.New(u.CreatedAt),
	}
}

func roleToProto(s string) drillv1.UserRole {
	switch s {
	case "candidate":
		return drillv1.UserRole_USER_ROLE_CANDIDATE
	case "admin":
		return drillv1.UserRole_USER_ROLE_ADMIN
	default:
		return drillv1.UserRole_USER_ROLE_UNSPECIFIED
	}
}

func planToProto(s string) drillv1.UserPlan {
	switch s {
	case "free":
		return drillv1.UserPlan_USER_PLAN_FREE
	case "pro":
		return drillv1.UserPlan_USER_PLAN_PRO
	default:
		return drillv1.UserPlan_USER_PLAN_UNSPECIFIED
	}
}

func grantToProto(g db.ListActiveGrantsRow) *drillv1.Grant {
	grant := &drillv1.Grant{
		Id:               g.ID.String(),
		Source:           g.Source,
		InitialMinutes:   g.InitialMinutes,
		RemainingMinutes: g.RemainingMinutes,
		CreateTime:       timestamppb.New(g.CreatedAt),
	}
	if g.ExpiresAt.Valid {
		grant.ExpireTime = timestamppb.New(g.ExpiresAt.Time)
	}
	return grant
}

func ledgerEntryToProto(e db.GetRecentLedgerEntriesRow) *drillv1.LedgerEntry {
	entry := &drillv1.LedgerEntry{
		Amount:     e.Amount,
		Reason:     e.Reason,
		CreateTime: timestamppb.New(e.CreatedAt),
	}
	if e.SessionID.Valid {
		sid := uuid.UUID(e.SessionID.Bytes).String()
		entry.SessionId = &sid
	}
	return entry
}
