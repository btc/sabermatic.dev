import { useEffect,useState } from "react";
import { Link } from "react-router-dom";

import { BrandName } from "@/components/brand-name";

const TAGLINES = [
  "system design, measured.",
  "measure what matters.",
  "practice with precision.",
  "the science of system design prep.",
  "data-driven system design prep.",
];

const CYCLE_MS = 3000;
const FADE_MS = 300;

export function Hero() {
  const [index, setIndex] = useState(0);
  const [visible, setVisible] = useState(true);
  const isLast = index === TAGLINES.length - 1;

  useEffect(() => {
    if (isLast) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;

    let fadeTimeout: ReturnType<typeof setTimeout>;
    const timer = setInterval(() => {
      setVisible(false);
      fadeTimeout = setTimeout(() => {
        setIndex((i) => Math.min(i + 1, TAGLINES.length - 1));
        setVisible(true);
      }, FADE_MS);
    }, CYCLE_MS);

    return () => {
      clearInterval(timer);
      clearTimeout(fadeTimeout);
    };
  }, [isLast]);

  return (
    <section aria-labelledby="hero-heading" className="flex min-h-screen flex-col items-center justify-center gap-8 px-4">
      <h1 id="hero-heading" className="text-foreground">
        <BrandName className="text-5xl font-light tracking-tight sm:text-7xl" />
      </h1>
      <p
        aria-live="polite"
        className={`text-lg text-muted-foreground transition-opacity duration-300 motion-reduce:transition-none sm:text-xl ${
          visible ? "opacity-100" : "opacity-0"
        }`}
      >
        {TAGLINES[index]}
      </p>
      <Link
        to="/signup"
        className="mt-4 rounded-md bg-primary px-8 py-3 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
      >
        Start practicing
      </Link>
    </section>
  );
}
