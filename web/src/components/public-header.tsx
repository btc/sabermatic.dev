import { Link } from "react-router-dom";

import { BrandName } from "@/components/brand-name";

export function PublicHeader() {
  return (
    <header className="border-b border-border">
      <div className="mx-auto flex h-12 max-w-5xl items-center justify-between px-4">
        <Link to="/" className="text-sm font-semibold tracking-wider text-muted-foreground">
          <BrandName />
        </Link>
        <div className="flex items-center gap-4">
          <Link
            to="/login"
            className="text-sm text-muted-foreground hover:text-foreground transition-colors"
          >
            Log in
          </Link>
          <Link
            to="/signup"
            className="rounded-md bg-primary px-4 py-1.5 text-xs font-medium text-primary-foreground"
          >
            Sign up
          </Link>
        </div>
      </div>
    </header>
  );
}
