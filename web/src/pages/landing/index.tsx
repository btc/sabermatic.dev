import { Hero } from "./hero";
import { Scoring } from "./scoring";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <Scoring />
    </div>
  );
}
