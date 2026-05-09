package aippatch

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/btc/drill/thirdparty/aippatch/internal/aippatchtest"
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

func TestApply_SoftDeletedRowReturnsNotFound(t *testing.T) {
	pool := aippatchtest.NewPool(t)
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		"INSERT INTO widgets (id, name, deleted_at) VALUES ($1, $2, NOW())", id, "old")
	require.NoError(t, err)

	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id", SoftDelete: "deleted_at",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
		},
	}
	require.NoError(t, m.Validate(nil))

	_, err = Apply(context.Background(), pool, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{Name: "new"},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		PKValue: id,
	})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestApply_PKMismatchReturnsNotFound(t *testing.T) {
	pool := aippatchtest.NewPool(t)
	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
		},
	}
	require.NoError(t, m.Validate(nil))

	_, err := Apply(context.Background(), pool, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{Name: "new"},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		PKValue: uuid.New(), // no row with this id
	})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestApply_HappyPath_NameOnly(t *testing.T) {
	pool := aippatchtest.NewPool(t)
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		"INSERT INTO widgets (id, name) VALUES ($1, $2)", id, "old")
	require.NoError(t, err)

	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
		},
	}
	require.NoError(t, m.Validate(nil))

	updated, err := Apply(context.Background(), pool, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{Name: "new"},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		PKValue: id,
	})
	require.NoError(t, err)
	require.Equal(t, "new", updated.GetName())
	require.Equal(t, id.String(), updated.GetId())
}

func TestApply_WhereUnknownColumnReturnsInvalidArg(t *testing.T) {
	pool := aippatchtest.NewPool(t)
	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
		},
	}
	require.NoError(t, m.Validate(nil))

	_, err := Apply(context.Background(), pool, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{Name: "x"},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		PKValue: uuid.New(),
		Where:   map[string]any{"deleted_at": nil}, // not bound
	})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestApply_WhereBoundColumnAccepted(t *testing.T) {
	pool := aippatchtest.NewPool(t)
	id := uuid.New()
	_, err := pool.Exec(context.Background(),
		"INSERT INTO widgets (id, name, enabled) VALUES ($1, $2, TRUE)", id, "old")
	require.NoError(t, err)

	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
			{Proto: "enabled", Column: "enabled", SQLType: "boolean", Writable: false},
		},
	}
	require.NoError(t, m.Validate(nil))

	// Where enabled=TRUE matches; PATCH succeeds.
	_, err = Apply(context.Background(), pool, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{Name: "new"},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		PKValue: id,
		Where:   map[string]any{"enabled": true},
	})
	require.NoError(t, err)

	// Where enabled=FALSE excludes; NotFound.
	_, err = Apply(context.Background(), pool, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{Name: "newer"},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		PKValue: id,
		Where:   map[string]any{"enabled": false},
	})
	require.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestApply_RoundTripAllTypes(t *testing.T) {
	pool := aippatchtest.NewPool(t)
	ctx := context.Background()
	id := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO widgets (id) VALUES ($1)", id)
	require.NoError(t, err)

	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "big_count", Column: "big_count", SQLType: "bigint", Writable: true},
			{Proto: "color", Column: "color", SQLType: "text", Writable: true, Codec: "enum:enum_color"},
			{Proto: "count", Column: "count", SQLType: "integer", Writable: true},
			{Proto: "create_time", Column: "created_at", SQLType: "timestamptz", Writable: false, Codec: "timestamp"},
			{Proto: "enabled", Column: "enabled", SQLType: "boolean", Writable: true},
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
			{Proto: "small_count", Column: "small_count", SQLType: "smallint", Writable: true},
		},
	}
	codecs := map[string]EnumCodec{
		"enum_color": {ToText: map[int32]string{int32(fixturepb.Color_COLOR_RED): "red", int32(fixturepb.Color_COLOR_BLUE): "blue"}},
	}
	require.NoError(t, m.Validate(codecs))

	updated, err := Apply(ctx, pool, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{
			Name:       "abc",
			Enabled:    true,
			Count:      42,
			SmallCount: 7,
			BigCount:   1 << 35,
			Color:      fixturepb.Color_COLOR_BLUE,
		},
		Mask: &fieldmaskpb.FieldMask{Paths: []string{
			"name", "enabled", "count", "small_count", "big_count", "color",
		}},
		PKValue: id,
	})
	require.NoError(t, err)
	require.Equal(t, "abc", updated.GetName())
	require.True(t, updated.GetEnabled())
	require.Equal(t, int32(42), updated.GetCount())
	require.Equal(t, int32(7), updated.GetSmallCount())
	require.Equal(t, int64(1<<35), updated.GetBigCount())
	require.Equal(t, fixturepb.Color_COLOR_BLUE, updated.GetColor())
	require.NotZero(t, updated.GetCreateTime().AsTime())
	require.Equal(t, id.String(), updated.GetId())
}

func TestApply_CloneOfPreservesInputFields(t *testing.T) {
	// A Mapping with only "name" bound should preserve unmapped fields (e.g.
	// "enabled") from the input message via proto.CloneOf, and overwrite only
	// the bound columns returned by the DB.
	pool := aippatchtest.NewPool(t)
	id := uuid.New()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		"INSERT INTO widgets (id, name) VALUES ($1, $2)", id, "original")
	require.NoError(t, err)

	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
		},
	}
	require.NoError(t, m.Validate(nil))

	// Input carries name="x" and enabled=true; enabled is not a binding on m.
	input := &fixturepb.Widget{Name: "x", Enabled: true}
	result, err := Apply(ctx, pool, m, Op[*fixturepb.Widget]{
		Message: input,
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		PKValue: id,
	})
	require.NoError(t, err)
	// Bound field updated from DB.
	require.Equal(t, "x", result.GetName())
	// Unmapped field preserved from CloneOf(input).
	require.True(t, result.GetEnabled(), "enabled should be preserved from input via CloneOf")
}

func TestApply_EnumOutOfMapReturnsInvalidArg(t *testing.T) {
	pool := aippatchtest.NewPool(t)
	ctx := context.Background()
	id := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO widgets (id) VALUES ($1)", id)
	require.NoError(t, err)

	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "color", Column: "color", SQLType: "text", Writable: true, Codec: "enum:enum_color"},
		},
	}
	// ToText only maps COLOR_RED; COLOR_BLUE is intentionally absent.
	codecs := map[string]EnumCodec{
		"enum_color": {ToText: map[int32]string{int32(fixturepb.Color_COLOR_RED): "red"}},
	}
	require.NoError(t, m.Validate(codecs))

	// Sending COLOR_BLUE (not in ToText) should produce InvalidArgument, not Internal.
	_, err = Apply(ctx, pool, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{Color: fixturepb.Color_COLOR_BLUE},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"color"}},
		PKValue: id,
	})
	require.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
}

func TestApply_AutoSetBumpsUpdatedAt(t *testing.T) {
	pool := aippatchtest.NewPool(t)
	id := uuid.New()
	ctx := context.Background()
	_, err := pool.Exec(ctx,
		"INSERT INTO widgets (id, name) VALUES ($1, $2)", id, "old")
	require.NoError(t, err)

	var before time.Time
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT updated_at FROM widgets WHERE id=$1", id).Scan(&before))

	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
		},
		// clock_timestamp advances within a transaction; NOW() does not.
		AutoSet: []AutoSetClause{{Column: "updated_at", SQLLiteral: "clock_timestamp()"}},
	}
	require.NoError(t, m.Validate(nil))

	_, err = Apply(ctx, pool, m, Op[*fixturepb.Widget]{
		Message: &fixturepb.Widget{Name: "new"},
		Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		PKValue: id,
	})
	require.NoError(t, err)

	var after time.Time
	require.NoError(t, pool.QueryRow(ctx,
		"SELECT updated_at FROM widgets WHERE id=$1", id).Scan(&after))
	require.True(t, after.After(before),
		"updated_at must advance: before=%v after=%v", before, after)
}

func TestApply_ParticipatesInCallerTx(t *testing.T) {
	pool := aippatchtest.NewPool(t)
	ctx := context.Background()
	id := uuid.New()
	_, err := pool.Exec(ctx, "INSERT INTO widgets (id, name) VALUES ($1, $2)", id, "before")
	require.NoError(t, err)

	m := &Mapping[*fixturepb.Widget]{
		Table: "widgets", PK: "id",
		Bindings: []Binding{
			{Proto: "id", Column: "id", SQLType: "uuid", Writable: false},
			{Proto: "name", Column: "name", SQLType: "text", Writable: true},
		},
	}
	require.NoError(t, m.Validate(nil))

	// Rollback case: Apply inside a tx, then return error → no row change.
	err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := Apply(ctx, tx, m, Op[*fixturepb.Widget]{
			Message: &fixturepb.Widget{Name: "rolled-back"},
			Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
			PKValue: id,
		})
		if err != nil {
			return err
		}
		return errors.New("force rollback")
	})
	require.ErrorContains(t, err, "force rollback")

	var name string
	require.NoError(t, pool.QueryRow(ctx, "SELECT name FROM widgets WHERE id=$1", id).Scan(&name))
	require.Equal(t, "before", name, "rollback should preserve original value")

	// Commit case: Apply inside a tx that commits → row updated.
	require.NoError(t, pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		_, err := Apply(ctx, tx, m, Op[*fixturepb.Widget]{
			Message: &fixturepb.Widget{Name: "committed"},
			Mask:    &fieldmaskpb.FieldMask{Paths: []string{"name"}},
			PKValue: id,
		})
		return err
	}))
	require.NoError(t, pool.QueryRow(ctx, "SELECT name FROM widgets WHERE id=$1", id).Scan(&name))
	require.Equal(t, "committed", name)
}
