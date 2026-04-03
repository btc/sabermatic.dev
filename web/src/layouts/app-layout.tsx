import { Link, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useMe, useLogout, useSessions } from "@/api/queries";
import { useRequireAuth } from "@/hooks/use-auth";
import { useTheme } from "@/hooks/use-theme";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem,
  DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

export function AppLayout() {
  const { isLoading, isAuthenticated } = useRequireAuth();
  const { data: user } = useMe();
  const { data: sessions } = useSessions();
  const logout = useLogout();
  const navigate = useNavigate();
  const location = useLocation();
  const { theme, setTheme } = useTheme();

  const hasAnySessions = (sessions?.length ?? 0) > 0;
  const initials = user?.display_name?.slice(0, 2).toUpperCase() ?? "?";

  const handleLogout = () => {
    logout.mutate(undefined, { onSuccess: () => navigate("/login") });
  };

  if (isLoading || !isAuthenticated) {
    return (
      <div className="flex h-screen items-center justify-center text-muted-foreground bg-background">
        Loading...
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="border-b border-border">
        <div className="mx-auto flex h-12 max-w-5xl items-center justify-between px-4">
          <div className="flex items-center gap-6">
            <Link to="/" className="text-sm font-semibold tracking-wider text-muted-foreground">
              DRILL
            </Link>
            <nav className="flex items-center gap-4 text-sm">
              <Link
                to="/"
                className={location.pathname === "/" ? "text-foreground" : "text-muted-foreground hover:text-foreground"}
              >
                Home
              </Link>
              {hasAnySessions ? (
                <Link
                  to="/history"
                  className={location.pathname === "/history" ? "text-foreground" : "text-muted-foreground hover:text-foreground"}
                >
                  History
                </Link>
              ) : (
                <span className="cursor-default text-muted-foreground/50">History</span>
              )}
            </nav>
          </div>
          <DropdownMenu>
            <DropdownMenuTrigger className="h-8 w-8 rounded-full bg-muted text-xs font-medium hover:bg-muted/80 cursor-pointer border-0 focus-visible:outline-none">
              {initials}
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem onClick={() => navigate("/settings")}>
                Settings
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => {
                const next = theme === "light" ? "dark" : theme === "dark" ? "system" : "light";
                setTheme(next);
              }}>
                Theme: {theme}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={handleLogout}>
                Log out
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-6">
        <Outlet />
      </main>
    </div>
  );
}
