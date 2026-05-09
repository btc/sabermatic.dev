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

func TestValidate_CodecSubsetAndFromText(t *testing.T) {
	codecs := map[string]EnumCodec{
		"enum_color": {
			ProtoEnum: "aippatch.fixture.v1.Color",
			ToText: map[int32]string{
				int32(fixturepb.Color_COLOR_RED):  "red",
				int32(fixturepb.Color_COLOR_BLUE): "blue",
			},
		},
		"unused_codec": { // should NOT end up in m.codecs
			ProtoEnum: "x.y.Z",
			ToText:    map[int32]string{0: "x"},
		},
	}
	m := &Mapping[*fixturepb.Widget]{
		Table:    "widgets",
		PK:       "id",
		Bindings: []Binding{{Proto: "color", Column: "color", SQLType: "text", Codec: "enum:enum_color"}},
	}
	require.NoError(t, m.Validate(codecs))
	require.Contains(t, m.codecs, "enum_color")
	require.NotContains(t, m.codecs, "unused_codec")
	c := m.codecs["enum_color"]
	require.Equal(t, int32(fixturepb.Color_COLOR_RED), c.FromText["red"])
	require.Equal(t, int32(fixturepb.Color_COLOR_BLUE), c.FromText["blue"])
}

func TestValidate_DanglingCodecRefRejected(t *testing.T) {
	m := &Mapping[*fixturepb.Widget]{
		Table:    "widgets",
		PK:       "id",
		Bindings: []Binding{{Proto: "color", Column: "color", SQLType: "text", Codec: "enum:nope"}},
	}
	err := m.Validate(map[string]EnumCodec{})
	require.ErrorContains(t, err, "nope")
}

func TestValidate_BindingProtoFieldMissing(t *testing.T) {
	m := &Mapping[*fixturepb.Widget]{
		Table:    "widgets",
		PK:       "id",
		Bindings: []Binding{{Proto: "no_such_field", Column: "x", SQLType: "text"}},
	}
	err := m.Validate(nil)
	require.ErrorContains(t, err, "no_such_field")
	require.False(t, m.validated.Load(), "validated should remain false on error")
}

func TestValidate_DuplicateBindingProtoField(t *testing.T) {
	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "name", Column: "name", SQLType: "text"},
			{Proto: "name", Column: "alias", SQLType: "text"},
		},
	}
	require.ErrorContains(t, m.Validate(nil), "duplicate")
}

func TestValidate_DuplicateBindingColumn(t *testing.T) {
	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "name", Column: "name", SQLType: "text"},
			{Proto: "enabled", Column: "name", SQLType: "boolean"},
		},
	}
	require.ErrorContains(t, m.Validate(nil), "duplicate")
}

func TestValidate_MissingTableOrPK(t *testing.T) {
	require.ErrorContains(t, (&Mapping[*fixturepb.Widget]{}).Validate(nil), "Table")
	require.ErrorContains(t, (&Mapping[*fixturepb.Widget]{Table: "x"}).Validate(nil), "PK")
}
