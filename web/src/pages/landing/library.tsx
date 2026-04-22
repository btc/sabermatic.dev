import { useQuery } from "@connectrpc/connect-query";
import { Link } from "react-router-dom";

import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { listFeaturedQuestions } from "@/pb/drill/v1/landing-LandingService_connectquery";
import { Difficulty } from "@/pb/drill/v1/question_pb";

const DIFFICULTY_CLASS: Record<number, string> = {
  [Difficulty.MEDIUM]: "bg-muted text-foreground",
  [Difficulty.HARD]: "bg-[hsl(0_84%_60%/.15)] text-[hsl(0_75%_45%)]",
};
const DIFFICULTY_LABEL: Record<number, string> = {
  [Difficulty.MEDIUM]: "medium",
  [Difficulty.HARD]: "hard",
};

function PlaceholderSvg() {
  return (
    <svg viewBox="0 0 200 200" preserveAspectRatio="none" className="block h-full w-full" aria-hidden>
      <rect width="200" height="200" fill="hsl(35 60% 82%)" />
      <polygon points="0,0 120,0 70,90 0,140" fill="hsl(22 58% 58%)" />
      <polygon points="120,0 200,0 200,100 140,70" fill="hsl(32 55% 68%)" />
      <polygon points="0,140 70,90 140,130 90,200 0,200" fill="hsl(200 25% 60%)" opacity="0.7" />
      <polygon points="140,70 200,100 200,200 90,200 140,130" fill="hsl(15 55% 48%)" />
      <circle cx="100" cy="100" r="22" fill="hsl(40 80% 75%)" opacity="0.85" />
      <polygon points="100,78 122,100 100,122 78,100" fill="hsl(25 55% 38%)" opacity="0.7" />
    </svg>
  );
}

export function Library() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data, isLoading, isError } = useQuery(listFeaturedQuestions, {});

  if (isError) return null;
  if (!isLoading && (!data || data.totalCount === 0)) return null;

  return (
    <section ref={ref} id="library" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 06 — Library
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            {data?.totalCount ?? "…"} questions. Real prompts. Real rubrics.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Hand-written by engineers who've sat on both sides of the table. Every question ships with an expert answer and the topics the interviewer is listening for.
          </p>
        </header>

        <div className="mb-4 flex items-baseline justify-between font-mono text-[11px] uppercase tracking-[0.1em] text-muted-foreground">
          <span>Questions · {data?.totalCount ?? "…"} total</span>
          <span>
            <b className="font-medium text-primary">Latest 3 / 5</b>
          </span>
        </div>
        <div
          className={`relative mb-9 h-[18px] overflow-hidden rounded-sm bg-primary ${
            isVisible ? "after:animate-[sweep_4s_ease-in-out_infinite]" : ""
          } motion-reduce:after:animate-none after:absolute after:inset-0 after:bg-[linear-gradient(90deg,transparent,rgba(255,255,255,0.35),transparent)] after:content-['']`}
        />

        <div className="grid grid-cols-1 gap-8 sm:grid-cols-2 md:grid-cols-3">
          {isLoading
            ? Array.from({ length: 6 }).map((_, i) => (
                <div key={i} data-testid="library-skeleton" className="flex flex-col gap-2.5">
                  <div className="aspect-[4/3] animate-pulse rounded-[14px] bg-muted" />
                  <div className="h-4 w-3/4 animate-pulse rounded bg-muted" />
                  <div className="h-3 w-1/2 animate-pulse rounded bg-muted" />
                </div>
              ))
            : (data?.questions ?? []).map((q) => (
                <Link
                  key={q.id}
                  to={`/sessions/new?question=${q.id}`}
                  className="group flex flex-col gap-2.5 no-underline text-inherit"
                >
                  <div className="overflow-hidden rounded-[14px] bg-muted">
                    {q.imageUrl ? (
                      <img
                        src={q.imageUrl}
                        alt=""
                        className="aspect-[4/3] w-full object-cover transition-transform duration-250 ease-out group-hover:scale-[1.02] motion-reduce:transition-none"
                      />
                    ) : (
                      <div className="aspect-[4/3] w-full transition-transform duration-250 ease-out group-hover:scale-[1.02] motion-reduce:transition-none">
                        <PlaceholderSvg />
                      </div>
                    )}
                  </div>
                  <h3 className="mt-1 text-sm font-medium">{q.title}</h3>
                  <div className="flex flex-wrap items-center gap-x-2.5 gap-y-1.5 font-mono text-[11px] text-muted-foreground">
                    {DIFFICULTY_LABEL[q.difficulty] ? (
                      <span className={`rounded-full px-2 py-0.5 text-[10px] tracking-wide ${DIFFICULTY_CLASS[q.difficulty] ?? "bg-muted"}`}>
                        {DIFFICULTY_LABEL[q.difficulty]}
                      </span>
                    ) : null}
                    {q.tags.join(" ")}
                  </div>
                </Link>
              ))}
        </div>
      </div>
    </section>
  );
}
