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
- **Hard case is possible.** Name divergences, enum codecs, always-set columns
  (e.g. `updated_at`), and opt-outs are expressible as overrides without
  escape hatches into custom Go.
- **No type casting in user code.** The public API is generic over the proto
  message type; callers never see `proto.Message` erasure.
- **Schema drift is a build-time error.** When proto fields rename, columns
  rename, or types diverge, the codegen tool fails CI before runtime can.
- **Replicable across projects.** Three artifacts (runtime library, codegen
  binary, yaml file) port to any Go service speaking ConnectRPC + pgx.
- **AIP-134-aligned with documented divergences.** Wire shape (resource +
  FieldMask in, full updated resource out) follows AIP-134. Empty-mask
  semantics deviate intentionally; see *Wire conformance note* below.

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
- **Nullable bound columns.** v0 bindings must reference `NOT NULL` columns.
  Nullable columns require `pgtype.*` decode handling and are deferred to v1.
- **`bytes`, `float`, `double` proto kinds.** Common but unused in drill v0
  resources; deferred to v1.
- **Nested mask paths.** v0 supports top-level proto fields only. Mask paths
  with dots (`address.street`) are rejected by codegen with a diagnostic.
- **Audit logging hooks.** v0 has no pre/post hook for emitting audit events.
  Callers wrap `Apply` themselves until v1 adds returned-diff or hooks.
- Replacing sqlc for SELECTs and non-PATCH UPDATEs. aippatch only owns dynamic
  PATCH UPDATEs.

## Wire conformance note

aippatch follows AIP-134 wire shape (resource + `FieldMask` in, full updated
resource out) with two intentional divergences:

1. **Empty `FieldMask`** is rejected with `InvalidArgument` (default
   `ErrorOnEmpty` policy). AIP-134 §Update specifies that an omitted mask
   "MUST" be treated as an implied mask covering all populated fields. drill
   prefers explicit intent over implicit broad updates; clients that need
   full-field updates must enumerate paths. The `EmptyMaskPolicy` is
   wire-affecting and considered permanent for any deployed service —
   document the chosen policy in the resource's API documentation.
2. **Nested mask paths** (`address.street`) are not supported in v0 because
   the codec set excludes nested message types. Codegen rejects yaml entries
   whose proto fields would require nested support. Roadmap: v1+.

A separate behavior change is wire-visible during the drill migration: today's
`UpdateProfile` returns `Internal` when the user is soft-deleted (the sqlc
`UpdateUserDisplayName` includes `AND deleted_at IS NULL`, returns
`pgx.ErrNoRows`, the handler wraps as `Internal`). aippatch returns
`NotFound` for the same case (zero-row update). This is the more correct AIP
behavior. The rollout plan calls it out so clients depending on the prior
code can adapt.

All other AIP-134 requirements (return the updated resource, honor mask paths
that are valid, reject unknown paths) are upheld.

## First-principles mechanics

To turn a PATCH RPC into `UPDATE … WHERE … RETURNING <cols>` you need nine
things:

1. **Presence detection** — which fields to apply.
2. **Proto-field → SQL-column mapping.**
3. **Value coercion** for the write side (proto value → SQL parameter).
4. **Row identity** — the primary key.
5. **Scope predicates** — tenancy / soft-delete / additional WHERE clauses.
6. **Per-field write authorization.**
7. **Per-field value validation.**
8. **Returned representation** — the post-update resource on the wire.
9. **Optimistic concurrency.**

`aippatch` v0 owns 1, 2, 3, 4, 5, and 8. Items **6** (authorization) and **7**
(validation) stay in the handler in v0; v2 promotes them to declarative.
Item **9** (concurrency) is deferred to v3. Read-back (item 8) requires
bidirectional coercion, so the v0 codec set covers every type the v0 target
resources use.

A tenth concern — **audit logging** — is intentionally out of v0; callers wrap
`Apply` for now. v1 considers a returned-diff or hooks API.

## Architecture

```
┌─────────────────────────┐         ┌──────────────────────────┐
│  pb/drill/v1/*.proto    │         │  sql/migrations/*.up.sql │
│  (proto contracts)      │         │  (logical schema)        │
└──────────┬──────────────┘         └────────────┬─────────────┘
           │ buf build -o buf.binpb              │ pg_query_go/v6
           │  (FileDescriptorSet)                │
           ▼                                     ▼
       ┌──────────────────────────────────────────────────────┐
       │  thirdparty/aippatch/cmd/aippatchgen/                │
       │  (standalone Go binary; CGO required for pg_query_go)│
       │  reads: descriptors + SQL schema + aippatch.yaml     │
       │  writes: typed Mapping[T] literals + InitPatches()   │
       └─────────────────┬────────────────────────────────────┘
                         │           ▲
                         │           │ aippatch.yaml
                         ▼           │  (codecs, overrides, writable, auto_set)
       ┌──────────────────────────────────────────────────────┐
       │  internal/patches/*.gen.go   (committed)             │
       │  e.g. var UserPatch = aippatch.Mapping[*User]{ … }   │
       │  var Codecs = map[string]aippatch.EnumCodec{ … }     │
       │  func InitPatches() error { Validate all mappings }  │
       └─────────────────┬────────────────────────────────────┘
                         │ imported by
                         ▼
       ┌──────────────────────────────────────────────────────┐
       │  internal/rpc/<svc>/server.go   (handler)            │
       │  aippatch.Apply(ctx, pool,                           │
       │      patches.UserPatch, Op[*User]{...})              │
       └─────────────────┬────────────────────────────────────┘
                         │ uses
                         ▼
       ┌──────────────────────────────────────────────────────┐
       │  thirdparty/aippatch/  (runtime library)             │
       │  • Mapping[T], Binding, AutoSetClause, Op[T],        │
       │    EmptyMaskPolicy, EnumCodec                        │
       │  • Apply[T] — validate → build → exec → scan         │
       │  • Codec dispatch: scalar / timestamp / enum         │
       │  • Self-contained: no drill imports                  │
       └──────────────────────────────────────────────────────┘
```

Five components, three new:

1. **`thirdparty/aippatch/`** — runtime library. Self-contained, no drill
   imports, ready to lift into a standalone Go module. Imports:
   `google.golang.org/protobuf`, `github.com/jackc/pgx/v5`,
   `github.com/huandu/go-sqlbuilder`, `github.com/google/uuid` (for
   `[16]byte`→canonical uuid string formatting on the read side). Reads back
   via `pgx.Rows.Values()` and populates the proto via reflection — no
   third-party row scanner is needed.
2. **`thirdparty/aippatch/cmd/aippatchgen/`** — codegen binary. Imports:
   `google.golang.org/protobuf` + `github.com/pganalyze/pg_query_go/v6`.
   Requires CGO (libpg_query); see *Risks* §1.
3. **`aippatch.yaml`** — at the repo root. Source of truth for codecs,
   resource bindings, name overrides, writable allow-list, and always-set
   columns.

Pre-existing components shrink:

4. **`internal/patches/*.gen.go`** — committed generated code, one file per
   resource, plus a single `init.gen.go` that emits the shared
   `var Codecs = map[string]aippatch.EnumCodec{...}` registry and
   `InitPatches() error`.
5. **`internal/rpc/<svc>/server.go`** — handlers shrink to ~12 lines.

### Boundary properties

- The runtime imports nothing from drill. It speaks proto and pgx.
- `aippatchgen` imports nothing from the runtime — it produces Go literals
  whose types satisfy the public runtime API.
- Handlers import nothing from `aippatchgen` — they import the runtime and the
  generated `patches` package.
- sqlc still owns SELECT, INSERT, and any non-PATCH UPDATE. aippatch only
  writes the dynamic PATCH UPDATE.
- The runtime's `DBTX` interface is satisfied by `*pgxpool.Pool`, `*pgx.Conn`,
  and `pgx.Tx` — `Apply` participates in a caller's transaction transparently
  when a `pgx.Tx` is passed.

## Public API (runtime)

```go
package aippatch

import "sync/atomic"

// Mapping is the static description of how a proto message round-trips
// through a SQL table. Generated by aippatchgen; never hand-edited.
type Mapping[T proto.Message] struct {
    Table      string
    PK         string                  // column name (PK value comes from Op)
    SoftDelete string                  // "" if none; framework adds "AND col IS NULL"
    EmptyMask  EmptyMaskPolicy         // ErrorOnEmpty (v0 default)
    Bindings   []Binding               // ordered alphabetically by Proto for stable diff
    AutoSet    []AutoSetClause         // always-set columns regardless of mask

    // Populated by Validate(); unexported.
    bindingsByProto  map[string]*Binding
    bindingsByColumn map[string]*Binding
    codecs           map[string]EnumCodec  // shared codec map passed in via Validate
    validated        atomic.Bool           // Apply rejects unvalidated mappings
}

// Binding pairs one proto field with one SQL column.
type Binding struct {
    Proto    string  // proto field name, e.g. "display_name"
    Column   string  // SQL column, e.g. "display_name" (or "created_at")
    SQLType  string  // diagnostic: "text", "timestamptz", "uuid", "boolean", "integer", "smallint", "bigint"
    Writable bool    // PATCH may set this column; default false (deny-by-default)
    Codec    string  // "" (scalar pass-through) | "timestamp" | "enum:<yaml-name>"
}

// AutoSetClause defines a SQL expression always written into the SET clause.
// Typical use: { Column: "updated_at", SQLLiteral: "NOW()" }. The literal is
// emitted as raw SQL — never sourced from user input. Codegen verifies the
// column exists in the table, is NOT NULL, and is not also a binding.
type AutoSetClause struct {
    Column     string
    SQLLiteral string
}

// EnumCodec maps a proto enum to/from a SQL text column. Generated literal
// form lives in patches/init.gen.go's Codecs registry. Validate() copies
// the relevant subset into Mapping.codecs.
type EnumCodec struct {
    ProtoEnum string             // e.g. "drill.v1.UserRole"
    ToText    map[int32]string   // proto enum number → SQL text
    FromText  map[string]int32   // SQL text → proto enum number (built by Validate from ToText)
}

// Op carries a single PATCH invocation's runtime data.
type Op[T proto.Message] struct {
    Message T                          // input proto carrying the new values; must be non-nil
    Mask    *fieldmaskpb.FieldMask     // which fields to apply
    PKValue any                        // value for the PK column (e.g. uuid.UUID)
    Where   map[string]any             // optional extra equality predicates.
                                       // KEYS MUST BE BOUND COLUMNS — runtime validates
                                       // against m.bindingsByColumn before composing SQL.
                                       // Keys are NOT escaped; values ARE pgx-parameterized.
                                       // Values must be scalar / pgx-bindable; v0 framework
                                       // supports equality only. List comparators (IN, !=,
                                       // <, etc.) are deferred — passing a slice value
                                       // produces SQL like `col = ARRAY[…]`, not `col IN (…)`.
}

// DBTX is the minimal pgx interface aippatch needs. It is a strict subset of
// pgx's query surface and is satisfied by *pgxpool.Pool, *pgx.Conn, and pgx.Tx
// — Apply participates in a caller's transaction when a pgx.Tx is passed.
// (Note: this is *not* the same DBTX that sqlc generates; aippatch's is
// smaller and read-only on the connection from a control-flow perspective.)
type DBTX interface {
    Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// EmptyMaskPolicy controls behavior when Op.Mask has zero paths.
type EmptyMaskPolicy int
const (
    ErrorOnEmpty       EmptyMaskPolicy = iota // v0 default; rejects with InvalidArgument
    UpdateAllWritable                          // future opt-in (not implemented in v0)
)

// Apply executes the PATCH described by op against m, and returns the
// updated proto message populated from the RETURNING row. op.Message must
// be non-nil; the returned T is a clone (via proto.CloneOf) of op.Message
// with mapped columns overwritten from RETURNING.
func Apply[T proto.Message](
    ctx context.Context, db DBTX,
    m *Mapping[T], op Op[T],
) (T, error)

// Validate is called once at startup, by InitPatches(). It indexes the
// binding maps; copies relevant codecs into m.codecs (subset reachable from
// m.Bindings); builds each EnumCodec's FromText from ToText; verifies every
// binding's proto path exists on T; verifies every binding's "enum:<name>"
// codec reference exists in the supplied codecs map; and sets the validated
// atomic flag. Returns error rather than panicking, per drill's
// no-panic-at-init rule. Idempotent up to the validated flag — repeated
// calls return nil after the first success. On error, validated is left
// false and the caller may retry after fixing the cause.
//
// Concurrency: callers should invoke Validate before serving any RPCs (the
// generated InitPatches() runs synchronously at startup). Two concurrent
// Validate calls on the same Mapping that both observe validated == false
// would each rebuild the indexes (harmless but redundant). v0 does not use
// sync.Once because real callers serialize startup; if strict-once
// semantics are needed, wrap InitPatches() in a sync.Once at the call site.
func (m *Mapping[T]) Validate(codecs map[string]EnumCodec) error
```

### Codec dispatch

The codec field on each Binding selects how the proto value is encoded to
SQL and decoded back:

- `""` — scalar pass-through (`StringKind`, `BoolKind`, `Int32Kind`,
  `Sint32Kind`, `Int64Kind`, `Sint64Kind`).
- `"timestamp"` — `google.protobuf.Timestamp` ↔ `time.Time` via `.AsTime()` /
  `timestamppb.New(...)`.
- `"enum:<yaml-name>"` — proto enum number ↔ SQL text via `m.codecs[<yaml-name>]`.
  Out-of-map values on the read side return `Internal` (data invariant
  violation); on the write side return `InvalidArgument`. Codecs are global
  to the yaml file and reusable across resources.

`Validate()` populates `m.codecs` from the shared `Codecs` map by selecting
only the codec names referenced by `m.Bindings`. The resulting per-Mapping
map is read-only after `Validate` returns, eliminating any concern about
shared-state mutation.

### Errors

All errors returned by `Apply` are `*connect.Error` with appropriate codes:

- `CodeInvalidArgument` — empty mask (when policy is `ErrorOnEmpty`), unknown
  mask path, non-writable mask path, nested mask path, enum write value not
  in declared map, nil `op.Message`, `op.Where` key not in `bindingsByColumn`.
- `CodeNotFound` — `UPDATE` matched zero rows (PK wrong, scope filter
  excluded the row, or row is soft-deleted).
- `CodeUnimplemented` — `EmptyMaskPolicy` is `UpdateAllWritable`. Codegen is
  the primary defense (rejects `update_writable` in yaml in v0); this is a
  defense-in-depth runtime check that fires only if a Mapping is constructed
  by hand or by a future codegen version.
- `CodeInternal` — pgx error, codec read-side data invariant violation,
  binding/proto desync that escaped boot-time `Validate`, or `Apply` called
  on an unvalidated `Mapping` (`InitPatches()` not invoked).

### Transactions

`Apply` does not start its own transaction. `DBTX` accepts both pools and
`pgx.Tx`; passing a tx makes `Apply` participate. On error, the caller's
transaction state is the caller's responsibility — pgx aborts an open tx on
any non-nil error per its standard contract. For multi-statement atomic
operations (e.g. PATCH + audit-event insert), wrap in `pgx.BeginFunc`:

```go
err := pgx.BeginFunc(ctx, s.b.Pool(), func(tx pgx.Tx) error {
    _, err := aippatch.Apply(ctx, tx, patches.UserPatch, op)
    if err != nil { return err }
    _, err = tx.Exec(ctx, "INSERT INTO audit_events ...")
    return err
})
```

## Codegen tool: `aippatchgen`

### CLI

```
aippatchgen [--check] [--config aippatch.yaml] [--out internal/patches]
            [--proto buf.binpb] [--migrations sql/migrations]
```

Defaults: reads `./aippatch.yaml`, writes to `./internal/patches/`, reads
`./buf.binpb` and `./sql/migrations/`. `--check` exits non-zero if any
generated file would change.

**CGO requirement.** `aippatchgen` links `libpg_query` via
`github.com/pganalyze/pg_query_go/v6`, which requires `CGO_ENABLED=1`. This
is the default in Go's `go build`, but some shops set `CGO_ENABLED=0`
globally; document at the top of `cmd/aippatchgen/main.go` and in
`thirdparty/aippatch/README.md`. The runtime library has no CGO requirement.

Wired into `Makefile` (drill's existing `generate` target gains the
`buf build -o buf.binpb` and `aippatchgen` steps):

```
generate:
	buf generate
	buf build -o buf.binpb
	sqlc generate
	go run ./thirdparty/aippatch/cmd/aippatchgen

test:
	... && go run ./thirdparty/aippatch/cmd/aippatchgen --check && ...
```

`buf.binpb` is committed to the repo (small, deterministic; CI can regenerate
and verify if desired).

### Inputs

1. **`buf.binpb`** — emitted by `buf build -o buf.binpb` (added as a new step
   in `make generate`; the existing `buf generate` does not emit a descriptor
   set). Unmarshaled into `*descriptorpb.FileDescriptorSet`; walked via
   `protoreflect.FileDescriptor`.
2. **`sql/migrations/*.up.sql`** — read in lexical order. Each statement
   parsed by `pg_query_go/v6`. The tool accumulates a logical schema:
   - `CREATE TABLE` → register table with columns `(name, type, not_null,
     default_present)`.
   - `ALTER TABLE … ADD COLUMN` → add column.
   - `ALTER TABLE … DROP COLUMN` → remove column.
   - `ALTER TABLE … ALTER COLUMN … TYPE` → change type.
   - `ALTER TABLE … ALTER COLUMN … SET / DROP NOT NULL` → flip nullability.
   - `ALTER TABLE … RENAME COLUMN` → rename.
   - `DROP TABLE` → remove table.
   - `ALTER TABLE … ADD/DROP CONSTRAINT`, `ADD/DROP DEFAULT`, and any other
     `AlterTableCmd` kind not enumerated above: ignored in v0.
   - Other statements (indexes, foreign-key constraints, CHECK constraints)
     are ignored in v0. CHECK constraint extraction (to validate enum codec
     maps) is a v1 feature.

   **Nullability** is interpreted from `NOT NULL`, `PRIMARY KEY` (implies
   NOT NULL), and `SET / DROP NOT NULL` migrations. The codegen's
   compatibility check enforces v0's "bound columns must be NOT NULL" rule.
3. **`aippatch.yaml`** — codecs + resource declarations + auto_set blocks
   (schema below).

### Algorithm

1. Load `buf.binpb` → `FileDescriptorSet`.
2. Replay migrations to build the logical schema state.
3. For each `resources[i]` in `aippatch.yaml`:
   1. Look up the proto message descriptor by full name.
   2. Look up the SQL table from the schema; resolve the PK column.
   3. For each proto field in the message, in **field-number order** (so
      diagnostic messages line up with the proto file's declaration order):
      - If `overrides[field].skip` is true → emit no binding; the field is
        registered as intentionally skipped and is not flagged by step 4
        below.
      - If `overrides[field].column` set → use that column.
      - Else → snake-case name match with the SQL column list.
      - If no match → diagnostic: "field X has no matching column; suggest
        `{ skip: true }` or `{ column: <name> }`."
      - Compatibility check between proto kind and column type (table below).
        On v0 also enforce: column is NOT NULL. On incompatibility →
        diagnostic.
      - Determine codec:
        - `MessageKind` with full name `google.protobuf.Timestamp` →
          `"timestamp"`.
        - Enum kind → require `overrides[field].codec` to name a declared
          codec; emit `"enum:<yaml-name>"`. If missing → diagnostic.
        - Scalar kind → `""`.
        - Anything else → diagnostic: "unsupported in v0; mark `skip: true`."
      - `Writable` = field name is in `resources[i].writable`.
   4. After processing, every proto field must either have a binding or
      `skip: true`. Any unmatched field is a diagnostic (this prevents the
      `proto.CloneOf`-base case from silently passing through unmapped
      fields with input-message values).
   5. For each `auto_set[col]` entry: verify the column exists in the table
      and is NOT NULL. **Reject if the column is *any* binding** (writable
      or non-writable) — auto-set must own the column entirely. The literal
      expression is emitted verbatim as raw SQL; codegen further validates
      the literal by parsing it with `pg_query_go` and rejecting
      multi-statement input or non-expression payloads. (v0 effectively
      restricts callers to a few well-known forms: `NOW()`,
      `CURRENT_TIMESTAMP`, integer constants, etc.)
   6. Validate enum-codec yaml entries: every codec's `map` values must be
      unique (no two enum values map to the same SQL text), to prevent the
      derived FromText map from being lossy. Diagnostic on duplicates.
   7. Validate yaml-side names: reject any `writable` or `overrides` key
      containing dots (these would imply nested-message paths, which v0
      does not support).
   8. Sort `Bindings` alphabetically by `Proto` and `AutoSet` alphabetically
      by `Column` for **stable diff output** (different from step 3's
      processing order).
   9. Reject `empty_mask: update_writable` with a diagnostic — v0 does not
      implement this policy. The runtime carries a defense-in-depth check
      that returns `CodeUnimplemented`, but the yaml is the primary
      enforcement point.
   10. Verify column uniqueness across emitted bindings: if two proto fields
       (after applying overrides) bind to the same SQL column, emit a
       diagnostic identifying both fields and the shared column. Without
       this check, a misconfigured yaml could produce a duplicate
       `RETURNING` list and a `SET` clause that Postgres rejects with
       "multiple assignments to column".
4. Emit one Go file per resource, plus one `init.gen.go` that emits a
   shared `var Codecs = map[string]aippatch.EnumCodec{...}` registry and
   `InitPatches() error` calling `Validate(Codecs)` on each mapping.
5. If `--check`: byte-compare to existing files; exit 1 on any diff.

### Type compatibility (v0)

| Proto kind | SQL types accepted | Codec | Notes |
|---|---|---|---|
| `StringKind` | `text`, `varchar`, `citext`, `uuid` | `""` | `uuid` columns: pgx returns `[16]byte`; runtime decodes via `uuid.UUID(v.([16]byte)).String()`. |
| `BoolKind` | `boolean` | `""` | |
| `Int32Kind`, `Sint32Kind` | `integer` | `""` | pgx returns `int32`. |
| `Int32Kind`, `Sint32Kind` | `smallint` | `""` | pgx returns `int16`; runtime widens to `int32` before `protoreflect.Set`. |
| `Int64Kind`, `Sint64Kind` | `bigint` | `""` | pgx returns `int64`. |
| `MessageKind`: `google.protobuf.Timestamp` | `timestamptz`, `timestamp` | `"timestamp"` | |
| `EnumKind` | `text`, `varchar` | `"enum:<yaml-name>"` | yaml-declared map keyed by enum value name. |
| **Deferred (v1)** | | | |
| `BytesKind` | `bytea` | (TBD) | Codegen rejects in v0. |
| `FloatKind`, `DoubleKind` | `real`, `double precision` | (TBD) | Codegen rejects in v0. |
| Any kind ↔ nullable column | | | Codegen rejects in v0; v1 adds `pgtype.*` decode. |
| `MessageKind` (non-Timestamp) | `jsonb` | `"jsonb"` | v1. |
| anything else | — | — | codegen error |

`uuid`-as-string is special-cased: a proto `string` field maps to a `uuid`
column when the column type is `uuid`, with `[16]byte`↔canonical-string
conversion in the runtime. The runtime imports `github.com/google/uuid` for
the canonical formatter.

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

aippatchgen: drill.v1.User.password_hash → users.password_hash: nullable column not supported in v0
  hint: mark { skip: true } or wait for v1 nullable support.

aippatchgen: drill.v1.User.address.street: nested mask paths not supported in v0

aippatchgen: drill.v1.User: auto_set column "updated_at" not found in table users
  hint: ensure migrations have run and column exists.

aippatchgen: drill.v1.User: auto_set column "display_name" conflicts with binding
  hint: auto_set columns must not also be bindings; remove the proto field's binding or pick a different column.
```

## Configuration: `aippatch.yaml`

```yaml
# Codecs are global and reusable across resources.
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
    empty_mask: error                # error → ErrorOnEmpty (default; v0 only valid value)
    writable: [display_name]         # deny-by-default
    auto_set:
      # NOTE: NOW() is constant within a transaction. If a test runs
      # SELECT-before, Apply, SELECT-after inside a single BeginFunc, the
      # before/after timestamps will be equal. Test patterns that need to
      # observe the bump must use clock_timestamp() instead, run the SELECTs
      # outside the surrounding transaction, or compare against a captured
      # NOW() bound (post >= captured). See *Testing strategy*.
      updated_at: NOW()              # raw SQL, applied to every PATCH; pg_query_go-validated as expression
    overrides:
      create_time: { column: created_at }
      role:        { codec: enum_role }
      plan:        { codec: enum_plan }
```

The `users` table has SQL-only columns (`password_hash`, `stripe_customer_id`,
`free_full_educators_used`, `updated_at`, `deleted_at`) that have no
corresponding `User` proto field. Codegen iterates *proto fields*, not SQL
columns, so SQL-only columns are simply not visited and produce no diagnostic
or binding. No yaml entry is needed for them.

One file per repo. `~10–20` lines per resource. Reviewers see policy and
mapping deltas in a single diff. Adding a writable field is one line.

The yaml-string `error` maps to the Go enum `ErrorOnEmpty`. The yaml-string
`update_writable` would map to `UpdateAllWritable`, but v0 codegen rejects
this value with a diagnostic (the runtime path is defense-in-depth only —
see *Errors* and Algorithm step 9). v1 will implement the policy and remove
the codegen rejection.

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
// Validate(Codecs) is called from InitPatches() at startup, never at package init.
var UserPatch = &aippatch.Mapping[*drillv1.User]{
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
    AutoSet: []aippatch.AutoSetClause{
        {Column: "updated_at", SQLLiteral: "NOW()"},
    },
}
```

The `id` column appears as a non-writable binding because the proto field
`id` should round-trip in the response. The framework does not deduplicate
PK from Bindings — PK identifies the row to update via `WHERE`, while
Bindings carries the read-back representation. If a future resource has a
PK column without a corresponding proto field (e.g. a synthetic table key
that's not exposed via the API), that column simply doesn't appear in
Bindings; `Returning(boundCols...)` won't include it; the WHERE clause still
uses `m.PK` for row identity.

`internal/patches/init.gen.go`:

```go
// Code generated by aippatchgen. DO NOT EDIT.
package patches

import (
    drillv1 "github.com/btc/drill/internal/pb/drill/v1"
    "github.com/btc/drill/thirdparty/aippatch"
)

// Codecs is the shared registry of enum codecs declared in aippatch.yaml.
// Validate(Codecs) selects the codecs reachable from each Mapping's Bindings.
var Codecs = map[string]aippatch.EnumCodec{
    "enum_role": {
        ProtoEnum: "drill.v1.UserRole",
        ToText: map[int32]string{
            int32(drillv1.UserRole_USER_ROLE_CANDIDATE): "candidate",
            int32(drillv1.UserRole_USER_ROLE_ADMIN):     "admin",
        },
        // FromText is built by Validate from ToText; no need to emit twice.
    },
    "enum_plan": {
        ProtoEnum: "drill.v1.UserPlan",
        ToText: map[int32]string{
            int32(drillv1.UserPlan_USER_PLAN_FREE): "free",
            int32(drillv1.UserPlan_USER_PLAN_PRO):  "pro",
        },
    },
}

// InitPatches validates every generated mapping. Call once at server startup.
// Returns the first validation error encountered; never panics.
func InitPatches() error {
    for _, fn := range []func(map[string]aippatch.EnumCodec) error{
        UserPatch.Validate,
        // …one entry per resource…
    } {
        if err := fn(Codecs); err != nil {
            return err
        }
    }
    return nil
}
```

**Init wiring.** `cmd/drill/main.go` calls `patches.InitPatches()` during
startup, propagating the error normally per drill's no-panic-at-init rule. No
package-level `mustValidate` helper is generated — `Validate(Codecs)` is
invoked explicitly. This eliminates init-time panics and makes the validation
point greppable.

`Apply` reads the `validated` atomic on every call; calling `Apply` before
`InitPatches()` returns `Internal` ("Mapping not initialized; call
InitPatches()") rather than a misleading "unknown field" error. The atomic
also prevents data races under `-race` if `InitPatches` and `Apply` are
hypothetically interleaved (in practice `InitPatches` runs synchronously
before any RPC handler is registered).

## Runtime: `Apply` walkthrough

```go
func Apply[T proto.Message](
    ctx context.Context, db DBTX,
    m *Mapping[T], op Op[T],
) (T, error) {
    var zero T

    // 0. Sanity guards.
    if m == nil || !m.validated.Load() {
        return zero, connectInternal("aippatch.Mapping not initialized; call your generated InitPatches() (or Mapping.Validate) during startup")
    }
    // ProtoReflect().IsValid() returns false for typed-nil pointers and
    // un-initialized messages, sidestepping the typed-nil interface trap
    // (`any(op.Message) == nil` is false for a nil *T). ProtoReflect itself
    // never returns a nil interface for protoc-gen-go-generated types.
    src := op.Message.ProtoReflect()
    if !src.IsValid() {
        return zero, connectInvalidArg("op.Message must not be nil")
    }

    // 1. Mask validation. GetPaths is nil-safe (returns nil for both nil
    // mask and empty paths), so a single len() check covers both.
    paths := op.Mask.GetPaths()
    if len(paths) == 0 {
        if m.EmptyMask == ErrorOnEmpty {
            return zero, connectInvalidArg("update_mask must not be empty")
        }
        // UpdateAllWritable: not implemented in v0. CodeUnimplemented
        // signals "feature not yet built" rather than a server bug.
        return zero, connect.NewError(connect.CodeUnimplemented,
            errors.New("UpdateAllWritable is unimplemented in v0"))
    }

    // 2. Resolve paths to bindings; reject unknown / non-writable / nested.
    // sqlbuilder.PostgreSQL.NewUpdateBuilder() is required (not the default
    // NewUpdateBuilder) — only the PostgreSQL flavor emits $1 placeholders
    // and a working RETURNING clause.
    desc := src.Descriptor()
    ub   := sqlbuilder.PostgreSQL.NewUpdateBuilder()
    ub.Update(m.Table)

    // Iterate Bindings (not client-supplied paths) in stable order so the
    // emitted SQL is deterministic regardless of the order the client put
    // paths in the mask. This makes pgx's prepared-statement cache hit
    // across calls with the same mask shape, and makes golden-test SQL
    // deterministic.
    maskSet := make(map[string]struct{}, len(paths))
    for _, p := range paths {
        if strings.Contains(p, ".") {
            return zero, connectInvalidArg("nested mask path not supported in v0: %q", p)
        }
        if _, ok := m.bindingsByProto[p]; !ok {
            return zero, connectInvalidArg("unknown field in update_mask: %q", p)
        }
        if !m.bindingsByProto[p].Writable {
            return zero, connectInvalidArg("field not writable: %q", p)
        }
        maskSet[p] = struct{}{}
    }
    sets := make([]string, 0, len(maskSet)+len(m.AutoSet))
    for i := range m.Bindings {
        b := &m.Bindings[i]
        if _, ok := maskSet[b.Proto]; !ok { continue }
        fd := desc.Fields().ByName(protoreflect.Name(b.Proto))
        if fd == nil {
            return zero, connectInternal("binding/proto desync: %q", b.Proto)
        }
        v, err := encode(op.Message, fd, b.Codec, m.codecs)
        if err != nil { return zero, err }
        sets = append(sets, ub.Assign(b.Column, v))
    }

    // 3. AutoSet columns: append raw SQL fragments unconditionally.
    for _, a := range m.AutoSet {
        sets = append(sets, fmt.Sprintf("%s = %s", a.Column, a.SQLLiteral))
    }
    ub.Set(sets...)

    // 4. WHERE clauses. Sort op.Where keys for deterministic SQL (so pgx's
    // prepared-statement cache hits across calls with the same shape).
    ub.Where(ub.Equal(m.PK, op.PKValue))
    if m.SoftDelete != "" {
        ub.Where(ub.IsNull(m.SoftDelete))   // first-class helper, not string concat
    }
    if len(op.Where) > 0 {
        keys := make([]string, 0, len(op.Where))
        for k := range op.Where { keys = append(keys, k) }
        sort.Strings(keys)
        for _, col := range keys {
            // Reject columns that are not bound — avoids any chance of
            // attacker-controlled identifiers reaching the SQL string.
            if _, ok := m.bindingsByColumn[col]; !ok {
                return zero, connectInvalidArg("unknown column in op.Where: %q", col)
            }
            ub.Where(ub.Equal(col, op.Where[col]))
        }
    }

    // 5. Returning explicit bound columns (not RETURNING *).
    boundCols := make([]string, len(m.Bindings))
    for i, b := range m.Bindings { boundCols[i] = b.Column }
    ub.Returning(boundCols...)

    sqlStr, args := ub.Build()

    // 6. Execute and read back.
    rows, err := db.Query(ctx, sqlStr, args...)
    if err != nil { return zero, connectInternal("query: %w", err) }
    defer rows.Close()

    if !rows.Next() {
        if err := rows.Err(); err != nil {
            return zero, connectInternal("query: %w", err)
        }
        return zero, connectNotFound("resource not found, soft-deleted, or filtered out")
    }
    cols := rows.FieldDescriptions()
    vals, err := rows.Values()
    if err != nil { return zero, connectInternal("scan: %w", err) }

    // 7. Build result proto from input message clone + RETURNING values.
    result := proto.CloneOf(op.Message)
    msg    := result.ProtoReflect()
    for i, c := range cols {
        b, ok := m.bindingsByColumn[string(c.Name)]
        if !ok { continue }
        fd := msg.Descriptor().Fields().ByName(protoreflect.Name(b.Proto))
        if fd == nil {
            return zero, connectInternal("binding/proto desync on read: %q", b.Proto)
        }
        if err := decode(msg, fd, vals[i], b.Codec, m.codecs); err != nil {
            return zero, connectInternal("decode %s: %w", b.Proto, err)
        }
    }
    return result, nil
}
```

Notable details:

- **`proto.CloneOf`** (added in protobuf-go v1.36.6; project uses v1.36.11)
  is type-safe: returns `T` directly, no `.(T)` assertion.
- **`Returning(boundCols...)`** sends only mapped columns over the wire from
  Postgres to Go. Unmapped columns (e.g. `password_hash`,
  `stripe_customer_id`) are not transmitted at all. Note: bound non-writable
  columns (e.g. `email`) ARE transmitted — they appear in the response proto
  per AIP-134's "return the updated resource" requirement. The benefit of
  `Returning(boundCols)` over `RETURNING *` is excluding *unmapped*
  columns, not all non-writable ones.
- **`ub.Set(sets...)`** is variadic over assignment strings. `ub.Assign(col,
  val)` returns `"col = $N"` with parameterized placeholder; raw expressions
  for `AutoSet` are formatted directly (`"updated_at = NOW()"`), with codegen
  guaranteeing the column and literal are safe (column existence + NOT NULL
  + not-also-a-binding; literal parsed by `pg_query_go` as a single
  expression).
- **`ub.Returning(...)`** is the library's first-class API; we do not use the
  marker-position-dependent `ub.SQL(...)` for this.
- **`op.Where` keys are validated** against `m.bindingsByColumn` — keys must
  be bound columns. Values are pgx-parameterized; keys are not escaped, and
  the validation step is the safety guarantee.

`encode` and `decode` are bounded switches over field kind × codec. The total
runtime is approximately 350 LoC including codec dispatch, error
constructors, and `Validate`.

### pgx native types on the read side

| SQL type | pgx returns | Proto kind expected | Conversion |
|---|---|---|---|
| `text`, `varchar`, `citext` | `string` | `StringKind` | direct |
| `uuid` | `[16]byte` | `StringKind` | `uuid.UUID(v.([16]byte)).String()` |
| `boolean` | `bool` | `BoolKind` | direct |
| `integer` | `int32` | `Int32Kind`, `Sint32Kind` | direct |
| `smallint` | `int16` | `Int32Kind`, `Sint32Kind` | widen: `int32(v.(int16))` |
| `bigint` | `int64` | `Int64Kind`, `Sint64Kind` | direct |
| `timestamptz`, `timestamp` | `time.Time` | `MessageKind: Timestamp` | `timestamppb.New(v.(time.Time))` |
| `text` (with enum codec) | `string` | `EnumKind` | reverse-map declared codec |

**Nullable columns return `pgtype.Text`/`pgtype.Timestamptz`/etc. from
`rows.Values()` rather than the underlying scalar.** v0 codegen rejects
nullable bound columns; v1 adds explicit `pgtype.*` decode. Any unexpected
pgx-native type at runtime is `Internal` (should be impossible if codegen
accepted the binding).

## Handler call site

drill's existing `UpdateProfile` (currently `internal/rpc/user/server.go:100-141`)
shrinks from ~45 lines to ~14:

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
    if slices.Contains(req.Msg.GetUpdateMask().GetPaths(), "display_name") {
        trimmed := strings.TrimSpace(req.Msg.GetUser().GetDisplayName())
        if trimmed == "" {
            return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("display_name must not be empty"))
        }
        req.Msg.GetUser().DisplayName = trimmed
    }

    updated, err := aippatch.Apply(ctx, s.b.Pool(), patches.UserPatch, aippatch.Op[*drillv1.User]{
        Message: req.Msg.GetUser(),
        Mask:    req.Msg.GetUpdateMask(),
        PKValue: u.ID,
    })
    if err != nil { return nil, err }

    return connect.NewResponse(&drillv1.UpdateProfileResponse{User: updated}), nil
}
```

(`s.b.Pool()` is a method on `*backend.Backend` returning `*pgxpool.Pool`.)

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
- nil `op.Message` (typed-nil pointer) → `InvalidArgument`
- unvalidated mapping (`InitPatches` not called) → `Internal`
- unknown mask path → `InvalidArgument`
- non-writable mask path → `InvalidArgument`
- nested mask path → `InvalidArgument`
- `op.Where` key not in bindings → `InvalidArgument`
- writable scalar (string, bool, int32 from `integer`, int32 from `smallint`,
  int64) write + read-back
- writable timestamp write + read-back
- writable enum write (valid & invalid) + read-back
- AutoSet column is bumped on every PATCH. **Important:** `NOW()` returns
  the transaction-start timestamp and is constant for the duration of a
  single transaction, so a `BeginFunc(SELECT before; Apply; SELECT after)`
  test would see equal values. Use one of:
  - `clock_timestamp()` in the test fixture's `auto_set` instead of `NOW()`,
    which advances within a transaction; or
  - run the SELECT-before, `Apply`, SELECT-after as separate top-level
    statements (no surrounding `BeginFunc`); or
  - SELECT `NOW()` to capture the test's own transaction-time bound
    before `Apply`, then SELECT the row's `updated_at` after `Apply`,
    asserting it's >= the captured time.
- soft-delete WHERE filters out deleted rows → `NotFound`
- PK mismatch → `NotFound`
- extra `Op.Where` predicate (valid bound column) excludes row → `NotFound`
- `Returning(boundCols)` does not include unmapped columns
- `proto.CloneOf` preserves input-message fields that have no binding
- map-iteration determinism: same `Op.Where` produces the same SQL string
  (assert via `pgx.LogQuery` or capturing builder output)

### Codegen golden tests (`thirdparty/aippatch/cmd/aippatchgen/aippatchgen_test.go`)

Fixtures under `testdata/`:
- `simple/` — proto + 2 migrations + yaml → expected `*.gen.go`
- `name_divergence/` — `create_time` ↔ `created_at`
- `enum_codec/` — proto enum + declared codec
- `auto_set/` — `updated_at: NOW()` and verified output
- `auto_set_conflict/` — `auto_set` column also a binding; expects diagnostic
- `auto_set_bad_literal/` — yaml literal is multi-statement; expects diagnostic
- `unsupported_kind/` — proto with bytes field; expects diagnostic
- `nullable_bound/` — writable on a nullable column; expects diagnostic
- `nested_path/` — proto with submessage, attempted writable; expects diagnostic
- `missing_column/` — proto field with no candidate; expects diagnostic
- `unmatched_proto_field/` — proto field neither matched nor `skip: true`;
  expects diagnostic
- `--check_drift/` — fixture with stale `*.gen.go`; expects exit 1

### Handler integration test (`internal/rpc/user/server_test.go`)

Existing tests in this file already assert via `connect.CodeOf(err)` (per
`internal/rpc/user/server_test.go:151`, `:169`, `:187`, etc.) — *not* on
error message text — so the migration to aippatch does **not** require
updating any test assertions.

The existing cases continue to cover the right wire behaviors:
- valid PATCH on `display_name` → response carries updated User; DB row's
  `display_name` and `updated_at` both change.
- empty mask → `InvalidArgument`.
- unknown / non-writable mask path → `InvalidArgument`.
- empty display_name → `InvalidArgument` (handler-side validation still
  runs).
- unauthenticated → `Unauthenticated`.

One new case added by the rollout:
- soft-deleted user PATCH → `NotFound` (was `Internal`; see *Wire conformance
  note*).

## Drill rollout plan

1. **Dependency adds.** `go get github.com/huandu/go-sqlbuilder@v1.36.0
   github.com/pganalyze/pg_query_go/v6` and commit `go.mod`/`go.sum`.
   The `@v1.36.0` floor is required — `UpdateBuilder.Returning(...)` was
   added in that release. (`github.com/google/uuid` is already in `go.mod`.)
2. **Add framework.** Land `thirdparty/aippatch/` runtime + `cmd/aippatchgen/`
   binary.
3. **Wire codegen.** Extend the existing `make generate` target (line 125 of
   `Makefile`) to add `buf build -o buf.binpb` and the `aippatchgen` step
   after `buf generate`. Add `aippatchgen --check` to `make test`.
4. **Add config.** `aippatch.yaml` at repo root with the `User` resource,
   enum codecs, and `auto_set: { updated_at: NOW() }`.
5. **Generate.** Create `internal/patches/`; run `make generate`. Review the
   diff manually first time, including `internal/patches/user.gen.go` and
   `internal/patches/init.gen.go`.
6. **Wire init.** Call `patches.InitPatches()` in `cmd/drill/main.go`
   startup; surface any error from `Validate(Codecs)` per drill's no-panic
   rule.
7. **Switch handler.** Replace `UpdateProfile` handler body with the shrunk
   version. Verify the proto wire contract is unchanged (or document the
   one wire change: soft-deleted user now returns `NotFound`, was `Internal`).
8. **Audit consumers of `b.UpdateDisplayName`.** Grep the codebase for
   callers; confirm only `UpdateProfile` calls it before deletion.
9. **Remove superseded sqlc.** Delete `UpdateUserDisplayName` from
   `sql/queries/users.sql` and `b.UpdateDisplayName`; regenerate sqlc.
10. **Verify.** `make test` (full CI: buf lint, codegen check, frontend
    typecheck+lint+tests, backend tests with race).

Rollback: single-commit revert. The proto wire contract is unchanged; only
the soft-deleted-user code differs (and clients should treat
`Internal`/`NotFound` symmetrically as transient or missing-resource).

## Spanda replication

Each Spanda repo gets three artifacts:

1. The `thirdparty/aippatch/` directory (initially copied from drill; once
   stable, extracted to its own module — see *Roadmap v0.5*).
2. The `aippatchgen` binary — `go install
   ./thirdparty/aippatch/cmd/aippatchgen`.
3. An `aippatch.yaml` skeleton.

Each project's `Makefile` wires `aippatchgen` into its `codegen` and `test`
targets. The runtime library and codegen binary contain no drill-specific
code; the yaml file, generated `*.gen.go`, and `Makefile` wiring are
project-specific by design. (Note for the eventual extraction: scrub
drill-specific examples from `thirdparty/aippatch/README.md` before
publishing the module.)

## Roadmap

| Tier | Feature | Notes |
|---|---|---|
| v0.5 | Extract `thirdparty/aippatch/` to its own Go module | Trigger: a second Spanda project consumes aippatch in production. New module path `github.com/<org>/aippatch`; drill's `go.mod` switches from local replace to versioned import; thirdparty/ directory removed. |
| v1 | Nullable bound columns (`pgtype.*` decode) | First wave of demand; many natural settings columns are nullable. |
| v1 | JSONB codec | Marshals proto sub-messages or `[]byte` to `jsonb` columns. |
| v1 | `bytes`, `float`, `double` proto kinds | Bytea / real / double precision support. |
| v1 | Pre/post hooks (or returned diff) for audit logging | `Apply` returns `(updated T, diff Diff, err error)` where Diff carries before/after for mask paths; handler emits audit events. |
| v1 | Proto3 explicit-optional + NULL semantics | AIP-134 clearing rule (`mask path + zero value → NULL`); meaningful for `optional` fields. |
| v1 | CHECK constraint extraction | Validate enum codec maps against `CHECK (col IN (…))` at codegen. |
| v1 | `UpdateAllWritable` empty-mask policy | Implement the "all populated/writable fields" path. v0 codegen rejects `update_writable` in yaml; the runtime defense-in-depth check returns `CodeUnimplemented` if a Mapping is somehow constructed with this policy. |
| v2 | Declarative validators | `NonEmptyTrimmed`, `LenBetween`, `URL`, `OneOf`. Per-resource yaml + handler-side composition. |
| v2 | AIP-193 error mapping | pgx error inspection: `unique_violation` → `AlreadyExists`, `fk_violation` → `FailedPrecondition`, `not_null_violation` / `check_violation` → `InvalidArgument`. Per-resource override map. |
| v3 | Per-field declarative authz | `admin_only_fields:` in yaml; layered with handler narrowing. |
| v3 | Concurrency / ETag (AIP-154) | Resource declares version column; Apply requires inbound etag and bumps on success. |
| v4 | Buf plugin | Proto annotations replace yaml entries; same generated output. |
| Out of scope | repeated, oneof, sub-resources | AIP-134 punts these to sub-resource RPCs. |

Each tier is backwards-compatible: v0 call sites do not change when later
tiers ship. New features are opt-in via `aippatch.yaml`.

## Risks

1. **`pg_query_go/v6` is a CGO dependency.** It wraps `libpg_query`. drill's
   production binary builds may run with `CGO_ENABLED=0` in some paths.
   Mitigation: `aippatchgen` is a developer/CI tool, not part of the
   production binary; CGO is only required where `aippatchgen` runs.
   Document at the top of `cmd/aippatchgen/main.go`:
   `// Requires CGO (libpg_query).` Add a `README.md` next to it stating
   the same. CI runners must have a C toolchain — drill's CI already does
   for testcontainers. **First-build cost:** `pg_query_go/v6` compiles part
   of the PostgreSQL parser from C source on first use; on a cold build
   cache this can take several minutes (varies by runner). CI runners
   should preserve `GOCACHE` and `GOMODCACHE` across runs (drill's CI
   already does).

2. **pgx-native ↔ proto type drift.** New SQL types added to drill in the
   future may not be in the runtime's `decode` switch. Mitigation:
   `aippatchgen` rejects unknown SQL types at codegen with a clear
   diagnostic; the runtime never sees a type the codegen accepted.
   `Returning(boundCols...)` (not `RETURNING *`) further reduces blast
   radius — unmapped columns are not transmitted from Postgres at all.

3. **`EmptyMaskPolicy` is wire-affecting and AIP-134-divergent.**
   `ErrorOnEmpty` deviates from AIP-134 §Update's "MUST treat omitted mask as
   all populated fields" — this is documented in *Wire conformance note*.
   Switching policies post-deploy is a breaking change visible to clients;
   document choice per resource in API docs.

4. **Validation duplication in v0.** Per-field validation lives in handlers
   until v2. New PATCH RPCs added before v2 must hand-roll trimming /
   non-empty / length checks. Mitigation: ship v2 quickly if duplication
   becomes painful; document the v0 expectation in the README.

5. **Audit logging is the caller's responsibility in v0.** Wrapping `Apply`
   in a transaction is the documented pattern for atomically logging audit
   events. v1 adds returned-diff support to remove the wrap. Mitigation: if
   audit comes due before v1 ships, the wrap-in-tx pattern is sufficient.

6. **Soft-deleted user wire change.** Today `UpdateProfile` returns
   `Internal` when the user is soft-deleted; aippatch returns `NotFound`.
   This is more correct AIP behavior, but is wire-visible. Mitigation:
   document in *Wire conformance note* and the rollout plan.

7. **`buf.binpb` drift.** If `buf.binpb` is committed and a developer regens
   `pb/*.pb.go` without re-running `buf build -o buf.binpb`, the codegen
   will be stale. Mitigation: `make generate` runs both in order;
   `aippatchgen --check` in CI catches drift.

8. **`op.Where` raw-identifier surface.** Keys in the map are interpolated
   into the SQL string by `go-sqlbuilder` (`ub.Equal(col, val)` parameterizes
   only the value). Mitigation: runtime validates every key against
   `m.bindingsByColumn` before composing SQL — keys must be a bound column.
   Document the constraint in the API; add a unit test for the rejection
   path.

## Decisions (locked, with rationale)

| # | Decision | Why |
|---|---|---|
| 1 | Runtime library + standalone codegen binary; not a buf plugin | Cleaner separation from buf's plugin machinery; reusable in non-buf contexts. |
| 2 | Working name `aippatch`; lives at `drill/thirdparty/aippatch/` | Signals AIP-134 lineage; thirdparty/ prepares clean extraction. |
| 3 | Generic `Mapping[T proto.Message]` (single type parameter) | No row-type coupling; framework is sqlc-independent. No type casts in user code. |
| 4 | Codegen consumes proto FileDescriptorSet + SQL migrations + yaml | Both schemas already on disk; yaml carries policy + overrides + auto_set only. |
| 5 | Generated `*.gen.go` files committed to repo | Mapping is reviewable in PRs; CI checks for drift via `--check`. |
| 6 | SQL builder: `huandu/go-sqlbuilder` (private to package) | Mature; `PostgreSQL.NewUpdateBuilder()` emits `$1` placeholders cleanly; `Returning(...)` is a first-class method. |
| 7 | Row scan: direct `pgx.Rows.Values()` + proto reflection (no third-party scanner) | We populate a proto via reflection rather than a Go row struct; avoids an unnecessary dependency and a proto-aware shim. |
| 8 | Empty FieldMask rejected with `InvalidArgument` (default) | drill prefers explicit intent; documented divergence from AIP-134; permanent per resource once deployed (see *Wire conformance note*). |
| 9 | Deny-by-default writable; opt in via `writable:` list | Security posture; consistent with AIP-134 §Update_Mask "must not allow output-only fields." |
| 10 | Codegen errors on unsupported field types | Bad fields stop at codegen; runtime never sees a type it cannot handle. |
| 11 | Framework reads back via `RETURNING <bound-columns>` (not `*`) and returns the populated proto | One round-trip; AIP-134 compliant; explicit column list excludes unmapped columns from the wire. (Bound non-writable columns are still returned — that is the AIP contract.) |
| 12 | v0 codec set: scalars + timestamps + enum, NOT-NULL columns only | Smallest set that covers drill's `User` and most Spanda CRUD shapes. JSONB / nullable / bytes / float in v1. |
| 13 | v0 first user: drill's `UpdateProfile` | Validates the framework against an existing target; replaces the most boilerplate-heavy code path today. |
| 14 | Boot validation via `Mapping.Validate(Codecs) error` propagated to `main` through generated `InitPatches() error` | drill's no-panic-at-init rule. No `mustValidate` panic helper in generated code. |
| 15 | `AutoSet` clauses (e.g. `updated_at: NOW()`) declared per-resource in yaml | Replaces sqlc's hand-rolled `updated_at = NOW()` in every UPDATE; codegen-checked column existence, NOT-NULL, not-also-a-binding, and pg_query_go-validated literal. |
| 16 | Always commit `buf.binpb` and regenerate via `buf build -o buf.binpb` | Eliminates need for a live buf-build dependency at codegen time; CI's `aippatchgen --check` catches drift. |
| 17 | Codecs declared once in yaml under `codecs:`, emitted as a shared `var Codecs` registry in `init.gen.go`, passed to each `Mapping.Validate(Codecs)` | Single source of truth; cross-resource reuse; no per-Mapping duplication; unexported `m.codecs` populated from the subset reachable from `m.Bindings`. |
| 18 | `op.Where` keys must be bound columns; runtime validates before composing SQL | Prevents identifier injection through the map-key surface; values are pgx-parameterized. |
| 19 | `validated` is `atomic.Bool`; `Apply` rejects unvalidated mappings with `Internal` | Defends against `Apply` calls that race ahead of `InitPatches()` under `-race`; production code never interleaves the two but the guard is cheap. |

## Open questions (deferred)

- **JSONB shape (v1)** — for proto sub-messages, marshal via `protojson` or
  accept opaque `[]byte` from the handler? Trade-offs around schema
  evolution.
- **AIP-154 ETag column type (v3)** — `bigint` counter, `uuid` token, or
  per-resource choice? Defer until use case is concrete.
- **Buf plugin migration path (v4)** — when (and if) v4 ships, the yaml
  format remains the source of truth for policy; only mappings move to proto
  annotations. Migration mechanics TBD.
- **Diff API shape (v1)** — for audit logging, is the returned `Diff` a
  `map[string]struct{Before, After any}`, or a typed proto-aware structure?
  Decide when v1 work begins.
