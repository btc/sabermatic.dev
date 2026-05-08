package aippatch

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

// We use *emptypb.Empty as a real proto.Message until fixturepb lands in
// Task 6 — it satisfies the generic constraint without a hand-rolled stub.

func TestMapping_ZeroValueValidatedFalse(t *testing.T) {
	m := Mapping[*emptypb.Empty]{}
	require.False(t, m.validated.Load(),
		"new Mapping should start with validated=false")
}

func TestEnumCodec_FromTextNotEmittedByDefault(t *testing.T) {
	// Generated code emits only ToText. FromText is built by Validate.
	c := EnumCodec{
		ProtoEnum: "test.v1.Color",
		ToText: map[int32]string{
			0: "red",
			1: "green",
		},
	}
	require.Nil(t, c.FromText, "FromText is constructed by Validate, not declared")
	require.Len(t, c.ToText, 2)
}
