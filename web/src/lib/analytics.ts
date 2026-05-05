// analytics.ts — fire-and-forget analytics event emission.
//
// Posts to /api/beacon, the canonical wire shape the backend expects:
//   { event_name, referrer, utm_source, utm_medium, utm_campaign, properties? }
//
// referrer comes from document.referrer (the only source of the EXTERNAL
// referrer for a beacon POST — the request's Referer header points at the SPA
// page that fired the beacon, which is useless for funnel attribution).
// utm_* come from the current URL's query params.
//
// Uses navigator.sendBeacon with a fetch fallback. Never throws; failures are
// silently swallowed so analytics never disrupts the user experience.

const BEACON_URL = "/api/beacon";

// Discriminated union enforces per-event required properties at type-check time.
// Only events in the backend allowlist (`internal/handler/beacon.go`) belong
// here — backend-emitted events (signup_completed, etc.) flow through their
// own server-side handlers, not the beacon.
export type TrackArgs =
  | { event: "landing_view" }
  | { event: "sample_view" }
  | { event: "signup_started"; props: { auth_method: "password" | "google" | "github" } };

interface BeaconPayload {
  event_name: string;
  referrer: string;
  utm_source: string;
  utm_medium: string;
  utm_campaign: string;
  properties?: Record<string, unknown>;
}

/**
 * Emit a client-side analytics event to the backend beacon endpoint.
 * Fire-and-forget: the function returns immediately and never throws.
 */
export function track(args: TrackArgs): void {
  try {
    const params = new URLSearchParams(window.location.search);
    const payload: BeaconPayload = {
      event_name: args.event,
      referrer: document.referrer || "",
      utm_source: params.get("utm_source") || "",
      utm_medium: params.get("utm_medium") || "",
      utm_campaign: params.get("utm_campaign") || "",
    };
    if ("props" in args) {
      payload.properties = args.props;
    }
    const body = JSON.stringify(payload);
    if (navigator.sendBeacon) {
      // sendBeacon returns false when the browser refuses to enqueue (queue
      // full, body too large, security restriction). Fall through to fetch
      // with keepalive in that case.
      const queued = navigator.sendBeacon(BEACON_URL, new Blob([body], { type: "application/json" }));
      if (queued) return;
    }
    // Fallback for environments without sendBeacon, or when sendBeacon refused.
    fetch(BEACON_URL, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body,
      keepalive: true,
    }).catch(() => {
      // Swallow — analytics must never surface errors to the user.
    });
  } catch {
    // Swallow all errors including JSON.stringify failures.
  }
}
