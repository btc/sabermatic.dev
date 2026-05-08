package aippatch

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

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

func TestEncode_Timestamp(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	w := &fixturepb.Widget{CreateTime: timestamppb.New(now)}
	fd := w.ProtoReflect().Descriptor().Fields().ByName("create_time")
	v, err := encode(w, fd, "timestamp", nil)
	require.NoError(t, err)
	require.WithinDuration(t, now, v.(time.Time), time.Microsecond)
}

func TestEncode_Enum(t *testing.T) {
	w := &fixturepb.Widget{Color: fixturepb.Color_COLOR_BLUE}
	fd := w.ProtoReflect().Descriptor().Fields().ByName("color")
	codecs := map[string]EnumCodec{
		"enum_color": {
			ToText:   map[int32]string{int32(fixturepb.Color_COLOR_RED): "red", int32(fixturepb.Color_COLOR_BLUE): "blue"},
			FromText: map[string]int32{"red": int32(fixturepb.Color_COLOR_RED), "blue": int32(fixturepb.Color_COLOR_BLUE)},
		},
	}
	v, err := encode(w, fd, "enum:enum_color", codecs)
	require.NoError(t, err)
	require.Equal(t, "blue", v)
}

func TestEncode_EnumOutOfMap(t *testing.T) {
	w := &fixturepb.Widget{Color: fixturepb.Color_COLOR_UNSPECIFIED} // unmapped value
	fd := w.ProtoReflect().Descriptor().Fields().ByName("color")
	codecs := map[string]EnumCodec{
		"enum_color": {
			ToText: map[int32]string{int32(fixturepb.Color_COLOR_RED): "red"},
		},
	}
	_, err := encode(w, fd, "enum:enum_color", codecs)
	require.Error(t, err, "unmapped enum value must error")
}

func TestDecode_Scalars(t *testing.T) {
	for _, tc := range []struct {
		field string
		raw   any
		want  protoreflect.Value
	}{
		{"name", "hi", protoreflect.ValueOfString("hi")},
		{"enabled", true, protoreflect.ValueOfBool(true)},
		{"count", int32(42), protoreflect.ValueOfInt32(42)},
		{"small_count", int16(3), protoreflect.ValueOfInt32(3)}, // widen int16 -> int32
		{"big_count", int64(1 << 40), protoreflect.ValueOfInt64(1 << 40)},
	} {
		t.Run(tc.field, func(t *testing.T) {
			w := &fixturepb.Widget{}
			msg := w.ProtoReflect()
			fd := msg.Descriptor().Fields().ByName(protoreflect.Name(tc.field))
			require.NoError(t, decode(msg, fd, tc.raw, "", nil))
			require.Equal(t, tc.want.Interface(), msg.Get(fd).Interface())
		})
	}
}

func TestDecode_UUIDFromBytes(t *testing.T) {
	w := &fixturepb.Widget{}
	msg := w.ProtoReflect()
	fd := msg.Descriptor().Fields().ByName("id")
	raw := [16]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0xfe, 0xdc, 0xba, 0x98, 0x76, 0x54, 0x32, 0x10}
	require.NoError(t, decode(msg, fd, raw, "", nil))
	require.Equal(t, "12345678-9abc-def0-fedc-ba9876543210", w.GetId())
}
