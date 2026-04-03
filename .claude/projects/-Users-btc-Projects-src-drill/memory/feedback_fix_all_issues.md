---
name: Fix all review issues
description: Never defer review findings — fix HIGHs, MEDIUMs, LOWs, and NITs immediately
type: feedback
---

Fix all review issues found, regardless of severity. HIGH, MEDIUM, LOW, and NIT all get fixed. Do not defer or label issues as "not blocking." If a reviewer raised it, fix it.

**Why:** The user expects issues to be addressed when found, not deferred. Deferring creates tech debt and signals lack of discipline.

**How to apply:** During code reviews, fix every issue in the same pass. Only skip if the issue is genuinely wrong (reviewer made an error), in which case explain why.
