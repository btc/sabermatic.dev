import { Link, useLocation } from "react-router-dom";

import { BrandName } from "@/components/brand-name";

const LANDING_NAV = [
  { href: "#library", label: "Questions" },
  { href: "#pipeline", label: "How it works" },
];

const LANDING_PATHS = new Set(["/", "/about"]);

export function PublicHeader() {
  const location = useLocation();
  const showAnchors = LANDING_PATHS.has(location.pathname);

  return (
    <header className="sticky top-0 z-40 border-b border-border bg-background/90 backdrop-blur-md">
      <div className="mx-auto flex h-[60px] max-w-[1120px] items-center justify-between px-5 sm:px-6 lg:px-10">
        {/* -mx-1 -my-3 cancels px-1 py-3 in flex layout: the link's hit area
            grows to ~44px (line-height 20 + 12 + 12) without shifting siblings.
            inline-block is required for vertical padding to actually grow an <a>. */}
        <Link
          to="/"
          className="inline-block -mx-1 -my-3 px-1 py-3 text-sm font-medium tracking-[-0.01em]"
        >
          <BrandName responsiveCompact />
        </Link>
        <nav aria-label="Public navigation" className="flex items-center gap-7 text-[13px] text-muted-foreground">
          {showAnchors &&
            LANDING_NAV.map((a) => (
              <a
                key={a.href}
                href={a.href}
                className="hidden sm:inline-block hover:text-foreground transition-colors"
              >
                {a.label}
              </a>
            ))}
          <Link to="/login" className="hover:text-foreground transition-colors">
            Log in
          </Link>
          <Link
            to="/signup"
            className="rounded-md bg-primary px-4 py-1.5 text-xs font-medium text-primary-foreground"
          >
            Sign up
          </Link>
        </nav>
      </div>
    </header>
  );
}
