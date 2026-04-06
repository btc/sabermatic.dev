# Cloud Bootstrap Script — Design Spec

**Date**: 2026-04-06
**Status**: Draft
**Supersedes**: `scripts/bootstrap.sh` (will be deleted)
**Purpose**: Interactive Python script that walks a human operator through first-time GCP deployment of Drill/Sabermatic, from zero to a running Cloud Run service.

---

## 1. Overview

`scripts/cloud_bootstrap.py` is a single-file, stdlib-only Python 3 script that replaces the existing `scripts/bootstrap.sh`. It runs 9 phases sequentially, with confirmation prompts between each. A JSON state file (`.cloud_bootstrap_state`) tracks completed phases, enabling resume after interruption.

The script is designed for a solo operator deploying from a laptop. It is not a CI tool.

---

## 2. Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Language | Python 3 (stdlib only) | Better error handling, state management, and prompting than bash. No pip deps needed. |
| State tracking | `.cloud_bootstrap_state` JSON file | Enables resume. Tracks completed phases and sub-steps (e.g. individual secrets). |
| Replaces | `scripts/bootstrap.sh` | New script covers the full flow (deps through first deploy). No reason to maintain both. |
| Project ID | Configurable, defaults to `sabermatic-prod` | User is prompted at start. Stored in state for resume. |
| Region | Configurable, defaults to `us-central1` | Prompted once, stored in state. |
| Secret handling | Interactive prompts with helper links | Secrets are pasted at the terminal, piped directly to `gcloud secrets versions add --data-file=-`. Never written to disk. |
| Docker build | Local `docker build` + push to Artifact Registry | Simplest first deploy. CI/CD handles subsequent deploys. |

---

## 3. Prerequisites

The script checks for these before starting:
- `python3` (3.10+)
- `gcloud`
- `terraform`
- `docker`
- `go`
- `node` / `npm`

Missing tools: the script prints install instructions (brew commands) and exits. It does not auto-install — that's `make deps` territory and the user should run it explicitly.

---

## 4. Phases

### Phase 1 — Check Dependencies

Check that all required CLI tools are available via `shutil.which()`. For each missing tool, print the install command. If any are missing, exit with a message like:

```
Missing tools:
  terraform  →  brew install hashicorp/tap/terraform
  docker     →  brew install --cask docker

Install these and re-run the script.
```

No auto-install. The user runs the commands themselves.

### Phase 2 — GCP Auth

1. Run `gcloud auth list --format=json` and check for an active account.
2. If no active account, prompt: "No active gcloud account. Press Enter to open browser login..."
3. Run `gcloud auth login` (interactive, opens browser).
4. Run `gcloud auth application-default login` (needed for Terraform).
5. Verify auth succeeded.

### Phase 3 — Create GCP Project + Enable Billing

1. Prompt for project ID (default: `sabermatic-prod`).
2. Run `gcloud projects create <project-id> --name="Sabermatic"`.
   - If project already exists, note it and continue.
3. Run `gcloud config set project <project-id>`.
4. Print billing link and wait for confirmation:
   ```
   Billing must be enabled before we can create resources.

   Open this URL and link a billing account:
     https://console.cloud.google.com/billing/linkedaccount?project=sabermatic-prod

   Press Enter when billing is enabled...
   ```
5. Verify billing is enabled via `gcloud billing projects describe <project-id> --format=json`.

### Phase 4 — Enable APIs + Bootstrap Infrastructure

Consolidates what the old `scripts/bootstrap.sh` did:

1. Enable GCP APIs:
   - `run.googleapis.com`
   - `sqladmin.googleapis.com`
   - `secretmanager.googleapis.com`
   - `artifactregistry.googleapis.com`
   - `storage.googleapis.com`
   - `cloudresourcemanager.googleapis.com`
   - `iam.googleapis.com`
   - `iamcredentials.googleapis.com`
   - `cloudtrace.googleapis.com`
   - `monitoring.googleapis.com`
   - `logging.googleapis.com`
2. Create Terraform state bucket (`<project-id>-tfstate`) with versioning.
3. Create service accounts:
   - `terraform@<project-id>` with roles: `roles/editor`, `roles/secretmanager.admin`, `roles/iam.securityAdmin`
   - `deployer@<project-id>` with roles: `roles/artifactregistry.writer`, `roles/run.developer`, `roles/iam.serviceAccountUser`
4. Set up Workload Identity Federation for GitHub Actions (pool: `github-actions`, provider: `github`, bound to repo).

All commands use `2>/dev/null || echo "already exists"` pattern for idempotency.

### Phase 5 — Terraform Init + Apply

1. Write `terraform/terraform.tfvars` from state values:
   ```hcl
   project_id  = "sabermatic-prod"
   region      = "us-central1"
   environment = "prod"
   github_repo = "btc/drill"
   ```
2. Run `terraform init -backend-config="bucket=<project-id>-tfstate"` in `terraform/`.
3. Run `terraform plan` and display output.
4. Prompt: "Apply this plan? [y/N]"
5. Run `terraform apply -auto-approve` (user already confirmed).
6. Capture outputs: `cloud_run_url`, `sql_connection_name`, `artifact_registry_url`.

Note: Cloud SQL provisioning takes 5–10 minutes. The script prints a message and waits.

### Phase 6 — Populate Secrets

For each secret, the script either auto-generates or prompts:

| Secret | Method |
|---|---|
| `database-url` | Already set by Terraform — skip |
| `auth-token-secret` | Auto-generate: `openssl rand -hex 32` |
| `anthropic-api-key` | Prompt user to paste |
| `openai-api-key` | Prompt user to paste |
| `mailgun-api-key` | Prompt with link: `https://app.mailgun.com/settings/api_security` |
| `mailgun-domain` | Prompt with link (same page) |
| `oauth-google-client-id` | Prompt with link: `https://console.cloud.google.com/apis/credentials?project=<project-id>` |
| `oauth-google-client-secret` | Prompt (same page) |
| `oauth-github-client-id` | Prompt with link: `https://github.com/settings/developers` |
| `oauth-github-client-secret` | Prompt (same page) |
| `stripe-secret-key` | Prompt with link: `https://dashboard.stripe.com/apikeys` |
| `stripe-webhook-secret` | Auto-set in Phase 7 after webhook registration — skip here |

Each secret is tracked individually in state (e.g. `secrets.anthropic-api-key: true`). Secrets can be skipped with "s" and populated on a later re-run.

Input uses `getpass.getpass()` so values are not echoed to terminal.

Secret values are piped to `gcloud secrets versions add <name> --data-file=-` via subprocess stdin. Never written to disk.

### Phase 7 — Stripe Setup

Creates products, prices, and webhook endpoint via the Stripe API (`urllib.request` against `https://api.stripe.com/v1/`). Requires the Stripe secret key collected in Phase 6.

#### 7a. Create Products & Prices

The script displays cost context before prompting:

```
Stripe Product Setup
────────────────────
Your estimated cost to serve is ~$0.01-0.025 per minute
(Claude Sonnet interviewer, Whisper STT, OpenAI TTS, Opus evaluation).

Comparable platforms charge $30-100/month.
```

Then walks through each product:

```
[1/4] Pro Monthly Subscription
  600 min/month, coach access, full educator analysis
  Price in cents (e.g. 3900 for $39/mo): ___

[2/4] 120-Minute Pack
  Price in cents (e.g. 1499 for $14.99): ___

[3/4] 300-Minute Pack
  Price in cents (e.g. 2999 for $29.99): ___

[4/4] 600-Minute Pack
  Price in cents (e.g. 4999 for $49.99): ___
```

For each product, the script:
1. `POST /v1/products` — creates the product (name, description)
2. `POST /v1/prices` — creates the price (amount, currency, recurring for subscription)
3. Stores the returned `price_id` in state

#### 7b. Register Webhook Endpoint

After the Cloud Run URL is known (from Terraform output):

1. `POST /v1/webhook_endpoints` with:
   - `url`: `<cloud_run_url>/api/webhooks/stripe`
   - `enabled_events[]`: `checkout.session.completed`, `invoice.paid`, `customer.subscription.deleted`, `customer.subscription.updated`
2. Extract `secret` from the response (the webhook signing secret)
3. Store in Secret Manager as `stripe-webhook-secret`

#### 7c. Store Price IDs as Env Vars

The 4 price IDs are plain env vars (not secrets). The script updates the Cloud Run service:

```
gcloud run services update drill \
  --update-env-vars STRIPE_PRO_PRICE_ID=price_xxx,STRIPE_PACK_120_PRICE_ID=price_yyy,...
```

Each sub-step is tracked individually in state for resume.

### Phase 8 — Build & Push Container

1. Get Artifact Registry URL from Terraform output.
2. Run `gcloud auth configure-docker <region>-docker.pkg.dev`.
3. Run `docker build -t <ar-url>/drill:latest .` from project root.
4. Run `docker push <ar-url>/drill:latest`.

### Phase 9 — Deploy to Cloud Run

1. Run `gcloud run deploy drill --image <ar-url>/drill:latest --region <region>`.
2. Fetch and display the service URL.
3. Print summary:
   ```
   Deploy complete!

   Cloud Run URL:  https://drill-xxxxx.a.run.app
   Cloud SQL:      sabermatic-prod:us-central1:drill-prod
   Artifact Reg:   us-central1-docker.pkg.dev/sabermatic-prod/drill

   Stripe:
     Pro subscription: price_xxx ($39.00/mo)
     120-min pack:     price_yyy ($14.99)
     300-min pack:     price_zzz ($29.99)
     600-min pack:     price_www ($49.99)
     Webhook:          <cloud_run_url>/api/webhooks/stripe

   Next steps:
     • Point sabermatic.dev DNS to the Cloud Run URL
     • Set up GitHub Actions secrets for CI/CD (see terraform output)
     • Update BASE_URL in cloud_run.tf to https://sabermatic.dev
   ```

---

## 5. State File Format

`.cloud_bootstrap_state` (gitignored):

```json
{
  "project_id": "sabermatic-prod",
  "region": "us-central1",
  "github_repo": "btc/drill",
  "phases": {
    "deps": true,
    "auth": true,
    "project": true,
    "bootstrap": true,
    "terraform": true,
    "secrets": {
      "auth-token-secret": true,
      "anthropic-api-key": true,
      "openai-api-key": false
    },
    "stripe": {
      "products": true,
      "webhook": false,
      "env_vars": false
    },
    "build": false,
    "deploy": false
  },
  "outputs": {
    "cloud_run_url": "https://drill-xxxxx.a.run.app",
    "sql_connection_name": "sabermatic-prod:us-central1:drill-prod",
    "artifact_registry_url": "us-central1-docker.pkg.dev/sabermatic-prod/drill"
  }
}
```

---

## 6. UX Details

- **Colors**: Green for success, yellow for prompts/warnings, red for errors, cyan for links. Via ANSI codes, no deps.
- **Prompts**: `input()` for confirmations, `getpass.getpass()` for secrets.
- **Progress**: Phase header printed before each phase: `[3/9] Create GCP Project`
- **Idempotency**: All `gcloud` create commands tolerate "already exists". Terraform is inherently idempotent. Secrets check for existing non-placeholder versions before prompting.
- **Ctrl-C**: State is saved after each completed phase/sub-step. Clean exit message.

---

## 7. File Changes

| Action | Path | Notes |
|---|---|---|
| Create | `scripts/cloud_bootstrap.py` | Main script |
| Delete | `scripts/bootstrap.sh` | Superseded |
| Update | `.gitignore` | Add `.cloud_bootstrap_state` |
| Update | `terraform/secrets.tf` | Add `stripe-secret-key`, `stripe-webhook-secret` to `secret_ids` |
| Update | `terraform/cloud_run.tf` | Add `STRIPE_SECRET_KEY` and `STRIPE_WEBHOOK_SECRET` as secret env refs; add `STRIPE_PRO_PRICE_ID`, `STRIPE_PACK_120_PRICE_ID`, `STRIPE_PACK_300_PRICE_ID`, `STRIPE_PACK_600_PRICE_ID` as plain env vars (initially empty, set by script via `gcloud run services update`) |
| Update | `.env.example` | Add Stripe env vars for local dev parity |

---

## 8. Out of Scope

- Custom domain mapping (DNS setup on Dynadot is manual)
- GitHub Actions CI/CD workflow files (separate task)
- SSL certificate provisioning (Cloud Run handles this automatically for custom domains)
- Subsequent deploys (CI/CD handles those)
