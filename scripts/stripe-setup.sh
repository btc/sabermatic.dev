#!/usr/bin/env bash
# Upsert STRIPE_WEBHOOK_SECRET into .env using the Stripe CLI's device-local
# session secret. Idempotent: re-runnable after `stripe logout && stripe login`.
set -euo pipefail

if ! command -v stripe >/dev/null 2>&1; then
    echo "error: stripe CLI not installed (brew install stripe/stripe-cli/stripe)" >&2
    exit 1
fi

if ! stripe config --list >/dev/null 2>&1; then
    echo "stripe CLI not configured; launching login..."
    stripe login
fi

secret="$(stripe listen --print-secret)"
if [[ -z "$secret" ]]; then
    echo "error: stripe listen --print-secret returned empty" >&2
    exit 1
fi

if [[ ! -f .env ]]; then
    echo "error: .env not found; copy .env.example first" >&2
    exit 1
fi

# Ensure .env ends with a newline before any append.
if [[ -s .env ]] && [[ "$(tail -c 1 .env)" != $'\n' ]]; then
    printf '\n' >> .env
fi

if grep -q '^STRIPE_WEBHOOK_SECRET=' .env; then
    # `-i.bak` form works on both BSD (macOS) and GNU sed.
    sed -i.bak "s|^STRIPE_WEBHOOK_SECRET=.*|STRIPE_WEBHOOK_SECRET=${secret}|" .env
    rm -f .env.bak
else
    echo "STRIPE_WEBHOOK_SECRET=${secret}" >> .env
fi

echo "STRIPE_WEBHOOK_SECRET written to .env"
