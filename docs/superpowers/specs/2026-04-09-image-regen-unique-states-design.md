# Image Regen: Fix River Unique States for generate_question_image

## Problem

`GenerateQuestionImageInsertOpts` uses `river.UniqueOpts{ByArgs: true}` without specifying
`ByState`. River's default uniqueness states include `completed`, meaning a successfully-completed
job permanently holds the unique slot for its `question_id`. After an image is generated, no
new job can ever be inserted for the same question — even if `image_url` is later set to NULL.

This was discovered when manually nulling `image_url` on the "News Feed" question and observing
that the sweep (running every minute, filtering `WHERE image_url IS NULL`) never re-queued a job.
Confirmed via: `unique_states = 11110101` on the completed River job row, where the `completed`
bit is set, causing the partial unique index to include the completed row indefinitely.

## Fix

Add explicit `ByState` to `GenerateQuestionImageInsertOpts` that excludes `completed` (and
`cancelled`/`discarded`, which River already excludes by default). Uniqueness only applies while
a job is actively in-flight.

```go
UniqueOpts: river.UniqueOpts{
    ByArgs: true,
    ByState: []rivertype.JobState{
        rivertype.JobStateAvailable,
        rivertype.JobStatePending,
        rivertype.JobStateRetryable,
        rivertype.JobStateRunning,
        rivertype.JobStateScheduled,
    },
},
```

This is the `UniqueOptsByStateDefault()` set minus `completed`.

## Behaviour After Fix

- **Duplicate prevention still works**: a job already queued/running/retryable for a question
  blocks new inserts — the sweep still can't flood the queue.
- **Regen works**: once a job completes, the unique slot is released. Setting `image_url = NULL`
  is sufficient; the next sweep cycle (≤60s) re-queues automatically.
- **Idempotency guard unchanged**: the worker's `if question.ImageUrl.Valid { return nil }` check
  still prevents wasted API calls if two jobs somehow race.

## Admin Regen Workflow (Going Forward)

Set `image_url = NULL` on the target question. The sweep handles the rest.

```sql
UPDATE questions SET image_url = NULL, updated_at = NOW() WHERE id = '<uuid>';
```

No River job manipulation needed.

## Migration Note

Existing completed `river_job` rows were inserted with the old `unique_states` bitmask (includes
`completed`). Those rows continue to block until manually deleted or until River prunes them via
`CompletedJobRetentionPeriod`. For any one-off regen before those rows expire, delete the
completed row first:

```sql
DELETE FROM river_job
WHERE kind = 'generate_question_image'
  AND args->>'question_id' = '<uuid>'
  AND state = 'completed';
```
