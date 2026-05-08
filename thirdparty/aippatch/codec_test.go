package aippatch

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"

	fixturepb "github.com/btc/drill/thirdparty/aippatch/internal/fixturepb"
)

func TestEncode_Scalars(t *testing.T) {
	w := &fixturepb.Widget{
		Name:       "hi",
		Enabled:    true,
		Count:      7,
		SmallCount: 3,
		BigCount:   1 << 40,
	}
	desc := w.ProtoReflect().Descriptor()

	for _, tc := range []struct {
		field    string
		expected any
	}{
		{"name", "hi"},
		{"enabled", true},
		{"count", int32(7)},
		{"small_count", int32(3)},
		{"big_count", int64(1 << 40)},
	} {
		t.Run(tc.field, func(t *testing.T) {
			fd := desc.Fields().ByName(protoreflect.Name(tc.field))
			require.NotNil(t, fd, "fixture must have field %q", tc.field)
			v, err := encode(w, fd, "", nil)
			require.NoError(t, err)
			require.Equal(t, tc.expected, v)
		})
	}
}
