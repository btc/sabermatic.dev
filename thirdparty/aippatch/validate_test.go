package aippatch

import (
	"testing"

	"github.com/stretchr/testify/require"

	fixturepb "github.com/btc/drill/thirdparty/aippatch/internal/fixturepb"
)

func TestValidate_HappyPath(t *testing.T) {
	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets",
		PK:    "id",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
		},
	}
	require.NoError(t, m.Validate(nil))
	require.True(t, m.validated.Load())
	require.Len(t, m.bindingsByProto, 2)
	require.Len(t, m.bindingsByColumn, 2)
	require.Equal(t, "name", m.bindingsByProto["name"].Column)

	// Idempotent: a second call returns nil.
	require.NoError(t, m.Validate(nil))
}
