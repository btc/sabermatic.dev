import { useEffect, useState } from "react";
import { Link } from "react-router-dom";

import { BallMark } from "@/components/ball-mark";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";

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
  const { ref: barRef, isVisible: barVisible } = useScrollReveal<HTMLDivElement>();

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
    <section aria-labelledby="hero-heading" className="px-5 pt-36 pb-28 text-center sm:px-6 lg:px-10">
      <div className="mx-auto max-w-[1120px]">
        <div className="mb-8 inline-flex items-center gap-2.5 rounded-full border border-border bg-card px-3 py-1.5 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground">
          <span className="h-1.5 w-1.5 rounded-full bg-primary" aria-hidden />
          system design prep, measured
        </div>

        <h1
          id="hero-heading"
          className="group mb-9 text-[clamp(56px,10vw,140px)] font-extrabold leading-[0.9] tracking-[-0.055em]"
          aria-label="Sabermatic dot DEV"
        >
          sabermatic<span className="font-bold tracking-[-0.03em] opacity-40" aria-hidden="true">[<BallMark className="group-hover:animate-[spin_2s_linear_infinite] motion-reduce:animate-none" />DEV]</span>
        </h1>

        <p
          aria-live="polite"
          className={`h-8 text-[clamp(17px,1.6vw,22px)] text-muted-foreground transition-opacity duration-300 motion-reduce:transition-none ${
            visible ? "opacity-100" : "opacity-0"
          }`}
        >
          {TAGLINES[index]}
        </p>

        <div className="mt-14 flex flex-wrap justify-center gap-3">
          <Link
            to="/signup"
            className="inline-flex items-center gap-2 rounded-lg bg-foreground px-[22px] py-[13px] text-sm font-medium text-background transition-transform hover:-translate-y-px"
          >
            Start practicing
            <span aria-hidden className="rounded bg-foreground/20 px-1.5 py-0.5 font-mono text-[10px]">↵</span>
          </Link>
          <Link
            to="/sample"
            className="inline-flex items-center rounded-lg border border-border-strong bg-card px-[22px] py-[13px] text-sm font-medium hover:bg-muted"
          >
            See sample session
          </Link>
        </div>

        <p className="mt-4 font-mono text-xs uppercase text-muted-foreground">3 sessions free · no card</p>

        <div ref={barRef} className="mx-auto mt-[88px] max-w-[960px]">
          <div className="relative h-[18px] overflow-hidden rounded-sm bg-primary">
            <div
              className={`absolute inset-0 bg-[linear-gradient(90deg,transparent,rgba(255,255,255,0.35),transparent)] ${
                barVisible ? "animate-[sweep_4s_ease-in-out_infinite]" : ""
              } motion-reduce:animate-none`}
            />
          </div>
          <div className="mt-3 flex justify-between font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground">
            <span>1 session completed</span>
            <span>
              <b className="font-medium text-primary">Latest 3/5</b>
            </span>
          </div>
        </div>
      </div>
    </section>
  );
}
