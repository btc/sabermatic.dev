import { Link } from "react-router-dom";

export function CTARepeat() {
  return (
    <section
      aria-label="Call to action"
      className="border-t border-border text-center px-5 py-40 sm:px-6 lg:px-10"
    >
      <div className="mx-auto max-w-[1120px]">
        <h2 className="mb-10 text-[clamp(40px,6vw,80px)] font-light leading-none tracking-[-0.035em]">
          practice with precision.
        </h2>
        <div className="mt-2 flex flex-wrap justify-center gap-3">
          <Link
            to="/signup"
            className="inline-flex items-center gap-2 rounded-lg bg-foreground px-[22px] py-[13px] text-sm font-medium text-background transition-transform hover:-translate-y-px"
          >
            Start practicing
            <span aria-hidden className="rounded bg-foreground/20 px-1.5 py-0.5 font-mono text-[10px]">↵</span>
          </Link>
        </div>
        <div className="mt-[22px] font-mono text-xs uppercase tracking-[0.08em] text-muted-foreground">
          3 sessions free · no card · cancel any time
        </div>
      </div>
    </section>
  );
}
