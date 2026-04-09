# River UI Installation Design

**Date:** 2026-04-09
**Status:** Approved

## Goal

Mount River UI at `/admin/jobs/` in the existing server, protected by the existing admin auth middleware. Accessible in production.

## Approach

Backend owns the riverui lifecycle, consistent with how it already owns the `river.Client` lifecycle. No changes to `handler.NewHandler` or `main.go` signatures.

## Changes

### `go.mod`

Add `riverqueue.com/riverui` (v0.15.0 already fetched). After adding the import in `backend.go`, run `go mod tidy` to promote `riverqueue.com/riverui` from `// indirect` to a direct dependency.

### `internal/backend/backend.go`

**Imports:** Add `"riverqueue.com/riverui"` as a direct import.

**Struct:** Add two fields:

```go
type Backend struct {
    pool         *pgxpool.Pool
    jobs         Jobs
    riverUI      http.Handler
    closeRiverUI context.CancelFunc
    cfg          *config.Config
    ...
}
```

**`New()`:** After `riverClient.Start(...)`, construct and start the riverui handler. Pass `nil` for `EndpointsOpts` (the library substitutes an empty value internally — no extra import required):

```go
uiCtx, closeRiverUI := context.WithCancel(context.Background())
endpoints := riverui.NewEndpoints(riverClient, nil)
uiHandler, err := riverui.NewHandler(&riverui.HandlerOpts{
    Endpoints: endpoints,
    Prefix:    "/admin/jobs",
    Logger:    slog.Default(),
})
if err != nil {
    closeRiverUI()
    riverClient.Stop(context.Background())
    pool.Close()
    return nil, fmt.Errorf("riverui handler: %w", err)
}
if err := uiHandler.Start(uiCtx); err != nil {
    closeRiverUI()
    riverClient.Stop(context.Background())
    pool.Close()
    return nil, fmt.Errorf("start riverui: %w", err)
}
```

Store in the returned struct: `riverUI: uiHandler, closeRiverUI: closeRiverUI`.

**`Close()`:** Call `b.closeRiverUI()` before stopping the river client. No nil guard needed — `closeRiverUI` is always set by `New()` before any error branches, and `Close()` is only called on backends returned by `New()`.

```go
b.closeRiverUI()
// existing: b.jobs.Stop(...)
// existing: b.pool.Close()
```

**Accessor:**

```go
func (b *Backend) RiverUIHandler() http.Handler { return b.riverUI }
```

### `internal/handler/admin.go` + `internal/handler/routes.go`

These two changes must land in the same commit — deleting `admin.go` removes `AdminJobsPlaceholder`, and `routes.go` must be updated in the same pass or the package will not compile.

Delete `admin.go` entirely (it has no other content).

In `routes.go`, replace the placeholder mount:

```go
// Before:
mux.Handle("GET /admin/jobs", requireAuth(requireAdmin(AdminJobsPlaceholder())))

// After:
mux.Handle("/admin/jobs/", requireAuth(requireAdmin(b.RiverUIHandler())))
```

Pattern changes from `GET /admin/jobs` to `/admin/jobs/` (prefix match, all methods) so riverui's internal API routes (`/admin/jobs/api/...`) are covered by the same auth chain.

### `internal/handler/routes.go` — CSRF exemption

River UI makes POST requests internally (job cancel, retry, etc.) with `application/json` bodies. They must be exempt from gorilla/csrf.

In `csrfMiddleware`, add a **standalone** `strings.HasPrefix` check for `/admin/jobs/`. Do NOT insert into `rpc.ConnectPathPrefixes()` — that slice is formatted without a leading slash and the loop wraps each entry as `"/"+prefix+"/"`, which would produce `"//admin/jobs//"`.

```go
// Add before the ConnectRPC loop:
if strings.HasPrefix(r.URL.Path, "/admin/jobs/") {
    next.ServeHTTP(w, r)
    return
}
```

Add a `// TODO: flip csrfMiddleware to opt-in model` comment here. The current exemption list (Stripe, ConnectRPC, now riverui) is growing. The right long-term fix is to explicitly protect only routes that accept form submissions, rather than exempting everything else — but that's a focused security refactor, not in scope here.

## What Does Not Change

- `handler.NewHandler` signature
- `handler.RegisterRoutes` signature
- `cmd/drill/main.go`
- `backend.Jobs` interface
- All existing tests

## Security

The entire `/admin/jobs/` prefix is wrapped in `requireAuth(requireAdmin(...))` before CSRF is consulted — the auth chain runs after the mux dispatches, inside `next`. So even though the CSRF middleware exempts `/admin/jobs/`, unauthenticated requests are still rejected by auth. The exemption is safe because River UI uses `application/json` bodies, which cannot be submitted cross-origin by a simple HTML form without a CORS preflight.

## Testing

No new tests required. The riverui handler is a third-party library. Verify manually by loading `/admin/jobs/` in the browser after startup.
