package aippatch

import (
	"context"

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

	_ = ctx
	_ = db
	_ = src
	return zero, connectInternal("Apply: not yet implemented")
}
