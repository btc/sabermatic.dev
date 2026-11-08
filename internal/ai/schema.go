package ai

import (
	"fmt"
	"slices"
	"sort"
)

// unsupportedSchemaKeywords are JSON Schema keywords that structured outputs
// (output_config.format) reject. Range and length constraints must be checked
// in Go after parsing instead.
var unsupportedSchemaKeywords = []string{
	"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
	"minLength", "maxLength", "pattern",
	"minItems", "maxItems", "uniqueItems", "contains",
	"minProperties", "maxProperties", "patternProperties",
	// Composition, conditional and tuple keywords. Branches under these are
	// not walked, so a nested object could skip the additionalProperties
	// check; none of the schemas here need them.
	"oneOf", "not", "if", "then", "else", "prefixItems",
	"dependentSchemas", "dependentRequired", "unevaluatedProperties", "unevaluatedItems",
	// References. Definitions under $defs/definitions are not walked, so
	// they are rejected until a schema needs them.
	"$ref", "$defs", "definitions",
}

// CheckOutputSchema reports whether schema stays inside the subset of JSON
// Schema that structured outputs accept. The first and last rules come from
// the API documentation; the middle two are stricter than the documentation
// requires, so output shape does not depend on behavior the docs leave
// unstated (optional properties, type arrays):
//
//   - every object sets additionalProperties:false;
//   - every object lists all of its properties in required (optional fields
//     are expressed as nullable with anyOf instead);
//   - "type" is a single string, never an array (use anyOf for unions);
//   - no numeric, string-length, or array-size constraints.
//
// It also rejects keywords it does not walk (oneOf, not, if/then/else,
// prefixItems, $ref, $defs and similar), so every object in an accepted
// schema has been checked.
func CheckOutputSchema(schema map[string]any) error {
	if schema == nil {
		return fmt.Errorf("ai: output schema is nil")
	}
	return checkSchemaNode(schema, "$")
}

func checkSchemaNode(node map[string]any, path string) error {
	for _, kw := range unsupportedSchemaKeywords {
		if _, ok := node[kw]; ok {
			return fmt.Errorf("ai: output schema %s: keyword %q is not supported by structured outputs", path, kw)
		}
	}
	if t, ok := node["type"]; ok {
		if _, isString := t.(string); !isString {
			return fmt.Errorf("ai: output schema %s: type must be a single string, got %T (use anyOf for unions)", path, t)
		}
	}

	if node["type"] == "object" {
		if ap, ok := node["additionalProperties"]; !ok || ap != false {
			return fmt.Errorf("ai: output schema %s: object must set additionalProperties to false", path)
		}
		props, _ := node["properties"].(map[string]any)
		required := stringList(node["required"])
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if !slices.Contains(required, name) {
				return fmt.Errorf("ai: output schema %s: property %q is not listed in required", path, name)
			}
			child, ok := props[name].(map[string]any)
			if !ok {
				return fmt.Errorf("ai: output schema %s.%s: property schema must be an object", path, name)
			}
			if err := checkSchemaNode(child, path+"."+name); err != nil {
				return err
			}
		}
		for _, name := range required {
			if _, ok := props[name]; !ok {
				return fmt.Errorf("ai: output schema %s: required property %q is not defined", path, name)
			}
		}
	}

	if items, ok := node["items"]; ok {
		child, ok := items.(map[string]any)
		if !ok {
			return fmt.Errorf("ai: output schema %s.items: must be a single schema object", path)
		}
		if err := checkSchemaNode(child, path+"[]"); err != nil {
			return err
		}
	}

	for _, kw := range []string{"anyOf", "allOf"} {
		raw, ok := node[kw]
		if !ok {
			continue
		}
		branches, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("ai: output schema %s.%s: must be a list of schema objects", path, kw)
		}
		for i, b := range branches {
			child, ok := b.(map[string]any)
			if !ok {
				return fmt.Errorf("ai: output schema %s.%s[%d]: must be a schema object", path, kw, i)
			}
			if err := checkSchemaNode(child, fmt.Sprintf("%s.%s[%d]", path, kw, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func stringList(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		out := make([]string, 0, len(l))
		for _, s := range l {
			if str, ok := s.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}
