import { useEffect, useState } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import { Link } from "react-router-dom";
import remarkGfm from "remark-gfm";

import { useEducator, useMe, useRequestEducator } from "@/api/queries";
import { useSampleEducator } from "@/api/sample-queries";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { GENERATING_MESSAGES } from "@/lib/constants";
import { EducatorStatus } from "@/pb/drill/v1/educator_pb";
import { UserPlan } from "@/pb/drill/v1/user_pb";

import { useSessionDetail } from "./session-detail-ctx";

// ---------------------------------------------------------------------------
// Heading extraction + slugify
// ---------------------------------------------------------------------------

interface Heading {
  id: string;
  text: string;
  level: number;
}

function slugify(text: string): string {
  return text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/-+$/, "");
}

function extractHeadings(markdown: string): Heading[] {
  const headings: Heading[] = [];
  const lines = markdown.split("\n");
  for (const line of lines) {
    const match = line.match(/^(#{1,3})\s+(.+)/);
    if (match && match[1] && match[2]) {
      const level = match[1].length;
      const text = match[2];
      const id = slugify(text);
      headings.push({ id, text, level });
    }
  }
  return headings;
}

function childrenToString(children: React.ReactNode): string {
  if (typeof children === "string") return children;
  if (Array.isArray(children)) return children.map(childrenToString).join("");
  return "";
}

// ---------------------------------------------------------------------------
// react-markdown custom components
// ---------------------------------------------------------------------------

const markdownComponents: Components = {
  h1: ({ children, ...props }: React.HTMLAttributes<HTMLHeadingElement>) => (
    <h1 id={slugify(childrenToString(children))} {...props} className="text-xl font-semibold mt-8 mb-3">
      {children}
    </h1>
  ),
  h2: ({ children, ...props }: React.HTMLAttributes<HTMLHeadingElement>) => (
    <h2 id={slugify(childrenToString(children))} {...props} className="text-lg font-semibold mt-6 mb-2">
      {children}
    </h2>
  ),
  h3: ({ children, ...props }: React.HTMLAttributes<HTMLHeadingElement>) => (
    <h3 id={slugify(childrenToString(children))} {...props} className="text-base font-semibold mt-5 mb-2">
      {children}
    </h3>
  ),
  p: ({ children, ...props }: React.HTMLAttributes<HTMLParagraphElement>) => (
    <p {...props} className="text-sm leading-relaxed mb-3">
      {children}
    </p>
  ),
  ul: ({ children, ...props }: React.HTMLAttributes<HTMLUListElement>) => (
    <ul {...props} className="list-disc pl-5 mb-3 space-y-1 text-sm">
      {children}
    </ul>
  ),
  ol: ({ children, ...props }: React.HTMLAttributes<HTMLOListElement>) => (
    <ol {...props} className="list-decimal pl-5 mb-3 space-y-1 text-sm">
      {children}
    </ol>
  ),
  code: ({ children, className, ...props }: React.HTMLAttributes<HTMLElement>) => (
    <code className={`${className ?? ""} font-mono text-xs bg-muted px-1 py-0.5 rounded`} {...props}>
      {children}
    </code>
  ),
  pre: ({ children, ...props }: React.HTMLAttributes<HTMLPreElement>) => (
    <pre {...props} className="bg-muted rounded-lg p-4 mb-3 overflow-x-auto text-xs font-mono">
      {children}
    </pre>
  ),
};

// ---------------------------------------------------------------------------
// Sticky TOC
// ---------------------------------------------------------------------------

interface TOCProps {
  headings: Heading[];
}

function TOC({ headings }: TOCProps) {
  if (headings.length === 0) return null;

  return (
    <nav className="w-48 shrink-0 sticky top-6 self-start">
      <p className="text-xs font-medium text-muted-foreground uppercase tracking-wider mb-3">
        Contents
      </p>
      <ul className="space-y-1">
        {headings.map((h) => (
          <li key={h.id} style={{ paddingLeft: `${(h.level - 1) * 12}px` }}>
            <a
              href={`#${h.id}`}
              className="text-xs text-muted-foreground hover:text-foreground transition-colors leading-snug block py-0.5"
            >
              {h.text}
            </a>
          </li>
        ))}
      </ul>
    </nav>
  );
}

// ---------------------------------------------------------------------------
// Cycling message component for generating state
// ---------------------------------------------------------------------------

function GeneratingView() {
  const [index, setIndex] = useState(0);

  useEffect(() => {
    const id = setInterval(() => {
      setIndex((i) => (i + 1) % GENERATING_MESSAGES.length);
    }, 3000);
    return () => clearInterval(id);
  }, []);

  return (
    <div className="flex flex-col items-center justify-center py-24 gap-6 px-4">
      <div className="size-3 rounded-full bg-primary animate-pulse" />
      <p className="text-sm text-muted-foreground text-center max-w-xs">
        {GENERATING_MESSAGES[index]}
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export default function DeepDive() {
  return <DeepDiveInner />;
}

function DeepDiveInner() {
  const { dataSource, sessionId } = useSessionDetail();

  const authEducator = useEducator(sessionId, dataSource === "api");
  const sampleEducator = useSampleEducator({ enabled: dataSource === "sample" });

  const educator = dataSource === "api" ? authEducator.data?.analysis : sampleEducator.data?.analysis;
  const isError = dataSource === "api" ? authEducator.isError : sampleEducator.isError;

  const requestEducator = useRequestEducator(sessionId);
  // Only fetch user data in authenticated mode — sample visitors are unauthenticated
  const meQuery = useMe({ enabled: dataSource === "api" });

  const isSample = dataSource === "sample";
  const isPro = !isSample && meQuery.data?.user?.plan === UserPlan.PRO;

  // Error state — separate from "not requested"
  if (isError) {
    return (
      <div className="flex flex-col items-center gap-4 py-16 text-center max-w-md mx-auto">
        <p className="text-sm text-muted-foreground">
          Could not load deep analysis. Please try again.
        </p>
        <Button
          variant="outline"
          type="button"
          onClick={() => requestEducator.mutate({ sessionId })}
          disabled={isSample || requestEducator.isPending}
        >
          {requestEducator.isPending ? "Requesting..." : "Retry"}
        </Button>
      </div>
    );
  }

  // Loading state
  const isLoading = dataSource === "api" ? authEducator.isLoading : sampleEducator.isLoading;
  if (isLoading) {
    return (
      <div className="flex flex-col items-center gap-4 py-16">
        <Skeleton className="h-6 w-48" />
        <Skeleton className="h-32 w-full max-w-2xl" />
      </div>
    );
  }

  // Not yet requested (null data)
  if (!educator) {
    return (
      <div className="flex flex-col items-center gap-4 py-16 text-center max-w-md mx-auto">
        <p className="text-sm text-muted-foreground leading-relaxed">
          Generate a deep analysis of this session. You'll get a model answer and detailed
          explanations for each gap identified.
        </p>
        {isPro ? (
          <Button
            type="button"
            onClick={() => requestEducator.mutate({ sessionId })}
            disabled={isSample || requestEducator.isPending}
          >
            {requestEducator.isPending ? "Requesting..." : "Generate"}
          </Button>
        ) : (
          <div className="flex flex-col items-center gap-2">
            <Button
              type="button"
              onClick={() => requestEducator.mutate({ sessionId })}
              disabled={isSample || requestEducator.isPending}
            >
              {requestEducator.isPending ? "Requesting..." : "Generate"}
            </Button>
            <p className="text-xs text-muted-foreground">
              Upgrade to access full analysis.{" "}
              <Link to="/settings/billing" className="underline hover:text-foreground transition-colors">
                View plans
              </Link>
            </p>
          </div>
        )}
      </div>
    );
  }

  // Generating
  if (educator.status === EducatorStatus.GENERATING) {
    return <GeneratingView />;
  }

  // Failed
  if (educator.status === EducatorStatus.FAILED) {
    return (
      <div className="flex flex-col items-center gap-4 py-16 text-center max-w-md mx-auto">
        <p className="text-sm text-muted-foreground">
          Deep analysis generation failed. This can happen during high demand.
        </p>
        <Button
          variant="outline"
          type="button"
          onClick={() => requestEducator.mutate({ sessionId })}
          disabled={isSample || requestEducator.isPending}
        >
          {requestEducator.isPending ? "Retrying..." : "Retry"}
        </Button>
      </div>
    );
  }

  // Completed — render content with sticky TOC
  const fullContent = [educator.modelAnswer ?? "", educator.gapDeepDives ?? ""]
    .filter(Boolean)
    .join("\n\n");

  const headings = extractHeadings(fullContent);

  return (
    <div className="flex gap-10 max-w-5xl">
      <TOC headings={headings} />
      <div className="flex-1 min-w-0 prose-sm">
        {educator.modelAnswer && (
          <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
            {educator.modelAnswer}
          </ReactMarkdown>
        )}
        {educator.gapDeepDives && (
          <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
            {educator.gapDeepDives}
          </ReactMarkdown>
        )}
      </div>
    </div>
  );
}
