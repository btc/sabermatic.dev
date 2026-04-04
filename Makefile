.PHONY: dev dev-api dev-web seed test test-short cover cover-html cover-func clean-cover

# Start Go backend + Vite frontend dev server. Ctrl-C kills both.
dev:
	@echo "Starting Drill → http://localhost:3000"
	@trap 'kill 0' EXIT; \
	$(MAKE) dev-api & \
	$(MAKE) dev-web & \
	sleep 2 && open http://localhost:3000 & \
	wait

dev-api:
	set -a && . ./.env && set +a && go run ./cmd/drill

dev-web:
	cd web && npm run dev

# Create dev user + seed questions. Run with server already up.
seed:
	./scripts/dev-seed.sh

# Run all tests (integration + unit). Requires Docker.
test:
	go test ./internal/... ./cmd/... -race -count=1 -timeout=300s

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
