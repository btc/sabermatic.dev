# Phase 5: Deployment Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deploy the Drill Go monolith to Google Cloud Run with Terraform IaC, GitHub Actions CI/CD, Secret Manager, and a one-time bootstrap script.

**Architecture:** Single Terraform root module in `terraform/` manages all GCP resources (Cloud Run, Cloud SQL, GCS, Artifact Registry, Secret Manager, IAM, WIF). GitHub Actions has two workflows: `ci.yml` for PR checks (vet, staticcheck, sqlc, tests) and `deploy.yml` for main-branch deploys (build, push to Artifact Registry, `gcloud run deploy`). A `scripts/bootstrap.sh` handles one-time project setup (API enablement, state bucket, service accounts, WIF). Multi-stage Dockerfile produces a distroless container.

**Tech Stack:** Terraform (google provider), GitHub Actions, Docker (distroless), gcloud CLI, Google Secret Manager, Workload Identity Federation

**Spec:** `docs/superpowers/specs/2026-04-03-phase5-deployment-design.md`

---

## File Map

### New files
| File | Responsibility |
|---|---|
| `Dockerfile` | Multi-stage build: Go binary on distroless/static |
| `.dockerignore` | Excludes terraform/, docs/, .git/, etc. from build context |
| `scripts/bootstrap.sh` | One-time GCP setup: APIs, state bucket, service accounts, WIF |
| `terraform/main.tf` | Provider config, GCS backend, API enablement |
| `terraform/variables.tf` | Input variables: project_id, region, environment, github_repo |
| `terraform/artifact_registry.tf` | Docker repository for container images |
| `terraform/cloud_sql.tf` | PostgreSQL 16 instance, database, user |
| `terraform/gcs.tf` | Audio storage bucket |
| `terraform/secrets.tf` | Secret Manager secret shells |
| `terraform/iam.tf` | Service accounts, IAM bindings |
| `terraform/cloud_run.tf` | Cloud Run service, env vars, secret refs, public invoker |
| `terraform/outputs.tf` | Service URL, SQL connection name, registry URL |
| `terraform/terraform.tfvars.example` | Example variable values |
| `.github/workflows/ci.yml` | PR checks: vet, staticcheck, sqlc diff, tests |
| `.github/workflows/deploy.yml` | Main-branch deploy: test, build, push, deploy, smoke test |

### Modified files
None — this phase creates only new files.

---

### Task 1: Dockerfile and .dockerignore

**Files:**
- Create: `Dockerfile`
- Create: `.dockerignore`

- [ ] **Step 1: Create .dockerignore**

```
terraform/
docs/
.git/
.github/
coverage.out
.env*
*.md
scripts/
```

- [ ] **Step 2: Create Dockerfile**

```dockerfile
FROM golang:1.25 AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o drill ./cmd/drill

FROM gcr.io/distroless/static-debian12
COPY --from=builder /app/drill /drill
EXPOSE 8080
ENTRYPOINT ["/drill"]
```

- [ ] **Step 3: Verify the Docker build succeeds locally**

```bash
cd /Users/btc/Projects/src/drill
docker build -t drill:local .
```

Expected: build completes, image created. The image won't *run* without DATABASE_URL etc., but it should build.

- [ ] **Step 4: Verify the image is small and uses distroless**

```bash
docker images drill:local --format '{{.Size}}'
```

Expected: image size under 50MB (Go binary + distroless base).

- [ ] **Step 5: Commit**

```bash
git add Dockerfile .dockerignore
git commit -m "feat(phase5): add Dockerfile and .dockerignore

Multi-stage build: golang:1.25 builder, distroless/static runtime.
CGO_ENABLED=0 for pure Go binary. Migrations embedded via embed.FS."
```

---

### Task 2: Bootstrap script

**Files:**
- Create: `scripts/bootstrap.sh`

- [ ] **Step 1: Create scripts directory**

```bash
mkdir -p /Users/btc/Projects/src/drill/scripts
```

- [ ] **Step 2: Create bootstrap.sh**

```bash
#!/usr/bin/env bash
#
# One-time GCP project bootstrap for Drill.
# Creates: API enablements, Terraform state bucket, service accounts, WIF.
#
# Prerequisites:
#   - gcloud CLI installed and authenticated
#   - A GCP project already created
#
# Usage: ./scripts/bootstrap.sh <PROJECT_ID> <GITHUB_REPO>
#   e.g.: ./scripts/bootstrap.sh drill-prod btc/drill

set -euo pipefail

PROJECT_ID="${1:?Usage: $0 <PROJECT_ID> <GITHUB_REPO>}"
GITHUB_REPO="${2:?Usage: $0 <PROJECT_ID> <GITHUB_REPO>}"
REGION="${3:-us-central1}"

echo "==> Configuring project: ${PROJECT_ID}"
gcloud config set project "${PROJECT_ID}"

# ---- Enable APIs ----
echo "==> Enabling APIs..."
gcloud services enable \
  run.googleapis.com \
  sqladmin.googleapis.com \
  secretmanager.googleapis.com \
  artifactregistry.googleapis.com \
  storage.googleapis.com \
  cloudresourcemanager.googleapis.com \
  iam.googleapis.com \
  iamcredentials.googleapis.com \
  cloudtrace.googleapis.com \
  monitoring.googleapis.com

# ---- Terraform state bucket ----
STATE_BUCKET="${PROJECT_ID}-tfstate"
echo "==> Creating Terraform state bucket: ${STATE_BUCKET}"
gcloud storage buckets create "gs://${STATE_BUCKET}" \
  --location="${REGION}" \
  --uniform-bucket-level-access \
  2>/dev/null || echo "    Bucket already exists, skipping."

gcloud storage buckets update "gs://${STATE_BUCKET}" --versioning

# ---- Service accounts ----
echo "==> Creating terraform service account..."
gcloud iam service-accounts create terraform \
  --display-name="Terraform" \
  2>/dev/null || echo "    SA already exists, skipping."

TERRAFORM_SA="terraform@${PROJECT_ID}.iam.gserviceaccount.com"

for role in roles/editor roles/secretmanager.admin roles/iam.securityAdmin; do
  gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${TERRAFORM_SA}" \
    --role="${role}" \
    --condition=None \
    --quiet
done

echo "==> Creating deployer service account..."
gcloud iam service-accounts create deployer \
  --display-name="CI/CD Deployer" \
  2>/dev/null || echo "    SA already exists, skipping."

DEPLOYER_SA="deployer@${PROJECT_ID}.iam.gserviceaccount.com"

for role in roles/artifactregistry.writer roles/run.developer roles/iam.serviceAccountUser; do
  gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
    --member="serviceAccount:${DEPLOYER_SA}" \
    --role="${role}" \
    --condition=None \
    --quiet
done

# ---- Workload Identity Federation ----
echo "==> Setting up Workload Identity Federation..."
WIF_POOL="github-actions"
WIF_PROVIDER="github"

gcloud iam workload-identity-pools create "${WIF_POOL}" \
  --location="global" \
  --display-name="GitHub Actions" \
  2>/dev/null || echo "    WIF pool already exists, skipping."

gcloud iam workload-identity-pools providers create-oidc "${WIF_PROVIDER}" \
  --location="global" \
  --workload-identity-pool="${WIF_POOL}" \
  --display-name="GitHub" \
  --issuer-uri="https://token.actions.githubusercontent.com" \
  --attribute-mapping="google.subject=assertion.sub,attribute.repository=assertion.repository" \
  --attribute-condition="assertion.repository=='${GITHUB_REPO}'" \
  2>/dev/null || echo "    WIF provider already exists, skipping."

WIF_POOL_ID=$(gcloud iam workload-identity-pools describe "${WIF_POOL}" \
  --location="global" --format="value(name)")

# Bind deployer SA to WIF for CI/CD
gcloud iam service-accounts add-iam-policy-binding "${DEPLOYER_SA}" \
  --role="roles/iam.workloadIdentityUser" \
  --member="principalSet://iam.googleapis.com/${WIF_POOL_ID}/attribute.repository/${GITHUB_REPO}" \
  --quiet

# Bind terraform SA to WIF (for future CI-driven terraform apply)
gcloud iam service-accounts add-iam-policy-binding "${TERRAFORM_SA}" \
  --role="roles/iam.workloadIdentityUser" \
  --member="principalSet://iam.googleapis.com/${WIF_POOL_ID}/attribute.repository/${GITHUB_REPO}" \
  --quiet

# ---- Output ----
echo ""
echo "==> Bootstrap complete!"
echo ""
echo "Terraform state bucket: gs://${STATE_BUCKET}"
echo "Terraform SA:           ${TERRAFORM_SA}"
echo "Deployer SA:            ${DEPLOYER_SA}"
echo "WIF Pool:               ${WIF_POOL_ID}"
echo ""
echo "Next steps:"
echo "  1. cd terraform/"
echo "  2. cp terraform.tfvars.example terraform.tfvars"
echo "  3. Edit terraform.tfvars with your values"
echo "  4. terraform init"
echo "  5. terraform plan"
echo "  6. terraform apply"
echo ""
echo "GitHub Actions secrets to configure:"
echo "  WIF_PROVIDER:  ${WIF_POOL_ID}/providers/${WIF_PROVIDER}"
echo "  DEPLOYER_SA:   ${DEPLOYER_SA}"
```

- [ ] **Step 3: Make it executable**

```bash
chmod +x /Users/btc/Projects/src/drill/scripts/bootstrap.sh
```

- [ ] **Step 4: Verify shell syntax**

```bash
bash -n /Users/btc/Projects/src/drill/scripts/bootstrap.sh
```

Expected: no output (no syntax errors).

- [ ] **Step 5: Commit**

```bash
git add scripts/bootstrap.sh
git commit -m "feat(phase5): add GCP bootstrap script

One-time setup: API enablement, Terraform state bucket, service
accounts (terraform + deployer), Workload Identity Federation for
GitHub Actions keyless auth."
```

---

### Task 3: Terraform — provider, variables, outputs

**Files:**
- Create: `terraform/main.tf`
- Create: `terraform/variables.tf`
- Create: `terraform/outputs.tf`
- Create: `terraform/terraform.tfvars.example`

- [ ] **Step 1: Create terraform directory**

```bash
mkdir -p /Users/btc/Projects/src/drill/terraform
```

- [ ] **Step 2: Create variables.tf**

```hcl
variable "project_id" {
  description = "GCP project ID"
  type        = string
}

variable "region" {
  description = "GCP region for all resources"
  type        = string
  default     = "us-central1"
}

variable "environment" {
  description = "Environment name, used in resource naming"
  type        = string
  default     = "prod"
}

variable "github_repo" {
  description = "GitHub repository in owner/repo format for WIF binding"
  type        = string
}
```

- [ ] **Step 3: Create main.tf**

```hcl
terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }

  backend "gcs" {
    # Bucket name set via -backend-config or terraform init.
    # e.g.: terraform init -backend-config="bucket=drill-prod-tfstate"
    prefix = "terraform/state"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}
```

- [ ] **Step 4: Create outputs.tf (empty for now, populated in later tasks)**

```hcl
output "cloud_run_url" {
  description = "Cloud Run service URL"
  value       = google_cloud_run_v2_service.drill.uri
}

output "sql_connection_name" {
  description = "Cloud SQL connection name for Auth Proxy"
  value       = google_sql_database_instance.drill.connection_name
}

output "artifact_registry_url" {
  description = "Artifact Registry Docker repository URL"
  value       = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.drill.repository_id}"
}
```

- [ ] **Step 5: Create terraform.tfvars.example**

```hcl
project_id  = "drill-prod"
region      = "us-central1"
environment = "prod"
github_repo = "btc/drill"
```

- [ ] **Step 6: Add .gitignore for Terraform**

Create `terraform/.gitignore`:

```
.terraform/
*.tfstate
*.tfstate.backup
*.tfvars
!terraform.tfvars.example
.terraform.lock.hcl
```

- [ ] **Step 7: Commit**

```bash
git add terraform/main.tf terraform/variables.tf terraform/outputs.tf terraform/terraform.tfvars.example terraform/.gitignore
git commit -m "feat(phase5): add Terraform provider, variables, outputs

GCS backend, google provider ~>6.0, variables for project_id, region,
environment, github_repo. Outputs for Cloud Run URL, SQL connection
name, Artifact Registry URL."
```

---

### Task 4: Terraform — Artifact Registry

**Files:**
- Create: `terraform/artifact_registry.tf`

- [ ] **Step 1: Create artifact_registry.tf**

```hcl
resource "google_artifact_registry_repository" "drill" {
  repository_id = "drill"
  location      = var.region
  format        = "DOCKER"
  description   = "Drill container images"
}
```

- [ ] **Step 2: Commit**

```bash
git add terraform/artifact_registry.tf
git commit -m "feat(phase5): add Terraform Artifact Registry resource"
```

---

### Task 5: Terraform — Cloud SQL

**Files:**
- Create: `terraform/cloud_sql.tf`

- [ ] **Step 1: Create cloud_sql.tf**

```hcl
resource "google_sql_database_instance" "drill" {
  name             = "drill-${var.environment}"
  database_version = "POSTGRES_16"
  region           = var.region

  settings {
    tier              = "db-f1-micro"
    disk_size         = 10
    disk_type         = "PD_SSD"
    disk_autoresize   = false
    availability_type = "ZONAL"

    backup_configuration {
      enabled                        = true
      point_in_time_recovery_enabled = false
      start_time                     = "03:00"
      transaction_log_retention_days = 7
      backup_retention_settings {
        retained_backups = 7
      }
    }

    ip_configuration {
      ipv4_enabled = true
    }
  }

  deletion_protection = true
}

resource "google_sql_database" "drill" {
  name     = "drill"
  instance = google_sql_database_instance.drill.name
}

resource "random_password" "db_password" {
  length  = 32
  special = false
}

resource "google_sql_user" "drill" {
  name     = "drill"
  instance = google_sql_database_instance.drill.name
  password = random_password.db_password.result
}
```

- [ ] **Step 2: Add random provider to main.tf**

Add to the `required_providers` block in `terraform/main.tf`:

```hcl
    random = {
      source  = "hashicorp/random"
      version = "~> 3.0"
    }
```

- [ ] **Step 3: Commit**

```bash
git add terraform/cloud_sql.tf terraform/main.tf
git commit -m "feat(phase5): add Terraform Cloud SQL resources

PostgreSQL 16, db-f1-micro, 10GB SSD, daily backups, ZONAL.
Random password for drill user."
```

---

### Task 6: Terraform — GCS bucket

**Files:**
- Create: `terraform/gcs.tf`

- [ ] **Step 1: Create gcs.tf**

```hcl
resource "google_storage_bucket" "audio" {
  name     = "${var.project_id}-audio"
  location = var.region

  uniform_bucket_level_access = true
  storage_class               = "STANDARD"

  force_destroy = false
}
```

- [ ] **Step 2: Commit**

```bash
git add terraform/gcs.tf
git commit -m "feat(phase5): add Terraform GCS bucket for audio storage"
```

---

### Task 7: Terraform — Secret Manager

**Files:**
- Create: `terraform/secrets.tf`

- [ ] **Step 1: Create secrets.tf**

This creates the secret shells. Values are populated manually after `terraform apply`.

```hcl
locals {
  secret_ids = [
    "database-url",
    "auth-token-secret",
    "anthropic-api-key",
    "openai-api-key",
    "mailgun-api-key",
    "mailgun-domain",
    "oauth-google-client-id",
    "oauth-google-client-secret",
    "oauth-github-client-id",
    "oauth-github-client-secret",
  ]
}

resource "google_secret_manager_secret" "secrets" {
  for_each  = toset(local.secret_ids)
  secret_id = each.key

  replication {
    auto {}
  }
}

# Store the generated DB password so the DATABASE_URL secret can reference it.
# The full DATABASE_URL must still be set manually (includes socket path).
resource "google_secret_manager_secret_version" "db_password" {
  secret      = google_secret_manager_secret.secrets["database-url"].id
  secret_data = "postgres://drill:${random_password.db_password.result}@/drill?host=/cloudsql/${google_sql_database_instance.drill.connection_name}"
}
```

- [ ] **Step 2: Commit**

```bash
git add terraform/secrets.tf
git commit -m "feat(phase5): add Terraform Secret Manager resources

10 secret shells, auto-populated DATABASE_URL with Cloud SQL Auth
Proxy socket path. Other values set manually after apply."
```

---

### Task 8: Terraform — IAM and service accounts

**Files:**
- Create: `terraform/iam.tf`

- [ ] **Step 1: Create iam.tf**

```hcl
# ---- Cloud Run runtime service account ----
resource "google_service_account" "drill_app" {
  account_id   = "drill-app"
  display_name = "Drill Cloud Run Runtime"
}

# Cloud SQL Auth Proxy
resource "google_project_iam_member" "app_cloudsql" {
  project = var.project_id
  role    = "roles/cloudsql.client"
  member  = "serviceAccount:${google_service_account.drill_app.email}"
}

# Secret Manager access
resource "google_project_iam_member" "app_secrets" {
  project = var.project_id
  role    = "roles/secretmanager.secretAccessor"
  member  = "serviceAccount:${google_service_account.drill_app.email}"
}

# Cloud Trace
resource "google_project_iam_member" "app_trace" {
  project = var.project_id
  role    = "roles/cloudtrace.agent"
  member  = "serviceAccount:${google_service_account.drill_app.email}"
}

# Cloud Monitoring metrics
resource "google_project_iam_member" "app_monitoring" {
  project = var.project_id
  role    = "roles/monitoring.metricWriter"
  member  = "serviceAccount:${google_service_account.drill_app.email}"
}

# Cloud Logging
resource "google_project_iam_member" "app_logging" {
  project = var.project_id
  role    = "roles/logging.logWriter"
  member  = "serviceAccount:${google_service_account.drill_app.email}"
}

# GCS audio bucket — scoped to bucket, not project-wide
resource "google_storage_bucket_iam_member" "app_audio" {
  bucket = google_storage_bucket.audio.name
  role   = "roles/storage.objectAdmin"
  member = "serviceAccount:${google_service_account.drill_app.email}"
}
```

- [ ] **Step 2: Commit**

```bash
git add terraform/iam.tf
git commit -m "feat(phase5): add Terraform IAM and service accounts

drill-app SA with: cloudsql.client, secretmanager.secretAccessor,
cloudtrace.agent, monitoring.metricWriter, logging.logWriter,
storage.objectAdmin (scoped to audio bucket)."
```

---

### Task 9: Terraform — Cloud Run service

**Files:**
- Create: `terraform/cloud_run.tf`

- [ ] **Step 1: Create cloud_run.tf**

```hcl
resource "google_cloud_run_v2_service" "drill" {
  name     = "drill"
  location = var.region
  ingress  = "INGRESS_TRAFFIC_ALL"

  template {
    service_account = google_service_account.drill_app.email

    scaling {
      min_instance_count = 0
      max_instance_count = 2
    }

    max_instance_request_concurrency = 100
    timeout                          = "3600s"

    volumes {
      name = "cloudsql"
      cloud_sql_instance {
        instances = [google_sql_database_instance.drill.connection_name]
      }
    }

    containers {
      # Placeholder image — first CI deploy will replace this.
      # Use a known-good public image that serves HTTP on 8080.
      image = "us-docker.pkg.dev/cloudrun/container/hello"

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      startup_probe {
        http_get {
          path = "/api/health"
          port = 8080
        }
        initial_delay_seconds = 5
        period_seconds        = 5
        failure_threshold     = 3
        timeout_seconds       = 3
      }

      volume_mounts {
        name       = "cloudsql"
        mount_path = "/cloudsql"
      }

      # ---- Non-secret env vars ----
      env {
        name  = "SERVER_PORT"
        value = "8080"
      }
      env {
        name  = "SERVER_SHUTDOWN_TIMEOUT_SEC"
        value = "30"
      }
      env {
        name  = "BASE_URL"
        value = "https://drill-${var.environment}" # Updated after first deploy with actual URL
      }
      env {
        name  = "OTEL_ENABLED"
        value = "true"
      }
      env {
        name  = "OTEL_EXPORTER"
        value = "google"
      }
      env {
        name  = "OTEL_SAMPLE_RATE"
        value = "1.0"
      }
      env {
        name  = "OTEL_SERVICE_NAME"
        value = "drill"
      }
      env {
        name  = "EMAIL_FROM"
        value = "noreply@drill.dev"
      }

      # ---- Secret env vars ----
      env {
        name = "DATABASE_URL"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["database-url"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "AUTH_TOKEN_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["auth-token-secret"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "ANTHROPIC_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["anthropic-api-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "OPENAI_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["openai-api-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "MAILGUN_API_KEY"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["mailgun-api-key"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "MAILGUN_DOMAIN"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["mailgun-domain"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "OAUTH_GOOGLE_CLIENT_ID"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["oauth-google-client-id"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "OAUTH_GOOGLE_CLIENT_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["oauth-google-client-secret"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "OAUTH_GITHUB_CLIENT_ID"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["oauth-github-client-id"].secret_id
            version = "latest"
          }
        }
      }
      env {
        name = "OAUTH_GITHUB_CLIENT_SECRET"
        value_source {
          secret_key_ref {
            secret  = google_secret_manager_secret.secrets["oauth-github-client-secret"].secret_id
            version = "latest"
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [
      # CI updates the image via gcloud run deploy — don't fight it.
      template[0].containers[0].image,
    ]
  }

  depends_on = [
    google_secret_manager_secret_version.db_password,
  ]
}

# Public access — auth is handled by the application layer.
resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.drill.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "allUsers"
}
```

- [ ] **Step 2: Commit**

```bash
git add terraform/cloud_run.tf
git commit -m "feat(phase5): add Terraform Cloud Run service

1 vCPU, 512MB, min 0 / max 2, concurrency 100, timeout 3600s.
Cloud SQL Auth Proxy sidecar, Secret Manager refs, startup probe
on /api/health. Image managed by CI (lifecycle ignore_changes)."
```

---

### Task 10: Terraform — validate

**Files:** None new — validation of existing terraform files.

- [ ] **Step 1: Run terraform fmt**

```bash
cd /Users/btc/Projects/src/drill/terraform
terraform fmt -check -recursive
```

Expected: no formatting issues, or fix any that appear.

- [ ] **Step 2: Run terraform validate (without init, just syntax)**

```bash
cd /Users/btc/Projects/src/drill/terraform
terraform fmt -recursive
terraform validate 2>&1 || true
```

Note: `terraform validate` requires `terraform init` which needs the GCS bucket to exist. For now, verify there are no HCL syntax errors by checking `terraform fmt` passes cleanly. A full `terraform init && terraform validate` will be done when the bootstrap script is actually run.

- [ ] **Step 3: Commit any formatting fixes**

```bash
cd /Users/btc/Projects/src/drill
git add terraform/
git diff --cached --quiet || git commit -m "style(phase5): terraform fmt"
```

---

### Task 11: GitHub Actions — CI workflow

**Files:**
- Create: `.github/workflows/ci.yml`

- [ ] **Step 1: Create .github/workflows directory**

```bash
mkdir -p /Users/btc/Projects/src/drill/.github/workflows
```

- [ ] **Step 2: Create ci.yml**

```yaml
name: CI

on:
  pull_request:
    branches: [main]

permissions:
  contents: read

jobs:
  check:
    runs-on: ubuntu-latest

    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: "1.25.x"

      - name: Install tools
        run: |
          go install honnef.co/go/tools/cmd/staticcheck@latest
          go install github.com/sqlc-dev/sqlc/cmd/sqlc@latest

      - name: Vet
        run: go vet ./...

      - name: Staticcheck
        run: staticcheck ./...

      - name: Sqlc diff
        run: sqlc diff

      - name: Test
        run: go test ./... -race -count=1 -timeout=300s
```

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "feat(phase5): add GitHub Actions CI workflow

PR checks: go vet, staticcheck, sqlc diff, go test -race.
Testcontainers handles Postgres (Docker available on ubuntu-latest)."
```

---

### Task 12: GitHub Actions — deploy workflow

**Files:**
- Create: `.github/workflows/deploy.yml`

- [ ] **Step 1: Create deploy.yml**

```yaml
name: Deploy

on:
  push:
    branches: [main]

permissions:
  contents: read
  id-token: write  # Required for WIF

env:
  PROJECT_ID: ${{ vars.GCP_PROJECT_ID }}
  REGION: ${{ vars.GCP_REGION }}
  SERVICE_NAME: drill
  IMAGE_REPO: ${{ vars.GCP_REGION }}-docker.pkg.dev/${{ vars.GCP_PROJECT_ID }}/drill/drill

jobs:
  deploy:
    runs-on: ubuntu-latest

    steps:
      - name: Checkout
        uses: actions/checkout@v4

      - name: Authenticate to Google Cloud
        uses: google-github-actions/auth@v2
        with:
          workload_identity_provider: ${{ secrets.WIF_PROVIDER }}
          service_account: ${{ secrets.DEPLOYER_SA }}

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version: "1.25.x"

      - name: Test
        run: go test ./... -race -count=1 -timeout=300s

      - name: Setup gcloud
        uses: google-github-actions/setup-gcloud@v2

      - name: Configure Docker for Artifact Registry
        run: gcloud auth configure-docker ${{ env.REGION }}-docker.pkg.dev --quiet

      - name: Build container image
        run: docker build -t ${{ env.IMAGE_REPO }}:${{ github.sha }} .

      - name: Push container image
        run: docker push ${{ env.IMAGE_REPO }}:${{ github.sha }}

      - name: Deploy to Cloud Run
        run: |
          gcloud run deploy ${{ env.SERVICE_NAME }} \
            --image=${{ env.IMAGE_REPO }}:${{ github.sha }} \
            --region=${{ env.REGION }} \
            --quiet

      - name: Smoke test
        run: |
          SERVICE_URL=$(gcloud run services describe ${{ env.SERVICE_NAME }} \
            --region=${{ env.REGION }} \
            --format='value(status.url)')
          echo "Service URL: ${SERVICE_URL}"

          # Wait for the new revision to be serving
          sleep 10

          STATUS=$(curl -s -o /dev/null -w '%{http_code}' "${SERVICE_URL}/api/health")
          if [ "${STATUS}" != "200" ]; then
            echo "Smoke test failed: /api/health returned ${STATUS}"
            exit 1
          fi
          echo "Smoke test passed: /api/health returned 200"
```

- [ ] **Step 2: Commit**

```bash
git add .github/workflows/deploy.yml
git commit -m "feat(phase5): add GitHub Actions deploy workflow

On push to main: test, build, push to Artifact Registry, gcloud run
deploy, smoke test /api/health. WIF for keyless GCP auth.
Image tagged with full git SHA for traceability."
```

---

### Task 13: Final validation and documentation

**Files:** None new — verify everything is in order.

- [ ] **Step 1: Verify all files exist**

```bash
cd /Users/btc/Projects/src/drill
ls -la Dockerfile .dockerignore
ls scripts/bootstrap.sh
ls terraform/main.tf terraform/variables.tf terraform/outputs.tf terraform/artifact_registry.tf terraform/cloud_sql.tf terraform/gcs.tf terraform/secrets.tf terraform/iam.tf terraform/cloud_run.tf terraform/terraform.tfvars.example terraform/.gitignore
ls .github/workflows/ci.yml .github/workflows/deploy.yml
```

Expected: all files exist, no errors.

- [ ] **Step 2: Verify Docker build still works with all new files**

```bash
cd /Users/btc/Projects/src/drill
docker build -t drill:local .
```

Expected: build succeeds. The .dockerignore should exclude terraform/, scripts/, .github/, etc. from the build context.

- [ ] **Step 3: Verify Go tests still pass**

```bash
cd /Users/btc/Projects/src/drill
go test ./... -race -count=1
```

Expected: all tests pass. This phase doesn't modify Go code, so no test changes expected.

- [ ] **Step 4: Review git log**

```bash
git log --oneline -15
```

Expected: series of phase5 commits on top of existing main branch.
