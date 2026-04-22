import { Coaching } from "./coaching";
import { Credits } from "./credits";
import { CTARepeat } from "./cta-repeat";
import { DeepDivePreview } from "./deep-dive";
import { Hero } from "./hero";
import { SampleSessionLink } from "./sample-session";
import { Scoring } from "./scoring";
import { StrengthsGaps } from "./strengths-gaps";
import { Transcript } from "./transcript";
import { VoicePipeline } from "./voice-pipeline";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <Scoring />
      <StrengthsGaps />
      <Transcript />
      <DeepDivePreview />
      <Coaching />
      <VoicePipeline />
      <SampleSessionLink />
      <Credits />
      <CTARepeat />
    </div>
  );
}
