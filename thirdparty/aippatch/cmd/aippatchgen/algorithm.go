package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	pg_query "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

var identifierRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func processAll(yaml *yamlConfig, files *protoregistry.Files, schema *Schema) ([]ResourceModel, map[string]CodecModel, Diagnostics) {
	var diags Diagnostics
	models := make([]ResourceModel, 0, len(yaml.Resources))
	codecsUsed := map[string]CodecModel{}

	// Pre-validate codec maps (step 6: ToText uniqueness).
	for name, c := range yaml.Codecs {
		seen := map[string]string{}
		for k, v := range c.Map {
			if prev, ok := seen[v]; ok {
				diags = append(diags, Diagnostic{
					Message: fmt.Sprintf("codec %q: duplicate map value %q (used by %q and %q)", name, v, prev, k),
				})
			}
			seen[v] = k
		}
	}

	for i := range yaml.Resources {
		m, used, ds := processResource(&yaml.Resources[i], yaml.Codecs, files, schema)
		diags = append(diags, ds...)
		if m != nil {
			models = append(models, *m)
		}
		for n, cm := range used {
			codecsUsed[n] = cm
		}
	}

	return models, codecsUsed, diags
}

func processResource(r *yamlResource, codecsYaml map[string]yamlCodec, files *protoregistry.Files, schema *Schema) (*ResourceModel, map[string]CodecModel, Diagnostics) {
	var diags Diagnostics

	// Step 9: reject update_writable in v0.
	switch r.EmptyMask {
	case "", "error":
		// OK
	case "update_writable":
		diags = append(diags, Diagnostic{
			Resource: r.Message,
			Message:  `empty_mask: "update_writable" is not supported in v0`,
			Hint:     `omit empty_mask or set it to "error" until v1 implements UpdateAllWritable`,
		})
	default:
		diags = append(diags, Diagnostic{
			Resource: r.Message,
			Message:  fmt.Sprintf("unknown empty_mask value %q", r.EmptyMask),
		})
	}

	// Step 7: reject yaml-side dotted names in writable / overrides.
	for _, w := range r.Writable {
		if strings.Contains(w, ".") {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("writable %q contains a dot — nested mask paths are not supported in v0", w),
			})
		}
	}
	for k := range r.Overrides {
		if strings.Contains(k, ".") {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("override key %q contains a dot — nested mask paths are not supported in v0", k),
			})
		}
	}

	// Look up proto + table.
	desc, err := lookupMessage(files, r.Message)
	if err != nil {
		diags = append(diags, Diagnostic{Resource: r.Message, Message: err.Error()})
		return nil, nil, diags
	}
	tbl, ok := schema.Table(r.Table)
	if !ok {
		diags = append(diags, Diagnostic{
			Resource: r.Message,
			Message:  fmt.Sprintf("table %q not found in migrations", r.Table),
		})
		return nil, nil, diags
	}

	// Validate PK column exists in table.
	if _, ok := tbl.Column(r.PK); !ok {
		candidates := columnNames(tbl)
		diags = append(diags, Diagnostic{
			Resource: r.Message,
			Message:  fmt.Sprintf("pk column %q not found in table %q", r.PK, r.Table),
			Hint:     fmt.Sprintf("candidate columns: %s", strings.Join(candidates, ", ")),
		})
		return nil, nil, diags
	}
	// Validate SoftDelete column exists in table (if set).
	if r.SoftDelete != "" {
		if _, ok := tbl.Column(r.SoftDelete); !ok {
			candidates := columnNames(tbl)
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("soft_delete column %q not found in table %q", r.SoftDelete, r.Table),
				Hint:     fmt.Sprintf("candidate columns: %s", strings.Join(candidates, ", ")),
			})
			return nil, nil, diags
		}
	}

	// Per proto-field processing.
	writableSet := map[string]bool{}
	for _, w := range r.Writable {
		writableSet[w] = true
	}
	bindings := []BindingModel{}
	skipped := map[string]bool{}
	diagnosedFields := map[string]bool{} // fields already covered by a diagnostic — used by step 4
	usedCodecs := map[string]CodecModel{}
	columnOwners := map[string]string{} // column -> first proto field that bound it
	for i := 0; i < desc.Fields().Len(); i++ {
		fd := desc.Fields().Get(i)
		fname := string(fd.Name())

		ov, hasOv := r.Overrides[fname]
		if hasOv && ov.Skip {
			skipped[fname] = true
			continue
		}

		// Resolve column.
		colName := fname
		if hasOv && ov.Column != "" {
			colName = ov.Column
		}
		col, colOK := tbl.Column(colName)
		if !colOK {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("field %q has no matching column %q in table %q", fname, colName, r.Table),
				Hint:     fmt.Sprintf("add to aippatch.yaml:\n    overrides:\n      %s: { column: <name> }   # or { skip: true }", fname),
			})
			diagnosedFields[fname] = true
			continue
		}

		// Compatibility check (Task 32 expands this).
		// Wording mirrors the spec's *Diagnostics* section ("unsupported in v0; mark skip:true").
		codec, sqlType, ok := compatibility(fd, col, ov, codecsYaml)
		if !ok {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message: fmt.Sprintf("field %q (kind %s) is unsupported in v0 against column %q (type %s)",
					fname, fd.Kind(), col.Name, col.Type),
				Hint: fmt.Sprintf("mark { skip: true } in aippatch.yaml:\n    overrides:\n      %s: { skip: true }", fname),
			})
			diagnosedFields[fname] = true
			continue
		}

		if !col.NotNull {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("%s.%s -> %s.%s: nullable column not supported in v0", r.Message, fname, r.Table, col.Name),
				Hint:     "mark { skip: true } or wait for v1 nullable support",
			})
			diagnosedFields[fname] = true
			continue
		}

		// Step 10: column uniqueness.
		if owner, taken := columnOwners[colName]; taken {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("column %q bound by multiple proto fields", colName),
				Hint:     fmt.Sprintf("conflicting fields: %s, %s — pick one binding or add `{ skip: true }`", owner, fname),
			})
			diagnosedFields[fname] = true
			continue
		}
		columnOwners[colName] = fname

		bindings = append(bindings, BindingModel{
			Proto:    fname,
			Column:   colName,
			SQLType:  sqlType,
			Writable: writableSet[fname],
			Codec:    codec,
		})

		// Track codec usage.
		if strings.HasPrefix(codec, "enum:") {
			cn := strings.TrimPrefix(codec, "enum:")
			yc := codecsYaml[cn]
			cm := CodecModel{Name: cn, ProtoEnum: yc.ProtoEnum}
			ed, err := files.FindDescriptorByName(protoreflect.FullName(yc.ProtoEnum))
			if err != nil {
				diags = append(diags, Diagnostic{
					Resource: r.Message,
					Message: fmt.Sprintf("codec %q: proto_enum %q not found in descriptor set: %v",
						cn, yc.ProtoEnum, err),
					Hint: "check the proto_enum value in aippatch.yaml matches the fully-qualified proto enum name",
				})
				diagnosedFields[fname] = true
				continue
			}
			enumDesc, ok := ed.(protoreflect.EnumDescriptor)
			if !ok {
				diags = append(diags, Diagnostic{
					Resource: r.Message,
					Message: fmt.Sprintf("codec %q: proto_enum %q is not an enum descriptor (got %T)",
						cn, yc.ProtoEnum, ed),
				})
				diagnosedFields[fname] = true
				continue
			}
			// Build the set of valid proto value names to detect stale yaml keys.
			validNames := map[string]bool{}
			goEnumType := string(enumDesc.Name())
			for j := 0; j < enumDesc.Values().Len(); j++ {
				vd := enumDesc.Values().Get(j)
				validNames[string(vd.Name())] = true
				if text, ok := yc.Map[string(vd.Name())]; ok {
					cm.Values = append(cm.Values, EnumValue{
						Number:  int32(vd.Number()),
						GoConst: goEnumType + "_" + string(vd.Name()),
						Text:    text,
					})
				}
			}
			// Identify yaml map keys that don't match any proto enum value name.
			var unknown []string
			for k := range yc.Map {
				if !validNames[k] {
					unknown = append(unknown, k)
				}
			}
			if len(unknown) > 0 {
				sort.Strings(unknown)
				validList := make([]string, 0, len(validNames))
				for n := range validNames {
					validList = append(validList, n)
				}
				sort.Strings(validList)
				diags = append(diags, Diagnostic{
					Resource: r.Message,
					Message: fmt.Sprintf("codec %q: yaml map keys %v do not match any proto enum value name",
						cn, unknown),
					Hint: fmt.Sprintf("valid value names: %s", strings.Join(validList, ", ")),
				})
			}
			usedCodecs[cn] = cm
		}
	}

	// Step 4: every proto field accounted for. The main loop already emits
	// diagnostics for compatibility / nullable / column-not-found failures
	// and records them in diagnosedFields. This safety net catches any
	// future path that drops a field silently (e.g. a code change that
	// adds a `continue` without populating diagnosedFields).
	bindingHas := make(map[string]bool, len(bindings))
	for _, b := range bindings {
		bindingHas[b.Proto] = true
	}
	for i := 0; i < desc.Fields().Len(); i++ {
		fname := string(desc.Fields().Get(i).Name())
		if skipped[fname] || bindingHas[fname] || diagnosedFields[fname] {
			continue
		}
		diags = append(diags, Diagnostic{
			Resource: r.Message,
			Message:  fmt.Sprintf("proto field %q has no binding and is not marked skip:true", fname),
			Hint:     fmt.Sprintf("add to aippatch.yaml:\n    overrides:\n      %s: { skip: true }   # or { column: <name> }", fname),
		})
	}

	// Writable references must be in proto descriptor.
	for w := range writableSet {
		if desc.Fields().ByName(protoreflect.Name(w)) == nil {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("writable field %q not present in proto descriptor", w),
			})
		}
	}

	// Step 5: AutoSet.
	autoSet := []AutoSetModel{}
	for col, lit := range r.AutoSet {
		if !identifierRe.MatchString(col) {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("auto_set column %q is not a plain SQL identifier", col),
				Hint:     "auto_set column names must match [A-Za-z_][A-Za-z0-9_]*",
			})
			continue
		}
		c, ok := tbl.Column(col)
		if !ok {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("auto_set column %q not found in table %s", col, r.Table),
			})
			continue
		}
		if !c.NotNull {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("auto_set column %q must be NOT NULL", col),
			})
			continue
		}
		if _, taken := columnOwners[col]; taken {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("auto_set column %q conflicts with binding", col),
				Hint:     "auto_set columns must not also be bindings; remove the binding or pick a different column",
			})
			continue
		}
		// Validate literal as a single Postgres expression.
		// We wrap in SELECT (...) and then inspect the parse tree:
		//   - exactly 1 statement (rejects "1); DROP TABLE x; SELECT (1")
		//   - that statement is a SELECT with exactly 1 target (rejects "NOW(), other_col = 'x'")
		res, err := pg_query.Parse("SELECT (" + lit + ")")
		if err != nil {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("auto_set column %q literal %q is not a valid Postgres expression: %v", col, lit, err),
			})
			continue
		}
		if len(res.Stmts) != 1 {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("auto_set column %q literal %q must be a single expression, not multiple statements", col, lit),
			})
			continue
		}
		sel := res.Stmts[0].Stmt.GetSelectStmt()
		if sel == nil || len(sel.GetTargetList()) != 1 {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("auto_set column %q literal %q must be a single expression target", col, lit),
			})
			continue
		}
		// A comma-separated list like "NOW(), col = 'x'" parses as a single
		// RowExpr target rather than multiple targets. Reject it explicitly.
		if tgt := sel.GetTargetList()[0].GetResTarget(); tgt != nil && tgt.GetVal().GetRowExpr() != nil {
			diags = append(diags, Diagnostic{
				Resource: r.Message,
				Message:  fmt.Sprintf("auto_set column %q literal %q must be a single expression target", col, lit),
			})
			continue
		}
		autoSet = append(autoSet, AutoSetModel{Column: col, SQLLiteral: lit})
	}

	// Step 8: stable sort.
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Proto < bindings[j].Proto })
	sort.Slice(autoSet, func(i, j int) bool { return autoSet[i].Column < autoSet[j].Column })

	emptyMask := "ErrorOnEmpty"

	// Derive the Go package alias from the proto file's go_package option,
	// not from the proto package name. The two are not the same: drill's
	// `package drill.v1; option go_package = "...;drillv1";` yields alias
	// "drillv1", while fixturepb's `package aippatch.fixture.v1; option
	// go_package = "...;fixturepb";` yields "fixturepb" (NOT
	// "aippatchfixturev1").
	pkgAlias := goPackageAlias(desc.ParentFile())
	if pkgAlias == "" {
		diags = append(diags, Diagnostic{
			Resource: r.Message,
			Message: fmt.Sprintf("proto file %q has no go_package option (or no ;<alias> suffix)",
				desc.ParentFile().Path()),
		})
		return nil, nil, diags
	}
	goType := "*" + pkgAlias + "." + string(desc.Name())

	return &ResourceModel{
		Message:    r.Message,
		Package:    pkgAlias,
		GoType:     goType,
		Table:      r.Table,
		PK:         r.PK,
		SoftDelete: r.SoftDelete,
		EmptyMask:  emptyMask,
		Bindings:   bindings,
		AutoSet:    autoSet,
	}, usedCodecs, diags
}

// compatibility maps a proto field kind + SQL column type into a codec
// string ("", "timestamp", "enum:<name>") and a normalized SQL type string,
// or returns ok=false on incompatibility.
func compatibility(fd protoreflect.FieldDescriptor, col *Column, ov yamlOverride, codecsYaml map[string]yamlCodec) (codec, sqlType string, ok bool) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		switch col.Type {
		case "text", "varchar", "citext":
			return "", col.Type, true
		case "uuid":
			return "", "uuid", true
		}
	case protoreflect.BoolKind:
		if col.Type == "boolean" || col.Type == "bool" {
			return "", "boolean", true
		}
	case protoreflect.Int32Kind, protoreflect.Sint32Kind:
		switch col.Type {
		case "integer", "int4", "int":
			return "", "integer", true
		case "smallint", "int2":
			return "", "smallint", true
		}
	case protoreflect.Int64Kind, protoreflect.Sint64Kind:
		if col.Type == "bigint" || col.Type == "int8" {
			return "", "bigint", true
		}
	case protoreflect.MessageKind:
		if fd.Message().FullName() == "google.protobuf.Timestamp" {
			if col.Type == "timestamptz" || col.Type == "timestamp" {
				return "timestamp", col.Type, true
			}
		}
	case protoreflect.EnumKind:
		if ov.Codec == "" {
			return "", "", false
		}
		if _, exists := codecsYaml[ov.Codec]; !exists {
			return "", "", false
		}
		if col.Type != "text" && col.Type != "varchar" {
			return "", "", false
		}
		return "enum:" + ov.Codec, col.Type, true
	}
	return "", "", false
}

// goPackageAlias extracts the Go package name from the proto file's
// go_package option. The option's syntax is "<importPath>[;<packageName>]".
// Returns the explicit packageName when present; falls back to the last
// path segment of importPath; returns "" if the option is absent.
func goPackageAlias(fd protoreflect.FileDescriptor) string {
	opts, ok := fd.Options().(*descriptorpb.FileOptions)
	if !ok || opts == nil {
		return ""
	}
	goPkg := opts.GetGoPackage()
	if goPkg == "" {
		return ""
	}
	if idx := strings.Index(goPkg, ";"); idx >= 0 {
		return goPkg[idx+1:]
	}
	if idx := strings.LastIndex(goPkg, "/"); idx >= 0 {
		return goPkg[idx+1:]
	}
	return goPkg
}

// ResourceModel is the in-memory representation of one generated *.gen.go.
type ResourceModel struct {
	Message    string // proto full name
	Package    string // proto Go package alias used in generated file
	GoType     string // generated Go type, e.g. "*drillv1.User"
	Table      string
	PK         string
	SoftDelete string
	EmptyMask  string // "ErrorOnEmpty" or "UpdateAllWritable"
	Bindings   []BindingModel
	AutoSet    []AutoSetModel
}

type BindingModel struct {
	Proto    string
	Column   string
	SQLType  string
	Writable bool
	Codec    string
}

type AutoSetModel struct {
	Column     string
	SQLLiteral string
}

// CodecModel is the in-memory representation of one entry in Codecs registry.
type CodecModel struct {
	Name      string
	ProtoEnum string      // proto full name (e.g. "drill.v1.UserRole")
	Values    []EnumValue // (number, qualified Go const name, text)
}

// EnumValue carries the **fully-qualified Go constant name** (without the
// package prefix), e.g. "UserRole_USER_ROLE_ADMIN". The template emits
// `int32({{$.Package}}.{{.GoConst}})` which expands to a valid reference.
type EnumValue struct {
	Number  int32
	GoConst string // e.g. "UserRole_USER_ROLE_ADMIN"
	Text    string // SQL text
}

// columnNames returns sorted column names for a table, used in hint messages.
func columnNames(tbl *Table) []string {
	cols := tbl.Columns()
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	sort.Strings(names)
	return names
}
