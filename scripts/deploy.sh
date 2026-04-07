#!/usr/bin/env bash
set -euo pipefail

PROJECT=sabermatic-production
REGION=us-central1
SERVICE=sabermatic
IMAGE="us-central1-docker.pkg.dev/${PROJECT}/${SERVICE}/${SERVICE}:latest"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "==> Configuring Docker auth"
gcloud auth configure-docker "${REGION}-docker.pkg.dev" --quiet

echo "==> Building image (linux/amd64)"
docker build --platform linux/amd64 -t "$IMAGE" "$ROOT"

echo "==> Pushing to Artifact Registry"
docker push "$IMAGE"

echo "==> Deploying to Cloud Run"
gcloud run deploy "$SERVICE" \
  --image "$IMAGE" \
  --region "$REGION" \
  --project "$PROJECT"

echo "==> Done"
gcloud run services describe "$SERVICE" \
  --region "$REGION" \
  --project "$PROJECT" \
  --format="value(status.url)"
