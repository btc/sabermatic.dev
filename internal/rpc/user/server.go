package user

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/reflect/protoreflect"
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

	return connect.NewResponse(usageSummaryToProto(summary)), nil
}

// usageSummaryToProto converts a backend UsageSummary to a GetUsageResponse.
// make() with len() produces non-nil empty slices even when the input is nil,
// so proto encoding always emits [] rather than null.
func usageSummaryToProto(s *backend.UsageSummary) *drillv1.GetUsageResponse {
	grants := make([]*drillv1.Grant, len(s.Grants))
	for i := range s.Grants {
		grants[i] = grantToProto(&s.Grants[i])
	}

	entries := make([]*drillv1.LedgerEntry, len(s.RecentActivity))
	for i, e := range s.RecentActivity {
		entries[i] = ledgerEntryToProto(e)
	}

	return &drillv1.GetUsageResponse{
		TotalBalance:   s.TotalBalance,
		FreeBalance:    s.FreeBalance,
		PaidBalance:    s.PaidBalance,
		Grants:         grants,
		RecentActivity: entries,
	}
}

// implementedUserFields is the allow-list of User proto fields that UpdateProfile
// supports. Fields not in this map are valid proto fields but not yet updatable.
var implementedUserFields = map[string]bool{
	"display_name": true,
}

// UpdateProfile updates the authenticated user's profile fields.
// AIP-134: PATCH semantics via update_mask. Only fields in the mask are modified.
func (s *Server) UpdateProfile(
	ctx context.Context,
	req *connect.Request[drillv1.UpdateProfileRequest],
) (*connect.Response[drillv1.UpdateProfileResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}

	mask := req.Msg.GetUpdateMask()
	if mask == nil || len(mask.Paths) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("update_mask must not be empty"))
	}

	// Validate each path against the proto descriptor and the allow-list.
	userDesc := (*drillv1.User)(nil).ProtoReflect().Descriptor()
	for _, path := range mask.Paths {
		fd := userDesc.Fields().ByName(protoreflect.Name(path))
		if fd == nil {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("unknown field in update_mask: %q", path))
		}
		if !implementedUserFields[path] {
			return nil, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("field not supported for update: %q", path))
		}
	}

	displayName := strings.TrimSpace(req.Msg.GetUser().GetDisplayName())
	if displayName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("display_name must not be empty"))
	}

	updated, err := s.b.UpdateDisplayName(ctx, user.ID, displayName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("update profile failed"))
	}

	return connect.NewResponse(&drillv1.UpdateProfileResponse{
		User: dbUserToProto(&updated),
	}), nil
}

// ExportData triggers an export of the authenticated user's data.
// TODO: implement when backend export mechanism is designed.
func (s *Server) ExportData(
	ctx context.Context,
	req *connect.Request[drillv1.ExportDataRequest],
) (*connect.Response[drillv1.ExportDataResponse], error) {
	if auth.UserFromContext(ctx) == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("ExportData not yet implemented"))
}

// dbUserToProto converts a db.User (sqlc model) to its proto representation.
// Used by UpdateProfile which returns the updated record from the database.
func dbUserToProto(u *db.User) *drillv1.User {
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

func grantToProto(g *db.ListActiveGrantsRow) *drillv1.Grant {
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
