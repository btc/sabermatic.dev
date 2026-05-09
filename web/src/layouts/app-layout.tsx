import { useQuery } from "@connectrpc/connect-query";
import { Link, Outlet, useLocation, useNavigate } from "react-router-dom";

import { useLogout } from "@/api/queries";
import { BrandName } from "@/components/brand-name";
import { KeptBanner } from "@/components/kept-banner";
import { ThemeSwitch } from "@/components/theme-switch";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { useRequireAuth } from "@/hooks/use-auth";
import { listSessions } from "@/pb/drill/v1/session-SessionService_connectquery";
import { UserRole } from "@/pb/drill/v1/user_pb";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";

export function AppLayout({ children }: { children?: React.ReactNode }) {
  const { isLoading, isAuthenticated } = useRequireAuth();
  const { data: meData } = useQuery(getMe, {});
  const user = meData?.user;
  const { data: sessionsResp } = useQuery(listSessions, {});
  const logout = useLogout();
  const navigate = useNavigate();
  const location = useLocation();
  const hasAnySessions = (sessionsResp?.sessions?.length ?? 0) > 0;
  const initials = user?.displayName?.slice(0, 2).toUpperCase() ?? "?";

  const handleLogout = () => {
    // useLogout owns the post-logout navigation (full-page reload to /). See
    // the comment in useLogout for the race details.
    logout.mutate({});
  };

  if (isLoading || !isAuthenticated) {
    return (
      <div className="flex h-screen items-center justify-center bg-background">
        <BrandName className="text-sm font-semibold tracking-wider text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="border-b border-border">
        <div className="mx-auto flex h-12 max-w-5xl items-center justify-between px-4">
          <Link to="/" className="hover:text-foreground transition-colors">
            <BrandName className="text-sm font-semibold tracking-wider text-muted-foreground" />
          </Link>
          <div className="flex items-center gap-4">
            <nav className="flex items-center gap-4 text-sm">
              {hasAnySessions ? (
                <Link
                  to="/history"
                  className={location.pathname === "/history" ? "text-foreground" : "text-muted-foreground hover:text-foreground"}
                >
                  Sessions
                </Link>
              ) : (
                <span className="cursor-default text-muted-foreground/50">Sessions</span>
              )}
              {user?.role === UserRole.ADMIN && (
                <a
                  href="/admin/jobs/"
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-muted-foreground hover:text-foreground"
                >
                  Jobs
                </a>
              )}
            </nav>
            <DropdownMenu>
              <DropdownMenuTrigger className="h-8 w-8 rounded-full bg-muted text-xs font-medium hover:bg-muted/80 cursor-pointer border-0 focus-visible:outline-none">
                {initials}
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end">
                <DropdownMenuItem onClick={() => navigate("/settings")}>
                  Settings
                </DropdownMenuItem>
                <DropdownMenuItem
                  onSelect={(e) => e.preventDefault()}
                  className="focus:bg-transparent data-[highlighted]:bg-transparent"
                >
                  <ThemeSwitch />
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={handleLogout}>
                  Log out
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
      </header>
      <KeptBanner />
      <main className="mx-auto max-w-5xl px-4 py-6">
        {children ?? <Outlet />}
      </main>
    </div>
  );
}
