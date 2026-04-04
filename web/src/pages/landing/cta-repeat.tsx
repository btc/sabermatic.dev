import { Link } from "react-router-dom";

export function CTARepeat() {
  return (
    <section className="flex flex-col items-center justify-center gap-4 py-24 px-4">
      <Link
        to="/signup"
        className="rounded-md bg-primary px-8 py-3 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90"
      >
        Start practicing
      </Link>
      <p className="text-xs text-muted-foreground">No credit card required</p>
    </section>
  );
}
