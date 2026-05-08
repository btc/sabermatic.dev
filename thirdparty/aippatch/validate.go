package aippatch

import (
	"fmt"

	"google.golang.org/protobuf/reflect/protoreflect"
)

// Validate is called once at startup, by the generated InitPatches().
//
// It indexes the binding maps; copies relevant codecs into m.codecs (subset
// reachable from m.Bindings); builds each EnumCodec's FromText from ToText;
// verifies every binding's proto path exists on T; verifies every binding's
// "enum:<name>" codec reference exists in the supplied codecs map; and sets
// the validated atomic flag.
//
// Returns error rather than panicking, per drill's no-panic-at-init rule.
// Idempotent up to the validated flag — repeated calls return nil after the
// first success. On error, validated is left false and the caller may retry.
//
// Concurrency: NOT safe for concurrent invocation on the same Mapping —
// callers must serialize calls per Mapping. The generated InitPatches()
// does this naturally.
func (m *Mapping[T]) Validate(codecs map[string]EnumCodec) error {
	if m.validated.Load() {
		return nil
	}
	if m.Table == "" {
		return fmt.Errorf("aippatch: Mapping.Table must be non-empty")
	}
	if m.PK == "" {
		return fmt.Errorf("aippatch: Mapping.PK must be non-empty for table %q", m.Table)
	}

	// Index bindings.
	m.bindingsByProto = make(map[string]*Binding, len(m.Bindings))
	m.bindingsByColumn = make(map[string]*Binding, len(m.Bindings))
	for i := range m.Bindings {
		b := &m.Bindings[i]
		if _, dup := m.bindingsByProto[b.Proto]; dup {
			return fmt.Errorf("aippatch: %s: duplicate binding for proto field %q", m.Table, b.Proto)
		}
		if _, dup := m.bindingsByColumn[b.Column]; dup {
			return fmt.Errorf("aippatch: %s: duplicate binding for column %q", m.Table, b.Column)
		}
		m.bindingsByProto[b.Proto] = b
		m.bindingsByColumn[b.Column] = b
	}

	// Verify every binding's proto path exists on T.
	desc := messageDescriptor[T]()
	for _, b := range m.Bindings {
		if desc.Fields().ByName(protoreflect.Name(b.Proto)) == nil {
			return fmt.Errorf("aippatch: %s: binding %q has no matching proto field on %s",
				m.Table, b.Proto, desc.FullName())
		}
	}

	// Codec selection deferred to Task 9 — for now leave m.codecs empty.
	m.codecs = make(map[string]EnumCodec)

	m.validated.Store(true)
	return nil
}

// messageDescriptor returns the descriptor for the proto type parameter T.
// `var zero T` for a pointer-typed T is a typed-nil pointer; for
// protoc-gen-go-generated types, ProtoReflect on a typed-nil receiver
// returns a non-nil Message whose Descriptor() is valid.
func messageDescriptor[T interface {
	ProtoReflect() protoreflect.Message
}]() protoreflect.MessageDescriptor {
	var zero T
	return zero.ProtoReflect().Descriptor()
}
