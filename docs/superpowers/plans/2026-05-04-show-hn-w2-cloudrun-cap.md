# Show HN W2 — Cloud Run instance cap implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bump Cloud Run `max_instance_count` from 2 to 3 to give Show HN traffic room to scale.

**Architecture:** Conceptually a one-line Terraform edit, but `terraform/cloud_run.tf` has `lifecycle.ignore_changes = [scaling, ...]` (added in commit `0159e45` to suppress GCP provider drift on `manual_instance_count`). That means **`terraform apply` will silently swallow the `2 → 3` change** — the Terraform diff is the documented intent only. The actual bump must be applied out-of-band via `gcloud run services update`. The Terraform value is updated to keep the documented intent in sync with reality. Bounded by W1's pool sizing: 16 conns × 3 instances = 48 conns < 50 max_connections. **Hard prerequisite:** W1 must be deployed and running stably before this applies, otherwise the cluster will exceed the Cloud SQL connection cap on the first scale-up.

**Tech Stack:** Terraform (documentation), Google Cloud Run, gcloud CLI.

**Spec:** `docs/superpowers/specs/2026-05-04-show-hn-prep-design.md` (Workstream 2).

---

## File structure

| File | Change |
|---|---|
| `terraform/cloud_run.tf` | Modify line 10: `max_instance_count = 2` → `3` |

No tests; verification is via `terraform plan` output and post-deploy capacity check.

---

## Tasks

### Task 1: Pre-flight gating checks

**Files:** none — read-only verification.

- [ ] **Step 1: Confirm W1 is deployed**

Run (use the `google_cloud_run_v2_service` schema — `template.containers[0].env`, not `spec.template.spec.containers[0].env`):

```bash
gcloud run services describe sabermatic \
  --region=$(gcloud config get-value run/region) \
  --format='value(template.containers[0].env)' | tr ';' '\n' | grep -i DATABASE_MAX_POOL_SIZE
```

Expected: empty output (we don't override; pool defaults to 16 from the new code) OR explicit `DATABASE_MAX_POOL_SIZE=16`. If you see `=80` or any value > 16, **STOP** — W1 is not yet on the deployed revision; ship that first.

- [ ] **Step 2: Confirm prod max_connections is what W1 assumed**

Run against prod Cloud SQL (via `cloud-sql-proxy` or `gcloud sql connect`):

```sql
SHOW max_connections;
```

Expected: `50`. If different, **STOP** — re-derive the pool/cap math in W1's spec section before applying this workstream.

- [ ] **Step 3: Check current Cloud SQL connection metric**

Open the Cloud SQL console for the prod instance and look at the "Active connections" metric over the last 24h. Expected: well under 16 in steady state (since prod runs on 1 instance pre-W2). If it's already pinning at 16, the AI workers are saturated and bumping `max_instance_count` will multiply the problem — investigate before proceeding.

---

### Task 2: Edit terraform

**Files:**
- Modify: `terraform/cloud_run.tf` (line 10)

- [ ] **Step 1: Apply the edit**

In `terraform/cloud_run.tf`, change line 10 from:

```hcl
    max_instance_count = 2
```

to:

```hcl
    max_instance_count = 3
```

(That's the entire change. Do not modify `max_instance_request_concurrency` (line 13, currently 100) or any other field.)

- [ ] **Step 2: Verify the diff**

```bash
git diff terraform/cloud_run.tf
```

Expected exact output:

```diff
diff --git a/terraform/cloud_run.tf b/terraform/cloud_run.tf
index <hash>..<hash> 100644
--- a/terraform/cloud_run.tf
+++ b/terraform/cloud_run.tf
@@ -7,7 +7,7 @@ resource "google_cloud_run_v2_service" "sabermatic" {
   ...
-    max_instance_count = 2
+    max_instance_count = 3
   ...
```

If any other lines are in the diff, revert (`git checkout terraform/cloud_run.tf`) and re-apply only the targeted line.

---

### Task 3: Confirm `terraform plan` will NOT apply this change (sanity check)

**Files:** none modified — verification.

- [ ] **Step 1: Run terraform plan**

```bash
cd terraform && terraform plan
```

Expected: **no change** to `google_cloud_run_v2_service.sabermatic`'s `scaling` block (the diff is suppressed by `lifecycle.ignore_changes = [..., scaling, ...]` at line 233). If terraform plan shows the scaling change, the `ignore_changes` was modified upstream — investigate before applying.

This is the opposite of a normal "verify your change is in the plan" check: we expect the plan to ignore the change because gcloud is the application channel for scaling settings.

---

### Task 4: Apply via gcloud (NOT terraform apply)

**Files:** none — live infrastructure change via gcloud.

- [ ] **Step 1: Apply the cap bump**

```bash
gcloud run services update sabermatic \
  --region=$(gcloud config get-value run/region) \
  --max-instances=3
```

Expected: `Service [sabermatic] revision [...] is deploying...` then `Done.` in ~10–30 seconds. Cloud Run does NOT shift traffic or cold-start; this is a metadata-only revision update.

Do NOT run `terraform apply` for this change — it is a no-op (per Task 3). Running it has no effect but is misleading and may produce surprising drift output if other unrelated resources happen to have pending changes.

- [ ] **Step 2: Verify the new ceiling is live**

```bash
gcloud run services describe sabermatic \
  --region=$(gcloud config get-value run/region) \
  --format='value(template.scaling.maxInstanceCount)'
```

Expected: `3`. (The legacy `--format='value(spec.template.metadata.annotations.run.googleapis.com/maxScale)'` queries the v1 Service API schema, which does not match `google_cloud_run_v2_service` and silently returns empty — DO NOT use it.)

---

### Task 5: Commit

**Files:** `terraform/cloud_run.tf` already modified.

- [ ] **Step 1: Commit**

```bash
git add terraform/cloud_run.tf
git commit -m "infra(cloud_run): bump max_instance_count to 3 for show hn capacity"
```

- [ ] **Step 2: Confirm clean tree**

```bash
git status
```

Expected: working tree clean.

---

### Task 6: Post-deploy capacity check

**Files:** none — observation only.

- [ ] **Step 1: Generate a small load to confirm no immediate connection issue**

Hit the landing page from a few different IPs/devices (or use a load tester at low RPS, e.g., 10 req/sec for 60 seconds). Cloud Run typically only spins up additional instances when concurrent requests exceed `max_instance_request_concurrency` per instance — at light load you'll stay on 1 instance and not exercise the new ceiling.

- [ ] **Step 2: Watch Cloud SQL "current connections"**

Open Cloud SQL console → metrics → "PostgreSQL connections". Expected: should not approach 48 during normal traffic. If you can briefly trigger a 2nd or 3rd instance via load test, confirm the cluster total stays ≤ 48.

- [ ] **Step 3: Watch Cloud Run instance count**

Cloud Run console → Sabermatic service → Metrics → "Container instance count". Confirm the ceiling is 3 (not 2).

---

## Done

W2 is complete. Cloud Run can now scale to 3 instances, total cluster capacity ~300 concurrent in-flight requests, total DB connection ceiling 48 of 50.

**Rollback:** revert `terraform/cloud_run.tf` change and `terraform apply` to return to `max_instance_count = 2`. No code change. (Useful as an emergency lever if connection saturation appears during the spike.)
