// analytics.ts — fire-and-forget analytics event emission.
//
// Uses navigator.sendBeacon with a fetch fallback. Never throws; failures are
// silently swallowed so analytics never disrupts the user experience.

const BEACON_URL = "/api/beacon";

type NoPropsEvent = {
  event: "landing_view" | "sample_view" | "email_verified" | "first_message_sent";
};

type PropsEvent = {
  event: "signup_started";
  props: { auth_method: "password" | "google" | "github" };
};

export type TrackArgs = NoPropsEvent | PropsEvent;

/**
 * Emit a client-side analytics event to the backend beacon endpoint.
 * Fire-and-forget: the function returns immediately and never throws.
 */
export function track(args: TrackArgs): void {
  try {
    const body = JSON.stringify(args);
    if (navigator.sendBeacon) {
      navigator.sendBeacon(BEACON_URL, new Blob([body], { type: "application/json" }));
    } else {
      // Fetch fallback for environments without sendBeacon (e.g. older Safari).
      fetch(BEACON_URL, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
        keepalive: true,
      }).catch(() => {
        // Swallow — analytics must never surface errors to the user.
      });
    }
  } catch {
    // Swallow all errors including JSON.stringify failures.
  }
}
