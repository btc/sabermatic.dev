import { useState } from "react";
import { Link } from "react-router-dom";

import { useAckKeptBanner, useMe } from "@/api/queries";

export function KeptBanner() {
  const { data: meData } = useMe();
  const [dismissed, setDismissed] = useState(false);
  const ack = useAckKeptBanner();

  const user = meData?.user;
  if (!user?.pendingKeptBanner || dismissed) return null;

  const handleDismiss = () => {
    setDismissed(true); // optimistic; banner hides immediately
    ack.mutate(
      {},
      {
        onError: (err) => {
          // Banner stays dismissed in this session; next page load reads
          // the server's still-true flag and re-shows the banner. Acceptable.
          console.warn("ackKeptBanner failed", err);
        },
      },
    );
  };

  return (
    <div
      role="alert"
      className="border-b border-border bg-muted/40 px-4 py-3 text-sm text-foreground"
    >
      <div className="mx-auto flex max-w-5xl items-center justify-between gap-4">
        <span>
          Welcome back — we kept your subscription active.{" "}
          <Link to="/settings/billing" className="underline hover:no-underline">
            Manage subscription
          </Link>
        </span>
        <button
          type="button"
          onClick={handleDismiss}
          aria-label="Dismiss"
          className="cursor-pointer rounded px-2 text-muted-foreground hover:bg-muted/60 hover:text-foreground"
        >
          ×
        </button>
      </div>
    </div>
  );
}
