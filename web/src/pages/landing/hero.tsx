import { useState, useEffect } from "react";
import { Link } from "react-router-dom";

const TAGLINES = [
  "system design, measured.",
  "measure what matters.",
  "practice with precision.",
  "the science of system design prep.",
  "data-driven system design prep.",
];

const CYCLE_MS = 3000;

export function Hero() {
  const [index, setIndex] = useState(0);
  const [visible, setVisible] = useState(true);
  const isLast = index === TAGLINES.length - 1;

  useEffect(() => {
    if (isLast) return; // Hold on final tagline

    const timer = setInterval(() => {
      setVisible(false);
      setTimeout(() => {
        setIndex((i) => i + 1);
        setVisible(true);
      }, 300); // fade out duration
    }, CYCLE_MS);

    return () => clearInterval(timer);
  }, [isLast]);

  return (
    <section className="flex min-h-screen flex-col items-center justify-center gap-8 px-4">
      <h1 className="text-5xl font-light tracking-tight text-foreground sm:text-7xl">
        sabermetric
      </h1>
      <p
        className={`text-lg text-muted-foreground transition-opacity duration-300 sm:text-xl ${
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
