.PHONY: dev dev-log seed test test-short cover cover-html cover-func clean-cover deps generate lint drillctl grant

test:
	@echo "=== go mod tidy ==="
	go mod tidy
	@echo "=== buf lint ==="
	buf lint
	@echo ""
	@echo "=== buf generate (verify clean) ==="
	buf generate
	@git diff --exit-code internal/pb/ web/src/pb/ || (echo "FAIL: buf generate produced uncommitted changes" && exit 1)
	@echo ""
	@echo "=== golangci-lint ==="
	# golangci-lint run ./...
	@echo ""
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
	@echo ""
	@echo "=== backend tests ==="
	go test ./internal/... ./cmd/... -race -count=1 -timeout=300s
	@echo ""
	@echo "=== all checks passed ==="

deploy: test
	./scripts/deploy.sh
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

# Print the admin DB password for use in Postico (separate credential from app user).
brian-db-password:
	@gcloud secrets versions access latest \
	  --secret=brian-db-password \
	  --project=sabermatic-production
