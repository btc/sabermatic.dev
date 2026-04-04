A mandatory review gate rule: after every subagent task that modifies code, a separate review agent must read the actual files and verify against the task spec before commit. No exceptions for "trivial" tasks.

A browser-after-each-commit rule: after any UI-affecting file is committed, load the page and verify before moving to the next task.

An agent sequencing rule: for tightly-coupled files, agents run sequentially instead of in parallel, with later agents receiving the committed output of earlier ones.

Backend list endpoints must never return nil slices. Go's `json.Marshal(nil)` produces `null`, not `[]`, which breaks frontend code expecting arrays. Always coerce nil to an empty slice in the backend method (not the handler) before returning.
