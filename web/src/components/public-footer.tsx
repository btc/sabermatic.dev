import { Link } from "react-router-dom";

/**
 * PublicFooter is mounted on landing, sample, the auth layout, and the legal
 * pages (/about, /terms, /privacy). Provides trust-signal links visible to
 * unauthenticated visitors.
 */
export function PublicFooter() {
  return (
    <footer className="border-t border-border mt-16">
      <div className="mx-auto max-w-[1120px] px-5 sm:px-6 lg:px-10 py-8 flex flex-col sm:flex-row items-center justify-between gap-4 text-sm text-muted-foreground">
        <div>© Spanda, LLC</div>
        <nav aria-label="Footer">
          <ul className="flex flex-wrap items-center gap-4">
            <li><Link to="/about" className="hover:text-foreground">About</Link></li>
            <li><Link to="/terms" className="hover:text-foreground">Terms</Link></li>
            <li><Link to="/privacy" className="hover:text-foreground">Privacy</Link></li>
            <li><a href="mailto:brian@spanda.llc" className="hover:text-foreground">Contact</a></li>
          </ul>
        </nav>
      </div>
    </footer>
  );
}
