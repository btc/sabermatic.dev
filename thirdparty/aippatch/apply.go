package aippatch

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
)

// Apply executes the PATCH described by op against m and returns the
// updated proto message populated from the RETURNING row.
func Apply[T proto.Message](
	ctx context.Context, db DBTX,
	m *Mapping[T], op Op[T],
) (T, error) {
	var zero T

	// 0. Sanity guards.
	if m == nil || !m.validated.Load() {
		return zero, connectInternal(
			"aippatch.Mapping not initialized; call your generated InitPatches() (or Mapping.Validate) during startup")
	}
	src := op.Message.ProtoReflect()
	if !src.IsValid() {
		return zero, connectInvalidArg("op.Message must not be nil")
	}

	// 1. Mask validation. GetPaths is nil-safe.
	paths := op.Mask.GetPaths()
	if len(paths) == 0 {
		if m.EmptyMask == ErrorOnEmpty {
			return zero, connectInvalidArg("update_mask must not be empty")
		}
		// UpdateAllWritable: not implemented in v0. CodeUnimplemented
		// signals "feature not yet built" rather than a server bug.
		return zero, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("UpdateAllWritable is unimplemented in v0"))
	}
	_ = ctx
	_ = db
	_ = src
	return zero, connectInternal("Apply: SET clause not yet implemented")
}
