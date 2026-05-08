package aippatch

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// decode sets a single proto field on m from a raw SQL value returned by pgx.
// codec and codecs mirror the encode signature; "" means scalar, "timestamp" and
// enum names are handled in Tasks 16 and 17 respectively.
func decode(m protoreflect.Message, fd protoreflect.FieldDescriptor, raw any, codec string, codecs map[string]EnumCodec) error {
	switch codec {
	case "":
		switch fd.Kind() {
		case protoreflect.StringKind:
			s, ok := raw.(string)
			if !ok {
				if u, isUUID := raw.([16]byte); isUUID {
					m.Set(fd, protoreflect.ValueOfString(formatUUID(u)))
					return nil
				}
				return fmt.Errorf("aippatch: decode %q: expected string, got %T", fd.Name(), raw)
			}
			m.Set(fd, protoreflect.ValueOfString(s))
		case protoreflect.BoolKind:
			b, ok := raw.(bool)
			if !ok {
				return fmt.Errorf("aippatch: decode %q: expected bool, got %T", fd.Name(), raw)
			}
			m.Set(fd, protoreflect.ValueOfBool(b))
		case protoreflect.Int32Kind, protoreflect.Sint32Kind:
			switch v := raw.(type) {
			case int32:
				m.Set(fd, protoreflect.ValueOfInt32(v))
			case int16:
				m.Set(fd, protoreflect.ValueOfInt32(int32(v)))
			default:
				return fmt.Errorf("aippatch: decode %q: expected int32/int16, got %T", fd.Name(), raw)
			}
		case protoreflect.Int64Kind, protoreflect.Sint64Kind:
			i, ok := raw.(int64)
			if !ok {
				return fmt.Errorf("aippatch: decode %q: expected int64, got %T", fd.Name(), raw)
			}
			m.Set(fd, protoreflect.ValueOfInt64(i))
		default:
			return fmt.Errorf("aippatch: decode %q: unsupported scalar kind %s", fd.Name(), fd.Kind())
		}
		return nil
	case "timestamp":
		t, ok := raw.(time.Time)
		if !ok {
			return fmt.Errorf("aippatch: decode %q: expected time.Time, got %T", fd.Name(), raw)
		}
		m.Set(fd, protoreflect.ValueOfMessage(timestamppb.New(t).ProtoReflect()))
		return nil
	default:
		// enum: implemented in Task 17.
		return fmt.Errorf("aippatch: decode: codec %q not implemented", codec)
	}
}

// formatUUID converts pgx's [16]byte uuid to canonical 8-4-4-4-12 string.
func formatUUID(b [16]byte) string { return uuid.UUID(b).String() }

// encode converts a proto field value to a SQL parameter for pgx.
// Codec values: "" (scalar), "timestamp" (proto.Timestamp -> time.Time),
// "enum:<name>" (proto enum -> SQL text via codecs[name]).
func encode(m proto.Message, fd protoreflect.FieldDescriptor, codec string, codecs map[string]EnumCodec) (any, error) {
	v := m.ProtoReflect().Get(fd)

	switch codec {
	case "":
		switch fd.Kind() {
		case protoreflect.StringKind:
			return v.String(), nil
		case protoreflect.BoolKind:
			return v.Bool(), nil
		case protoreflect.Int32Kind, protoreflect.Sint32Kind:
			return int32(v.Int()), nil
		case protoreflect.Int64Kind, protoreflect.Sint64Kind:
			return v.Int(), nil
		default:
			return nil, fmt.Errorf("aippatch: encode: unsupported scalar kind %s for field %q",
				fd.Kind(), fd.Name())
		}
	case "timestamp":
		return v.Message().Interface().(*timestamppb.Timestamp).AsTime(), nil
	default:
		if !strings.HasPrefix(codec, "enum:") {
			return nil, fmt.Errorf("aippatch: encode: unknown codec %q", codec)
		}
		name := strings.TrimPrefix(codec, "enum:")
		c, ok := codecs[name]
		if !ok {
			return nil, fmt.Errorf("aippatch: encode: codec %q not in registry", name)
		}
		num := int32(v.Enum())
		text, ok := c.ToText[num]
		if !ok {
			return nil, fmt.Errorf("aippatch: encode: enum value %d not in codec %q ToText", num, name)
		}
		return text, nil
	}
}
