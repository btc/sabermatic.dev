import { Link } from "react-router-dom";

export default function NotFound() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-4 py-20 text-center">
      <h1 className="text-4xl font-bold">404</h1>
      <p className="text-lg text-muted-foreground">Page not found</p>
      <Link
        to="/"
        className="text-primary underline underline-offset-4 hover:text-primary/80"
      >
        Go home
      </Link>
    </div>
  );
}
