package main

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
