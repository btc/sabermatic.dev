.PHONY: dev dev-log dev-build dev-watch dev-air seed test test-short cover cover-html cover-func clean-cover deps generate lint drillctl grant

test:
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

# Start embedded dev server. Vite rebuilds on save, air bounces Go binary.
# One origin (port 8080), no proxy, no CSRF issues. Ctrl-C kills all.
dev:
	@echo "Starting Drill → http://localhost:8080"
	@trap 'kill 0' EXIT; \
	$(MAKE) dev-build && \
	$(MAKE) dev-watch & \
	$(MAKE) dev-air & \
	sleep 3 && open http://localhost:8080 & \
	wait

# Same as dev, but tee all output to tmp/dev.log for easy inspection.
dev-log:
	@mkdir -p tmp
	@$(MAKE) dev 2>&1 | tee tmp/dev.log

# Initial frontend build before air starts
dev-build:
	cd web && npm install && npm run build

# Vite watch mode — rebuilds web/dist/ on frontend file changes
dev-watch:
	cd web && npx vite build --watch

# air watches web/dist/ + Go source, rebuilds and restarts Go binary
dev-air:
	set -a && . ./.env && set +a && air

# Create dev user with free trial grant. Does NOT require the server.
seed:
	set -a && . ./.env && set +a && go run ./cmd/drillctl seed

# Build drillctl binary to project root.
drillctl:
	go build -o drillctl ./cmd/drillctl

# Create admin grant. Usage: make grant MINUTES=100m [EMAIL=dev@drill.dev]
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
	brew install bufbuild/buf/buf

# Regenerate protobuf code from .proto sources.
generate:
	buf generate

# Run golangci-lint (same config as CI).
lint:
	golangci-lint run ./...
