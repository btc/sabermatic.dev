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
      <div className="mx-auto flex h-[60px] max-w-[1120px] items-center justify-between px-10 sm:px-6">
        <Link to="/" className="text-sm font-medium tracking-[-0.01em]">
          <BrandName />
        </Link>
        <nav aria-label="Public navigation" className="flex items-center gap-7 text-[13px] text-muted-foreground">
          {showAnchors &&
            LANDING_NAV.map((a) => (
              <a key={a.href} href={a.href} className="hover:text-foreground transition-colors">
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
