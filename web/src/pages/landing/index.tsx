import { Hero } from "./hero";
import { Scoring } from "./scoring";
import { StrengthsGaps } from "./strengths-gaps";
import { Annotations } from "./annotations";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <Scoring />
      <StrengthsGaps />
      <Annotations />
    </div>
  );
}
