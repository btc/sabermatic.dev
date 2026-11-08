package ai

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckOutputSchema(t *testing.T) {
	obj := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{
			"type":                 "object",
			"properties":           props,
			"required":             required,
			"additionalProperties": false,
		}
	}
	str := map[string]any{"type": "string"}

	tests := []struct {
		name    string
		schema  map[string]any
		wantErr string
	}{
		{name: "valid flat", schema: obj(map[string]any{"a": str}, "a")},
		{
			name: "valid nullable object via anyOf",
			schema: obj(map[string]any{
				"q": map[string]any{"anyOf": []any{obj(map[string]any{"t": str}, "t"), map[string]any{"type": "null"}}},
			}, "q"),
		},
		{
			name:   "valid array of objects",
			schema: obj(map[string]any{"xs": map[string]any{"type": "array", "items": obj(map[string]any{"t": str}, "t")}}, "xs"),
		},
		{name: "nil", schema: nil, wantErr: "nil"},
		{
			name:    "missing additionalProperties",
			schema:  map[string]any{"type": "object", "properties": map[string]any{"a": str}, "required": []string{"a"}},
			wantErr: "additionalProperties",
		},
		{name: "optional property", schema: obj(map[string]any{"a": str, "b": str}, "a"), wantErr: `"b" is not listed in required`},
		{name: "required undefined", schema: obj(map[string]any{"a": str}, "a", "z"), wantErr: `"z" is not defined`},
		{
			name:    "type array",
			schema:  obj(map[string]any{"a": map[string]any{"type": []any{"object", "null"}}}, "a"),
			wantErr: "single string",
		},
		{
			name:    "minimum",
			schema:  obj(map[string]any{"a": map[string]any{"type": "integer", "minimum": 1}}, "a"),
			wantErr: "minimum",
		},
		{
			name:    "nested object in items without additionalProperties",
			schema:  obj(map[string]any{"xs": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{}}}}, "xs"),
			wantErr: "$.xs[]",
		},
		{
			name:    "oneOf",
			schema:  obj(map[string]any{"a": map[string]any{"oneOf": []any{str, map[string]any{"type": "null"}}}}, "a"),
			wantErr: `"oneOf"`,
		},
		{
			name:    "not",
			schema:  obj(map[string]any{"a": map[string]any{"type": "string", "not": map[string]any{"const": "x"}}}, "a"),
			wantErr: `"not"`,
		},
		{
			name:    "if",
			schema:  obj(map[string]any{"a": map[string]any{"type": "string", "if": str, "then": str}}, "a"),
			wantErr: `"if"`,
		},
		{
			name:    "prefixItems",
			schema:  obj(map[string]any{"a": map[string]any{"type": "array", "prefixItems": []any{str}}}, "a"),
			wantErr: `"prefixItems"`,
		},
		{
			name: "$defs with unchecked object",
			schema: func() map[string]any {
				s := obj(map[string]any{"a": map[string]any{"$ref": "#/$defs/x"}}, "a")
				s["$defs"] = map[string]any{"x": map[string]any{"type": "object", "properties": map[string]any{}}}
				return s
			}(),
			wantErr: `"$defs"`,
		},
		{
			name:    "$ref",
			schema:  obj(map[string]any{"a": map[string]any{"$ref": "#/$defs/x"}}, "a"),
			wantErr: `"$ref"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckOutputSchema(tt.schema)
			if tt.wantErr == "" {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
