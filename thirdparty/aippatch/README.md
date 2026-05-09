# aippatch

A small runtime library + sibling code generator for AIP-134 PATCH RPCs that
map proto messages with FieldMask onto dynamic Postgres UPDATE statements.

See `docs/superpowers/specs/2026-05-07-aippatch-design.md` for the full design.

## Runtime

The runtime library (this directory) imports no drill code and depends only on:

- `google.golang.org/protobuf`
- `github.com/jackc/pgx/v5`
- `github.com/huandu/go-sqlbuilder` v1.36.0+
- `github.com/google/uuid`

## Codegen

The codegen tool at `./cmd/aippatchgen` parses `aippatch.yaml`, the proto
FileDescriptorSet (`buf.binpb`), and SQL migrations to emit `*.gen.go`
mappings. It links `libpg_query` and **requires `CGO_ENABLED=1`** (Go's
default; some shops set `CGO_ENABLED=0` globally — set it back to 1 to
build aippatchgen). The runtime library itself has no CGO dependency.
