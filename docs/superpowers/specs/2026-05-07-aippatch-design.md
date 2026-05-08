# aippatch — proto/SQL PATCH framework

**Status:** spec
**Author:** Brian Tiger Chow
**Date:** 2026-05-07

## Summary

`aippatch` is a small Go library plus a sibling code-generator that turn AIP-134
PATCH RPCs into safe, dynamic Postgres `UPDATE` statements. The proto message
and `FieldMask` carry intent on the wire; an `aippatch.yaml` config plus
codegen produce a typed `Mapping[T]` per resource; a runtime `Apply[T]` call
validates the mask, builds the SQL, executes it, and returns the post-update
proto. Built first inside drill at `thirdparty/aippatch/`; designed to be
lifted into a standalone Go module and re-used across Spanda LLC products.

## Goals

- **Common case is one yaml entry.** Adding a new writable field on a resource
  is: declare it in `aippatch.yaml`, regenerate, ship.
- **Hard case is possible.** Name divergences, enum codecs, and opt-outs are
  expressible as overrides without escape hatches into custom Go.
- **No type casting in user code.** The public API is generic over the proto
  message type; callers never see `proto.Message` erasure.
- **Schema drift is a build-time error.** When proto fields rename, columns
  rename, or types diverge, the codegen tool fails CI before runtime can.
- **Replicable across projects.** Three artifacts (runtime library, codegen
  binary, yaml file) port to any Go service speaking ConnectRPC + pgx.
- **AIP-134 compliant on the wire.** PATCH responses carry the updated
  resource. `FieldMask` semantics are honored.

## Non-goals (v0)

- Declarative authorization (per-field write gating). v0 keeps all authz in
  handler code.
- Declarative validation (`NonEmptyTrimmed`, `LenBetween`, `URL`, …). v0 keeps
  per-field validation in handler code.
- AIP-193 error-code mapping (`unique_violation` → `AlreadyExists`, etc.). v0
  returns `Internal` for unmapped pgx errors and `NotFound` for zero-row
  updates.
- Optimistic concurrency / ETag (AIP-154). v0 has no version column awareness.
- JSONB, repeated, oneof, message-as-jsonb, proto3 explicit-optional / NULL
  semantics. v0 codecs cover scalars, timestamps, and enums only; everything
  else fails at codegen with a diagnostic.
- Replacing sqlc for SELECTs and non-PATCH UPDATEs. aippatch only owns dynamic
  PATCH UPDATEs.

## First-principles mechanics

To turn a PATCH RPC into `UPDATE … WHERE … RETURNING *` you need nine things:

1. **Presence detection** — which fields to apply.
2. **Proto-field → SQL-column mapping.**
3. **Value coercion** for the write side (proto value → SQL parameter).
4. **Row identity** — the primary key.
5. **Scope predicates** — tenancy / soft-delete / additional WHERE clauses.
6. **Per-field write authorization.**
7. **Per-field value validation.**
8. **Returned representation** — the post-update resource on the wire.
9. **Optimistic concurrency.**

`aippatch` v0 owns 1, 2, 3, 4, 5, and 8. Items 6 and 7 stay in the handler;
item 9 is deferred. Read-back (item 8) requires bidirectional coercion, so the
v0 codec set covers every type the v0 target resources use.

## Architecture

```
┌─────────────────────────┐         ┌──────────────────────────┐
│  pb/drill/v1/*.proto    │         │  sql/migrations/*.up.sql │
│  (proto contracts)      │         │  (logical schema)        │
└──────────┬──────────────┘         └────────────┬─────────────┘
           │ buf build → buf.binpb               │ pg_query_go
           │  (FileDescriptorSet)                │
           ▼                                     ▼
       ┌─────────────────────────────────────────────────┐
       │  cmd/aippatchgen  (standalone Go binary)        │
       │  reads: descriptors + SQL schema + aippatch.yaml│
       │  writes: typed Mapping[T] literals (Go)         │
       └─────────────────┬───────────────────────────────┘
                         │           ▲
                         │           │ aippatch.yaml
                         ▼           │  (codecs, overrides, writable)
       ┌─────────────────────────────────────────────────┐
       │  internal/patches/*.gen.go   (committed)        │
       │  e.g. var UserPatch = aippatch.Mapping[*User]{} │
       └─────────────────┬───────────────────────────────┘
                         │ imported by
                         ▼
       ┌─────────────────────────────────────────────────┐
       │  internal/rpc/<svc>/server.go   (handler)       │
       │  aippatch.Apply(ctx, pool,                      │
       │      patches.UserPatch, Op[*User]{...})         │
       └─────────────────┬───────────────────────────────┘
                         │ uses
                         ▼
       ┌─────────────────────────────────────────────────┐
       │  thirdparty/aippatch/  (runtime library)        │
       │  • Mapping[T], Binding, Op[T], EmptyMaskPolicy  │
       │  • Apply[T] — validate → build → exec → scan    │
       │  • Codec dispatch: scalar / timestamp / enum    │
       │  • Self-contained: no drill imports             │
       └─────────────────────────────────────────────────┘
```

Five components, three new:

1. **`thirdparty/aippatch/`** — runtime library. Self-contained, no drill
   imports, ready to lift into a standalone Go module. Imports:
   `google.golang.org/protobuf`, `github.com/jackc/pgx/v5`,
   `github.com/huandu/go-sqlbuilder`. Reads back via `pgx.Rows.Values()` and
   populates the proto via reflection — no third-party row scanner is needed.
2. **`thirdparty/aippatch/cmd/aippatchgen/`** — codegen binary. Imports:
   `google.golang.org/protobuf` + `github.com/pganalyze/pg_query_go/v5`.
3. **`aippatch.yaml`** — at the repo root. Source of truth for codecs,
   resource bindings, name overrides, and writable allow-list.

Pre-existing components shrink:

4. **`internal/patches/*.gen.go`** — committed generated code, one file per
   resource.
5. **`internal/rpc/<svc>/server.go`** — handlers shrink to ~12 lines.

### Boundary properties

- The runtime imports nothing from drill. It speaks proto and pgx.
- `aippatchgen` imports nothing from the runtime — it produces Go literals
  whose types satisfy the public runtime API.
- Handlers import nothing from `aippatchgen` — they import the runtime and the
  generated `patches` package.
- sqlc still owns SELECT, INSERT, and any non-PATCH UPDATE. aippatch only
  writes the dynamic PATCH UPDATE.

## Public API (runtime)

```go
package aippatch

// Mapping is the static description of how a proto message round-trips
// through a SQL table. Generated by aippatchgen; never hand-edited.
type Mapping[T proto.Message] struct {
    Table      string
    PK         string                  // column name (PK value comes from Op)
    SoftDelete string                  // "" if none; framework adds "AND col IS NULL"
    EmptyMask  EmptyMaskPolicy         // ErrorOnEmpty (v0 default)
    Bindings   []Binding               // ordered, alphabetical by Proto

    // Populated by Validate(); unexported.
    bindingsByProto  map[string]*Binding
    bindingsByColumn map[string]*Binding
}

// Binding pairs one proto field with one SQL column.
type Binding struct {
    Proto    string  // proto field name, e.g. "display_name"
    Column   string  // SQL column, e.g. "display_name" (or "created_at")
    SQLType  string  // diagnostic: "text", "timestamptz", "uuid", "boolean", "integer", …
    Writable bool    // PATCH may set this column; default false (deny-by-default)
    Codec    string  // "" (scalar pass-through) | "timestamp" | "enum:<name>"
}

// Op carries a single PATCH invocation's runtime data.
type Op[T proto.Message] struct {
    Message T                          // input proto carrying the new values
    Mask    *fieldmaskpb.FieldMask     // which fields to apply
    PKValue any                        // value for the PK column (e.g. uuid.UUID)
    Where   map[string]any             // optional extra equality predicates
}

// DBTX is the minimal pgx interface aippatch needs (matches sqlc's DBTX).
type DBTX interface {
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// EmptyMaskPolicy controls behavior when Op.Mask has zero paths.
type EmptyMaskPolicy int
const (
    ErrorOnEmpty       EmptyMaskPolicy = iota // v0 default
    UpdateAllWritable                          // future opt-in (not implemented in v0)
)

// Apply executes the PATCH described by op against m, and returns the
// updated proto message populated from the RETURNING * row.
func Apply[T proto.Message](
    ctx context.Context, db DBTX,
    m *Mapping[T], op Op[T],
) (T, error)

// Validate is called by generated package init; checks every binding's
// proto path against the descriptor of T and indexes binding maps.
// Returns error rather than panicking, per drill's no-panic-at-init rule.
func (m *Mapping[T]) Validate() error
```

The codec registry inside the runtime is keyed by the `Codec` string on each
Binding. v0 ships three:

- `""` — scalar pass-through (`StringKind`, `BoolKind`, `Int32Kind`,
  `Sint32Kind`, `Int64Kind`, `Sint64Kind`)
- `"timestamp"` — `google.protobuf.Timestamp` ↔ `time.Time` via `.AsTime()` /
  `timestamppb.New(...)`
- `"enum:<name>"` — proto enum number ↔ SQL text via a declared map; both
  directions look up the map keyed by the enum's protoreflect name. Out-of-map
  values on the read side return `Internal` (data invariant violation); on the
  write side return `InvalidArgument`.

### Errors

All errors returned by `Apply` are `*connect.Error` with appropriate codes:

- `CodeInvalidArgument` — empty mask (when policy is `ErrorOnEmpty`), unknown
  mask path, non-writable mask path, enum write value not in declared map.
- `CodeNotFound` — `UPDATE` matched zero rows (PK wrong or scope filter
  excluded the row).
- `CodeInternal` — pgx error, codec read-side data invariant violation, or
  binding/proto desync that escaped boot-time `Validate`.

## Codegen tool: `aippatchgen`

### CLI

```
aippatchgen [--check] [--config aippatch.yaml] [--out internal/patches]
            [--proto buf.binpb] [--migrations sql/migrations]
```

Defaults: reads `./aippatch.yaml`, writes to `./internal/patches/`, reads
`./buf.binpb` and `./sql/migrations/`. `--check` exits non-zero if any
generated file would change. Wired into `Makefile`:

```
codegen: ; buf generate && sqlc generate && go run ./thirdparty/aippatch/cmd/aippatchgen
test: ; … && go run ./thirdparty/aippatch/cmd/aippatchgen --check && …
```

### Inputs

1. **`buf.binpb`** — emitted by `buf build -o buf.binpb` as part of
   `buf generate`. Unmarshaled into `*descriptorpb.FileDescriptorSet`; walked
   via `protoreflect.FileDescriptor`.
2. **`sql/migrations/*.up.sql`** — read in lexical order. Each statement
   parsed by `pg_query_go`. The tool accumulates a logical schema:
   - `CREATE TABLE` → register table with columns `(name, type, nullable)`
   - `ALTER TABLE … ADD COLUMN` → add column
   - `ALTER TABLE … DROP COLUMN` → remove column
   - `ALTER TABLE … ALTER COLUMN … TYPE` → change type
   - `ALTER TABLE … RENAME COLUMN` → rename
   - `DROP TABLE` → remove table
   - Other statements (indexes, constraints, FK refs) are ignored.
3. **`aippatch.yaml`** — codecs + resource declarations (schema below).

### Algorithm

1. Load `buf.binpb` → `FileDescriptorSet`.
2. Replay migrations to build the logical schema state.
3. For each `resources[i]` in `aippatch.yaml`:
   1. Look up the proto message descriptor by full name.
   2. Look up the SQL table from the schema; resolve the PK column.
   3. For each proto field in the message, in field-number order:
      - If `overrides[field].skip` is true → drop.
      - If `overrides[field].column` set → use that column.
      - Else → snake-case name match with the SQL column list.
      - If no match → record diagnostic: "field X has no matching column;
        list candidates and suggest yaml fix."
      - Compatibility check between proto kind and column type (table below).
        If incompatible → diagnostic.
      - Determine codec:
        - `MessageKind` with full name `google.protobuf.Timestamp` → `"timestamp"`.
        - Enum kind → `"enum:" + name` from `overrides[field].codec`. If missing
          → diagnostic: "enum field requires explicit codec in overrides."
        - Scalar kind → `""`.
        - Anything else → diagnostic: "unsupported in v0; mark `skip: true`."
      - `Writable` = field name is in `resources[i].writable`.
   4. Sort bindings alphabetically by `Proto` for stable output.
4. Emit one Go file per resource.
5. If `--check`: byte-compare to existing files; exit 1 on any diff.

### Type compatibility (v0)

| Proto kind | SQL types accepted | Codec |
|---|---|---|
| `StringKind` | `text`, `varchar`, `citext`, `uuid` | `""` |
| `BoolKind` | `boolean` | `""` |
| `Int32Kind`, `Sint32Kind` | `integer`, `smallint` | `""` |
| `Int64Kind`, `Sint64Kind` | `bigint` | `""` |
| `MessageKind`: `google.protobuf.Timestamp` | `timestamptz`, `timestamp` | `"timestamp"` |
| `EnumKind` | `text`, `varchar` | `"enum:<name>"` (declared) |
| anything else | — | codegen error |

`uuid`-as-string is special-cased: a proto `string` field maps to a `uuid`
column when the column type is `uuid`, with `[16]byte`↔string conversion in
the runtime.

### Diagnostics

Codegen failures are clear and actionable:

```
aippatchgen: drill.v1.User: field "create_time" has no matching column
  proto field type: google.protobuf.Timestamp
  candidate columns: [created_at, updated_at, deleted_at]
  hint: add to aippatch.yaml:
    overrides:
      create_time: { column: created_at }

aippatchgen: drill.v1.User: field "role" has unsupported type without codec
  proto field kind: enum (drill.v1.UserRole)
  hint: declare codec and override:
    codecs:
      enum_role: { proto_enum: drill.v1.UserRole, map: { ... } }
    resources:
      - message: drill.v1.User
        overrides: { role: { codec: enum_role } }

aippatchgen: drill.v1.User: writable field "display_name" not present in proto descriptor
```

## Configuration: `aippatch.yaml`

```yaml
codecs:
  enum_role:
    proto_enum: drill.v1.UserRole
    map:
      USER_ROLE_CANDIDATE: candidate
      USER_ROLE_ADMIN:     admin
  enum_plan:
    proto_enum: drill.v1.UserPlan
    map:
      USER_PLAN_FREE: free
      USER_PLAN_PRO:  pro

resources:
  - message: drill.v1.User
    table: users
    pk: id
    soft_delete: deleted_at
    empty_mask: error                # error (default) | update_writable
    writable: [display_name]         # AIP-203 deny-by-default
    overrides:
      create_time: { column: created_at }
      role:        { codec: enum_role }
      plan:        { codec: enum_plan }
```

One file per repo. `~10–20` lines per resource. Reviewers see policy and
mapping deltas in a single diff. Adding a writable field is one line.

## Generated file shape

`internal/patches/user.gen.go`:

```go
// Code generated by aippatchgen. DO NOT EDIT.
package patches

import (
    drillv1 "github.com/btc/drill/internal/pb/drill/v1"
    "github.com/btc/drill/thirdparty/aippatch"
)

// UserPatch is the PATCH mapping for drill.v1.User → users.
var UserPatch = mustValidate(&aippatch.Mapping[*drillv1.User]{
    Table:      "users",
    PK:         "id",
    SoftDelete: "deleted_at",
    EmptyMask:  aippatch.ErrorOnEmpty,
    Bindings: []aippatch.Binding{
        {Proto: "create_time",    Column: "created_at",     SQLType: "timestamptz", Writable: false, Codec: "timestamp"},
        {Proto: "display_name",   Column: "display_name",   SQLType: "text",        Writable: true,  Codec: ""},
        {Proto: "email",          Column: "email",          SQLType: "text",        Writable: false, Codec: ""},
        {Proto: "email_verified", Column: "email_verified", SQLType: "boolean",     Writable: false, Codec: ""},
        {Proto: "id",             Column: "id",             SQLType: "uuid",        Writable: false, Codec: ""},
        {Proto: "plan",           Column: "plan",           SQLType: "text",        Writable: false, Codec: "enum:enum_plan"},
        {Proto: "role",           Column: "role",           SQLType: "text",        Writable: false, Codec: "enum:enum_role"},
    },
})

func mustValidate[T proto.Message](m *aippatch.Mapping[T]) *aippatch.Mapping[T] {
    if err := m.Validate(); err != nil {
        // Per drill's no-panic-at-init rule, the generated package exposes
        // an Init() that returns the error; main wires it up. The
        // mustValidate helper is only used in tests; production wiring uses
        // an explicit constructor that returns (mappings, error).
        panic(err)
    }
    return m
}
```

**Init wiring:** to comply with drill's no-panic-at-init rule, the production
build does *not* use the `mustValidate` helper. Instead `aippatchgen` emits an
`InitPatches() error` function that calls `Validate()` on every generated
mapping and returns the first error. `cmd/server/main.go` calls it during
startup and propagates the error normally. The `mustValidate` helper exists
only for test-side use where panicking is acceptable.

## Runtime: `Apply` walkthrough

```go
func Apply[T proto.Message](
    ctx context.Context, db DBTX,
    m *Mapping[T], op Op[T],
) (T, error) {
    var zero T

    // 1. Mask validation
    paths := op.Mask.GetPaths()
    if len(paths) == 0 {
        if m.EmptyMask == ErrorOnEmpty {
            return zero, connectInvalidArg("update_mask must not be empty")
        }
        // Future: collect all writable bindings as paths.
    }

    // 2. Resolve paths to bindings; reject unknown / non-writable.
    sets := make([]string, 0, len(paths))   // go-sqlbuilder Assign exprs
    desc := op.Message.ProtoReflect().Descriptor()
    ub   := sqlbuilder.PostgreSQL.NewUpdateBuilder()
    ub.Update(m.Table)

    for _, p := range paths {
        b, ok := m.bindingsByProto[p]
        if !ok {
            return zero, connectInvalidArg("unknown field in update_mask: %q", p)
        }
        if !b.Writable {
            return zero, connectInvalidArg("field not writable: %q", p)
        }
        fd := desc.Fields().ByName(protoreflect.Name(p))
        if fd == nil {
            return zero, connectInternal("binding/proto desync: %q", p)
        }
        v, err := encode(op.Message, fd, b.Codec, m.codecs) // proto value → SQL parameter
        if err != nil { return zero, err }
        sets = append(sets, ub.Assign(b.Column, v))
    }
    ub.Set(sets...)

    // 3. WHERE clauses.
    ub.Where(ub.Equal(m.PK, op.PKValue))
    if m.SoftDelete != "" {
        ub.Where(m.SoftDelete + " IS NULL")
    }
    for col, v := range op.Where {
        ub.Where(ub.Equal(col, v))
    }
    ub.SQL("RETURNING *")

    sqlStr, args := ub.Build()

    // 4. Execute and read back via RETURNING *.
    rows, err := db.Query(ctx, sqlStr, args...)
    if err != nil { return zero, connectInternal("query: %w", err) }
    defer rows.Close()

    if !rows.Next() {
        if err := rows.Err(); err != nil {
            return zero, connectInternal("query: %w", err)
        }
        return zero, connectNotFound("resource not found or filtered out")
    }
    cols := rows.FieldDescriptions()
    vals, err := rows.Values()
    if err != nil { return zero, connectInternal("scan: %w", err) }

    // 5. Build result proto from input message + RETURNING values.
    result := proto.Clone(op.Message).(T)
    msg    := result.ProtoReflect()
    for i, c := range cols {
        b, ok := m.bindingsByColumn[string(c.Name)]
        if !ok { continue } // unmapped column → ignore
        fd := msg.Descriptor().Fields().ByName(protoreflect.Name(b.Proto))
        if err := decode(msg, fd, vals[i], b.Codec, m.codecs); err != nil {
            return zero, connectInternal("decode %s: %w", b.Proto, err)
        }
    }
    return result, nil
}
```

`encode` and `decode` are bounded switches over field kind × codec. The total
runtime is approximately 300 LoC including codec dispatch, error
constructors, and `Validate`.

### pgx native types on the read side

| SQL type | pgx returns | Proto kind expected | Conversion |
|---|---|---|---|
| `text`, `varchar`, `citext` | `string` | `StringKind` | direct |
| `uuid` | `[16]byte` | `StringKind` | `uuid.UUID(v).String()` |
| `boolean` | `bool` | `BoolKind` | direct |
| `integer`, `smallint` | `int32` | `Int32Kind`, `Sint32Kind` | direct |
| `bigint` | `int64` | `Int64Kind`, `Sint64Kind` | direct |
| `timestamptz`, `timestamp` | `time.Time` | `MessageKind: Timestamp` | `timestamppb.New(v)` |
| `text` (with enum codec) | `string` | `EnumKind` | reverse-map declared codec |

Any other pgx-native type encountered at runtime is an `Internal` error
(should have been caught by codegen's compatibility check).

## Handler call site

drill's existing `UpdateProfile` (currently `internal/rpc/user/server.go:100-141`)
shrinks from ~45 lines to ~12:

```go
func (s *Server) UpdateProfile(
    ctx context.Context,
    req *connect.Request[drillv1.UpdateProfileRequest],
) (*connect.Response[drillv1.UpdateProfileResponse], error) {
    u := auth.UserFromContext(ctx)
    if u == nil {
        return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
    }

    // Per-field validation lives in the handler in v0. v2 makes it declarative.
    if maskHasPath(req.Msg.GetUpdateMask(), "display_name") {
        trimmed := strings.TrimSpace(req.Msg.GetUser().GetDisplayName())
        if trimmed == "" {
            return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("display_name must not be empty"))
        }
        req.Msg.GetUser().DisplayName = trimmed
    }

    updated, err := aippatch.Apply(ctx, s.b.Pool, patches.UserPatch, aippatch.Op[*drillv1.User]{
        Message: req.Msg.GetUser(),
        Mask:    req.Msg.GetUpdateMask(),
        PKValue: u.ID,
    })
    if err != nil { return nil, err }

    return connect.NewResponse(&drillv1.UpdateProfileResponse{User: updated}), nil
}
```

The hand-rolled `implementedUserFields` allow-list and per-path validation
loop disappear. Authorization (the unauthenticated check) and per-field value
validation (the trim + non-empty check) remain in the handler.

## Testing strategy

Three layers:

### Runtime unit tests (`thirdparty/aippatch/apply_test.go`)

Table-driven against testcontainers Postgres. Drill already uses
`testcontainers-go/modules/postgres` (per `go.mod`); aippatch tests reuse the
same approach with a small fixture schema independent of drill's migrations.

Cases:
- empty mask → `InvalidArgument`
- unknown mask path → `InvalidArgument`
- non-writable mask path → `InvalidArgument`
- writable scalar (string, bool, int32, int64) write + read-back
- writable timestamp write + read-back
- writable enum write (valid & invalid) + read-back
- soft-delete WHERE filters out deleted rows → `NotFound`
- PK mismatch → `NotFound`
- extra `Op.Where` predicate excludes row → `NotFound`
- `RETURNING *` populates fields not touched by mask
- `proto.Clone` preserves input-message fields that have no binding

### Codegen golden tests (`thirdparty/aippatch/cmd/aippatchgen/aippatchgen_test.go`)

Fixtures under `testdata/`:
- `simple/` — proto + 2 migrations + yaml → expected `*.gen.go`
- `name_divergence/` — `create_time` ↔ `created_at`
- `enum_codec/` — proto enum + declared codec
- `unsupported_kind/` — proto with bytes field (not in v0); expects diagnostic
- `missing_column/` — proto field with no candidate; expects diagnostic
- `--check_drift/` — fixture with stale `*.gen.go`; expects exit 1

### Handler integration test (`internal/rpc/user/server_test.go`)

Uses drill's existing `backendtest.SeedUser` to create a real user, then
exercises `UpdateProfile` end-to-end through the connect handler:
- valid PATCH on `display_name` → response carries updated User; DB row
  updated.
- empty mask → `InvalidArgument`.
- mask with `email` (non-writable) → `InvalidArgument`.
- unauthenticated → `Unauthenticated`.

These tests already exist for the current implementation; they should pass
unchanged after the migration.

## Drill rollout plan

1. Add `thirdparty/aippatch/` runtime package and `cmd/aippatchgen/` binary.
2. Add `aippatch.yaml` at repo root with the `User` resource and enum codecs.
3. Add `internal/patches/` directory; wire `aippatchgen` into `make codegen`.
4. Add `aippatchgen --check` to `make test`.
5. Generate `internal/patches/user.gen.go`. Review the diff manually.
6. Wire `patches.InitPatches()` into `cmd/server/main.go` startup; surface
   any error from `Validate()`.
7. Replace `UpdateProfile` handler body with the shrunk version.
8. Delete the `UpdateUserDisplayName` query from `sql/queries/users.sql` and
   regenerate sqlc.
9. Run `make test`; existing handler tests should pass unchanged.

Rollback: single-commit revert. The proto wire contract is unchanged.

## Spanda replication

Each Spanda repo gets three artifacts:

1. The `thirdparty/aippatch/` directory (initially copied from drill; once
   stable, extracted to its own module and imported).
2. The `aippatchgen` binary — `go install ./thirdparty/aippatch/cmd/aippatchgen`.
3. A `aippatch.yaml` skeleton.

Each project's `Makefile` wires `aippatchgen` into its `codegen` and `test`
targets. No drill-specific code is required.

## Roadmap

| Tier | Feature | Notes |
|---|---|---|
| v1 | JSONB codec | Marshals proto sub-messages or `[]byte` to `jsonb` columns. Likely first non-v0 demand. |
| v1 | Proto3 explicit-optional + NULL semantics | AIP-134 clearing rule (`mask path + zero value → NULL`); only meaningful for `optional` fields. |
| v2 | Declarative validators | `NonEmptyTrimmed`, `LenBetween`, `URL`, `OneOf`. Per-resource yaml + handler-side composition. |
| v2 | AIP-193 error mapping | pgx error inspection: `unique_violation` → `AlreadyExists`, `fk_violation` → `FailedPrecondition`, `not_null_violation` / `check_violation` → `InvalidArgument`. Per-resource override map. |
| v3 | Per-field declarative authz | `admin_only_fields:` in yaml; layered with handler narrowing. |
| v3 | Concurrency / ETag (AIP-154) | Resource declares version column; Apply requires inbound etag and bumps on success. |
| v4 | Buf plugin | Proto annotations replace yaml entries; same generated output. |
| Out of scope | repeated, oneof, sub-resources | AIP-134 punts these to sub-resource RPCs. |

Each tier is backwards-compatible: v0 call sites do not change when later
tiers ship. New features are opt-in via `aippatch.yaml`.

## Risks

1. **`pg_query_go` is a CGO dependency.** It wraps `libpg_query`. drill's
   production binary builds may run with `CGO_ENABLED=0` in some paths.
   Mitigation: `aippatchgen` is a developer/CI tool, not part of the
   production binary; CGO is only required where `aippatchgen` runs. Document
   this in the package README.

2. **pgx-native ↔ proto type drift.** New SQL types added to drill in the
   future may not be in the runtime's `decode` switch. Mitigation:
   `aippatchgen` rejects unknown SQL types at codegen with a clear diagnostic;
   the runtime never sees a type the codegen accepted.

3. **`EmptyMaskPolicy` is wire-affecting.** Switching from `ErrorOnEmpty` to
   `UpdateAllWritable` changes behavior visible to clients. Mitigation:
   document as a per-resource permanent decision; `error` is the v0 default
   and recommended.

4. **Validation duplication in v0.** Per-field validation lives in handlers
   until v2. New PATCH RPCs added before v2 must hand-roll trimming /
   non-empty / length checks. Mitigation: ship v2 quickly if the duplication
   becomes painful; document the v0 expectation in the README.

5. **`proto.Clone` for the result base.** `Apply` clones `op.Message` as the
   starting point for the returned proto. Fields not in the RETURNING row
   keep their input-message values. For PATCH this is fine (all DB-backed
   fields are populated by RETURNING). For non-DB fields (rare; would only
   exist if the proto carries computed-only fields), the input message's
   values pass through. Mitigation: document; reject in codegen any proto
   field that has no `skip: true` and no binding match.

## Decisions (locked, with rationale)

| # | Decision | Why |
|---|---|---|
| 1 | Runtime library + standalone codegen binary; not a buf plugin | Cleaner separation from buf's plugin machinery; reusable in non-buf contexts. |
| 2 | Working name `aippatch`; lives at `drill/thirdparty/aippatch/` | Signals AIP-134 lineage; thirdparty/ prepares clean extraction. |
| 3 | Generic `Mapping[T proto.Message]` (single type parameter) | No row-type coupling; framework is sqlc-independent. No type casts in user code. |
| 4 | Codegen consumes proto FileDescriptorSet + SQL migrations + yaml | Both schemas already on disk; yaml carries policy + overrides only. |
| 5 | Generated `*.gen.go` files committed to repo | Mapping is reviewable in PRs; CI checks for drift via `--check`. |
| 6 | SQL builder: `huandu/go-sqlbuilder` (private to package) | Mature; `PostgreSQL.NewUpdateBuilder()` emits `$1` placeholders cleanly. |
| 7 | Row scan: direct `pgx.Rows.Values()` + proto reflection (no third-party scanner) | A scanner like `scany/v2` would target a Go row struct; we populate a proto via reflection instead. Avoids an unnecessary dependency and a proto-aware shim. |
| 8 | Empty FieldMask rejected with `InvalidArgument` (default) | Strict; matches drill's existing behavior; relax later if a use case warrants. |
| 9 | Deny-by-default writable; opt in via `writable:` list | AIP-203; security posture. |
| 10 | Codegen errors on unsupported field types | Bad fields stop at codegen; runtime never sees a type it cannot handle. |
| 11 | Framework reads back via `RETURNING *` and returns the populated proto | One round-trip; AIP-134 compliant on the wire; pulls codec set up to cover every type in target protos. |
| 12 | v0 codec set: scalars + timestamps + enum | Smallest set that covers drill's `User` and most Spanda CRUD shapes. JSONB and others in v1+. |
| 13 | v0 first user: drill's `UpdateProfile` | Validates the framework against an existing target; replaces the most boilerplate-heavy code path today. |
| 14 | Boot validation via `Mapping.Validate() error` propagated to `main` | drill's no-panic-at-init rule. |

## Open questions (deferred)

- **JSONB shape** — for v1: do we marshal proto sub-messages as JSON via
  `protojson`, or accept opaque `[]byte` from the handler? Trade-offs around
  schema evolution.
- **AIP-154 ETag column type** — `bigint` counter, `uuid` token, or
  per-resource choice? Defer to v3 when the use case is concrete.
- **Buf plugin migration** — when (and if) v4 ships, the yaml format remains
  the source of truth for policy; only mappings move to proto annotations.
  Migration path TBD.

