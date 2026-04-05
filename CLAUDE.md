# Code Conventions

Backend list endpoints must never return nil slices. Go's `json.Marshal(nil)` produces `null`, not `[]`. Coerce in the backend method for REST, in db-to-proto converters for ConnectRPC.

Never panic at init time. Propagate errors explicitly up to main.

Test user creation goes through `backendtest.SeedUser` (handler tests) or `seedUser` (backend internal tests), both calling `b.Signup()`. Never create users via raw SQL in tests — test state must match production state. To test unusual states (expired grants, zero balance), start from real state and mutate.

Shared test helper packages follow the `httptest` convention: `internal/jobs/jobtest/`, `internal/backendtest/`. Don't duplicate helpers across packages.

# API & Proto

Proto is the API contract. After editing `.proto` files, run `buf generate` and commit generated code in `internal/pb/` and `web/src/pb/`. Never hand-edit generated files.

ConnectRPC services follow Google AIPs where practical. Standard methods use AIP naming, pagination (AIP-158), error codes (AIP-193). Custom methods use AIP-136. Skip resource names (AIP-122) and field behavior annotations (AIP-203).

`repeated` proto fields must map to empty slices, not nil. Always return initialized slices from db-to-proto converters.

Update methods use field masks for partial updates (AIP-134).

ConnectRPC handlers live in `internal/rpc/{service}/`. REST handlers in `internal/handler/` are being incrementally migrated. New endpoints should be ConnectRPC services.

Verify library API signatures against installed versions before writing plan code blocks.

# Process

Practice red-green TDD when investigating bugs. Write a failing test first, then fix.

After writing a spec, plan, or implementation, run Opus subagent review — up to 3 rounds. Fix ALL findings (high, medium, low, nit). Escalate disagreements to user.

After every subagent task that modifies code, a separate review agent must read actual files and verify against spec. No exceptions.

For tightly-coupled files, agents run sequentially, with later agents receiving committed output of earlier ones.

After any UI-affecting commit, load the page in the browser and verify before moving on.
