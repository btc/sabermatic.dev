.PHONY: dev dev-log seed test test-short cover cover-html cover-func clean-cover clean-test-containers deps generate lint drillctl grant cloudsql-proxy db-password test-protos test-frontend test-backend stripe-setup stripe-pack-buy stripe-sub-start stripe-sub-cancel stripe-resend stripe-trigger regen-og

test-protos:
	@echo "=== buf lint ==="
	buf lint
	@echo ""
	@echo "=== buf generate (verify clean) ==="
	buf generate
	@git diff --exit-code internal/pb/ web/src/pb/ || (echo "FAIL: buf generate produced uncommitted changes" && exit 1)
	go run ./thirdparty/aippatch/cmd/aippatchgen --check

test-frontend:
	@echo "=== frontend deps ==="
	cd web && npm install
	@echo ""
	@echo "=== frontend typecheck ==="
	cd web && npx tsc --noEmit -p tsconfig.app.json
	@echo ""
	@echo "=== frontend lint ==="
	cd web && npx eslint .
	@echo ""
	@echo "=== frontend tests ==="
	cd web && npx vitest run

test-backend:
	@echo "=== backend tests ==="
	go test ./internal/... ./cmd/... -race -count=1 -timeout=300s

test: clean-test-containers test-protos test-frontend test-backend
	@echo ""
	@echo "=== go mod tidy ==="
	go mod tidy
	@echo ""
	@echo "=== golangci-lint ==="
	golangci-lint run ./...
	@echo ""
	@echo "=== all checks passed ==="

deploy: clean-test-containers test
	./scripts/deploy.sh

# Remove stale testcontainers-managed containers from prior runs. When a test
# panics during container startup, ryuk may not register the orphan, so it
# accumulates and slows Docker Desktop until new containers can't acquire a
# mapped port within the wait timeout. The org.testcontainers=true label is
# applied by testcontainers-go to every container it creates, so this filter
# only touches test artifacts, never user-started containers.
clean-test-containers:
	@docker ps -aq --filter label=org.testcontainers=true | xargs -r docker rm -f >/dev/null 2>&1 || true
# Start dev server via overmind. Ctrl-C kills all processes cleanly.
# Runs: npm build, vite watch, air (Go rebuild). One origin on :8080.
dev:
	@echo "Starting Sabermatic → http://localhost:8080"
	pkill -f 'air$$' || true
	pkill -f 'tmp/drill' || true
	@cd web && npm install && npm run build
	overmind start -f Procfile.dev

# Same as dev, but tee all output to tmp/dev.log for easy inspection.
dev-log:
	@mkdir -p tmp
	overmind start -f Procfile.dev 2>&1 | tee tmp/dev.log

# Create dev user with free trial grant. Does NOT require the server.
seed:
	set -a && . ./.env && set +a && go run ./cmd/drillctl seed

# Build drillctl binary to project root.
drillctl:
	go build -o drillctl ./cmd/drillctl

# Create admin grant. Usage: make grant MINUTES=100m [EMAIL=dev@sabermatic.dev]
grant: drillctl
	set -a && . ./.env && set +a && ./drillctl grant $(if $(EMAIL),--email $(EMAIL)) $(MINUTES)

# Run unit tests only (no Docker needed).
test-short:
	go test ./internal/... ./cmd/... -short -race -count=1

# Generate cross-package coverage profile.
# This captures coverage across package boundaries (e.g., handler tests
# exercising conductor code), giving the true coverage picture.
COVER_OUT := coverage-cross.out
cover:
	go test -coverprofile=$(COVER_OUT) -covermode=atomic \
		-coverpkg=./internal/... \
		./internal/... ./cmd/... \
		-race -count=1 -timeout=300s
	@echo ""
	@echo "Coverage profile: $(COVER_OUT)"
	@echo "Run 'make cover-html' to view in browser, or 'make cover-func' for per-function summary."

# Open interactive HTML coverage report in browser.
# Green = covered, red = uncovered, yellow = partial.
cover-html: $(COVER_OUT)
	go tool cover -html=$(COVER_OUT)

# Print per-function coverage sorted by percentage (lowest first).
cover-func: $(COVER_OUT)
	go tool cover -func=$(COVER_OUT) | sort -t'	' -k3 -n

# Per-package coverage (standard, not cross-package). Faster, useful for
# checking coverage of a single package's own tests.
cover-pkg:
	go test -coverprofile=coverage-pkg.out -covermode=atomic \
		./internal/... ./cmd/... \
		-race -count=1 -timeout=300s
	go tool cover -html=coverage-pkg.out

# Clean up coverage artifacts.
clean-cover:
	rm -f coverage-cross.out coverage-pkg.out cover_*.out coverage.out

$(COVER_OUT):
	@$(MAKE) cover

# Install project-level dev tools (CLIs, linters, codegen).
# Run once after clone, or when tool versions change.
deps:
	brew install \
		bufbuild/buf/buf \
		overmind \
		tmux

# Regenerate protobuf code from .proto sources.
generate:
	buf generate
	buf build -o buf.binpb
	sqlc generate
	go run ./thirdparty/aippatch/cmd/aippatchgen

# Run golangci-lint (same config as CI).
lint:
	golangci-lint run ./...

# Tunnel to the prod Cloud SQL instance via Cloud SQL Auth Proxy.
# Exposes postgres on localhost:5433 — use this in Postico:
#   Host: localhost  Port: 5433  User: sabermatic  DB: sabermatic
CLOUDSQL_INSTANCE := sabermatic-production:us-central1:sabermatic-production
CLOUDSQL_PORT     := 5433
cloudsql-proxy:
	cloud_sql_proxy -instances=$(CLOUDSQL_INSTANCE)=tcp:$(CLOUDSQL_PORT)

# Print the sabermatic DB password for use in Postico.
db-password:
	@gcloud secrets versions access latest \
	  --secret=database-url \
	  --project=sabermatic-production \
	  | sed -n 's|.*://sabermatic:\([^@]*\)@.*|\1|p' \
	  | tr -d '\n' \
	  | pbcopy
	@echo "Copied to clipboard."

# --- Stripe scenarios ----------------------------------------------------------
# U is the target user's UUID. Chosen over USER to avoid colliding with the
# shell's auto-exported USER env var (which Make inherits as a default).

stripe-setup:
	./scripts/stripe-setup.sh

stripe-pack-buy:
	go run ./cmd/stripescenario pack-buy --user "$(U)" $(if $(MINUTES),--minutes $(MINUTES),)

stripe-sub-start:
	go run ./cmd/stripescenario sub-start --user "$(U)"

stripe-sub-cancel:
	go run ./cmd/stripescenario sub-cancel --user "$(U)"

stripe-resend:
	go run ./cmd/stripescenario resend "$(EVENT)"

# Passthrough for ad-hoc event triggers (unhandled-event exploration).
stripe-trigger:
	stripe trigger "$(TYPE)"

# Regenerate OG images from their SVG sources. Requires rsvg-convert (brew install librsvg).
# NOTE: run on macOS only — ui-sans-serif fallback differs across hosts (macOS: SF Pro;
# Linux: DejaVu Sans), so regenerating on Linux produces different pixels and would
# drift the committed baseline.
regen-og:
	@which rsvg-convert > /dev/null || (echo "rsvg-convert not found — brew install librsvg"; exit 1)
	rsvg-convert -w 1200 -h 630 web/public/og-landing.svg -o web/public/og-landing.png
	rsvg-convert -w 1200 -h 630 web/public/og-sample.svg  -o web/public/og-sample.png
	@echo "Regenerated web/public/og-landing.png and web/public/og-sample.png"
