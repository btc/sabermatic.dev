import { Coaching } from "./coaching";
import { CTARepeat } from "./cta-repeat";
import { Hero } from "./hero";
import { Library } from "./library";
import { Scoring } from "./scoring";
import { StrengthsGaps } from "./strengths-gaps";
import { Transcript } from "./transcript";
import { VoicePipeline } from "./voice-pipeline";

export default function Landing() {
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
