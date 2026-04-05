A mandatory review gate rule: after every subagent task that modifies code, a separate review agent must read the actual files and verify against the task spec before commit. No exceptions for "trivial" tasks.

A browser-after-each-commit rule: after any UI-affecting file is committed, load the page and verify before moving to the next task.

An agent sequencing rule: for tightly-coupled files, agents run sequentially instead of in parallel, with later agents receiving the committed output of earlier ones.

Backend list endpoints must never return nil slices. Go's `json.Marshal(nil)` produces `null`, not `[]`, which breaks frontend code expecting arrays. Always coerce nil to an empty slice in the backend method (not the handler) before returning.

Practice red green TDD when investigating issues. Before jumping to implementation and fixes, create a test that detects the bug. Then prompt user to allow you to fix the bug and thus green the test.

After writing a spec, plan, or code implementation, let an Opus subagent review. Do up to 3 rounds of review. Fix ALL valid findings whether high, medium, low, or nit. If you disagree with agent review finding, please escalate to the user. If a round of review surfaces no findings, you may end the reviews early. After reviews are complete, please provide a summary of findings and fixes categorized high, medium, low, nit.

Proto is the API contract. After editing .proto files, run `buf generate` and commit the generated code in `internal/pb/` and `web/src/pb/`. Never hand-edit generated files.

ConnectRPC services follow Google AIPs where practical. Standard methods (Get, List, Create, Update, Delete) use AIP naming, pagination (AIP-158), and error code conventions (AIP-193). Custom methods (retry, checkout) use AIP-136 naming. Skip resource names (AIP-122) and field behavior annotations (AIP-203) — we use flat UUIDs and defer annotation verbosity.

`repeated` fields in proto responses must map to empty slices, not nil. Same principle as the existing nil-slice rule, extended to proto conversion: always return an initialized slice from db-to-proto converters.

ConnectRPC service handlers live in `internal/rpc/{service}/`. REST handlers in `internal/handler/` are being incrementally migrated. New API endpoints should be implemented as ConnectRPC services, not REST handlers.

Never panic at init time. Propagate errors explicitly up to main. This applies to `rpc.Register`, `handler.NewHandler`, and any startup-path function.

Never use `git add -A` or `git add .` when untracked files exist that shouldn't be committed. Stage specific files by name. This is especially dangerous during rebase conflict resolution where untracked files get swept in silently.

Verify library API signatures against installed versions before writing implementation code in plans. Plan code blocks that haven't been compiled against real type definitions can be wrong (e.g., `credentials` option that doesn't exist, single-return function that actually returns error).

Update methods should use field masks for partial updates. AIP compliance (AIP-134).
