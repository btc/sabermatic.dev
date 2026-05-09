package aippatch

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/huandu/go-sqlbuilder"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

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

	// 1. Mask.
	paths := op.Mask.GetPaths()
	if len(paths) == 0 {
		if m.EmptyMask == ErrorOnEmpty {
			return zero, connectInvalidArg("update_mask must not be empty")
		}
		return zero, connectUnimplemented("UpdateAllWritable is unimplemented in v0")
	}

	// 2. Validate paths.
	maskSet := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		if strings.Contains(p, ".") {
			return zero, connectInvalidArg("nested mask path not supported in v0: %q", p)
		}
		b, ok := m.bindingsByProto[p]
		if !ok {
			return zero, connectInvalidArg("unknown field in update_mask: %q", p)
		}
		if !b.Writable {
			return zero, connectInvalidArg("field not writable: %q", p)
		}
		maskSet[p] = struct{}{}
	}

	// 3. Build SET clause: iterate Bindings in declaration order (codegen
	// emits alphabetical for stable diff) filtered by maskSet, so SQL is
	// deterministic regardless of client-supplied path order. AutoSet
	// appended at the end.
	desc := src.Descriptor()
	ub := sqlbuilder.PostgreSQL.NewUpdateBuilder()
	ub.Update(m.Table)
	sets := make([]string, 0, len(maskSet)+len(m.AutoSet))
	for i := range m.Bindings {
		b := &m.Bindings[i]
		if _, ok := maskSet[b.Proto]; !ok {
			continue
		}
		fd := desc.Fields().ByName(protoreflect.Name(b.Proto))
		if fd == nil {
			return zero, connectInternal("binding/proto desync: %q", b.Proto)
		}
		v, err := encode(op.Message, fd, b.Codec, m.codecs)
		if err != nil {
			// encode errors are predominantly server-side configuration
			// issues that escaped Validate (unsupported kind, codec not in
			// registry, missing ToText entry). Surface as Internal so the
			// caller doesn't mistake them for client mistakes.
			return zero, connectInternal("encode %s: %s", b.Proto, err.Error())
		}
		sets = append(sets, ub.Assign(b.Column, v))
	}
	for _, a := range m.AutoSet {
		sets = append(sets, fmt.Sprintf("%s = %s", a.Column, a.SQLLiteral))
	}
	ub.Set(sets...)

	// 4. WHERE.
	ub.Where(ub.Equal(m.PK, op.PKValue))
	if m.SoftDelete != "" {
		ub.Where(ub.IsNull(m.SoftDelete))
	}
	if len(op.Where) > 0 {
		keys := make([]string, 0, len(op.Where))
		for k := range op.Where {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, col := range keys {
			if _, ok := m.bindingsByColumn[col]; !ok {
				return zero, connectInvalidArg("unknown column in op.Where: %q", col)
			}
			ub.Where(ub.Equal(col, op.Where[col]))
		}
	}

	// 5. Returning bound columns.
	boundCols := make([]string, len(m.Bindings))
	for i, b := range m.Bindings {
		boundCols[i] = b.Column
	}
	ub.Returning(boundCols...)

	sqlStr, args := ub.Build()

	// 6. Execute.
	rows, err := db.Query(ctx, sqlStr, args...)
	if err != nil {
		return zero, connectInternal("query: %s", err.Error())
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return zero, connectInternal("query: %s", err.Error())
		}
		return zero, connectNotFound("resource not found, soft-deleted, or filtered out")
	}
	cols := rows.FieldDescriptions()
	vals, err := rows.Values()
	if err != nil {
		return zero, connectInternal("scan: %s", err.Error())
	}

	// 7. Build result via CloneOf.
	result := proto.CloneOf(op.Message)
	msg := result.ProtoReflect()
	for i, c := range cols {
		b, ok := m.bindingsByColumn[string(c.Name)]
		if !ok {
			continue
		}
		fd := msg.Descriptor().Fields().ByName(protoreflect.Name(b.Proto))
		if fd == nil {
			return zero, connectInternal("binding/proto desync on read: %q", b.Proto)
		}
		if err := decode(msg, fd, vals[i], b.Codec, m.codecs); err != nil {
			return zero, connectInternal("decode %s: %s", b.Proto, err.Error())
		}
	}
	return result, nil
}
