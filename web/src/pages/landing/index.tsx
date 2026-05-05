import { useEffect, useRef } from "react";

import { track } from "@/lib/analytics";

import { Coaching } from "./coaching";
import { CTARepeat } from "./cta-repeat";
import { Hero } from "./hero";
import { Library } from "./library";
import { Scoring } from "./scoring";
import { StrengthsGaps } from "./strengths-gaps";
import { Transcript } from "./transcript";
import { VoicePipeline } from "./voice-pipeline";

// Module-scoped guard so landing_view fires at most once per page load —
// not on StrictMode dev double-invoke, not on auth-state-flip remounts, not
// on browser-back to /. visitor_id de-dup at query time would mask the
// inflation, but raw counts and time-series would be skewed.
let landingViewFired = false;

export default function Landing() {
  const fired = useRef(landingViewFired);
  useEffect(() => {
    if (fired.current) return;
    fired.current = true;
    landingViewFired = true;
    track({ event: "landing_view" });
  }, []);

  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <VoicePipeline />
      <Scoring />
      <StrengthsGaps />
      <Transcript />
      <Coaching />
      <Library />
      <CTARepeat />
    </div>
  );
}
