# Frontend Lint & Test Hardening Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Strengthen frontend and backend quality tooling — ESLint a11y + import sorting, Vitest coverage, lefthook pre-commit hooks, golangci-lint baseline, and composable Makefile targets.

**Architecture:** Add ESLint plugins and autofix existing violations, configure Vitest coverage reporting, set up lefthook for pre-commit enforcement, enable golangci-lint with `new-from-rev: main` baseline, and decompose the monolithic `make test` into composable targets.

**Tech Stack:** ESLint 9 (flat config), Vitest, Lefthook, golangci-lint, TypeScript, Go

**Verification commands:** After every task that modifies code, run the appropriate check:
- Frontend changes: `cd web && npx tsc --noEmit -p tsconfig.app.json && npx eslint . && npx vitest run`
- Backend changes: `go test ./internal/... ./cmd/... -race -count=1 -timeout=300s`
- Final verification: `make test`

---

### Task 1: Install ESLint plugins and Prettier (commented out)

**Files:**
- Modify: `web/package.json`
- Modify: `web/eslint.config.js`

- [ ] **Step 1: Install devDependencies**

```bash
cd web && npm install --save-dev eslint-plugin-jsx-a11y eslint-plugin-simple-import-sort prettier eslint-config-prettier
```

- [ ] **Step 2: Update `web/eslint.config.js`**

Replace the entire file with:

```js
import js from '@eslint/js'
import globals from 'globals'
import jsxA11y from 'eslint-plugin-jsx-a11y'
import reactHooks from 'eslint-plugin-react-hooks'
import reactRefresh from 'eslint-plugin-react-refresh'
import simpleImportSort from 'eslint-plugin-simple-import-sort'
import tseslint from 'typescript-eslint'
import { defineConfig, globalIgnores } from 'eslint/config'
// import prettier from 'eslint-config-prettier'

export default defineConfig([
  globalIgnores(['dist', 'src/pb']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      js.configs.recommended,
      tseslint.configs.recommended,
      jsxA11y.flatConfigs.recommended,
      reactHooks.configs.flat.recommended,
      reactRefresh.configs.vite,
      // Uncomment to enable Prettier (disables conflicting format rules):
      // prettier,
    ],
    plugins: {
      'simple-import-sort': simpleImportSort,
    },
    languageOptions: {
      ecmaVersion: 2023,
      globals: globals.browser,
    },
    rules: {
      'react-refresh/only-export-components': ['warn', { allowConstantExport: true }],
      'simple-import-sort/imports': 'error',
      'simple-import-sort/exports': 'error',
    },
  },
])
```

- [ ] **Step 3: Autofix existing import sort violations**

```bash
cd web && npx eslint --fix .
```

This resolves all import ordering violations in-place. Review the changes with `git diff` to confirm they're only import reordering.

- [ ] **Step 4: Verify ESLint passes**

Run: `cd web && npx eslint .`
Expected: No errors. Possibly warnings from react-refresh (pre-existing).

- [ ] **Step 5: Verify TypeScript still compiles**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json`
Expected: No errors.

- [ ] **Step 6: Verify tests still pass**

Run: `cd web && npx vitest run`
Expected: All 15 tests pass.

- [ ] **Step 7: Commit**

Stage the config files and all autofixed source files:

```bash
git add web/package.json web/package-lock.json web/eslint.config.js
git add web/src/
git diff --cached --stat
```

Review the `git diff --cached --stat` output to confirm changes are only import reordering + config files. Then commit:

```bash
git commit -m "feat: add eslint jsx-a11y, import sorting, and prettier (commented out)"
```

---

### Task 2: Add Vitest coverage reporting

**Files:**
- Modify: `web/vitest.config.ts`
- Modify: `web/package.json`
- Modify: `web/.gitignore`

- [ ] **Step 1: Install coverage dependency**

```bash
cd web && npm install --save-dev @vitest/coverage-v8
```

- [ ] **Step 2: Update `web/vitest.config.ts`**

Replace the entire file with:

```ts
import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import path from "path";

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  test: {
    globals: true,
    environment: "jsdom",
    setupFiles: ["./src/test-setup.ts"],
    coverage: {
      provider: "v8",
      reporter: ["text", "lcov"],
      include: ["src/**/*.{ts,tsx}"],
      exclude: ["src/pb/**", "src/test-setup.ts"],
      // TODO(test-coverage): add thresholds once baseline is established
    },
  },
});
```

- [ ] **Step 3: Add `test:coverage` script to `web/package.json`**

In the `"scripts"` section, add after `"test"`:

```json
"test:coverage": "vitest run --coverage"
```

- [ ] **Step 4: Add `coverage/` to `web/.gitignore`**

Append to the end of `web/.gitignore`:

```
# Test coverage
coverage/
```

- [ ] **Step 5: Verify coverage works**

Run: `cd web && npx vitest run --coverage`
Expected: Tests pass and a coverage summary table is printed. Note the baseline numbers.

- [ ] **Step 6: Verify regular tests still work**

Run: `cd web && npx vitest run`
Expected: All tests pass (coverage not generated without `--coverage`).

- [ ] **Step 7: Commit**

```bash
git add web/vitest.config.ts web/package.json web/package-lock.json web/.gitignore
git commit -m "feat: add Vitest coverage reporting with v8 provider"
```

---

### Task 3: Enable golangci-lint with baseline

**Files:**
- Create: `.golangci.yml`

- [ ] **Step 1: Verify golangci-lint is installed**

Run: `which golangci-lint`
Expected: Path to binary (e.g., `/Users/btc/lib/go/bin/golangci-lint`). If not found, install: `brew install golangci-lint`.

- [ ] **Step 2: Create `.golangci.yml`**

Create at repo root `/Users/btc/Projects/src/drill/.golangci.yml`:

```yaml
issues:
  new-from-rev: main
```

- [ ] **Step 3: Verify golangci-lint runs clean**

Run: `golangci-lint run ./...`
Expected: No issues (all existing issues are before `main`, so they're excluded). If any issues appear, they are in code changed since main on this branch — fix them.

- [ ] **Step 4: Commit**

```bash
git add .golangci.yml
git commit -m "feat: enable golangci-lint with new-from-rev baseline"
```

---

### Task 4: Decompose Makefile test targets

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Replace the `test` target and `.PHONY` line**

In the Makefile, replace the `.PHONY` line and `test` target. The current `.PHONY` line is:

```makefile
.PHONY: dev dev-log seed test test-short cover cover-html cover-func clean-cover deps generate lint drillctl grant cloudsql-proxy db-password
```

Replace with:

```makefile
.PHONY: dev dev-log seed test test-short cover cover-html cover-func clean-cover deps generate lint drillctl grant cloudsql-proxy db-password test-protos test-frontend test-backend
```

Then replace the entire `test:` target (lines 3-31 of current Makefile) with:

```makefile
test-protos:
	@echo "=== buf lint ==="
	buf lint
	@echo ""
	@echo "=== buf generate (verify clean) ==="
	buf generate
	@git diff --exit-code internal/pb/ web/src/pb/ || (echo "FAIL: buf generate produced uncommitted changes" && exit 1)

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

test: test-protos test-frontend test-backend
	@echo ""
	@echo "=== go mod tidy ==="
	go mod tidy
	@echo ""
	@echo "=== golangci-lint ==="
	golangci-lint run ./...
	@echo ""
	@echo "=== all checks passed ==="
```

- [ ] **Step 2: Verify `make test-frontend` works**

Run: `make test-frontend`
Expected: npm install, tsc, eslint, and vitest all pass.

- [ ] **Step 3: Verify `make test-backend` works**

Run: `make test-backend`
Expected: All Go tests pass with `-race`.

- [ ] **Step 4: Verify `make test` composes correctly**

Run: `make test`
Expected: All three sub-targets run, then go mod tidy and golangci-lint, then "all checks passed".

- [ ] **Step 5: Commit**

```bash
git add Makefile
git commit -m "refactor: decompose make test into test-protos, test-frontend, test-backend"
```

---

### Task 5: Set up Lefthook pre-commit hooks

**Files:**
- Create: `.lefthook.yml`

- [ ] **Step 1: Install lefthook**

```bash
brew install lefthook
```

Verify: `lefthook version`
Expected: Version output (e.g., `2.1.5`).

- [ ] **Step 2: Create `.lefthook.yml`**

Create at repo root `/Users/btc/Projects/src/drill/.lefthook.yml`:

```yaml
pre-commit:
  parallel: true
  commands:
    frontend-lint:
      glob: "web/src/**/*.{ts,tsx}"
      run: cd web && npx eslint --fix {staged_files} && git add {staged_files}
    frontend-typecheck:
      glob: "web/src/**/*.{ts,tsx}"
      run: cd web && npx tsc --noEmit -p tsconfig.app.json
    backend-vet:
      glob: "**/*.go"
      run: go vet ./...
```

- [ ] **Step 3: Install the hooks**

```bash
lefthook install
```

Expected: Output indicating hooks were installed. Verify: `ls .git/hooks/pre-commit` should show the lefthook shim.

- [ ] **Step 4: Test the hooks work**

Make a trivial whitespace change to a `.tsx` file, stage it, and run:

```bash
lefthook run pre-commit
```

Expected: `frontend-lint` and `frontend-typecheck` run. `backend-vet` does NOT run (no `.go` files staged).

Revert the test change: `git checkout -- web/`

- [ ] **Step 5: Commit**

```bash
git add .lefthook.yml
git commit -m "feat: add lefthook pre-commit hooks for lint, typecheck, and go vet"
```

---

### Task 6: Final verification

- [ ] **Step 1: Run full test suite**

Run: `make test`
Expected: All checks pass — test-protos, test-frontend (with new a11y + import rules), test-backend, golangci-lint.

- [ ] **Step 2: Verify lefthook is active**

Run: `lefthook run pre-commit`
Expected: Hooks execute (may skip if no files match globs, which is fine).

- [ ] **Step 3: Verify no regressions**

Run: `cd web && npx tsc --noEmit -p tsconfig.app.json && npx eslint . && npx vitest run`
Expected: All pass with zero errors.
