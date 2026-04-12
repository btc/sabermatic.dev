# CSRF Audit (deprecated)

CSRF middleware was removed on 2026-04-12. Browser-based CSRF protection
in this app comes from ConnectRPC's `Connect-Protocol-Version` custom
header (forcing CORS preflight), same-origin SPA deployment, and
SameSite=Lax session cookies. The Stripe webhook is signature-verified;
OAuth callbacks use the state parameter.

See [`docs/superpowers/specs/2026-04-12-http-package-refactor-design.md`](./superpowers/specs/2026-04-12-http-package-refactor-design.md) for the rationale and full security model.
