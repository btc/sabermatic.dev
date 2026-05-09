package main

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessResource_Happy(t *testing.T) {
	files, err := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	require.NoError(t, err)
	schema, err := loadSchema(filepath.Join("..", "..", "internal", "fixturepb", "migrations"))
	require.NoError(t, err)

	yaml := yamlConfig{
		Codecs: map[string]yamlCodec{
			"enum_color": {
				ProtoEnum: "aippatch.fixture.v1.Color",
				Map:       map[string]string{"COLOR_RED": "red", "COLOR_BLUE": "blue"},
			},
		},
		Resources: []yamlResource{{
			Message:    "aippatch.fixture.v1.Widget",
			Table:      "widgets",
			PK:         "id",
			SoftDelete: "deleted_at",
			EmptyMask:  "error",
			Writable:   []string{"name"},
			AutoSet:    map[string]string{"updated_at": "NOW()"},
			Overrides: map[string]yamlOverride{
				"color":       {Codec: "enum_color"},
				"create_time": {Column: "created_at"},
			},
		}},
	}

	models, codecs, diags := processAll(&yaml, files, schema)
	require.Empty(t, diags, "no diagnostics for happy path: %v", diags)
	require.Len(t, models, 1)
	r := models[0]

	// Check binding count and a couple of names.
	bindingProtos := make([]string, len(r.Bindings))
	for i, b := range r.Bindings {
		bindingProtos[i] = b.Proto
	}
	sort.Strings(bindingProtos)
	require.Equal(t, []string{
		"big_count", "color", "count", "create_time",
		"enabled", "id", "name", "small_count",
	}, bindingProtos)

	// Spot-check writable + codec assignment.
	for _, b := range r.Bindings {
		switch b.Proto {
		case "name":
			require.True(t, b.Writable)
		case "color":
			require.Equal(t, "enum:enum_color", b.Codec)
		case "create_time":
			require.Equal(t, "created_at", b.Column)
			require.Equal(t, "timestamp", b.Codec)
		}
	}

	// AutoSet present.
	require.Len(t, r.AutoSet, 1)
	require.Equal(t, "updated_at", r.AutoSet[0].Column)
	require.Equal(t, "NOW()", r.AutoSet[0].SQLLiteral)

	// Codec emitted into the registry.
	require.Contains(t, codecs, "enum_color")
}

func TestProcessResource_NullableBoundField_Diagnostic(t *testing.T) {
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(`CREATE TABLE widgets (id UUID PRIMARY KEY, name TEXT)`))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
		Writable: []string{"name"},
		Overrides: map[string]yamlOverride{
			"enabled": {Skip: true}, "count": {Skip: true},
			"small_count": {Skip: true}, "big_count": {Skip: true},
			"color": {Skip: true}, "create_time": {Skip: true},
		},
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	require.Contains(t, diags.Err().Error(), "nullable column not supported in v0")
}

func TestProcessResource_AutoSetBadIdentifier_Diagnostic(t *testing.T) {
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(`CREATE TABLE widgets (id UUID PRIMARY KEY, "user-id" TEXT NOT NULL)`))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
		AutoSet: map[string]string{"user-id": "NOW()"},
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.Contains(t, diags.Err().Error(), `auto_set column "user-id" is not a plain SQL identifier`)
}

func TestProcessResource_CodecDuplicateValue_Diagnostic(t *testing.T) {
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(`CREATE TABLE widgets (id UUID PRIMARY KEY)`))
	yaml := yamlConfig{
		Codecs: map[string]yamlCodec{
			"enum_color": {ProtoEnum: "x", Map: map[string]string{"A": "x", "B": "x"}},
		},
	}
	_, _, diags := processAll(&yaml, files, schema)
	require.Contains(t, diags.Err().Error(), "duplicate map value")
}

func TestProcessResource_UpdateWritableRejected_Diagnostic(t *testing.T) {
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(`CREATE TABLE widgets (id UUID PRIMARY KEY)`))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
		EmptyMask: "update_writable",
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.Contains(t, diags.Err().Error(), `update_writable" is not supported in v0`)
}

func TestProcessResource_UnmatchedProtoField_Diagnostic(t *testing.T) {
	// Widget has 8 fields but the table only has id; the seven other proto
	// fields have neither a column match nor an explicit skip:true. Every
	// field must be diagnosed (not silently dropped).
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(`CREATE TABLE widgets (id UUID PRIMARY KEY)`))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	msg := diags.Err().Error()
	for _, fname := range []string{"name", "enabled", "count", "small_count", "big_count", "color", "create_time"} {
		require.Contains(t, msg, fname, "expected diagnostic mentioning %q", fname)
	}
}

func TestProcessResource_AutoSetConflict_Diagnostic(t *testing.T) {
	// auto_set names a column that is also bound to a proto field.
	// The Widget.name field maps to the "name" column by default.
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(
		`CREATE TABLE widgets (id UUID PRIMARY KEY, name TEXT NOT NULL DEFAULT '')`,
	))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
		AutoSet: map[string]string{"name": "NOW()"},
		Overrides: map[string]yamlOverride{
			"enabled": {Skip: true}, "count": {Skip: true},
			"small_count": {Skip: true}, "big_count": {Skip: true},
			"color": {Skip: true}, "create_time": {Skip: true},
		},
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	require.Contains(t, diags.Err().Error(), "conflicts with binding")
}

func TestProcessResource_AutoSetBadLiteral_Diagnostic(t *testing.T) {
	// auto_set literal contains multiple statements — not a valid Postgres expression.
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(
		`CREATE TABLE widgets (id UUID PRIMARY KEY, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
		AutoSet: map[string]string{"updated_at": "NOW(); DROP TABLE x"},
		Overrides: map[string]yamlOverride{
			"name": {Skip: true}, "enabled": {Skip: true}, "count": {Skip: true},
			"small_count": {Skip: true}, "big_count": {Skip: true},
			"color": {Skip: true}, "create_time": {Skip: true},
		},
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	require.Contains(t, diags.Err().Error(), "not a valid Postgres expression")
}

func TestProcessResource_AutoSetColumnNotFound_Diagnostic(t *testing.T) {
	// auto_set names a column that doesn't exist in the table.
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(`CREATE TABLE widgets (id UUID PRIMARY KEY)`))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
		AutoSet: map[string]string{"nonexistent_col": "NOW()"},
		Overrides: map[string]yamlOverride{
			"name": {Skip: true}, "enabled": {Skip: true}, "count": {Skip: true},
			"small_count": {Skip: true}, "big_count": {Skip: true},
			"color": {Skip: true}, "create_time": {Skip: true},
		},
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	require.Contains(t, diags.Err().Error(), "nonexistent_col")
}

func TestProcessResource_AutoSetColumnNullable_Diagnostic(t *testing.T) {
	// auto_set names a nullable column — must be rejected.
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(
		`CREATE TABLE widgets (id UUID PRIMARY KEY, updated_at TIMESTAMPTZ)`,
	))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
		AutoSet: map[string]string{"updated_at": "NOW()"},
		Overrides: map[string]yamlOverride{
			"name": {Skip: true}, "enabled": {Skip: true}, "count": {Skip: true},
			"small_count": {Skip: true}, "big_count": {Skip: true},
			"color": {Skip: true}, "create_time": {Skip: true},
		},
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	require.Contains(t, diags.Err().Error(), "must be NOT NULL")
}

func TestProcessResource_AutoSetMultiStatementLiteral_Diagnostic(t *testing.T) {
	// Injection payload that parses as 3 statements after the wrapping.
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(
		`CREATE TABLE widgets (id UUID PRIMARY KEY, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
		AutoSet: map[string]string{"updated_at": "1); DROP TABLE x; SELECT (1"},
		Overrides: map[string]yamlOverride{
			"name": {Skip: true}, "enabled": {Skip: true}, "count": {Skip: true},
			"small_count": {Skip: true}, "big_count": {Skip: true},
			"color": {Skip: true}, "create_time": {Skip: true},
		},
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	require.Contains(t, diags.Err().Error(), "single expression")
}

func TestProcessResource_AutoSetMultiTargetLiteral_Diagnostic(t *testing.T) {
	// Payload that expands SET clause: "NOW(), other_col = 'x'" parses as
	// one statement but has two targets in the SELECT's target list.
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(
		`CREATE TABLE widgets (id UUID PRIMARY KEY, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
		AutoSet: map[string]string{"updated_at": "NOW(), other_col = 'x'"},
		Overrides: map[string]yamlOverride{
			"name": {Skip: true}, "enabled": {Skip: true}, "count": {Skip: true},
			"small_count": {Skip: true}, "big_count": {Skip: true},
			"color": {Skip: true}, "create_time": {Skip: true},
		},
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	require.Contains(t, diags.Err().Error(), "single expression")
}

func TestProcessResource_PKColumnMissing_Diagnostic(t *testing.T) {
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(`CREATE TABLE widgets (row_id UUID PRIMARY KEY)`))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	err := diags.Err().Error()
	require.Contains(t, err, `pk column "id" not found`)
	require.Contains(t, err, "row_id", "hint should list candidate columns")
}

func TestProcessResource_SoftDeleteColumnMissing_Diagnostic(t *testing.T) {
	files, _ := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	schema := newSchema()
	require.NoError(t, schema.applySQL(`CREATE TABLE widgets (id UUID PRIMARY KEY)`))
	yaml := yamlConfig{Resources: []yamlResource{{
		Message: "aippatch.fixture.v1.Widget", Table: "widgets", PK: "id",
		SoftDelete: "removed_at",
	}}}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	err := diags.Err().Error()
	require.Contains(t, err, `soft_delete column "removed_at" not found`)
	require.Contains(t, err, "id", "hint should list candidate columns")
}

func TestProcessResource_ProtoEnumNotFound_Diagnostic(t *testing.T) {
	// proto_enum names a non-existent descriptor — should emit a diagnostic.
	files, err := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	require.NoError(t, err)
	schema, err := loadSchema(filepath.Join("..", "..", "internal", "fixturepb", "migrations"))
	require.NoError(t, err)
	yaml := yamlConfig{
		Codecs: map[string]yamlCodec{
			"bad_codec": {
				ProtoEnum: "aippatch.fixture.v1.NoSuchEnum",
				Map:       map[string]string{"VALUE": "val"},
			},
		},
		Resources: []yamlResource{{
			Message:  "aippatch.fixture.v1.Widget",
			Table:    "widgets",
			PK:       "id",
			Writable: []string{"name"},
			Overrides: map[string]yamlOverride{
				"color":       {Codec: "bad_codec"},
				"create_time": {Column: "created_at"},
			},
		}},
	}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	require.Contains(t, diags.Err().Error(), "not found in descriptor set")
}

func TestProcessResource_StaleYamlMapKeys_Diagnostic(t *testing.T) {
	// yaml Map key "COLOR_PURPLE" doesn't exist in the proto enum.
	files, err := loadProto(filepath.Join("testdata", "proto_basic", "buf.binpb"))
	require.NoError(t, err)
	schema, err := loadSchema(filepath.Join("..", "..", "internal", "fixturepb", "migrations"))
	require.NoError(t, err)
	yaml := yamlConfig{
		Codecs: map[string]yamlCodec{
			"enum_color": {
				ProtoEnum: "aippatch.fixture.v1.Color",
				Map: map[string]string{
					"COLOR_RED":    "red",
					"COLOR_PURPLE": "purple", // stale — not in proto
				},
			},
		},
		Resources: []yamlResource{{
			Message:  "aippatch.fixture.v1.Widget",
			Table:    "widgets",
			PK:       "id",
			Writable: []string{"name"},
			Overrides: map[string]yamlOverride{
				"color":       {Codec: "enum_color"},
				"create_time": {Column: "created_at"},
			},
		}},
	}
	_, _, diags := processAll(&yaml, files, schema)
	require.NotEmpty(t, diags)
	err2 := diags.Err().Error()
	require.Contains(t, err2, "COLOR_PURPLE")
	require.Contains(t, err2, "do not match any proto enum value name")
}
