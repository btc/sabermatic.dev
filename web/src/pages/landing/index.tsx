import { Hero } from "./hero";
import { Scoring } from "./scoring";
import { StrengthsGaps } from "./strengths-gaps";
import { Annotations } from "./annotations";
import { DeepDivePreview } from "./deep-dive";
import { Coaching } from "./coaching";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <Scoring />
      <StrengthsGaps />
      <Annotations />
      <DeepDivePreview />
      <Coaching />
    </div>
  );
}
