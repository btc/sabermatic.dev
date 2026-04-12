# HTTP Package Refactor & CSRF Removal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove dead CSRF middleware, consolidate HTTP layer in `internal/handler/`, move auth middleware where it belongs, collapse `NewHandler` + `RegisterRoutes`, inline local-dev storage routing, and add Stripe webhook test coverage.

**Architecture:** Seven sequential tasks, each ending on green CI and individually revertable. Steps 1-3 are deletions/consolidations against current shape; steps 4-6 reshape the package; step 7 finishes housekeeping and adds the missing Stripe test. The package keeps its existing name (`handler`).

**Tech Stack:** Go 1.23, Connect-RPC, gorilla/csrf (being removed), goth (OAuth), stripe-go/v82, OpenTelemetry. Frontend: React 18, TanStack Query, Vitest.

**Spec:** [`docs/superpowers/specs/2026-04-12-http-package-refactor-design.md`](../specs/2026-04-12-http-package-refactor-design.md)

---

## Task 1: Delete CSRF surface

Remove every CSRF code path from backend and frontend. The middleware protects zero routes today (verified in spec §Background). After this task, `gorilla/csrf` is no longer a dependency, the frontend no longer plumbs `X-CSRF-Token`, and `docs/csrf-audit.md` is stubbed out.

**Files:**
- Modify: `internal/handler/routes.go` (delete `csrfMiddleware`, `isCSRFExempt`, `auth.DeriveKey(..., "csrf")` call, simplify `NewHandler` chain)
- Delete: `internal/handler/csrf_test.go`
- Delete: `internal/handler/oauth_test.go` (entire file is CSRF tests; OAuth tests live in `oauth_flow_test.go`)
- Modify: `web/src/api/client.ts` (remove `getCsrfToken`, `mutationHeaders`)
- Modify: `web/src/main.tsx` (remove CSRF priming fetch)
- Modify: `web/src/vite-env.d.ts` (remove `__csrfToken` declaration)
- Modify: `web/src/api/__tests__/client.test.ts` (remove CSRF assertion test, drop `delete window.__csrfToken` from `beforeEach`)
- Modify: `web/src/api/queries.ts` (delete dead `useCreateQuestion` + unused `Question` import)
- Modify: `go.mod` and `go.sum` (via `go mod tidy`)
- Modify: `docs/csrf-audit.md` (stub to one line pointing to spec)

- [ ] **Step 1.1: Delete `internal/handler/csrf_test.go`**

```bash
rm /Users/btc/Projects/src/drill/internal/handler/csrf_test.go
```

- [ ] **Step 1.2: Delete `internal/handler/oauth_test.go`**

The file is misnamed — it contains only CSRF middleware tests, not OAuth tests. OAuth tests are in `oauth_flow_test.go`.

```bash
rm /Users/btc/Projects/src/drill/internal/handler/oauth_test.go
```

- [ ] **Step 1.3: Rewrite `internal/handler/routes.go` to remove CSRF**

Replace the entire file with this content. Compared to current: drops `csrf` and `auth` imports (no longer needed since neither `csrfMiddleware` nor `DeriveKey` are called here), drops `secureCookies` derivation from `NewHandler` (only `SecurityHeaders` consumes it now), deletes `csrfMiddleware` and `isCSRFExempt`, simplifies the chain to `SecurityHeaders(secureCookies, otelHandler)`.

```go
package handler

import (
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/branding"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/rpc"
)

// NewHandler builds the full HTTP handler chain: routes, OTel tracing, and
// security headers. Returns a ready-to-use http.Handler.
func NewHandler(b *backend.Backend, spaFS fs.FS) (http.Handler, error) {
	cfg := b.Config()
	baseURL := cfg.Auth.BaseURL
	secureCookies := cfg.Auth.SecureCookies()

	mux := http.NewServeMux()
	if err := RegisterRoutes(mux, b); err != nil {
		return nil, fmt.Errorf("register routes: %w", err)
	}
	spaHandler, err := SPAHandler(spaFS, baseURL)
	if err != nil {
		return nil, fmt.Errorf("spa handler: %w", err)
	}
	mux.Handle("/", spaHandler)

	otelHandler := otelhttp.NewMiddleware(drilotel.AppName,
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if r.Pattern != "" {
				return r.Pattern
			}
			return r.Method + " " + r.URL.Path
		}),
		otelhttp.WithFilter(func(r *http.Request) bool {
			for _, prefix := range rpc.ConnectPathPrefixes() {
				if strings.HasPrefix(r.URL.Path, "/"+prefix+"/") {
					return false
				}
			}
			return true
		}),
	)(mux)

	return SecurityHeaders(secureCookies, otelHandler), nil
}

// RegisterRoutes sets up all HTTP routes on the given mux.
// Used by NewHandler for production and directly by tests.
//
// Security model: this app does not use anti-CSRF tokens. ConnectRPC's
// Connect-Protocol-Version custom header forces a CORS preflight, which
// the absence of permissive CORS config blocks; the SPA is served
// same-origin; session cookies are SameSite=Lax; the Stripe webhook is
// signature-verified; OAuth callbacks use the state parameter. If a future
// cookie-authenticated REST mutation endpoint is added, wrap it in a
// per-route CSRF helper at registration time.
func RegisterRoutes(mux *http.ServeMux, b *backend.Backend) error {
	if err := rpc.Register(mux, b); err != nil {
		return fmt.Errorf("rpc register: %w", err)
	}

	mux.HandleFunc("GET /api/health", Health(b))

	// Admin — requires both auth and admin role.
	requireAuth := auth.RequireAuth(b)
	requireAdmin := auth.RequireAdmin()
	mux.Handle("/admin/jobs/", requireAuth(requireAdmin(b.RiverUIHandler())))

	// OAuth
	mux.HandleFunc("GET /api/auth/oauth/{provider}", OAuthStart(b))
	mux.HandleFunc("GET /api/auth/oauth/{provider}/callback", OAuthCallback(b))

	// Stripe webhook — signature-verified by the handler.
	mux.HandleFunc("POST /api/webhooks/stripe", PostStripeWebhook(b))

	return nil
}

// ogRoute defines OG meta tag content for a public route.
type ogRoute struct {
	title       string
	description string
	image       string // path relative to base URL
}

var ogRoutes = map[string]ogRoute{
	"/": {
		title:       branding.AppName,
		description: "data-driven system design prep",
		image:       "/og-landing.png",
	},
	"/about": {
		title:       branding.AppName,
		description: "data-driven system design prep",
		image:       "/og-landing.png",
	},
	"/sample": {
		title:       branding.AppName + " — sample evaluation",
		description: "See a real system design interview evaluated across 5 dimensions",
		image:       "/og-sample.png",
	},
}

// SPAHandler serves the embedded SPA. Static assets served directly.
// All other paths return index.html for client-side routing.
// For paths with OG tags defined, the tags are injected before </head>.
// baseURL is the public URL (e.g., "https://sabermatic.dev") used for
// absolute og:url and og:image values. Pass "" for tests.
func SPAHandler(fsys fs.FS, baseURL string) (http.Handler, error) {
	sub, err := fs.Sub(fsys, "web/dist")
	if err != nil {
		return nil, fmt.Errorf("embed sub: %w", err)
	}

	indexBytes, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read index.html: %w", err)
	}
	indexHTML := string(indexBytes)

	if !strings.Contains(indexHTML, "</head>") {
		slog.Warn("index.html missing </head> — OG tags will not be injected")
	}

	// Pre-compute OG-injected HTML at init time
	ogPages := make(map[string][]byte, len(ogRoutes))
	for path, og := range ogRoutes {
		tags := fmt.Sprintf(
			`<meta property="og:title" content="%s">`+
				`<meta property="og:description" content="%s">`+
				`<meta property="og:type" content="website">`+
				`<meta property="og:url" content="%s%s">`+
				`<meta property="og:image" content="%s%s">`,
			html.EscapeString(og.title), html.EscapeString(og.description),
			html.EscapeString(baseURL), html.EscapeString(path),
			html.EscapeString(baseURL), html.EscapeString(og.image),
		)
		ogPages[path] = []byte(strings.Replace(indexHTML, "</head>", tags+"</head>", 1))
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			if _, err := fs.Stat(sub, strings.TrimPrefix(path, "/")); err == nil {
				// Hashed asset files are immutable and can be cached forever
				if strings.Contains(path, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// Serve pre-computed OG-injected HTML if this path has OG tags
		if body, ok := ogPages[path]; ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
			return
		}

		// Fall back to index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}), nil
}
```

- [ ] **Step 1.4: Run `go mod tidy` to drop `gorilla/csrf`**

Run: `cd /Users/btc/Projects/src/drill && go mod tidy`
Expected: `go.mod` no longer lists `github.com/gorilla/csrf`. `go.sum` updated.

- [ ] **Step 1.5: Verify backend builds and tests pass**

Run: `cd /Users/btc/Projects/src/drill && go build ./... && go test ./internal/handler/... -race -count=1 -timeout=120s`
Expected: build succeeds, all handler tests pass.

- [ ] **Step 1.6: Rewrite `web/src/main.tsx` to drop CSRF priming**

Replace the file with this content (removes the CSRF priming fetch and the comment block above it):

```tsx
import "./index.css";

import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ErrorBoundary } from "react-error-boundary";
import { BrowserRouter } from "react-router-dom";
import { Toaster } from "sonner";

import { transport } from "./api/transport";
import { App } from "./app";
import { ErrorFallback } from "./components/error-fallback";
import { ThemeProvider } from "./contexts/theme-context";
import { useTheme } from "./hooks/use-theme";
import { initTelemetry } from "./telemetry/provider";

initTelemetry();

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
    },
  },
});

function ThemedToaster() {
  const { resolvedTheme } = useTheme();
  return <Toaster theme={resolvedTheme} position="bottom-right" richColors />;
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <ThemeProvider>
      <ErrorBoundary
        FallbackComponent={ErrorFallback}
        onReset={() => window.location.reload()}
      >
        <QueryClientProvider client={queryClient}>
          <TransportProvider transport={transport}>
            <BrowserRouter>
              <App />
            </BrowserRouter>
          </TransportProvider>
        </QueryClientProvider>
      </ErrorBoundary>
      <ThemedToaster />
    </ThemeProvider>
  </StrictMode>,
);
```

- [ ] **Step 1.7: Rewrite `web/src/vite-env.d.ts`**

Replace with:

```ts
/// <reference types="vite/client" />
```

- [ ] **Step 1.8: Rewrite `web/src/api/client.ts`**

Replace the entire file with this content. Drops `getCsrfToken`, drops the `mutationHeaders` helper, inlines content-type setting per method.

```ts
export class ApiError extends Error {
  constructor(
    public status: number,
    public body: unknown,
  ) {
    super(`API error ${status}`);
    this.name = "ApiError";
  }
}

async function request<T>(url: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(url, {
    credentials: "same-origin",
    ...options,
  });
  // NOTE: `null as T` is a known type lie — all DELETE callers expect void/null
  // and there is no caller that reads the return value of a 204 response.
  if (response.status === 204) {
    return null as T;
  }
  // Read text first so the body stream is available for both JSON parse and
  // error reporting. Calling response.json() first consumes the stream, making
  // a subsequent response.text() call return an empty string.
  const text = await response.text();
  let data: unknown;
  try {
    data = JSON.parse(text);
  } catch {
    data = text || `Non-JSON response (${response.status})`;
  }
  if (!response.ok) {
    throw new ApiError(response.status, data);
  }
  return data as T;
}

function jsonHeaders(body?: unknown): HeadersInit | undefined {
  if (body === undefined) return undefined;
  return { "Content-Type": "application/json" };
}

export const apiClient = {
  get<T>(url: string): Promise<T> {
    return request<T>(url, { method: "GET" });
  },
  post<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "POST",
      headers: jsonHeaders(body),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  put<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "PUT",
      headers: jsonHeaders(body),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  patch<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "PATCH",
      headers: jsonHeaders(body),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  delete<T>(url: string): Promise<T> {
    return request<T>(url, {
      method: "DELETE",
    });
  },
};
```

- [ ] **Step 1.9: Rewrite `web/src/api/__tests__/client.test.ts`**

Replace with:

```ts
import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient, ApiError } from "../client";

describe("apiClient", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it("sends GET request and parses JSON", async () => {
    const mockData = { id: "123", email: "test@example.com" };
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify(mockData), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const result = await apiClient.get("/api/me");
    expect(result).toEqual(mockData);
    expect(fetch).toHaveBeenCalledWith("/api/me", expect.objectContaining({
      method: "GET",
      credentials: "same-origin",
    }));
  });

  it("sends POST with JSON body", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ id: "456" }), { status: 200 }),
    );
    await apiClient.post("/api/sessions", { question_id: "q1" });
    expect(fetch).toHaveBeenCalledWith("/api/sessions", expect.objectContaining({
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ question_id: "q1" }),
    }));
  });

  it("throws ApiError on 4xx response", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ error: "not found" }), { status: 404 }),
      ),
    );
    await expect(apiClient.get("/api/sessions/bad")).rejects.toThrow(ApiError);
    await expect(apiClient.get("/api/sessions/bad")).rejects.toMatchObject({ status: 404 });
  });

  it("returns null for DELETE with 204", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(null, { status: 204 }),
    );
    const result = await apiClient.delete("/api/auth/account");
    expect(result).toBeNull();
  });
});
```

- [ ] **Step 1.10: Delete dead `useCreateQuestion` from `web/src/api/queries.ts`**

In `web/src/api/queries.ts`, remove three things:

(1) The import line at the top:

```ts
import { apiClient } from "./client";
import type { Question } from "./types";
```

(2) The "Questions" section comment and the `useCreateQuestion` export:

```ts
// --- Questions (still REST until QuestionService gets CreateQuestion) ---

export function useCreateQuestion() {
  const qc = useQueryClient();
  return useTanStackMutation({
    mutationFn: (data: { title: string; prompt: string; difficulty: string; tags: string[] }) =>
      apiClient.post<Question>("/api/questions", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["questions"] }),
  });
}
```

(3) `useTanStackMutation` is no longer used after deleting `useCreateQuestion`. Update the existing `@tanstack/react-query` import from:

```ts
import { useMutation as useTanStackMutation, useQueryClient } from "@tanstack/react-query";
```

to:

```ts
import { useQueryClient } from "@tanstack/react-query";
```

Verify by greping: `cd /Users/btc/Projects/src/drill/web && grep -n "apiClient\|useTanStackMutation\|Question\b" src/api/queries.ts` should return no matches (the type `Question` is also unused after this change).

If you find the `Question` type still referenced somewhere else (other than this file), leave the type definition in `web/src/api/types.ts` alone — only remove the import in `queries.ts`.

- [ ] **Step 1.11: Stub `docs/csrf-audit.md`**

Replace the entire file with:

```markdown
# CSRF Audit (deprecated)

CSRF middleware was removed on 2026-04-12. Browser-based CSRF protection
in this app comes from ConnectRPC's `Connect-Protocol-Version` custom
header (forcing CORS preflight), same-origin SPA deployment, and
SameSite=Lax session cookies. The Stripe webhook is signature-verified;
OAuth callbacks use the state parameter.

See [`docs/superpowers/specs/2026-04-12-http-package-refactor-design.md`](./superpowers/specs/2026-04-12-http-package-refactor-design.md) for the rationale and full security model.
```

- [ ] **Step 1.12: Run full CI**

Run: `cd /Users/btc/Projects/src/drill && make test`
Expected: buf lint, codegen check, frontend typecheck, frontend lint, frontend tests, backend tests all pass.

If `useCreateQuestion` is referenced anywhere else (e.g., in a `web/src/pages/` file), tsc will fail with "module has no exported member 'useCreateQuestion'". Search and delete those references too — `cd /Users/btc/Projects/src/drill/web && grep -rn "useCreateQuestion" src/` should return empty.

- [ ] **Step 1.13: Browser smoke test**

Run `make dev`. In a browser:

1. Sign up with a new email at `/signup`. Verify cookie-based session persists across reload.
2. Log out and log back in.
3. Navigate to `/billing` and start a checkout (should redirect to Stripe Checkout).
4. Click an OAuth provider button and complete login (Google or GitHub depending on what's configured locally).
5. Verify no console errors, no `X-CSRF-Token` request headers in DevTools Network tab.
6. Verify the `sabermatic_csrf` cookie may still be present from before this change — that's harmless; it'll expire naturally.

- [ ] **Step 1.14: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add internal/handler/routes.go web/src/api/client.ts web/src/api/queries.ts web/src/api/__tests__/client.test.ts web/src/main.tsx web/src/vite-env.d.ts docs/csrf-audit.md go.mod go.sum
git rm internal/handler/csrf_test.go internal/handler/oauth_test.go
git commit -m "$(cat <<'EOF'
refactor: remove dead CSRF middleware

The CSRF middleware protected zero routes — every registered non-GET
route was in the exemption list (Stripe webhook, /admin/jobs/) or
Connect-prefixed. ConnectRPC's Connect-Protocol-Version header forces a
CORS preflight that the absence of permissive CORS config blocks; the
SPA is served same-origin; session cookies are SameSite=Lax. The Stripe
webhook is signature-verified; OAuth callbacks use the state parameter.

Removes gorilla/csrf, the X-CSRF-Token client plumbing, the dead
useCreateQuestion hook (no server route), and stubs docs/csrf-audit.md.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Delete `ConnectPathPrefixes()`

The function existed only to feed the CSRF exemption list. Without CSRF, it has no callers. Note: it's still referenced inside `routes.go`'s OTel filter (kept after Step 1) — that filter strips Connect spans, which is independent of CSRF and must continue working. So the function stays alive but the spec's intent was to delete it. Re-read step 2 in spec — yes, spec says delete, but the OTel filter at `routes.go:48` still uses it. Resolve: keep the function (the OTel filter is a real caller).

Update the spec/plan understanding: `ConnectPathPrefixes` has one remaining caller (OTel filter) and stays. Skip the deletion. Document this discovery.

- [ ] **Step 2.1: Verify `ConnectPathPrefixes` callers**

Run: `cd /Users/btc/Projects/src/drill && grep -rn "ConnectPathPrefixes" --include="*.go"`
Expected: matches in `internal/rpc/register.go` (definition) and `internal/handler/routes.go` (OTel filter caller). The CSRF caller was deleted in Task 1.

- [ ] **Step 2.2: Decision — function stays**

The OTel filter in `routes.go:NewHandler` uses `ConnectPathPrefixes()` to skip duplicate spans on Connect routes (Connect's own otelconnect interceptor already creates spans). This is unrelated to CSRF and is a legitimate caller. The function stays.

If you read the spec and expected to delete this function, the spec is incorrect on this point. Do not delete it. Skip to Task 3.

- [ ] **Step 2.3: Update spec to reflect this finding**

Edit `docs/superpowers/specs/2026-04-12-http-package-refactor-design.md`. Find the "Step 2 — Delete ConnectPathPrefixes()" section and replace with:

```markdown
### Step 2 — Verify `ConnectPathPrefixes()` callers (no deletion)

`ConnectPathPrefixes()` has a second caller besides the deleted CSRF exemption: the OTel middleware filter in `NewHandler` uses it to skip duplicate spans on Connect routes (Connect's own `otelconnect` interceptor already emits spans). This caller is unrelated to CSRF and remains. The function stays.

- Verify: `grep -rn "ConnectPathPrefixes" --include="*.go"` shows callers in `register.go` (definition) and `routes.go` (OTel filter only, no longer in CSRF middleware).
```

- [ ] **Step 2.4: Commit spec update**

```bash
cd /Users/btc/Projects/src/drill
git add docs/superpowers/specs/2026-04-12-http-package-refactor-design.md
git commit -m "docs: keep ConnectPathPrefixes (OTel filter still uses it)

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Move local-dev storage mux into `NewHandler`

Today `cmd/drill/main.go` wraps the HTTP handler in a second mux to serve `/storage/` for local-dev file serving. Move that registration inside `RegisterRoutes` (conditional on `cfg.Storage.Backend == "local"`). One mux, one place.

**Files:**
- Modify: `internal/handler/routes.go` (add `/storage/` registration in `RegisterRoutes`)
- Modify: `cmd/drill/main.go` (delete the wrapping-mux block)

- [ ] **Step 3.1: Verify `Backend.Config()` exposes Storage fields**

Already verified during planning: `cfg.Storage.Backend` and `cfg.Storage.LocalDir` exist (`internal/config/config.go:116-121`). Continue.

- [ ] **Step 3.2: Modify `RegisterRoutes` to mount `/storage/` for local backend**

Open `internal/handler/routes.go`. Find the `RegisterRoutes` function (after Task 1's rewrite). After the `mux.HandleFunc("POST /api/webhooks/stripe", PostStripeWebhook(b))` line and before the closing `return nil`, insert:

```go
	// Local-dev storage server: serve uploaded files via HTTP so the browser
	// can load them. In production (cfg.Storage.Backend == "gcs"), files are
	// served directly from GCS by signed URLs.
	if b.Config().Storage.Backend == "local" {
		mux.Handle("/storage/", http.StripPrefix("/storage/",
			http.FileServer(http.Dir(b.Config().Storage.LocalDir))))
	}
```

- [ ] **Step 3.3: Delete the wrapping-mux block in `cmd/drill/main.go`**

Open `cmd/drill/main.go`. Find this block (currently around lines 88-96):

```go
	// Local dev: serve storage files via HTTP so browser can load them.
	if cfg.Storage.Backend == "local" {
		inner := h
		storageMux := http.NewServeMux()
		storageMux.Handle("/storage/", http.StripPrefix("/storage/",
			http.FileServer(http.Dir(cfg.Storage.LocalDir))))
		storageMux.Handle("/", inner)
		h = storageMux
	}
```

Delete the entire block (including the comment).

- [ ] **Step 3.4: Verify backend builds and tests pass**

Run: `cd /Users/btc/Projects/src/drill && go build ./... && go test ./internal/handler/... ./cmd/... -race -count=1 -timeout=120s`
Expected: build succeeds, tests pass.

- [ ] **Step 3.5: Browser smoke test for storage**

Run `make dev`. Open the app. If you have an existing session with uploaded audio, navigate to a session page and verify audio loads. If not, run through a fresh interview to record audio and verify playback.

Also check the browser console for any CSP violations on `<audio>` or `<img>` elements pointing to `/storage/`. The CSP at `internal/handler/security.go:14` allows `default-src 'self'`, which covers same-origin `/storage/` paths; `media-src` falls back to `default-src` so audio under `'self'` is allowed. If you see a CSP violation, the issue is likely a `media-src` directive that needs explicit allowance — escalate before continuing.

- [ ] **Step 3.6: Run full CI**

Run: `cd /Users/btc/Projects/src/drill && make test`
Expected: green.

- [ ] **Step 3.7: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add internal/handler/routes.go cmd/drill/main.go
git commit -m "$(cat <<'EOF'
refactor: move local-dev storage mux into RegisterRoutes

Previously cmd/drill/main.go wrapped the HTTP handler in a second mux
to serve /storage/ in local dev. Move the registration into
RegisterRoutes conditional on cfg.Storage.Backend == "local". One mux,
one place. Production (gcs backend) is unaffected.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Move `auth/middleware.go` to `handler/middleware.go`; merge `SecurityHeaders` into it; merge tests

Move `RequireAuth`, `RequireAdmin`, and supporting code from `internal/auth/middleware.go` into `internal/handler/middleware.go`. Move `SecurityHeaders` from `internal/handler/security.go` into the same file. Merge `internal/auth/middleware_test.go` and `internal/handler/security_test.go` into `internal/handler/middleware_test.go`. The `auth` package is now identity primitives only.

**Files:**
- Create: `internal/handler/middleware.go`
- Create: `internal/handler/middleware_test.go`
- Delete: `internal/auth/middleware.go`
- Delete: `internal/auth/middleware_test.go`
- Delete: `internal/handler/security.go`
- Delete: `internal/handler/security_test.go`
- Modify: `internal/handler/routes.go` (callers reference `auth.RequireAuth` / `auth.RequireAdmin` — change to local references after the move)

- [ ] **Step 4.1: Create `internal/handler/middleware.go`**

Write this content (combines `auth/middleware.go` body + `handler/security.go` body, with package changed to `handler`, removes the `auth` package import for `WithUser`/`UserFromContext`/`HashSessionToken`/`SessionCookieName`/`AuthUser`/`SessionAuthenticator` which stay in the `auth` package and are now imported):

```go
package handler

import (
	"encoding/json"
	"net/http"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/btc/drill/internal/auth"
)

// SecurityHeaders returns a middleware that sets standard security hardening
// headers on every response. HSTS is only set when secureCookies is true
// (production/HTTPS).
func SecurityHeaders(secureCookies bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: https://storage.googleapis.com; connect-src 'self' wss:; font-src 'self'; frame-ancestors 'none'")

		if secureCookies {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}

		next.ServeHTTP(w, r)
	})
}

// RequireAuth returns middleware that validates the session cookie and
// injects the authenticated user into the request context.
func RequireAuth(sa auth.SessionAuthenticator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(auth.SessionCookieName)
			if err != nil || cookie.Value == "" {
				writeAuthError(w, http.StatusUnauthorized, "authentication required")
				return
			}

			tokenHash := auth.HashSessionToken(cookie.Value)
			user, err := sa.AuthenticateSession(r.Context(), tokenHash)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid or expired session")
				return
			}

			span := trace.SpanFromContext(r.Context())
			span.SetAttributes(attribute.String("user_id", user.ID.String()))

			next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), user)))
		})
	}
}

// RequireAdmin returns middleware that checks the authenticated user has
// the admin role. Must be applied after RequireAuth.
func RequireAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := auth.UserFromContext(r.Context())
			if user == nil || user.Role != "admin" {
				writeAuthError(w, http.StatusForbidden, "admin access required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeAuthError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
```

- [ ] **Step 4.2: Delete `internal/auth/middleware.go`**

```bash
rm /Users/btc/Projects/src/drill/internal/auth/middleware.go
```

- [ ] **Step 4.3: Update `internal/auth/sessions.go` (or wherever) to keep `WithUser`/`UserFromContext`/`AuthUser`/`SessionCookieName`/`SessionAuthenticator`**

These types were defined in `auth/middleware.go` but are pure data types and interfaces — they belong in the `auth` package, not the `handler` package.

Run: `grep -n "type AuthUser\|type contextKey\|userContextKey\|func WithUser\|func UserFromContext\|SessionCookieName\|type SessionAuthenticator" /Users/btc/Projects/src/drill/internal/auth/*.go`

If they're already gone (because you deleted `middleware.go` in step 4.2), recreate them in a new file `internal/auth/user_context.go`:

```go
package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// AuthUser is the authenticated user extracted from the session cookie.
type AuthUser struct {
	ID            uuid.UUID
	Email         string
	DisplayName   string
	Role          string
	Plan          string
	EmailVerified bool
	CreatedAt     time.Time
}

type contextKey string

const userContextKey contextKey = "auth_user"

// WithUser stores the authenticated user in the context.
func WithUser(ctx context.Context, user *AuthUser) context.Context {
	return context.WithValue(ctx, userContextKey, user)
}

// UserFromContext extracts the authenticated user from the context.
// Returns nil if no user is present.
func UserFromContext(ctx context.Context) *AuthUser {
	user, _ := ctx.Value(userContextKey).(*AuthUser)
	return user
}

const SessionCookieName = "drill_session"

// SessionAuthenticator validates a hashed session token and returns the
// authenticated user. Implemented by *backend.Backend.
type SessionAuthenticator interface {
	AuthenticateSession(ctx context.Context, tokenHash string) (*AuthUser, error)
}
```

- [ ] **Step 4.4: Update `internal/handler/routes.go` to use local references**

Find these lines in `RegisterRoutes`:

```go
	requireAuth := auth.RequireAuth(b)
	requireAdmin := auth.RequireAdmin()
```

Change to:

```go
	requireAuth := RequireAuth(b)
	requireAdmin := RequireAdmin()
```

If the `auth` import is no longer used in `routes.go` after this change, remove it. Check: `grep -n "auth\." /Users/btc/Projects/src/drill/internal/handler/routes.go`. If the only remaining `auth.` reference is in the import block, remove the import. (It will be — `auth.DeriveKey` was deleted in Task 1, and `RequireAuth`/`RequireAdmin` are now local.)

- [ ] **Step 4.5: Delete `internal/handler/security.go`**

```bash
rm /Users/btc/Projects/src/drill/internal/handler/security.go
```

- [ ] **Step 4.6: Create `internal/handler/middleware_test.go` (merged tests)**

Combines `auth/middleware_test.go` + `handler/security_test.go`, repackaged as `handler_test`, references updated to local types:

```go
package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/handler"
)

// --- SecurityHeaders ---

func TestSecurityHeaders_AlwaysPresent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := handler.SecurityHeaders(false, mux)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))
	assert.Equal(t, "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https://storage.googleapis.com; connect-src 'self' wss:; font-src 'self'; frame-ancestors 'none'", w.Header().Get("Content-Security-Policy"))
	assert.Empty(t, w.Header().Get("Strict-Transport-Security"), "HSTS should not be set for non-secure")
}

func TestSecurityHeaders_HSTSWhenSecure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	h := handler.SecurityHeaders(true, mux)

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, "max-age=63072000; includeSubDomains", w.Header().Get("Strict-Transport-Security"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", w.Header().Get("Referrer-Policy"))
}

// --- RequireAuth / RequireAdmin ---

func TestRequireAuth_NoCookie(t *testing.T) {
	h := handler.RequireAuth(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestRequireAdmin_NoUser(t *testing.T) {
	h := handler.RequireAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/jobs", nil)
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireAdmin_CandidateRole(t *testing.T) {
	h := handler.RequireAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/jobs", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.AuthUser{ID: uuid.New(), Role: "candidate"}))
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestRequireAdmin_AdminRole(t *testing.T) {
	h := handler.RequireAdmin()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/admin/jobs", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.AuthUser{ID: uuid.New(), Role: "admin"}))
	h.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// --- Pure auth context tests (kept here since they're tightly coupled to RequireAuth) ---

func TestAuthUser_FromContext(t *testing.T) {
	user := &auth.AuthUser{
		ID:    uuid.New(),
		Email: "test@example.com",
		Role:  "candidate",
	}
	ctx := auth.WithUser(context.Background(), user)

	got := auth.UserFromContext(ctx)
	require.NotNil(t, got)
	require.Equal(t, user.ID, got.ID)
	require.Equal(t, user.Email, got.Email)
}

func TestAuthUser_FromContext_Missing(t *testing.T) {
	got := auth.UserFromContext(context.Background())
	require.Nil(t, got)
}
```

- [ ] **Step 4.7: Delete `internal/auth/middleware_test.go` and `internal/handler/security_test.go`**

```bash
rm /Users/btc/Projects/src/drill/internal/auth/middleware_test.go
rm /Users/btc/Projects/src/drill/internal/handler/security_test.go
```

- [ ] **Step 4.8: Find any other callers of `auth.RequireAuth` or `auth.RequireAdmin`**

Run: `cd /Users/btc/Projects/src/drill && grep -rn "auth\.RequireAuth\|auth\.RequireAdmin" --include="*.go"`
Expected: only `internal/handler/routes.go` (already fixed in step 4.4).

If there are other callers, change `auth.RequireAuth` → `handler.RequireAuth` (and import `internal/handler`) at those sites. Do not change the function signature.

- [ ] **Step 4.9: Verify build and tests**

Run: `cd /Users/btc/Projects/src/drill && go build ./... && go test ./internal/handler/... ./internal/auth/... -race -count=1 -timeout=120s`
Expected: build succeeds, tests pass.

- [ ] **Step 4.10: Run full CI**

Run: `cd /Users/btc/Projects/src/drill && make test`
Expected: green.

- [ ] **Step 4.11: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add internal/handler/middleware.go internal/handler/middleware_test.go internal/handler/routes.go internal/auth/user_context.go
git rm internal/auth/middleware.go internal/auth/middleware_test.go internal/handler/security.go internal/handler/security_test.go
git commit -m "$(cat <<'EOF'
refactor: move HTTP middleware to handler package

Move RequireAuth, RequireAdmin, and SecurityHeaders into a single
handler/middleware.go. Auth context types (AuthUser, WithUser,
UserFromContext, SessionCookieName, SessionAuthenticator) stay in the
auth package as identity primitives. Tests merged into
handler/middleware_test.go.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Split `routes.go` into `server.go` + `spa.go`; rename `routes_test.go`

Extract `SPAHandler`, `ogRoute`, `ogRoutes`, and OG injection into `spa.go`. Rename `routes.go` → `server.go` (now contains only `NewHandler` and `RegisterRoutes`). Rename `routes_test.go` → `spa_test.go` (it only tests `SPAHandler`).

**Files:**
- Create: `internal/handler/spa.go`
- Rename: `internal/handler/routes.go` → `internal/handler/server.go`
- Rename: `internal/handler/routes_test.go` → `internal/handler/spa_test.go`

- [ ] **Step 5.1: Create `internal/handler/spa.go`**

Write this content (the SPA-related code extracted from `routes.go`):

```go
package handler

import (
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/btc/drill/internal/branding"
)

// ogRoute defines OG meta tag content for a public route.
type ogRoute struct {
	title       string
	description string
	image       string // path relative to base URL
}

var ogRoutes = map[string]ogRoute{
	"/": {
		title:       branding.AppName,
		description: "data-driven system design prep",
		image:       "/og-landing.png",
	},
	"/about": {
		title:       branding.AppName,
		description: "data-driven system design prep",
		image:       "/og-landing.png",
	},
	"/sample": {
		title:       branding.AppName + " — sample evaluation",
		description: "See a real system design interview evaluated across 5 dimensions",
		image:       "/og-sample.png",
	},
}

// SPAHandler serves the embedded SPA. Static assets served directly.
// All other paths return index.html for client-side routing.
// For paths with OG tags defined, the tags are injected before </head>.
// baseURL is the public URL (e.g., "https://sabermatic.dev") used for
// absolute og:url and og:image values. Pass "" for tests.
func SPAHandler(fsys fs.FS, baseURL string) (http.Handler, error) {
	sub, err := fs.Sub(fsys, "web/dist")
	if err != nil {
		return nil, fmt.Errorf("embed sub: %w", err)
	}

	indexBytes, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		return nil, fmt.Errorf("read index.html: %w", err)
	}
	indexHTML := string(indexBytes)

	if !strings.Contains(indexHTML, "</head>") {
		slog.Warn("index.html missing </head> — OG tags will not be injected")
	}

	// Pre-compute OG-injected HTML at init time
	ogPages := make(map[string][]byte, len(ogRoutes))
	for path, og := range ogRoutes {
		tags := fmt.Sprintf(
			`<meta property="og:title" content="%s">`+
				`<meta property="og:description" content="%s">`+
				`<meta property="og:type" content="website">`+
				`<meta property="og:url" content="%s%s">`+
				`<meta property="og:image" content="%s%s">`,
			html.EscapeString(og.title), html.EscapeString(og.description),
			html.EscapeString(baseURL), html.EscapeString(path),
			html.EscapeString(baseURL), html.EscapeString(og.image),
		)
		ogPages[path] = []byte(strings.Replace(indexHTML, "</head>", tags+"</head>", 1))
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			if _, err := fs.Stat(sub, strings.TrimPrefix(path, "/")); err == nil {
				// Hashed asset files are immutable and can be cached forever
				if strings.Contains(path, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// Serve pre-computed OG-injected HTML if this path has OG tags
		if body, ok := ogPages[path]; ok {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			_, _ = w.Write(body)
			return
		}

		// Fall back to index.html for client-side routing
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	}), nil
}
```

- [ ] **Step 5.2: Rewrite `internal/handler/routes.go` (will be renamed to `server.go` next)**

Replace with just the assembly + registration code:

```go
package handler

import (
	"fmt"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/rpc"
)

// NewHandler builds the full HTTP handler chain: routes, OTel tracing, and
// security headers. Returns a ready-to-use http.Handler.
func NewHandler(b *backend.Backend, spaFS fs.FS) (http.Handler, error) {
	cfg := b.Config()
	baseURL := cfg.Auth.BaseURL
	secureCookies := cfg.Auth.SecureCookies()

	mux := http.NewServeMux()
	if err := RegisterRoutes(mux, b); err != nil {
		return nil, fmt.Errorf("register routes: %w", err)
	}
	spaHandler, err := SPAHandler(spaFS, baseURL)
	if err != nil {
		return nil, fmt.Errorf("spa handler: %w", err)
	}
	mux.Handle("/", spaHandler)

	otelHandler := otelhttp.NewMiddleware(drilotel.AppName,
		otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
			if r.Pattern != "" {
				return r.Pattern
			}
			return r.Method + " " + r.URL.Path
		}),
		otelhttp.WithFilter(func(r *http.Request) bool {
			for _, prefix := range rpc.ConnectPathPrefixes() {
				if strings.HasPrefix(r.URL.Path, "/"+prefix+"/") {
					return false
				}
			}
			return true
		}),
	)(mux)

	return SecurityHeaders(secureCookies, otelHandler), nil
}

// RegisterRoutes sets up all HTTP routes on the given mux.
// Used by NewHandler for production and directly by tests.
//
// Security model: this app does not use anti-CSRF tokens. ConnectRPC's
// Connect-Protocol-Version custom header forces a CORS preflight, which
// the absence of permissive CORS config blocks; the SPA is served
// same-origin; session cookies are SameSite=Lax; the Stripe webhook is
// signature-verified; OAuth callbacks use the state parameter. If a future
// cookie-authenticated REST mutation endpoint is added, wrap it in a
// per-route CSRF helper at registration time.
func RegisterRoutes(mux *http.ServeMux, b *backend.Backend) error {
	if err := rpc.Register(mux, b); err != nil {
		return fmt.Errorf("rpc register: %w", err)
	}

	mux.HandleFunc("GET /api/health", Health(b))

	// Admin — requires both auth and admin role.
	requireAuth := RequireAuth(b)
	requireAdmin := RequireAdmin()
	mux.Handle("/admin/jobs/", requireAuth(requireAdmin(b.RiverUIHandler())))

	// OAuth
	mux.HandleFunc("GET /api/auth/oauth/{provider}", OAuthStart(b))
	mux.HandleFunc("GET /api/auth/oauth/{provider}/callback", OAuthCallback(b))

	// Stripe webhook — signature-verified by the handler.
	mux.HandleFunc("POST /api/webhooks/stripe", PostStripeWebhook(b))

	// Local-dev storage server: serve uploaded files via HTTP so the browser
	// can load them. In production (cfg.Storage.Backend == "gcs"), files are
	// served directly from GCS by signed URLs.
	if b.Config().Storage.Backend == "local" {
		mux.Handle("/storage/", http.StripPrefix("/storage/",
			http.FileServer(http.Dir(b.Config().Storage.LocalDir))))
	}

	return nil
}
```

Note: this requires `io/fs` import for the `fs.FS` parameter — add it. Final import list should be:

```go
import (
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/rpc"
)
```

- [ ] **Step 5.3: Rename `routes.go` → `server.go`**

```bash
git mv /Users/btc/Projects/src/drill/internal/handler/routes.go /Users/btc/Projects/src/drill/internal/handler/server.go
```

- [ ] **Step 5.4: Rename `routes_test.go` → `spa_test.go`**

```bash
git mv /Users/btc/Projects/src/drill/internal/handler/routes_test.go /Users/btc/Projects/src/drill/internal/handler/spa_test.go
```

- [ ] **Step 5.5: Verify build and tests**

Run: `cd /Users/btc/Projects/src/drill && go build ./... && go test ./internal/handler/... -race -count=1 -timeout=120s`
Expected: build succeeds, tests pass.

- [ ] **Step 5.6: Run full CI**

Run: `cd /Users/btc/Projects/src/drill && make test`
Expected: green.

- [ ] **Step 5.7: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add internal/handler/spa.go internal/handler/server.go internal/handler/spa_test.go
git commit -m "$(cat <<'EOF'
refactor: split routes.go into server.go and spa.go

server.go now contains only NewHandler and RegisterRoutes (with a
security-model comment explaining why there is no CSRF middleware).
SPA serving and OG-tag injection move to spa.go. Tests rename to
spa_test.go to match.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Collapse `NewHandler` + `RegisterRoutes` into single entry point

Migrate the 8 test sites that call `handler.RegisterRoutes(mux, b)` to call `handler.NewHandler(b, fstest.MapFS{...})` instead. Demote `RegisterRoutes` to unexported `registerRoutes`. Tests now use the same handler chain as production (SecurityHeaders + OTel).

**Files:**
- Modify: `internal/handler/server.go` (rename `RegisterRoutes` → `registerRoutes`)
- Modify: `internal/testutil/testutil.go`
- Modify: `internal/handler/health_test.go`
- Modify: `internal/handler/oauth_flow_test.go` (5 call sites)

- [ ] **Step 6.1: Capture pre-migration timing baseline**

Run: `cd /Users/btc/Projects/src/drill && go test ./internal/handler/... -count=1 -race -timeout=120s 2>&1 | tail -5`

Record the wall-clock time printed (e.g., `ok  github.com/btc/drill/internal/handler  12.345s`). Save this number — you'll compare after migration to check for >20% regression.

Baseline: ____ seconds.

- [ ] **Step 6.2: Add a test helper for building handlers**

Add this helper to `internal/testutil/testutil.go`. Place it after `SignupAndLogin`:

```go
// minimalIndexHTML is the bare HTML used for handler tests. SPAHandler reads
// this from the fstest.MapFS during NewHandler construction.
const minimalIndexHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"></head><body></body></html>`

// NewTestHandler builds the production HTTP handler with a minimal in-memory
// SPA. Tests call this instead of handler.RegisterRoutes so they exercise the
// same middleware chain as production (SecurityHeaders, OTel tracing).
func NewTestHandler(t *testing.T, b *backend.Backend) http.Handler {
	t.Helper()
	fsys := fstest.MapFS{
		"web/dist/index.html": &fstest.MapFile{Data: []byte(minimalIndexHTML)},
	}
	h, err := handler.NewHandler(b, fsys)
	require.NoError(t, err)
	return h
}
```

Add `"testing/fstest"` to the imports of `testutil.go`.

- [ ] **Step 6.3: Update `SignupAndLogin` in `testutil.go` to use the new helper**

In `internal/testutil/testutil.go`, replace:

```go
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
```

with:

```go
	srv := httptest.NewServer(NewTestHandler(t, b))
	t.Cleanup(srv.Close)
```

After this, `testutil.go` no longer references `handler.RegisterRoutes` directly except via `NewTestHandler`.

- [ ] **Step 6.4: Update `internal/handler/health_test.go`**

Replace:

```go
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
```

with:

```go
	h := testutil.NewTestHandler(t, b)

	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
```

Remove the `"github.com/btc/drill/internal/handler"` import if no longer used; add `"github.com/btc/drill/internal/testutil"` if missing.

- [ ] **Step 6.5: Update `internal/handler/oauth_flow_test.go` — all 5 call sites**

For each occurrence of:

```go
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))
```

(at the spec-documented lines 56, 80, 118, 161, 188), replace with:

```go
	h := testutil.NewTestHandler(t, b)
```

Then update every subsequent reference in that test from `mux` to `h` (e.g., `mux.ServeHTTP(w, req)` → `h.ServeHTTP(w, req)`, `httptest.NewServer(mux)` → `httptest.NewServer(h)`).

After all 5 edits, run: `cd /Users/btc/Projects/src/drill && grep -n "mux\|RegisterRoutes" internal/handler/oauth_flow_test.go`
Expected: no matches (or only matches in unrelated comments).

- [ ] **Step 6.6: Demote `RegisterRoutes` to unexported in `internal/handler/server.go`**

Find both occurrences of `RegisterRoutes` in `server.go` and rename to `registerRoutes` (lowercase):

```go
	if err := registerRoutes(mux, b); err != nil {  // inside NewHandler
```

and:

```go
func registerRoutes(mux *http.ServeMux, b *backend.Backend) error {  // function definition
```

The doc comment on `registerRoutes` no longer mentions tests:

```go
// registerRoutes sets up all HTTP routes on the given mux.
//
// Security model: ...
```

- [ ] **Step 6.7: Verify no external callers of `handler.RegisterRoutes` remain**

Run: `cd /Users/btc/Projects/src/drill && grep -rn "handler\.RegisterRoutes\|RegisterRoutes" --include="*.go"`
Expected: only the unexported `registerRoutes` definition and its single call site inside `NewHandler`. No external callers.

- [ ] **Step 6.8: Verify build and tests**

Run: `cd /Users/btc/Projects/src/drill && go build ./... && go test ./internal/handler/... ./internal/testutil/... -race -count=1 -timeout=120s`
Expected: build succeeds, tests pass.

- [ ] **Step 6.9: Capture post-migration timing and compare**

Run: `cd /Users/btc/Projects/src/drill && go test ./internal/handler/... -count=1 -race -timeout=120s 2>&1 | tail -5`

Compare to the baseline from Step 6.1. If the wall-clock time grew by more than 20%, the per-test cost of `otelhttp.NewMiddleware` instantiation matters. In that case, modify `NewTestHandler` to skip the OTel middleware:

```go
func NewTestHandler(t *testing.T, b *backend.Backend) http.Handler {
	t.Helper()
	fsys := fstest.MapFS{
		"web/dist/index.html": &fstest.MapFile{Data: []byte(minimalIndexHTML)},
	}
	// Tests use the production handler chain except for OTel middleware,
	// which adds non-trivial per-call overhead and is not under test.
	h, err := handler.NewTestHandlerNoOtel(b, fsys)
	require.NoError(t, err)
	return h
}
```

And add a corresponding `NewTestHandlerNoOtel` to `server.go`:

```go
// NewTestHandlerNoOtel is the same as NewHandler but skips otelhttp wrapping.
// Tests use this to avoid the per-request OTel middleware cost.
func NewTestHandlerNoOtel(b *backend.Backend, spaFS fs.FS) (http.Handler, error) {
	cfg := b.Config()
	baseURL := cfg.Auth.BaseURL
	secureCookies := cfg.Auth.SecureCookies()

	mux := http.NewServeMux()
	if err := registerRoutes(mux, b); err != nil {
		return nil, fmt.Errorf("register routes: %w", err)
	}
	spaHandler, err := SPAHandler(spaFS, baseURL)
	if err != nil {
		return nil, fmt.Errorf("spa handler: %w", err)
	}
	mux.Handle("/", spaHandler)

	return SecurityHeaders(secureCookies, mux), nil
}
```

If the regression is under 20%, leave it as a single entry point.

Document the chosen path here when executing: ____ (single-entry / split-with-NoOtel).

- [ ] **Step 6.10: Run full CI**

Run: `cd /Users/btc/Projects/src/drill && make test`
Expected: green.

- [ ] **Step 6.11: Browser smoke test (signup, session create)**

Run `make dev`. Sign up a new user, create a new interview session, verify the page loads and the cookie persists.

- [ ] **Step 6.12: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add internal/handler/server.go internal/handler/health_test.go internal/handler/oauth_flow_test.go internal/testutil/testutil.go
git commit -m "$(cat <<'EOF'
refactor: collapse handler.RegisterRoutes into NewHandler

Tests now build the production handler chain via testutil.NewTestHandler
instead of registering routes onto a bare mux. Single entry point;
RegisterRoutes is unexported. Higher-fidelity tests, fewer questions
about which API to use.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Rename `billing.go` → `stripe.go`; delete `auth.go`; add Stripe webhook test (TDD)

Rename for accuracy. Delete dead `writeJSON`/`writePaidBalanceRequired`. Add a real test for `PostStripeWebhook` covering signature-verify happy path, signature-verify failure, and missing-secret behavior.

**Files:**
- Rename: `internal/handler/billing.go` → `internal/handler/stripe.go`
- Create: `internal/handler/stripe_test.go`
- Delete: `internal/handler/auth.go`

- [ ] **Step 7.1: Verify `writeJSON` and `writePaidBalanceRequired` are unused**

Run: `cd /Users/btc/Projects/src/drill && grep -rn "writeJSON\|writePaidBalanceRequired" --include="*.go" internal/ cmd/`
Expected: only the definitions in `internal/handler/auth.go`. No callers.

If you find a caller, escalate before deleting.

- [ ] **Step 7.2: Delete `internal/handler/auth.go`**

```bash
git rm /Users/btc/Projects/src/drill/internal/handler/auth.go
```

- [ ] **Step 7.3: Rename `billing.go` → `stripe.go`**

```bash
git mv /Users/btc/Projects/src/drill/internal/handler/billing.go /Users/btc/Projects/src/drill/internal/handler/stripe.go
```

Also remove the now-stale comment at the top of the file. Open `internal/handler/stripe.go` and delete this line:

```go
// (PostCheckout and PostPortal migrated to ConnectRPC BillingService)
```

- [ ] **Step 7.4: Verify build still passes after deletions**

Run: `cd /Users/btc/Projects/src/drill && go build ./...`
Expected: success.

- [ ] **Step 7.5: Write the failing Stripe webhook test (TDD red phase)**

Create `internal/handler/stripe_test.go`:

```go
package handler_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/btc/drill/internal/handler"
)

const testWebhookSecret = "whsec_test_secret_for_unit_tests_only_must_be_at_least_some_length"

func TestStripeWebhook_ValidSignature(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	cfg := b.Config()
	cfg.Stripe.WebhookSecret = testWebhookSecret
	b.SetConfig(cfg)

	// A minimal Stripe event payload. The handler routes by event type;
	// "ping" is unknown so HandleStripeWebhook returns nil (success).
	payload := []byte(`{"id":"evt_test_1","type":"ping","data":{"object":{}}}`)
	signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
		Payload: payload,
		Secret:  testWebhookSecret,
	})

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/stripe", bytes.NewReader(signed.Payload))
	req.Header.Set("Stripe-Signature", signed.Header)

	w := httptest.NewRecorder()
	handler.PostStripeWebhook(b)(w, req)

	require.Equal(t, http.StatusOK, w.Code)
}

func TestStripeWebhook_InvalidSignature(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	cfg := b.Config()
	cfg.Stripe.WebhookSecret = testWebhookSecret
	b.SetConfig(cfg)

	payload := []byte(`{"id":"evt_test_2","type":"ping","data":{"object":{}}}`)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set("Stripe-Signature", "t=0,v1=invalid_signature")

	w := httptest.NewRecorder()
	handler.PostStripeWebhook(b)(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestStripeWebhook_MissingSecret(t *testing.T) {
	t.Parallel()

	b := pg.NewBackend(t)
	cfg := b.Config()
	cfg.Stripe.WebhookSecret = "" // explicitly unset
	b.SetConfig(cfg)

	payload := []byte(`{"id":"evt_test_3","type":"ping","data":{"object":{}}}`)

	req := httptest.NewRequest(http.MethodPost, "/api/webhooks/stripe", bytes.NewReader(payload))
	req.Header.Set("Stripe-Signature", "t=0,v1=anything")

	w := httptest.NewRecorder()
	handler.PostStripeWebhook(b)(w, req)

	// Deliberate: returns 200 to prevent Stripe from retrying when the secret
	// is misconfigured. The misconfiguration is logged for ops to detect.
	require.Equal(t, http.StatusOK, w.Code)
}
```

- [ ] **Step 7.6: Run the new tests to verify they pass**

Run: `cd /Users/btc/Projects/src/drill && go test ./internal/handler/ -run TestStripeWebhook -race -count=1 -timeout=60s -v`
Expected: all three tests pass.

If `TestStripeWebhook_ValidSignature` fails because `b.HandleStripeWebhook` errors on the unknown "ping" event type, switch the test event to a real Stripe event type that `HandleStripeWebhook` accepts as a no-op or success. Open `internal/backend/billing.go` (or wherever `HandleStripeWebhook` lives) and find the switch on `event.Type`. Pick a type that returns `nil` (e.g., an event type that's logged but not acted on); update the test payload accordingly.

If you can't find a good no-op event type, the cleanest alternative is to use `customer.subscription.deleted` for a non-existent customer (handler should return nil for unknown customers per the spec's mention "HandleStripeWebhook returns nil for permanent non-errors (unknown customer, duplicate event)").

- [ ] **Step 7.7: Verify `webhook.GenerateTestSignedPayload` is the correct API**

If `webhook.GenerateTestSignedPayload` doesn't compile, run: `cd /Users/btc/Projects/src/drill && grep -rn "GenerateTestSignedPayload\|UnsignedPayload" --include="*.go" $(go env GOMODCACHE)/github.com/stripe/stripe-go/v82@*/webhook/ 2>/dev/null | head -10`

The signature in stripe-go/v82 v82.5.1 is:
```go
func GenerateTestSignedPayload(p *UnsignedPayload) *SignedPayload
type UnsignedPayload struct { Payload []byte; Secret string; Timestamp time.Time; Scheme string }
type SignedPayload struct { Payload []byte; Header string; Secret string; Timestamp time.Time }
```

If the test uses `Timestamp` zero value and the verifier rejects it as too old, set an explicit timestamp:

```go
import "time"
// ...
signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{
    Payload:   payload,
    Secret:    testWebhookSecret,
    Timestamp: time.Now(),
})
```

- [ ] **Step 7.8: Verify the rename and deletion didn't break anything else**

Run: `cd /Users/btc/Projects/src/drill && go build ./... && go test ./internal/handler/... -race -count=1 -timeout=120s`
Expected: green.

- [ ] **Step 7.9: Run full CI**

Run: `cd /Users/btc/Projects/src/drill && make test`
Expected: green.

- [ ] **Step 7.10: Browser smoke test for Stripe webhook**

In one terminal: `make dev`.
In another: `stripe listen --forward-to localhost:8080/api/webhooks/stripe`.
In a third: `stripe trigger checkout.session.completed` (or similar).

Verify the dev server logs show the webhook was received and processed without error.

- [ ] **Step 7.11: Commit**

```bash
cd /Users/btc/Projects/src/drill
git add internal/handler/stripe.go internal/handler/stripe_test.go
git rm internal/handler/billing.go internal/handler/auth.go
git commit -m "$(cat <<'EOF'
refactor: rename billing.go to stripe.go; delete dead handler/auth.go; add stripe_test.go

handler/billing.go was a misnomer — billing logic moved to ConnectRPC
months ago; only the Stripe webhook remained. handler/auth.go contained
only writeJSON and writePaidBalanceRequired, both dead post-migration.

Adds first test coverage for PostStripeWebhook: valid signature, invalid
signature, and missing-secret behavior.

Co-Authored-By: Claude Opus 4.6 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Final verification

After all 7 tasks are committed:

- [ ] **Final.1: Full CI**

Run: `cd /Users/btc/Projects/src/drill && make test`
Expected: green.

- [ ] **Final.2: Confirm final file layout matches spec**

Run: `cd /Users/btc/Projects/src/drill && ls internal/handler/`
Expected (no specific order): `health.go`, `health_test.go`, `main_test.go`, `middleware.go`, `middleware_test.go`, `oauth.go`, `oauth_flow_test.go`, `server.go`, `spa.go`, `spa_test.go`, `stripe.go`, `stripe_test.go`, plus `data/` directory.

Should NOT exist: `auth.go`, `billing.go`, `csrf_test.go`, `oauth_test.go`, `routes.go`, `routes_test.go`, `security.go`, `security_test.go`.

Run: `cd /Users/btc/Projects/src/drill && ls internal/auth/`
Expected: `keys.go`, `keys_test.go`, `oauth.go`, `oauth_test.go`, `passwords.go`, `passwords_test.go`, `sessions.go`, `sessions_test.go`, `tokens.go`, `tokens_test.go`, `user_context.go` (created in Task 4.3 if it was needed — verify).

Should NOT exist: `middleware.go`, `middleware_test.go`.

- [ ] **Final.3: Confirm `gorilla/csrf` is gone**

Run: `cd /Users/btc/Projects/src/drill && grep -rn "gorilla/csrf\|csrfMiddleware\|isCSRFExempt\|sabermatic_csrf\|__csrfToken\|X-CSRF-Token" --include="*.go" --include="*.ts" --include="*.tsx" .`
Expected: no matches in `internal/`, `cmd/`, `web/src/`. Possible matches in `docs/csrf-audit.md` (the stub explanation) and `docs/superpowers/` historical plans — those are fine.

- [ ] **Final.4: Final commit if any drift was found**

If steps Final.1-3 surfaced issues, fix and commit. Otherwise the refactor is complete.
