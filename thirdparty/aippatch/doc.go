// Package aippatch turns AIP-134 PATCH RPCs into safe, dynamic Postgres
// UPDATE statements. The proto message and FieldMask carry intent on the
// wire; an aippatch.yaml plus a sibling code generator produce a typed
// Mapping[T] per resource; a runtime Apply[T] call validates the mask,
// builds the SQL, executes it, and returns the post-update proto.
//
// This package contains the runtime library only. It imports no drill
// code and is suitable for extraction into a standalone Go module. The
// codegen tool lives at ./cmd/aippatchgen and requires CGO (libpg_query).
package aippatch
