package aippatch

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	fixturepb "github.com/btc/drill/thirdparty/aippatch/internal/fixturepb"
)

func TestApply_NilMappingReturnsInternal(t *testing.T) {
	_, err := Apply[*fixturepb.Widget](context.Background(), nil, nil, Op[*fixturepb.Widget]{})
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

func TestApply_UnvalidatedMappingReturnsInternal(t *testing.T) {
	m := &Mapping[*fixturepb.Widget]{Table: "widgets", PK: "id"}
	_, err := Apply[*fixturepb.Widget](context.Background(), nil, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		PKValue: "x",
	})
	require.Equal(t, connect.CodeInternal, connect.CodeOf(err))
	require.ErrorContains(t, err, "InitPatches")
}

func TestApply_NilMessageReturnsInvalidArg(t *testing.T) {
	m := &Mapping[*fixturepb.Widget]{Table: "widgets", PK: "id"}
	require.NoError(t, m.Validate(nil))
	var typedNil *fixturepb.Widget
	_, err := Apply[*fixturepb.Widget](context.Background(), nil, m, Op[*fixturepb.Widget]{
		Message: typedNil,
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestApply_EmptyMaskReturnsInvalidArg(t *testing.T) {
	m := mustValidatedFixtureMapping(t)
	_, err := Apply(context.Background(), nil, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{},
		Mask:    &fieldmaskpb.FieldMask{},
	})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestApply_NilMaskReturnsInvalidArg(t *testing.T) {
	m := mustValidatedFixtureMapping(t)
	_, err := Apply(context.Background(), nil, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{},
		Mask:    nil,
	})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestApply_UpdateAllWritableReturnsUnimplemented(t *testing.T) {
	t.Parallel() // mapping is unshared (mustValidatedFixtureMapping returns a fresh value), so post-Validate mutation of EmptyMask is race-safe.
	m := mustValidatedFixtureMapping(t)
	m.EmptyMask = UpdateAllWritable
	// Apply reads m.EmptyMask directly; no re-Validate needed because
	// validate() does not gate on EmptyMask. If a future Validate() change
	// adds such a gate, re-validate here.
	_, err := Apply(context.Background(), nil, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{},
		Mask:    &fieldmaskpb.FieldMask{},
	})
	require.Equal(t, connect.CodeUnimplemented, connect.CodeOf(err))
}

func TestApply_NestedMaskPath(t *testing.T) {
	m := mustValidatedFixtureMapping(t)
	_, err := Apply(context.Background(), nil, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name.sub"}},
	})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestApply_UnknownMaskPath(t *testing.T) {
	m := mustValidatedFixtureMapping(t)
	_, err := Apply(context.Background(), nil, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"made_up"}},
	})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestApply_NonWritableMaskPath(t *testing.T) {
	m := mustValidatedFixtureMapping(t)
	_, err := Apply(context.Background(), nil, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{Id: "x"},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"id"}}, // id is non-writable in fixture mapping
	})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

// mustValidatedFixtureMapping returns a Mapping with a single writable "name" binding.
func mustValidatedFixtureMapping(t *testing.T) *Mapping[*fixturepb.Widget] {
	t.Helper()
	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
		},
	}
	require.NoError(t, m.Validate(nil))
	return m
}
