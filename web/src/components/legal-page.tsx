import type { ReactNode } from "react";

import { PublicFooter } from "@/components/public-footer";
import { PublicHeader } from "@/components/public-header";

interface LegalPageProps {
  title: string;
  children: ReactNode;
}

/**
 * LegalPage is the shell for /about, /terms, /privacy — public header,
 * max-width prose content, public footer. Relies on @tailwindcss/typography
 * for the `prose` classes (installed in Task 2a).
 */
export function LegalPage({ title, children }: LegalPageProps) {
  return (
    <div className="min-h-screen bg-background text-foreground flex flex-col">
      <PublicHeader />
      <main className="flex-1 mx-auto max-w-2xl w-full px-4 py-12 prose prose-neutral dark:prose-invert">
        <h1>{title}</h1>
        {children}
      </main>
      <PublicFooter />
    </div>
  );
}
