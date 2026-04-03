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
echo ""
echo "GitHub Actions repository variables to configure:"
echo "  GCP_PROJECT_ID: ${PROJECT_ID}"
echo "  GCP_REGION:     ${REGION}"
