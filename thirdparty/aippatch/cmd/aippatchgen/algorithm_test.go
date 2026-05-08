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

	models, codecs, diags := processAll(yaml, files, schema)
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
