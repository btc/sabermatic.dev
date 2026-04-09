# River UI Installation Design

**Date:** 2026-04-09
**Status:** Approved

## Goal

Mount River UI at `/admin/jobs/` in the existing server, protected by the existing admin auth middleware. Accessible in production.

## Approach

Backend owns the riverui lifecycle, consistent with how it already owns the `river.Client` lifecycle. No changes to `handler.NewHandler` or `main.go` signatures.

## Changes

### `go.mod`

Add `riverqueue.com/riverui` (v0.15.0 already fetched).

### `internal/backend/backend.go`

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

**`New()`:** After `riverClient.Start(...)`, construct and start the riverui handler:

```go
uiCtx, closeRiverUI := context.WithCancel(context.Background())
endpoints := riverui.NewEndpoints(riverClient, &riverui.EndpointsOpts[pgx.Tx]{})
uiHandler, err := riverui.NewHandler(&riverui.HandlerOpts{
    Endpoints: endpoints,
    Prefix:    "/admin/jobs",
    Logger:    slog.Default(),
})
if err != nil {
    riverClient.Stop(context.Background())
    pool.Close()
    return nil, fmt.Errorf("riverui handler: %w", err)
}
if err := uiHandler.Start(uiCtx); err != nil {
    riverClient.Stop(context.Background())
    pool.Close()
    return nil, fmt.Errorf("start riverui: %w", err)
}
```

Store in the returned struct: `riverUI: uiHandler, closeRiverUI: closeRiverUI`.

**`Close()`:** Call `b.closeRiverUI()` before stopping the river client.

**Accessor:**

```go
func (b *Backend) RiverUIHandler() http.Handler { return b.riverUI }
```

### `internal/handler/admin.go`

Delete `AdminJobsPlaceholder` and the file entirely. It has no other content.

### `internal/handler/routes.go`

Replace the placeholder mount:

```go
// Before:
mux.Handle("GET /admin/jobs", requireAuth(requireAdmin(AdminJobsPlaceholder())))

// After:
mux.Handle("/admin/jobs/", requireAuth(requireAdmin(b.RiverUIHandler())))
```

Note: pattern changes from `GET /admin/jobs` to `/admin/jobs/` (prefix match, all methods) so riverui's internal API routes are covered.

## What Does Not Change

- `handler.NewHandler` signature
- `handler.RegisterRoutes` signature
- `cmd/drill/main.go`
- `backend.Jobs` interface
- All existing tests

## CSRF Exemption

River UI makes POST requests internally (job cancel, retry, etc.). These go to `/admin/jobs/api/...`. They must be exempt from gorilla/csrf. Add `/admin/jobs/` to the CSRF exemption filter in `csrfMiddleware` alongside the existing ConnectRPC exemptions.

## Security

The entire `/admin/jobs/` prefix is wrapped in `requireAuth(requireAdmin(...))` before CSRF is applied, so only authenticated admin users reach River UI at all. The CSRF exemption is safe because River UI uses `application/json` bodies (not `application/x-www-form-urlencoded`), which cannot be submitted cross-origin by a simple HTML form.

## Testing

No new tests required. The riverui handler is a third-party library. Verify manually by loading `/admin/jobs/` in the browser after startup.
