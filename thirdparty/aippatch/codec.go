package aippatch

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
		// enum: handled in next task.
		return nil, fmt.Errorf("aippatch: encode: codec %q not implemented", codec)
	}
}
