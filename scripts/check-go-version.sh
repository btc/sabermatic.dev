#!/usr/bin/env bash
set -euo pipefail

# Check that go.mod and Dockerfile agree on the Go major.minor version.
# Run from the repo root, or pass the repo root as $1.

ROOT="${1:-.}"

MOD=$(grep '^go ' "$ROOT/go.mod" | awk '{print $2}' | cut -d. -f1,2)
DOCKER=$(grep 'FROM golang:' "$ROOT/Dockerfile" | head -1 | sed 's/.*golang:\([0-9]*\.[0-9]*\).*/\1/')

echo "go.mod:      ${MOD}"
echo "Dockerfile:  ${DOCKER}"

if [ "$MOD" != "$DOCKER" ]; then
  echo ""
  echo "ERROR: Go version mismatch — go.mod ($MOD) != Dockerfile ($DOCKER)"
  exit 1
fi

echo "OK: versions match."
