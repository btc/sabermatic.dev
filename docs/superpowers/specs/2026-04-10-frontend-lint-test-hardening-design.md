# Frontend Lint & Test Hardening

**Goal:** Strengthen frontend quality tooling — better lint rules, coverage reporting, pre-commit enforcement, and composable Makefile targets — without writing new tests (follow-up session).

**Tech Stack:** ESLint 9 (flat config), Vitest, Lefthook, TypeScript

---

## 1. Makefile target decomposition

Decompose the monolithic `test` target into three composable targets:

- **`test-protos`** — buf lint, buf generate, git diff --exit-code check
- **`test-frontend`** — npm install, tsc --noEmit -p tsconfig.app.json, eslint, vitest run
- **`test-backend`** — go test ./internal/... ./cmd/... -race -count=1 -timeout=300s

Rewrite `test` to compose: `test: test-protos test-frontend test-backend` plus go mod tidy and the commented-out golangci-lint line.

Add all three new targets to the `.PHONY` line.

The `deploy: test` dependency continues to work unchanged.

## 2. ESLint plugin additions

### 2a. eslint-plugin-jsx-a11y

Add `eslint-plugin-jsx-a11y` as a devDependency. Add `jsxA11y.flatConfigs.recommended` to the `extends` array in `eslint.config.js`. This catches:

- Missing alt text on images
- Broken ARIA attributes
- Click handlers on non-interactive elements
- Missing label associations

### 2b. eslint-plugin-simple-import-sort

Add `eslint-plugin-simple-import-sort` as a devDependency. Add two rules:

```js
'simple-import-sort/imports': 'error',
'simple-import-sort/exports': 'error',
```

This is autofixable — run `npx eslint --fix .` once after adding to resolve all existing violations.

### 2c. Prettier (commented out, ready to enable)

Add `prettier` and `eslint-config-prettier` as devDependencies. In `eslint.config.js`, add a commented-out import and config entry:

```js
// import prettier from 'eslint-config-prettier'
// Uncomment to enable Prettier: add `prettier` to the extends array
```

No `.prettierrc` file yet — we'll create one when we enable it.

## 3. Vitest coverage

Add `@vitest/coverage-v8` as a devDependency.

Update `vitest.config.ts` to add coverage config:

```ts
coverage: {
  provider: 'v8',
  reporter: ['text', 'lcov'],
  include: ['src/**/*.{ts,tsx}'],
  exclude: ['src/pb/**', 'src/test-setup.ts'],
  // TODO(test-coverage): add thresholds once baseline is established
},
```

Add a `test:coverage` script to package.json: `"test:coverage": "vitest run --coverage"`

Add `coverage/` to the web `.gitignore`.

## 4. Lefthook pre-commit hooks

Install lefthook. Create `.lefthook.yml` at repo root:

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
    backend-lint:
      glob: "**/*.go"
      run: go vet ./...
```

- ESLint runs with `--fix` to autofix import ordering, then re-stages
- tsc runs full project check (can't typecheck individual files)
- go vet for Go (lightweight, catches real bugs)
- Parallel execution for speed
- No vitest on commit (too slow — that's what `make test-frontend` is for)

Run `lefthook install` to activate.

## 5. .gitignore updates

Add `coverage/` to `web/.gitignore` (or root `.gitignore` if web doesn't have its own).

## 6. Out of scope

- Writing new tests (follow-up session)
- Enabling Prettier (tomorrow / future)
- Playwright E2E integration
- golangci-lint enablement
