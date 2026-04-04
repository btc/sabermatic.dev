import { Hero } from "./hero";
import { Scoring } from "./scoring";
import { StrengthsGaps } from "./strengths-gaps";
import { Annotations } from "./annotations";
import { DeepDivePreview } from "./deep-dive";
import { Coaching } from "./coaching";
import { VoicePipeline } from "./voice-pipeline";
import { SampleSessionLink } from "./sample-session";
import { Credits } from "./credits";
import { CTARepeat } from "./cta-repeat";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <Scoring />
      <StrengthsGaps />
      <Annotations />
      <DeepDivePreview />
      <Coaching />
      <VoicePipeline />
      <SampleSessionLink />
      <Credits />
      <CTARepeat />
    </div>
  );
}
